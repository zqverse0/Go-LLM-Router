package main

import (
	"context"
	"fmt"
	"llm-gateway/core"
	"llm-gateway/models"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
)

func main() {
	// 创建日志器
	log := logrus.New()
	log.SetLevel(logrus.InfoLevel)
	log.SetFormatter(&logrus.JSONFormatter{})

	// 配置日志输出：同时输出到文件（供前端查看）和 Stdout（供 Docker 查看）
	// 使用带轮转的文件写入器 (10MB 限制)，确保轻量化
	rotator, err := core.NewLogRotator("gateway.log", 10)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, rotator))
	} else {
		log.Warn("Failed to init log rotator, using default stderr")
	}

	// 🔇 关闭 Gin Debug 模式输出
	gin.SetMode(gin.ReleaseMode)

	// 1. 初始化数据库
	db, err := initDatabase(log)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// [Auto-Maintenance] Start background task to prune old logs (Retention: 7 days)
	startAutoPrune(db, log)

	// 2. 初始化核心组件
	// 创建 HTTP Client (Task 2: Dependency Injection)
	httpClient := &http.Client{
		Timeout: 300 * time.Second, // 较长的超时时间以适应 LLM 推理
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// 【Task B】 初始化异步日志记录器
	asyncLogger := core.NewAsyncRequestLogger(db, log)
	defer asyncLogger.Close() // 确保程序退出时刷新剩余日志

	// 初始化 SecretProvider
	// ⚠️ 用户要求去除加密：使用明文存储 (NoOpSecretProvider)
	sp := core.NewNoOpSecretProvider()
	log.Info("🔓 Encryption DISABLED (Plain text mode requested)")

	// 创建 LoadBalancer (Task 1 & 2)
	lb, err := core.NewLoadBalancer(
		db, 
		log, 
		core.GlobalKeyManager, 
		sp,
	)
	if err != nil {
		log.Fatal("Failed to create load balancer:", err)
	}

	// 【Task C】 创建代理处理器 (注入依赖)
	proxyHandler := core.NewProxyHandler(lb, httpClient, log, asyncLogger)

	// 创建Gin引擎
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()

	// 添加中间件
	engine.Use(gin.RecoveryWithWriter(log.Writer()))
	engine.Use(corsMiddleware())
	
	// 【Task 3】 添加 IP 限流中间件
	engine.Use(RateLimitMiddleware())

	// 【Task B】 为业务接口单独添加请求日志中间件 (使用异步日志器)
	api := engine.Group("/")
	api.Use(RequestLoggerMiddleware(asyncLogger))
	{
		// 路由处理逻辑下沉到 ProxyHandler
		api.POST("/v1/chat/completions", verifyAdminToken(lb), proxyHandler.HandleProxyRequest())
		api.POST("/v1/images/generations", verifyAdminToken(lb), proxyHandler.HandleProxyRequest()) // Support Image Gen
		
		// Inbound Adapters (Reverse Conversion)
		api.POST("/v1/messages", verifyAdminToken(lb), proxyHandler.HandleClaudeMessage)
		// Capture "gemini-pro:generateContent" as a single param ":model"
		api.POST("/v1beta/models/:model", verifyAdminToken(lb), proxyHandler.HandleGeminiGenerateContent)
	}

	// 设置路由
	setupRoutes(engine, lb, proxyHandler)

	// 获取端口
	gatewaySettings := lb.GetGatewaySettings()
	port := gatewaySettings.Port
	if port == 0 {
		port = 8000
	}

	// 创建HTTP服务器
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: engine,
	}

	// 启动服务器
	go func() {
		log.Infof("Starting LLM Gateway on port %d", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server:", err)
		}
	}()

	// 等待中断信号以优雅地关闭服务器
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down server...")

	// 设置超时以完成正在进行的请求
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Info("Server exited")
}

