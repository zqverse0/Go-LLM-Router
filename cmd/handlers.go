package main

import (
	"errors"
	"fmt"
	"llm-gateway/core"
	"llm-gateway/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// parseAndValidateID 解析并验证字符串ID为uint
func parseAndValidateID(idStr string, paramName string) (uint, error) {
	if idStr == "" {
		return 0, fmt.Errorf("missing %s parameter", paramName)
	}

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: must be a number", paramName)
	}

	return uint(id), nil
}

// withTransaction 执���事务处理，自动处理错误回滚
func withTransaction(db *gorm.DB, fn func(*gorm.DB) error) error {
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r) // re-panic after rollback
		}
	}()

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// safeMaskKey 安全地脱敏密钥，避免切片越界
func safeMaskKey(key string) string {
	if key == "" {
		return "***"
	}

	if len(key) <= 8 {
		// 对于短密钥，只显示前2位
		if len(key) <= 4 {
			return key[:1] + "***"
		}
		return key[:2] + "***" + key[len(key)-2:]
	}

	// 显示前8位和后4位
	return key[:8] + "..." + key[len(key)-4:]
}

// handleRoot 处理根路径请求
func handleRoot(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(200, gin.H{
			"name":    "LLM API Aggregation Gateway",
			"version": "2.0.0",
			"endpoints": gin.H{
				"chat":        "/v1/chat/completions",
				"health":      "/health",
				"dashboard":   "/dashboard",
				"admin_stats": "/admin/stats",
			},
			"model_groups": getGroupIDs(router),
			"timestamp":    time.Now().Unix(),
		})
	}
}

// handleHealth 处理健康检查
func handleHealth(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(200, models.HealthResponse{
			Status:      "healthy",
			Gateway:     "LLM API Aggregation Gateway",
			ModelGroups: getGroupIDs(router),
			Timestamp:   time.Now().Unix(),
		})
	}
}

// handleDashboard 处理管理员仪表板（完整版）
func handleDashboard() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Data(200, "text/html; charset=utf-8", []byte(DashboardHTML))
	}
}

// handleListModelGroups 处理获取模型组列表
func handleListModelGroups(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从数据库直接查询，避免缓存问题
		var dbGroups []models.ModelGroup
		if err := router.GetDB().Find(&dbGroups).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to query model groups: "+err.Error()))
			return
		}

		// 查询每个组的模型数量
		type ModelGroupInfo struct {
			ID          uint                   `json:"id"`
			GroupID     string                 `json:"group_id"`
			Strategy    string                 `json:"strategy"`
			ModelCount  int                    `json:"model_count"`
			Models      []models.ModelConfig   `json:"models,omitempty"`
		}

		var groupsInfo []ModelGroupInfo
		for _, group := range dbGroups {
			var modelCount int64
			if err := router.GetDB().Model(&models.ModelConfig{}).Where("model_group_id = ?", group.ID).Count(&modelCount).Error; err != nil {
				router.GetLogger().Errorf("Failed to count models for group %d: %v", group.ID, err)
				modelCount = 0
			}

			var modelConfigs []models.ModelConfig
			if err := router.GetDB().Where("model_group_id = ?", group.ID).Find(&modelConfigs).Error; err != nil {
				router.GetLogger().Errorf("Failed to load models for group %d: %v", group.ID, err)
				modelConfigs = []models.ModelConfig{}
			}

			groupsInfo = append(groupsInfo, ModelGroupInfo{
				ID:         group.ID,
				GroupID:    group.GroupID,
				Strategy:   group.Strategy,
				ModelCount: int(modelCount),
				Models:     modelConfigs,
			})
		}

		c.JSON(200, models.NewSuccessResponse("Model groups retrieved successfully", groupsInfo))
	}
}

