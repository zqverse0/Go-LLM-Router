package main

import (
	"errors"
	"llm-gateway/core"
	"llm-gateway/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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

// handleListModelGroups 处理获取模型组列表
func handleListModelGroups(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从数据库直接查询，避免缓存问题
		var dbGroups []models.ModelGroup
		if err := router.GetDB().Find(&dbGroups).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to query model groups: "+err.Error()))
			return
		}

		// 获取每个组的模型数量
		result := make([]gin.H, 0, len(dbGroups))
		for _, group := range dbGroups {
			var modelCount int64
			if err := router.GetDB().Model(&models.ModelConfig{}).Where("model_group_id = ?", group.ID).Count(&modelCount).Error; err != nil {
				router.GetLogger().Errorf("Failed to count models for group %s: %v", group.GroupID, err)
				modelCount = 0
			}

			result = append(result, gin.H{
				"id":       group.ID,        // 数据库主键ID
				"group_id": group.GroupID,   // 用户定义的ID
				"strategy": group.Strategy,
				"models":   int(modelCount), // 模型数量
			})
		}

		c.JSON(200, models.NewSuccessResponse("Model groups retrieved successfully", result))
	}
}

// handleCreateModelGroup 处理创建模型组
func handleCreateModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req models.CreateModelGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request: "+err.Error()))
			return
		}

		// 验证模型组是否已存在
		if _, err := router.GetModelGroup(req.GroupID); err == nil {
			c.JSON(409, models.NewErrorResponse("Model group already exists"))
			return
		}

		// 创建模型组
		group := models.ModelGroup{
			GroupID:  req.GroupID,
			Strategy: req.Strategy,
		}

		// 写入数据库
		if err := router.GetDB().Create(&group).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to create model group: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			// 即使缓存刷新失败，数据已经写入数据库
			router.GetLogger().Warnf("Failed to refresh cache after creating model group: %v", err)
		}

		// 返回创建的模型组信息，包含数据库ID
		c.JSON(201, models.NewSuccessResponse("Model group created successfully", gin.H{
			"id":       group.ID,        // 数据库主键ID
			"group_id": group.GroupID,   // 用户定义的ID
			"strategy": group.Strategy,
		}))
	}
}

// handleGetModelGroup 处理获取单个模型组
func handleGetModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIDStr := c.Param("group_id")

		var group models.ModelGroup
		db := router.GetDB()

		// 先尝试作为数字ID查询
		if id, err := strconv.ParseUint(groupIDStr, 10, 32); err == nil {
			// 按主键ID查询，包含预加载的模型和API密钥
			if err := db.Preload("Models").Preload("Models.APIKeys").
				Preload("Models.Stats").First(&group, id).Error; err == nil {
				// 找到了，直接返回
				buildModelGroupResponse(c, group)
				return
			}
		}

		// 如果按ID没找到，尝试按group_id字段查询
		if err := db.Preload("Models").Preload("Models.APIKeys").
			Preload("Models.Stats").Where("group_id = ?", groupIDStr).First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Model group not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query model group: "+err.Error()))
			}
			return
		}

		// 找到了，构建响应
		buildModelGroupResponse(c, group)
	}
}

// buildModelGroupResponse 构建模型组响应数据
func buildModelGroupResponse(c *gin.Context, group models.ModelGroup) {
	// 构建模型数据
	modelData := make([]gin.H, len(group.Models))
	for i, model := range group.Models {
		// 构建API密钥数据
		keys := make([]gin.H, len(model.APIKeys))
		for j, key := range model.APIKeys {
			keys[j] = gin.H{
				"id":         key.ID,
				"key_value":  models.MaskAPIKey(key.KeyValue),
				"full_key":   key.KeyValue, // 完整的key，用于复制
				"created_at": key.CreatedAt,
			}
		}

		// 获取请求统计（如果有）
		var totalRequests int64
		if model.Stats != nil {
			totalRequests = model.Stats.TotalRequests
		}

		modelData[i] = gin.H{
			"id":             model.ID,
			"provider_name":  model.ProviderName,
			"upstream_url":   model.UpstreamURL,
			"upstream_model": model.UpstreamModel,
			"timeout":        model.Timeout,
			"keys_count":     len(keys),
			"keys":           keys,
			"total_requests": totalRequests,
			"created_at":     model.CreatedAt,
		}
	}

	c.JSON(200, models.NewSuccessResponse("Model group retrieved successfully", gin.H{
		"id":       group.ID,
		"group_id": group.GroupID,
		"strategy": group.Strategy,
		"models":   modelData,
	}))
}

