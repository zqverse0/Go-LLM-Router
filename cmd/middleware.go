package main

import (
	"bytes"
	"io"
	"llm-gateway/core"
	"llm-gateway/models"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// AdminAuthMiddleware 管理员鉴权中间件
func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		var token string
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = authHeader[7:]
			} else {
				token = authHeader
			}
		}
		if token == "" {
			token = c.Query("token")
		}
		if token == "" {
			token = c.GetHeader("x-api-key")
		}

		if token == "" {
			c.AbortWithStatusJSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{Message: "Missing authentication token", Type: "authentication_error"},
			})
			return
		}

		db, exists := c.Get("db")
		if !exists {
			c.AbortWithStatus(500)
			return
		}

		var adminKey models.AdminKey
		if err := db.(*gorm.DB).Where("key = ?", token).First(&adminKey).Error; err != nil {
			c.AbortWithStatusJSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{Message: "Invalid token", Type: "authentication_error"},
			})
			return
		}

		c.Set("admin_id", adminKey.ID)
		c.Set("admin_name", adminKey.Name)
		c.Next()
	}
}

// RequestLoggerMiddleware 异步请求日志中间件
// 修改：将日志发送到 AsyncRequestLogger Channel，而不是直接写入 Logrus
func RequestLoggerMiddleware(asyncLogger *core.AsyncRequestLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		
		// 备份 Body 以便读取 (Log 可能会用到，但 ProxyHandler 内部已经构建了更详细的 Log)
		// 这里主要是为了捕获那些没有经过 ProxyHandler 的请求（如 404，或管理接口）
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

		// 只记录非 Proxy 路径的请求（Proxy 路径由 ProxyHandler 内部记录更详细的信息）
		// 或者记录所有请求作为基础 Access Log
		// 这里演示记录所有非 200 或管理接口的访问
		
		if asyncLogger != nil && (statusCode >= 400 || strings.HasPrefix(c.Request.URL.Path, "/admin")) {
			logEntry := &models.RequestLog{
				Time:       start,
				Method:     c.Request.Method,
				Path:       c.Request.URL.Path,
				Status:     statusCode,
				LatencyMs:  float64(latency.Milliseconds()),
				ClientIP:   clientIP,
				UserAgent:  c.Request.UserAgent(),
			}
			if statusCode >= 400 && len(bodyBytes) > 0 {
				logEntry.ErrorMsg = string(bodyBytes) // 简单记录 Body 作为错误上下文
			}
			
			asyncLogger.Log(logEntry)
		}
	}
}