// handleCreateModelGroup 处理创建模型组
func handleCreateModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var group models.ModelGroup
		if err := c.ShouldBindJSON(&group); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request format: "+err.Error()))
			return
		}

		// 设置默认策略
		if group.Strategy == "" {
			group.Strategy = "fallback"
		}

		// 使用 Unscoped() 检查是否存在（包括软删除的记录）
		var existingGroup models.ModelGroup
		err := router.GetDB().Unscoped().Where("group_id = ?", group.GroupID).First(&existingGroup).Error

		if err == nil {
			// 记录存在（包括软删除的）
			if existingGroup.DeletedAt.Valid {
				// 记录已被软删除，执行正确的"复活"操作
				existingGroup.Strategy = group.Strategy
				existingGroup.DeletedAt = gorm.DeletedAt{} // 正确重置软删除

				if err := router.GetDB().Unscoped().Save(&existingGroup).Error; err != nil {
					c.JSON(500, models.NewErrorResponse("Failed to restore model group: "+err.Error()))
					return
				}

				// 刷新缓存
				if err := router.RefreshData(); err != nil {
					router.GetLogger().Warnf("Failed to refresh cache after restoring model group: %v", err)
				}

				// 返回恢复后的数据
				c.JSON(200, models.NewSuccessResponse("Model group restored successfully", gin.H{
					"id":       existingGroup.ID,
					"group_id": group.GroupID,
					"strategy": group.Strategy,
				}))
			} else {
				// 记录存在且未被删除
				c.JSON(400, models.NewErrorResponse("Group ID already exists"))
				return
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			// 记录完全不存在，创建新记录
			if err := router.GetDB().Create(&group).Error; err != nil {
				router.GetLogger().Errorf("[ERROR] AddGroup | ID: %s | Error: %v", group.GroupID, err)
				c.JSON(500, models.NewErrorResponse("Failed to create model group: "+err.Error()))
				return
			}

			// 刷新缓存
			if err := router.RefreshData(); err != nil {
				router.GetLogger().Warnf("Failed to refresh cache after creating model group: %v", err)
			}

			router.GetLogger().Infof("[INFO] CreateGroup | ID: %s | Strategy: %s | Success", group.GroupID, group.Strategy)
			c.JSON(200, models.NewSuccessResponse("Model group created successfully", group))
		} else {
			// 数据库查询错误
			router.GetLogger().Errorf("[ERROR] AddGroup | Database check failed | Error: %v", err)
			c.JSON(500, models.NewErrorResponse("Failed to check model group: "+err.Error()))
			return
		}
	}
}

// handleGetModelGroup 处理获取单个模型组
func handleGetModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIDStr := c.Param("group_id")

		var group models.ModelGroup
		var err error

		// 尝试先按ID查找（数字ID）
		if id, parseErr := parseAndValidateID(groupIDStr, "group_id"); parseErr == nil {
			err = router.GetDB().First(&group, id).Error
			if err != nil {
				c.JSON(404, models.NewErrorResponse("Model group not found"))
				return
			}
		} else {
			// 如果不是数字，按GroupID查找（字符串）
			err = router.GetDB().Where("group_id = ?", groupIDStr).First(&group).Error
			if err != nil {
				c.JSON(404, models.NewErrorResponse("Model group not found"))
				return
			}
		}

		// 查询模型配置
		var modelConfigs []models.ModelConfig
		if err := router.GetDB().Where("model_group_id = ?", group.ID).Find(&modelConfigs).Error; err != nil {
			modelConfigs = []models.ModelConfig{}
		}

		// 查询每个模型的API密钥
		for i := range modelConfigs {
			var keys []models.APIKey
			if err := router.GetDB().Where("model_config_id = ?", modelConfigs[i].ID).Find(&keys).Error; err != nil {
				router.GetLogger().Errorf("Failed to load keys for model %d: %v", modelConfigs[i].ID, err)
				keys = []models.APIKey{}
			}
			modelConfigs[i].APIKeys = keys
		}

		group.Models = modelConfigs

		c.JSON(200, models.NewSuccessResponse("Model group retrieved successfully", group))
	}
}