// handleUpdateModelGroup 处理更新模型组
func handleUpdateModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID := c.Param("group_id")
		var req models.UpdateModelGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request: "+err.Error()))
			return
		}

		// 验证模型组是否存在
		if _, err := router.GetModelGroup(groupID); err != nil {
			c.JSON(404, models.NewErrorResponse(err.Error()))
			return
		}

		// 这里应该调用数据库更新逻辑
		c.JSON(200, models.NewSuccessResponse("Model group updated successfully", gin.H{
			"group_id": groupID,
		}))
	}
}

// handleDeleteModelGroup 处理删除模型组
func handleDeleteModelGroup(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID := c.Param("group_id")

		// 转换为数字ID
		id, err := strconv.ParseUint(groupID, 10, 32)
		if err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid group ID: must be a number"))
			return
		}

		// 开始事务
		tx := router.GetDB().Begin()
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// 查询模型组
		var group models.ModelGroup
		if err := tx.First(&group, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Model group not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query model group: "+err.Error()))
			}
			tx.Rollback()
			return
		}

		// 删除相关的统计信息
		if err := tx.Where("model_group_id = ?", id).Delete(&models.ModelStats{}).Error; err != nil {
			tx.Rollback()
			c.JSON(500, models.NewErrorResponse("Failed to delete stats: "+err.Error()))
			return
		}

		// 删除模型组（级联删除模型和API密钥）
		if err := tx.Delete(&group).Error; err != nil {
			tx.Rollback()
			c.JSON(500, models.NewErrorResponse("Failed to delete model group: "+err.Error()))
			return
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to commit transaction: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after deleting model group: %v", err)
		}

		c.JSON(200, models.NewSuccessResponse("Model group deleted successfully", gin.H{
			"group_id": groupID,
		}))
	}
}

// handleCreateModel 处理创建模型
func handleCreateModel(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupIDParam := c.Param("group_id")

		// 转换为数字ID
		groupID, err := strconv.ParseUint(groupIDParam, 10, 32)
		if err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid group ID: must be a number"))
			return
		}

		var req models.CreateModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request: "+err.Error()))
			return
		}

		// 验证模型组是否存在
		var group models.ModelGroup
		if err := router.GetDB().First(&group, groupID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Model group not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query model group: "+err.Error()))
			}
			return
		}

		// 创建模型配置
		model := models.ModelConfig{
			ProviderName:  req.ProviderName,
			UpstreamURL:   req.UpstreamURL,
			UpstreamModel: req.UpstreamModel,
			ModelGroupID:  uint(groupID),
			Timeout:       req.Timeout,
		}

		// 开始事务
		tx := router.GetDB().Begin()
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// 创建模型配置
		if err := tx.Create(&model).Error; err != nil {
			tx.Rollback()
			c.JSON(500, models.NewErrorResponse("Failed to create model: "+err.Error()))
			return
		}

		// 创建API密钥
		for _, keyValue := range req.Keys {
			apiKey := models.APIKey{
				KeyValue:      keyValue,
				ModelConfigID: model.ID,
			}
			if err := tx.Create(&apiKey).Error; err != nil {
				tx.Rollback()
				c.JSON(500, models.NewErrorResponse("Failed to create API key: "+err.Error()))
				return
			}
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to commit transaction: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			// 即使缓存刷新失败，数据已经写入数据库
			router.GetLogger().Warnf("Failed to refresh cache after creating model: %v", err)
		}

		c.JSON(201, models.NewSuccessResponse("Model created successfully", gin.H{
			"id":             model.ID,
			"group_id":       groupIDParam,
			"provider_name":  req.ProviderName,
			"upstream_url":   req.UpstreamURL,
			"upstream_model": req.UpstreamModel,
			"keys_count":     len(req.Keys),
		}))
	}
}

