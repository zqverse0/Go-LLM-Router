package main

import (
	"bytes"
	"io"
	"llm-gateway/core"
	"llm-gateway/models"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
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
func RequestLoggerMiddleware(asyncLogger *core.AsyncRequestLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

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
				logEntry.ErrorMsg = string(bodyBytes)
			}
			
			asyncLogger.Log(logEntry)
		}
	}
}

// IPRateLimiter 简单的 IP 限流器
type IPRateLimiter struct {
	ips    map[string]*rate.Limiter
	mu     sync.Mutex
	rate   rate.Limit
	burst  int
}

func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
	return &IPRateLimiter{
		ips:   make(map[string]*rate.Limiter),
		rate:  r,
		burst: b,
	}
}

// GetLimiter 获取或创建 IP 对应的限流器
func (i *IPRateLimiter) GetLimiter(ip string) *rate.Limiter {
	i.mu.Lock()
	defer i.mu.Unlock()

	limiter, exists := i.ips[ip]
	if !exists {
		limiter = rate.NewLimiter(i.rate, i.burst)
		i.ips[ip] = limiter
	}

	return limiter
}

// 全局限流器实例 (每秒 10 次请求，突发 20 次)
var globalLimiter = NewIPRateLimiter(10, 20)

// RateLimitMiddleware IP 限流中间件
func RateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		limiter := globalLimiter.GetLimiter(clientIP)

		if !limiter.Allow() {
			logrus.Warnf("Rate limit exceeded for IP: %s", clientIP)
			c.AbortWithStatusJSON(429, gin.H{
				"error": gin.H{
					"message": "Too Many Requests",
					"type":    "rate_limit_error",
				},
			})
			return
		}

		c.Next()
	}
}