// handleUpdateModelGroup 处理更新模型组
func handleUpdateModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIDStr := c.Param("group_id")

		var updateData struct {
			Strategy string `json:"strategy"`
		}

		if err := c.ShouldBindJSON(&updateData); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request format: "+err.Error()))
			return
		}

		var group models.ModelGroup
		var err error

		// 尝试先按ID查找（数字ID）
		if id, parseErr := parseAndValidateID(groupIDStr, "group_id"); parseErr == nil {
			err = router.GetDB().First(&group, id).Error
		} else {
			// 如果不是数字，按GroupID查找（字符串）
			err = router.GetDB().Where("group_id = ?", groupIDStr).First(&group).Error
		}

		if err != nil {
			c.JSON(404, models.NewErrorResponse("Model group not found"))
			return
		}

		// 更新策略
		if err := router.GetDB().Model(&group).Update("strategy", updateData.Strategy).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to update model group: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after updating model group: %v", err)
		}

		c.JSON(200, models.NewSuccessResponse("Model group updated successfully", gin.H{
			"group_id": group.GroupID, // 返回实际的GroupID
			"strategy": updateData.Strategy,
		}))
	}
}

// handleDeleteModelGroup 处理删除模型组
func handleDeleteModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIDStr := c.Param("group_id")

		var group models.ModelGroup
		var err error

		// 尝试先按ID查���（数字ID）
		if id, parseErr := parseAndValidateID(groupIDStr, "group_id"); parseErr == nil {
			err = router.GetDB().First(&group, id).Error
		} else {
			// 如果不是数字，按GroupID查找（字符串）
			err = router.GetDB().Where("group_id = ?", groupIDStr).First(&group).Error
		}

		if err != nil {
			c.JSON(404, models.NewErrorResponse("Model group not found"))
			return
		}

		// 使用改进的事务处理
		if err := withTransaction(router.GetDB(), func(tx *gorm.DB) error {
			// 查询相关模型
			var modelConfigs []models.ModelConfig
			if err := tx.Where("model_group_id = ?", group.ID).Find(&modelConfigs).Error; err != nil {
				return fmt.Errorf("failed to query models: %w", err)
			}

			// 删除模型统计、API密钥
			for _, model := range modelConfigs {
				if err := tx.Where("model_config_id = ?", model.ID).Delete(&models.ModelStats{}).Error; err != nil {
					return fmt.Errorf("failed to delete model stats: %w", err)
				}

				if err := tx.Where("model_config_id = ?", model.ID).Delete(&models.APIKey{}).Error; err != nil {
					return fmt.Errorf("failed to delete API keys: %w", err)
				}
			}

			// 删除模型配置
			if err := tx.Where("model_group_id = ?", group.ID).Delete(&models.ModelConfig{}).Error; err != nil {
				return fmt.Errorf("failed to delete model configs: %w", err)
			}

			// 删除模型组统计
			if err := tx.Where("model_group_id = ?", group.ID).Delete(&models.ModelStats{}).Error; err != nil {
				return fmt.Errorf("failed to delete group stats: %w", err)
			}

			// 删除模型组
			if err := tx.Delete(&group).Error; err != nil {
				return fmt.Errorf("failed to delete model group: %w", err)
			}

			return nil
		}); err != nil {
			router.GetLogger().Errorf("[ERROR] DeleteGroup | ID: %s | Error: %v", group.GroupID, err)
			c.JSON(500, models.NewErrorResponse(err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after deleting model group: %v", err)
		}

		router.GetLogger().Infof("[INFO] DeleteGroup | ID: %s | Success", group.GroupID)
		c.JSON(200, models.NewSuccessResponse("Model group deleted successfully", gin.H{
			"group_id": group.GroupID, // 返回实际的GroupID
		}))
	}
}