// handleUpdateModel 处理更新模型
func handleUpdateModel(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		modelIDStr := c.Param("model_id")
		modelID, err := strconv.ParseUint(modelIDStr, 10, 32)
		if err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid model ID"))
			return
		}

		var req models.UpdateModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request: "+err.Error()))
			return
		}

		// 查询模型
		var model models.ModelConfig
		if err := router.GetDB().First(&model, modelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Model not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query model: "+err.Error()))
			}
			return
		}

		// 使用结构体更新，只更新非零值字段
		updates := make(map[string]interface{})

		if req.ProviderName != nil && *req.ProviderName != model.ProviderName {
			updates["provider_name"] = *req.ProviderName
		}
		if req.UpstreamURL != nil && *req.UpstreamURL != model.UpstreamURL {
			updates["upstream_url"] = *req.UpstreamURL
		}
		if req.UpstreamModel != nil && *req.UpstreamModel != model.UpstreamModel {
			updates["upstream_model"] = *req.UpstreamModel
		}
		if req.Timeout != nil && *req.Timeout != model.Timeout {
			updates["timeout"] = *req.Timeout
		}

		// 如果没有任何字段需要更新
		if len(updates) == 0 {
			c.JSON(200, models.NewSuccessResponse("Model updated successfully (no changes)", gin.H{
				"model_id": modelID,
			}))
			return
		}

		// 更新数据库
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
		modelID, err := strconv.ParseUint(modelIDStr, 10, 32)
		if err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid model ID"))
			return
		}

		// 开始事务
		tx := router.GetDB().Begin()
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// 查询模型
		var model models.ModelConfig
		if err := tx.First(&model, modelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Model not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query model: "+err.Error()))
			}
			tx.Rollback()
			return
		}

		// 删除相关的API密钥
		if err := tx.Where("model_config_id = ?", modelID).Delete(&models.APIKey{}).Error; err != nil {
			tx.Rollback()
			c.JSON(500, models.NewErrorResponse("Failed to delete API keys: "+err.Error()))
			return
		}

		// 删除模型
		if err := tx.Delete(&model).Error; err != nil {
			tx.Rollback()
			c.JSON(500, models.NewErrorResponse("Failed to delete model: "+err.Error()))
			return
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to commit transaction: "+err.Error()))
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

// handleCreateAPIKey 处理创建API Key
func handleCreateAPIKey(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		modelIDStr := c.Param("model_id")
		modelID, err := strconv.ParseUint(modelIDStr, 10, 32)
		if err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid model ID"))
			return
		}

		var req struct {
			Key string `json:"key" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid request: "+err.Error()))
			return
		}

		// 验证模型是否存在
		var model models.ModelConfig
		if err := router.GetDB().First(&model, modelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("Model not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query model: "+err.Error()))
			}
			return
		}

		// 创建API密钥
		apiKey := models.APIKey{
			KeyValue:      req.Key,
			ModelConfigID: uint(modelID),
		}

		if err := router.GetDB().Create(&apiKey).Error; err != nil {
			c.JSON(500, models.NewErrorResponse("Failed to create API key: "+err.Error()))
			return
		}

		// 刷新缓存
		if err := router.RefreshData(); err != nil {
			router.GetLogger().Warnf("Failed to refresh cache after creating API key: %v", err)
		}

		c.JSON(201, models.NewSuccessResponse("API key created successfully", gin.H{
			"model_id": modelID,
			"key_id":   apiKey.ID,
			"key":      models.MaskAPIKey(req.Key),
		}))
	}
}

// handleDeleteAPIKey 处理删除API Key
func handleDeleteAPIKey(router *core.StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		keyIDStr := c.Param("key_id")
		keyID, err := strconv.ParseUint(keyIDStr, 10, 32)
		if err != nil {
			c.JSON(400, models.NewErrorResponse("Invalid key ID"))
			return
		}

		// 查询并删除API Key
		var apiKey models.APIKey
		if err := router.GetDB().First(&apiKey, keyID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, models.NewErrorResponse("API key not found"))
			} else {
				c.JSON(500, models.NewErrorResponse("Failed to query API key: "+err.Error()))
			}
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

// Helper functions

func getGroupIDs(router *core.StatelessModelRouter) []string {
	groups := router.GetAllModelGroups()
	ids := make([]string, 0, len(groups))
	for groupID := range groups {
		ids = append(ids, groupID)
	}
	return ids
}