// initDatabase 初始化数据库
func initDatabase(log *logrus.Logger) (*gorm.DB, error) {
	// 打开数据库连接 - 【优化】只记录错误，不打印 SQL 语句
	db, err := gorm.Open(sqlite.Open("gateway.db"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Error), // 只在出错时记录日志
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	// [DB Optimization]
	// 1. Disable WAL to keep a single file (classic mode)
	db.Exec("PRAGMA journal_mode = DELETE;")
	// 2. Enable Auto-Vacuum to reclaim disk space after deletes
	db.Exec("PRAGMA auto_vacuum = FULL;")
	// 3. Force a VACUUM now to shrink the file
	db.Exec("VACUUM;")

	// 自动迁移
	if err := models.AutoMigrate(db); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	// 强制创建索引 (Task: Fix Stats Upsert)
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_model_stats_config_id ON model_stats(model_config_id)")

	// 初始化默认数据
	initialAdminKey, err := models.InitializeDefaultData(db)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize default data: %w", err)
	}

	// 如果生成了初始管理员密钥，打印提示
	if initialAdminKey != "" {
		log.Infof("")
		log.Infof("⚠️  No admin keys found. Generated initial key: [ %s ]", initialAdminKey)
		log.Infof("Please save this key to access the dashboard.")
		log.Infof("Use it as: Authorization: Bearer %s", initialAdminKey)
		log.Infof("")
	}

	log.Info("Database initialized successfully")

	return db, nil
}

// setupRoutes 设置路由
func setupRoutes(engine *gin.Engine, lb *core.LoadBalancer, proxyHandler *core.ProxyHandler) {
	// 公开路由 - 无需鉴权，无访问日志
	engine.GET("/", handleRoot(lb))
	engine.GET("/health", handleHealth(lb))
	engine.GET("/demo", handleDashboard())
	engine.GET("/dashboard", handleDashboard())

	// 管理API路由组
	admin := engine.Group("/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("db", lb.GetDB())
		AdminAuthMiddleware()(c)
	})
	{
		// 模型组管理
		admin.GET("/model-groups", handleListModelGroups(lb))
		admin.POST("/model-groups", handleCreateModelGroup(lb))
		admin.GET("/model-groups/:group_id", handleGetModelGroup(lb))
		admin.PUT("/model-groups/:group_id", handleUpdateModelGroup(lb))
		admin.DELETE("/model-groups/:group_id", handleDeleteModelGroup(lb))

		// 模型管理
		admin.POST("/model-groups/:group_id/models", handleCreateModel(lb))
		admin.PUT("/models/:model_id", handleUpdateModel(lb))
		admin.DELETE("/models/:model_id", handleDeleteModel(lb))

		// API Key管理
		admin.POST("/models/:model_id/keys", handleCreateAPIKey(lb))
		admin.DELETE("/keys/:key_id", handleDeleteAPIKey(lb))

		// 统计信息
		admin.GET("/stats", handleStats(lb))
		// 日志查询
		admin.GET("/logs", handleGetRequestLogs(lb))
		admin.GET("/system-logs", handleGetSystemLogs())

		// 配置重载
		admin.POST("/reload", handleReload(lb))

		// Admin Key 管理
		admin.GET("/admin-keys", handleListAdminKeys())
		admin.POST("/admin-keys", handleCreateAdminKey())
		admin.DELETE("/admin-keys/:id", handleDeleteAdminKey())
	}
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept, Accept-Encoding, X-CSRF-Token, Authorization, X-API-Key")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// startAutoPrune starts a background goroutine to clean up old request logs
func startAutoPrune(db *gorm.DB, log *logrus.Logger) {
	go func() {
		log.Info("🧹 Auto-prune task started (Retention: 7 days)")
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		// Run once immediately on startup
		pruneLogs(db, log)

		for range ticker.C {
			pruneLogs(db, log)
		}
	}()
}

func pruneLogs(db *gorm.DB, log *logrus.Logger) {
	// Delete logs older than 7 days
	retentionDate := time.Now().AddDate(0, 0, -7)
	result := db.Where("created_at < ?", retentionDate).Delete(&models.RequestLog{})
	
	if result.Error != nil {
		log.Errorf("❌ Failed to prune old logs: %v", result.Error)
	} else if result.RowsAffected > 0 {
		log.Infof("🧹 Pruned %d old request logs", result.RowsAffected)
		// Optimize storage after deletion
		db.Exec("VACUUM;") 
	}
}