// handleCreateModel 处理创建模型
func handleCreateModel(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIDStr := c.Param("group_id")

		var req models.CreateModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request format: "+err.Error()))
			return
		}

		var group models.ModelGroup
		var err error

		// 尝试先按ID查找（数字ID）
		if id, parseErr := parseAndValidateID(groupIDStr, "group_id"); parseErr == nil {
			err = router.GetDB().Unscoped().First(&group, id).Error
		} else {
			// 如果不是数字，按GroupID查找（字符串）
			err = router.GetDB().Unscoped().Where("group_id = ?", groupIDStr).First(&group).Error
		}

		if err != nil {
			c.JSON(404, models.NewErrorResponse("Model group not found"))
			return
		}

		// 使用事务处理模型和API密钥的创建
		if err := withTransaction(router.GetDB(), func(tx *gorm.DB) error {
			// 创建模型配置
			model := models.ModelConfig{
				ProviderName:  req.ProviderName,
				UpstreamURL:   req.UpstreamURL,
				UpstreamModel: req.UpstreamModel,
				Timeout:       req.Timeout,
				ModelGroupID:  group.ID,
			}

			if err := tx.Create(&model).Error; err != nil {
				return fmt.Errorf("failed to create model: %w", err)
			}

			// 创建API密钥
			for _, key := range req.Keys {
				if key == "" {
					continue // 跳过空密钥
				}
				apiKey := models.APIKey{
					KeyValue:      key,
					ModelConfigID: model.ID,
				}
				if err := tx.Create(&apiKey).Error; err != nil {
					return fmt.Errorf("failed to create API key: %w", err)
				}
			}

			return nil
		}); err != nil {
			router.GetLogger().Errorf("[ERROR] CreateModel | Group: %s | Model: %s | Error: %v", group.GroupID, req.UpstreamModel, err)
			c.JSON(500, models.NewErrorResponse("Failed to create model: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after creating model: %v", err)
		}

		router.GetLogger().Infof("[INFO] CreateModel | Group: %s | Model: %s | Keys: %d | Success", group.GroupID, req.UpstreamModel, len(req.Keys))
		c.JSON(200, models.NewSuccessResponse("Model created successfully", gin.H{
			"provider_name":  req.ProviderName,
			"upstream_url":   req.UpstreamURL,
			"upstream_model": req.UpstreamModel,
			"timeout":        req.Timeout,
			"keys_count":     len(req.Keys),
		}))
	}
}

// handleUpdateModel 处理更新模型
func handleUpdateModel(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		modelIDStr := c.Param("model_id")

		var updateData struct {
			ProviderName  string `json:"provider_name"`
			UpstreamURL   string `json:"upstream_url"`
			UpstreamModel string `json:"upstream_model"`
			Timeout       int    `json:"timeout"`
		}

		if err := c.ShouldBindJSON(&updateData); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request format: "+err.Error()))
			return
		}

		// 验证并解析模型ID
		modelID, err := parseAndValidateID(modelIDStr, "model_id")
		if err != nil {
			c.JSON(400, models.NewErrorResponse(err.Error()))
			return
		}

		var model models.ModelConfig
		if err := router.GetDB().First(&model, modelID).Error; err != nil {
			c.JSON(404, models.NewErrorResponse("Model not found"))
			return
		}

		// 更新模型配置
		updates := map[string]interface{}{
			"provider_name":  updateData.ProviderName,
			"upstream_url":   updateData.UpstreamURL,
			"upstream_model": updateData.UpstreamModel,
			"timeout":        updateData.Timeout,
		}

		if err := router.GetDB().Model(&model).Updates(updates).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to update model: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after updating model: %v", err)
		}

		c.JSON(200, models.NewSuccessResponse("Model updated successfully", gin.H{
			"model_id": modelID,
		}))
	}
}

