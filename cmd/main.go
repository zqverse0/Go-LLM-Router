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
	// 【Task A】 初始化全局高性能 HTTP Client
	core.InitHTTPClient()

	// 【Task B】 初始化异步日志记录器
	asyncLogger := core.NewAsyncRequestLogger(db, log)
	defer asyncLogger.Close() // 确保程序退出时刷新剩余日志

	// 创建无状态模型路由器
	router, err := core.NewStatelessModelRouter(db, log)
	if err != nil {
		log.Fatal("Failed to create stateless model router:", err)
	}

	// 【Task C】 创建无状态代理处理器 (注入异步日志器)
	proxyHandler := core.NewProxyHandlerStateless(router, log, asyncLogger)

	// 创建Gin引擎
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()

	// 添加中间件
	engine.Use(gin.RecoveryWithWriter(log.Writer()))
	engine.Use(corsMiddleware())

	// 【Task B】 为业务接口单独添加请求日志中间件 (使用异步日志器)
	api := engine.Group("/")
	api.Use(RequestLoggerMiddleware(asyncLogger))
	{
		api.POST("/v1/chat/completions", verifyAdminToken(router), proxyHandler.HandleProxyRequest(router))
	}

	// 设置路由
	setupRoutes(engine, router, proxyHandler)

	// 获取端口
	gatewaySettings := router.GetGatewaySettings()
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
func setupRoutes(engine *gin.Engine, router *core.StatelessModelRouter, proxyHandler *core.ProxyHandlerStateless) {
	// 公开路由 - 无需鉴权，无访问日志
	engine.GET("/", handleRoot(router))
	engine.GET("/health", handleHealth(router))
	engine.GET("/demo", handleDashboard())
	engine.GET("/dashboard", handleDashboard())

	// 管理API路由组
	admin := engine.Group("/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("db", router.GetDB())
		AdminAuthMiddleware()(c)
	})
	{
		// 模型组管理
		admin.GET("/model-groups", handleListModelGroups(router))
		admin.POST("/model-groups", handleCreateModelGroup(router))
		admin.GET("/model-groups/:group_id", handleGetModelGroup(router))
		admin.PUT("/model-groups/:group_id", handleUpdateModelGroup(router))
		admin.DELETE("/model-groups/:group_id", handleDeleteModelGroup(router))

		// 模型管理
		admin.POST("/model-groups/:group_id/models", handleCreateModel(router))
		admin.PUT("/models/:model_id", handleUpdateModel(router))
		admin.DELETE("/models/:model_id", handleDeleteModel(router))

		// API Key管理
		admin.POST("/models/:model_id/keys", handleCreateAPIKey(router))
		admin.DELETE("/keys/:key_id", handleDeleteAPIKey(router))

		// 统计信息
		admin.GET("/stats", handleStats(router))

		// 配置重载
		admin.POST("/reload", handleReload(router))

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

// verifyAdminToken 验证管理员Token中间件 (用于代理接口)
func verifyAdminToken(router *core.StatelessModelRouter) gin.HandlerFunc {
	return AdminAuthMiddleware() // 复用统一的 Auth Middleware
}