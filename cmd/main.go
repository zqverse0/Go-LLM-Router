package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"llm-gateway/core"
	"llm-gateway/models"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	// 创建���志器
	log := logrus.New()
	log.SetLevel(logrus.InfoLevel)
	log.SetFormatter(&logrus.JSONFormatter{})
	// 🔇 关闭 Gin Debug 模式输出
	gin.SetMode(gin.ReleaseMode)

	// 初始化数据库
	db, err := initDatabase(log)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// 创建无状态模型路由器
	router, err := core.NewStatelessModelRouter(db, log)
	if err != nil {
		log.Fatal("Failed to create stateless model router:", err)
	}

	// 创建无状态代理处理器
	proxyHandler := core.NewProxyHandlerStateless(router, log)

	// 创建Gin引擎
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()

	// 添加中间件 - 【优化】只对业务接口使用详细日志
	engine.Use(gin.RecoveryWithWriter(log.Writer()))
	engine.Use(corsMiddleware())

	// 【优化】为业务接口单独添加请求日志中间件
	// 管理接口和健康检查不记录访问日志
	api := engine.Group("/")
	api.Use(requestLoggerMiddleware(log))
	{
		api.POST("/v1/chat/completions", verifyGatewayToken(router), proxyHandler.HandleProxyRequest(router))
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

	// 设置���时以完成正在进行的请求
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	if err := models.InitializeDefaultData(db); err != nil {
		return nil, fmt.Errorf("failed to initialize default data: %w", err)
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

	// OpenAI兼容的聊天接口 - 单独的日志记录（已在上面添加到 api 组）
	// engine.POST("/v1/chat/completions", verifyGatewayToken(router), proxyHandler.ProxyRequest)

	// 管理API路由组 - 静默模式，不记录访问日志
	admin := engine.Group("/admin")
	admin.Use(AuthMiddleware(router))
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
	}
}

// requestLoggerMiddleware 请求日志中间件 - 【优化】只记录业务接口和错误
func requestLoggerMiddleware(log *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录开始时间
		start := time.Now()

		// 读取请求体（如果存在）
		var bodyBytes []byte
		var readErr error

		if c.Request.Body != nil {
			bodyBytes, readErr = io.ReadAll(c.Request.Body)
			// 关闭原始 body
			c.Request.Body.Close()

			if readErr != nil {
				log.Errorf("Failed to read request body: %v", readErr)
			}
		}

		// 【关键修复】重新设置请求体，以便后续处理器可以读取
		// 使用 bytes.NewBuffer 而不是 strings.NewReader，支持二进制数据
		if bodyBytes != nil {
			// 确保创建了一个全新的 Reader
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			// 验证 Body 是否正确设置
			if c.Request.Body == nil {
				log.Error("Failed to restore request body - Body is nil!")
			}
		}

		// 处理请求
		c.Next()

		// 计算延迟
		latency := time.Since(start)

		// 获取客户端 IP
		clientIP := c.ClientIP()

		// 获取状态码
		statusCode := c.Writer.Status()

		// 【优化】只记录错误和非成功状态码的请求
		if statusCode >= 400 {
			// 构建日志字段
			fields := logrus.Fields{
				"method":      c.Request.Method,
				"path":        c.Request.URL.Path,
				"query":       c.Request.URL.RawQuery,
				"status":      statusCode,
				"latency":     latency,
				"client_ip":   clientIP,
				"user_agent":  c.Request.UserAgent(),
				"content_len": c.Request.ContentLength,
			}

			// 添加 Body 读取状态信息
			if readErr != nil {
				fields["body_read_error"] = readErr.Error()
			}

			// 如果是 POST/PUT/PATCH 请求且有请求体，记录请求体内容（限制长度）
			if len(bodyBytes) > 0 &&
				(c.Request.Method == "POST" || c.Request.Method == "PUT" || c.Request.Method == "PATCH") {
				// 限制请求体日志长度，避免日志过大
				bodyStr := string(bodyBytes)
				if len(bodyStr) > 1000 {
					bodyStr = bodyStr[:1000] + "...(truncated)"
				}
				fields["request_body"] = bodyStr
				fields["body_size"] = len(bodyBytes)
			}

			// 根据状态码选择日志级别
			entry := log.WithFields(fields)
			if statusCode >= 500 {
				entry.Error("Server error")
			} else if statusCode >= 400 {
				entry.Warn("Client error")
			}
		}

		// 【优化】对于 200 状态码，只在调试模式下记录
		if statusCode == 200 && os.Getenv("DEBUG") == "true" {
			log.Debugf("Request processed - %s %s (status: %d, latency: %v)",
				c.Request.Method, c.Request.URL.Path, statusCode, latency)
		}
	}
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-API-Key")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// AuthMiddleware 健壮的认证中间件，支持 Header 和 Query 两种方式
func AuthMiddleware(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// OPTIONS 请求直接放行
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		var token string

		// 1. 优先从 Authorization Header 获取
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			// 支持 "Bearer " 前缀
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = authHeader[7:]
			} else {
				token = authHeader
			}
		}

		// 2. 如果 Header 中没有，从 Query 参数获取
		if token == "" {
			token = c.Query("token")
		}

		// 3. 如果还没有，从 x-api-key Header 获取
		if token == "" {
			token = c.GetHeader("x-api-key")
		}

		// 验证 token
		if token == "" {
			c.JSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Missing authentication token. Please provide token in Authorization header (Bearer <token>), x-api-key header, or ?token=<token> query parameter",
					Type:    "authentication_error",
				},
			})
			c.Abort()
			return
		}

		gatewaySettings := router.GetGatewaySettings()
		if token != gatewaySettings.APIKey {
			c.JSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Invalid authentication token",
					Type:    "authentication_error",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// verifyGatewayToken 验证网关Token中间件（保留用于聊天接口）
func verifyGatewayToken(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Missing Authorization header",
					Type:    "authentication_error",
				},
			})
			c.Abort()
			return
		}

		if !checkAuthPrefix(authHeader) {
			c.JSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Invalid Authorization header format",
					Type:    "authentication_error",
				},
			})
			c.Abort()
			return
		}

		token := authHeader[7:] // 去掉 "Bearer " 前缀
		gatewaySettings := router.GetGatewaySettings()
		if token != gatewaySettings.APIKey {
			c.JSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Invalid gateway token",
					Type:    "authentication_error",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// checkAuthPrefix 检查认证前缀
func checkAuthPrefix(authHeader string) bool {
	return len(authHeader) > 7 && authHeader[:7] == "Bearer "
}