// handleDeleteModel 处理删除模型
func handleDeleteModel(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		modelIDStr := c.Param("model_id")

		// 验证并解析模型ID
		modelID, err := parseAndValidateID(modelIDStr, "model_id")
		if err != nil {
			c.JSON(400, models.NewErrorResponse(err.Error()))
			return
		}

		var model models.ModelConfig
		if err := router.GetDB().First(&model, modelID).Error; err != nil {
			c.JSON(404, models.NewErrorResponse("Model not found"))
			return
		}

		// 使用改进的事务处理
		if err := withTransaction(router.GetDB(), func(tx *gorm.DB) error {
			// 删除API密钥
			if err := tx.Where("model_config_id = ?", model.ID).Delete(&models.APIKey{}).Error; err != nil {
				return fmt.Errorf("failed to delete API keys: %w", err)
			}

			// 删除统计数据
			if err := tx.Where("model_config_id = ?", model.ID).Delete(&models.ModelStats{}).Error; err != nil {
				return fmt.Errorf("failed to delete model stats: %w", err)
			}

			// 删除模型配置
			if err := tx.Delete(&model).Error; err != nil {
				return fmt.Errorf("failed to delete model: %w", err)
			}

			return nil
		}); err != nil {
			c.JSON(500, models.NewErrorResponse(err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after deleting model: %v", err)
		}

		c.JSON(200, models.NewSuccessResponse("Model deleted successfully", gin.H{
			"model_id": modelID,
		}))
	}
}

// handleCreateAPIKey 处理创建API密钥
func handleCreateAPIKey(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		modelIDStr := c.Param("model_id")

		// 验证并解析模型ID
		modelID, err := parseAndValidateID(modelIDStr, "model_id")
		if err != nil {
			c.JSON(400, models.NewErrorResponse(err.Error()))
			return
		}

		var requestData struct {
			Key string `json:"key" binding:"required"`
		}

		if err := c.ShouldBindJSON(&requestData); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request format: "+err.Error()))
			return
		}

		var model models.ModelConfig
		if err := router.GetDB().First(&model, modelID).Error; err != nil {
			c.JSON(404, models.NewErrorResponse("Model not found"))
			return
		}

		// 检查是否已存在（包括软删除的记录）
		var existingKey models.APIKey
		err = router.GetDB().Unscoped().Where("key_value = ? AND model_config_id = ?", requestData.Key, model.ID).First(&existingKey).Error

		if err == nil {
			if existingKey.DeletedAt.Valid {
				// 记录已被软删除，执行恢复操作
				existingKey.DeletedAt = gorm.DeletedAt{}
				if err = router.GetDB().Unscoped().Save(&existingKey).Error; err != nil {
					c.JSON(500, models.NewErrorResponse("Failed to restore API key: "+err.Error()))
					return
				}
				c.JSON(200, models.NewSuccessResponse("API key restored successfully", existingKey))
			} else {
				// 记录存在且未被删除
				c.JSON(400, models.NewErrorResponse("API key already exists"))
				return
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			// 记录完全不存在，创建新记录
			apiKey := models.APIKey{
				KeyValue:      requestData.Key,
				ModelConfigID: model.ID,
			}

			if err := router.GetDB().Create(&apiKey).Error; err != nil {
				router.GetLogger().Errorf("[ERROR] CreateAPIKey | Model: %d | Error: %v", model.ID, err)
				c.JSON(500, models.NewErrorResponse("Failed to create API key: "+err.Error()))
				return
			}
			router.GetLogger().Infof("[INFO] CreateAPIKey | Model: %d | Success", model.ID)
			c.JSON(200, models.NewSuccessResponse("API key created successfully", apiKey))
		} else {
			// 数据库查询错误
			router.GetLogger().Errorf("[ERROR] CreateAPIKey | Model: %d | Database check failed | Error: %v", model.ID, err)
			c.JSON(500, models.NewErrorResponse("Failed to check API key: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after creating API key: %v", err)
		}
	}
}

// handleDeleteAPIKey 处理删除API密钥
func handleDeleteAPIKey(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		keyIDStr := c.Param("key_id")

		// 验证并解析密钥ID
		keyID, err := parseAndValidateID(keyIDStr, "key_id")
		if err != nil {
			c.JSON(400, models.NewErrorResponse(err.Error()))
			return
		}

		var apiKey models.APIKey
		if err := router.GetDB().First(&apiKey, keyID).Error; err != nil {
			c.JSON(404, models.NewErrorResponse("API key not found"))
			return
		}

		// 删除API Key
		if err := router.GetDB().Delete(&apiKey).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to delete API key: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after deleting API key: %v", err)
		}

		c.JSON(200, models.NewSuccessResponse("API key deleted successfully", gin.H{
			"key_id": keyID,
		}))
	}
}

// handleStats 处理统计信息
func handleStats(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		stats := router.GetTotalStats()
		c.JSON(200, models.NewSuccessResponse("Stats retrieved successfully", stats))
	}
}

// handleReload 处理配置重载
func handleReload(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := router.RefreshData(); err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to reload configuration"))
			return
		}

		c.JSON(200, models.NewSuccessResponse("Configuration reloaded successfully", gin.H{
			"timestamp": time.Now().Unix(),
		}))
	}
}

