package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"llm-gateway/core"
	"llm-gateway/core/security"
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
)

func main() {
	// 创建日志器
	log := logrus.New()
	log.SetLevel(logrus.InfoLevel)
	log.SetFormatter(&logrus.JSONFormatter{})
	// 🔇 关闭 Gin Debug 模式输出
	gin.SetMode(gin.ReleaseMode)

	// 1. 初始化数据库
	db, err := initDatabase(log)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

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

	// 初始化 SecretProvider (Task 4: Auto-Managed Encryption)
	secretKey, err := getOrCreateSecretKey("gateway.key")
	if err != nil {
		log.Fatalf("Failed to load or generate secret key: %v", err)
	}

	sp, err := security.NewAESSecretProvider(secretKey)
	if err != nil {
		log.Fatalf("Failed to initialize secret provider: %v", err)
	}
	log.Info("🔒 Encryption enabled (using auto-managed key in 'gateway.key')")

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

	// 自动迁移
	if err := models.AutoMigrate(db); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

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


// getOrCreateSecretKey 获取或创建持久化的加密密钥
func getOrCreateSecretKey(filename string) (string, error) {
	// 1. 尝试读取现有密钥
	if _, err := os.Stat(filename); err == nil {
		content, err := os.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("failed to read key file: %w", err)
		}
		key := string(content)
		if len(key) != 32 {
			return "", fmt.Errorf("invalid key length in %s: expected 32 bytes, got %d", filename, len(key))
		}
		return key, nil
	}

	// 2. 生成新密钥 (32 bytes for AES-256)
	// 注意：NewAESSecretProvider 接受的是原始字符串字节，要求 len(key) == 32
	// 为了避免不可见字符问题，我们生成 16 字节的随机数据并 Hex 编码成 32 字符的字符串
	// 这样 key 既是 32 字节长，又是纯文本可见的
	
	// 这里我们直接生成 32 个随机可见字符可能比较麻烦，
	// 更简单的做法是生成 32 字节的随机数，但为了方便文件查看，我们生成 16 字节随机数 -> Hex 编码 -> 32 字符
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	
	// Hex 编码后的长度是 16 * 2 = 32
	newKey := hex.EncodeToString(randomBytes)

	// 3. 写入文件
	if err := os.WriteFile(filename, []byte(newKey), 0600); err != nil {
		return "", fmt.Errorf("failed to write key file: %w", err)
	}

	fmt.Printf("\n🔑 Generated new encryption key and saved to '%s'\n", filename)
	fmt.Println("    Do not share this file if you are in production!")

	return newKey, nil
}
