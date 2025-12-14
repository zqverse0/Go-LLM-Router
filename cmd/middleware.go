package main

import (
	"strings"
	"llm-gateway/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdminAuthMiddleware 管理员鉴权中间件
func AdminAuthMiddleware() gin.HandlerFunc {
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

		// 查询数据库验证管理员密钥
		db, exists := c.Get("db")
		if !exists {
			c.JSON(500, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Database connection not available",
					Type:    "internal_error",
				},
			})
			c.Abort()
			return
		}

		var adminKey models.AdminKey
		if err := db.(*gorm.DB).Where("key = ?", token).First(&adminKey).Error; err != nil {
			c.JSON(401, models.ErrorResponse{
				Error: models.ErrorDetail{
					Message: "Invalid authentication token",
					Type:    "authentication_error",
				},
			})
			c.Abort()
			return
		}

		// 将管理员信息存储到上下文
		c.Set("admin_id", adminKey.ID)
		c.Set("admin_name", adminKey.Name)

		c.Next()
	}
}