// handleListAdminKeys 处理列出所有管理员密钥
func handleListAdminKeys() gin.HandlerFunc {
	return func(c *gin.Context) {
		db := c.MustGet("db").(*gorm.DB)

		var adminKeys []models.AdminKey
		if err := db.Order("id ASC").Find(&adminKeys).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to query admin keys: "+err.Error()))
			return
		}

		// 返回完整密钥信息（包含原始密钥和脱敏预览）
		type AdminKeyResponse struct {
			ID        uint   `json:"id"`
			Name      string `json:"name"`
			Key       string `json:"key"`          // 【修改】返回完整密钥
			KeyPreview string `json:"key_preview"` // 保留脱敏预览用于显示
			CreatedAt int64  `json:"created_at"`
		}

		response := make([]AdminKeyResponse, len(adminKeys))
		for i, key := range adminKeys {
			// 使用安全的密钥脱敏函数
			keyPreview := safeMaskKey(key.Key)
			response[i] = AdminKeyResponse{
				ID:         key.ID,
				Name:       key.Name,
				Key:        key.Key,         // 【修改】返回完整密钥
				KeyPreview: keyPreview,
				CreatedAt:  key.CreatedAt.Unix(),
			}
		}

		c.JSON(200, models.NewSuccessResponse("Admin keys retrieved successfully", response))
	}
}

// handleCreateAdminKey 处理创建新的管理员密钥
func handleCreateAdminKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		db := c.MustGet("db").(*gorm.DB)

		var request struct {
			Name string `json:"name" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request format: "+err.Error()))
			return
		}

		// 检查是否已有管理员密钥
		var count int64
		if err := db.Model(&models.AdminKey{}).Count(&count).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to check admin keys: "+err.Error()))
			return
		}

		// 创建新的管理员密钥
		adminKey := models.AdminKey{
			Name: request.Name,
			Key:  models.GenerateAdminKey(),
		}

		if err := db.Create(&adminKey).Error; err != nil {
			// 检查是否是唯一索引冲突
			if err.Error() == "UNIQUE constraint failed: admin_keys.key" {
				c.JSON(500, models.NewErrorResponse("Generated admin key already exists, please try again"))
				return
			}
			c.JSON(500, models.NewErrorResponse("Failed to create admin key: "+err.Error()))
			return
		}

		c.JSON(200, models.NewSuccessResponse("Admin key created successfully", gin.H{
			"id":   adminKey.ID,
			"name": adminKey.Name,
			"key":  adminKey.Key, // 只在创建时返回完整的密钥
		}))
	}
}

// handleDeleteAdminKey 处理删除管理员密钥
func handleDeleteAdminKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		db := c.MustGet("db").(*gorm.DB)

		idStr := c.Param("id")
		id, err := parseAndValidateID(idStr, "admin key ID")
		if err != nil {
			c.JSON(400, models.NewErrorResponse(err.Error()))
			return
		}

		// 禁止删除 ID 为 1 的初始 Root Key
		if id == 1 {
			c.JSON(403, models.NewErrorResponse("Cannot delete the initial root key"))
			return
		}

		// 检查要删除的密钥是否存在
		var adminKey models.AdminKey
		if err := db.First(&adminKey, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Admin key not found"))
				return
			}
			c.JSON(500, models.NewErrorResponse("Failed to query admin key: "+err.Error()))
			return
		}

		// 使用事务来防止竞态条件
		if err := withTransaction(db, func(tx *gorm.DB) error {
			// 检查是否是最后一个管理员密钥（在事务内重新检查）
			var count int64
			if err := tx.Model(&models.AdminKey{}).Count(&count).Error; err != nil {
				return fmt.Errorf("failed to count admin keys: %w", err)
			}
			if count <= 1 {
				return fmt.Errorf("cannot delete the last admin key")
			}

			// 删除管理员密钥
			if err := tx.Delete(&adminKey).Error; err != nil {
				return fmt.Errorf("failed to delete admin key: %w", err)
			}

			return nil
		}); err != nil {
			status := 500
			if err.Error() == "cannot delete the last admin key" {
				status = 400
			}
			c.JSON(status, models.NewErrorResponse(err.Error()))
			return
		}
		c.JSON(200, models.NewSuccessResponse("Admin key deleted successfully", gin.H{
			"id":   adminKey.ID,
			"name": adminKey.Name,
		}))
	}
}

// Helper functions

func getGroupIDs(router *core.StatelessModelRouter) []string {
	groups := router.GetAllModelGroups()
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.GroupID)
	}
	return ids
}

