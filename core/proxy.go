package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/core/adapter"
	"llm-gateway/models"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// ProxyHandlerStateless 无状态代理处理器
type ProxyHandlerStateless struct {
	router      *StatelessModelRouter
	logger      *logrus.Logger
	asyncLogger *AsyncRequestLogger // 引入异步日志记录器
}

// NewProxyHandlerStateless 创建新的无状态代理处理器
func NewProxyHandlerStateless(router *StatelessModelRouter, logger *logrus.Logger, asyncLogger *AsyncRequestLogger) *ProxyHandlerStateless {
	// 确保全局客户端已初始化
	if GlobalHTTPClient == nil {
		InitHTTPClient()
	}

	return &ProxyHandlerStateless{
		router:      router,
		logger:      logger,
		asyncLogger: asyncLogger,
	}
}

// getClientIP 获取客户端真实IP地址
func getClientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	if ip, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		return ip
	}
	return c.Request.RemoteAddr
}

// getAdapter 根据提供商获取适配器
func (h *ProxyHandlerStateless) getAdapter(provider string) adapter.ProviderAdapter {
	switch strings.ToLower(provider) {
	case "gemini":
		return adapter.NewGeminiAdapter()
	case "openai":
		return adapter.NewOpenAIAdapter()
	default:
		// 默认使用 OpenAI 透传
		return adapter.NewOpenAIAdapter()
	}
}

// ProxyRequest 处理代理请求 - 重构为使用 Adapter 和 GlobalClient
func (h *ProxyHandlerStateless) ProxyRequest(c *gin.Context, routing *models.RoutingInfo, requestData models.ChatCompletionRequest) {
	startTime := time.Now()
	clientIP := getClientIP(c)
	requestID := fmt.Sprintf("%d", startTime.UnixNano())

	// 准备日志对象
	reqLog := &models.RequestLog{
		RequestID:  requestID,
		Time:       startTime,
		Method:     c.Request.Method,
		Path:       c.Request.URL.Path,
		ClientIP:   clientIP,
		ModelGroup: routing.GroupID,
	}

	h.logger.Infof("🚀 Request: ID=%s | Model=%s | IP=%s | Stream=%v", requestID, routing.GroupID, clientIP, requestData.Stream)

	group, err := h.router.GetModelGroup(routing.GroupID)
	if err != nil {
		h.logger.Errorf("Failed to get model group %s: %v", routing.GroupID, err)
		h.sendFinalErrorResponse(c, 404, nil, fmt.Errorf("model group '%s' not found", routing.GroupID))
		return
	}

	maxAttempts := h.router.CalculateMaxRetries(routing.GroupID)
	
	// 游标初始化逻辑...
	var modelCursor, keyCursor int
	hasAvailableKeys := false
	for _, model := range group.Models {
		if keys, err := h.router.GetModelKeys(model.ID); err == nil && len(keys) > 0 {
			hasAvailableKeys = true
			break
		}
	}
	if !hasAvailableKeys {
		h.sendFinalErrorResponse(c, 503, nil, fmt.Errorf("no models in group '%s' have API keys configured", routing.GroupID))
		return
	}

	if routing.IsPinned && routing.ModelIndex != nil {
		if *routing.ModelIndex >= 0 && *routing.ModelIndex < len(group.Models) {
			modelCursor = *routing.ModelIndex
			keyCursor = 0
		} else {
			h.sendFinalErrorResponse(c, 400, nil, fmt.Errorf("model index out of bounds"))
			return
		}
	} else {
		modelCursor = h.router.GetInitialModelIndex(routing.GroupID)
		if len(group.Models) > 0 {
			initialModel := group.Models[modelCursor%len(group.Models)]
			keyCursor = h.router.GetInitialKeyIndex(initialModel.ID)
		}
	}

	// 核心循环
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selectedModelIndex := modelCursor % len(group.Models)
		selectedModel := group.Models[selectedModelIndex]

		modelKeys, err := h.router.GetModelKeys(selectedModel.ID)
		if err != nil || len(modelKeys) == 0 {
			if routing.IsPinned {
				h.sendFinalErrorResponse(c, 503, nil, fmt.Errorf("pinned model has no keys"))
				return
			}
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), 0, routing.IsPinned, group.Strategy)
			continue
		}

		selectedKeyIndex := keyCursor % len(modelKeys)
		selectedKey := modelKeys[selectedKeyIndex]
		targetURL := strings.TrimSpace(selectedModel.UpstreamURL)

		h.logger.Infof("🎯 Attempt %d/%d: Using [%s] (%s) -> %s", attempt+1, maxAttempts, selectedModel.UpstreamModel, selectedModel.ProviderName, targetURL)

		// 【Task C】 使用适配器创建请求
		providerAdapter := h.getAdapter(selectedModel.ProviderName)
		req, err := providerAdapter.ConvertRequest(c, requestData, selectedKey, targetURL)
		
		if err != nil {
			h.logger.Errorf("Adapter conversion failed: %v", err)
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			continue
		}

		// 【Task A】 使用全局 Client
		resp, err := GlobalHTTPClient.Do(req)
		latency := time.Since(startTime).Seconds() * 1000 // ms

		if err != nil {
			h.logger.Warnf("⚠️ Attempt %d Failed: Network error - %v", attempt+1, err)
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			continue
		}

		// 填充日志
		reqLog.ModelID = selectedModel.UpstreamModel
		reqLog.Provider = selectedModel.ProviderName
		reqLog.LatencyMs = latency
		reqLog.Status = resp.StatusCode

		if resp.StatusCode == 200 {
			// 成功
			h.router.UpdateStats(routing.GroupID, selectedModelIndex, true, latency)
			
			// 【Task C】 使用适配器处理响应
			if err := providerAdapter.HandleResponse(c, resp, requestData.Stream); err != nil {
				h.logger.Errorf("Response handling failed: %v", err)
			}
			
			resp.Body.Close()
			
			// 【Task B】 异步记录日志
			if h.asyncLogger != nil {
				h.asyncLogger.Log(reqLog)
			}
			return
		} else {
			// 失败
			h.router.UpdateStats(routing.GroupID, selectedModelIndex, false, latency)
			
			// 读取错误并记录
			errorBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			reqLog.ErrorMsg = string(errorBody)

			if h.router.IsHardError(resp.StatusCode, nil) {
				h.skipToNextModel(&modelCursor, &keyCursor, len(group.Models), routing.IsPinned, group.Strategy)
			} else {
				h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			}
		}
	}

	h.sendFinalErrorResponse(c, 502, nil, fmt.Errorf("all attempts failed"))
	
	// 记录最终失败日志
	reqLog.Status = 502
	reqLog.ErrorMsg = "All attempts failed"
	if h.asyncLogger != nil {
		h.asyncLogger.Log(reqLog)
	}
}

// sendFinalErrorResponse (复用原有逻辑，略微简化)
func (h *ProxyHandlerStateless) sendFinalErrorResponse(c *gin.Context, statusCode int, resp *http.Response, err error) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": err.Error(),
			"type":    "service_unavailable",
		},
	})
}

// advanceCursors (复用原有逻辑)
func (h *ProxyHandlerStateless) advanceCursors(modelCursor, keyCursor *int, totalModels, totalKeys int, isPinned bool, strategy string) bool {
	if *keyCursor < totalKeys-1 {
		*keyCursor++
		return true
	}
	if isPinned {
		return false
	}
	*modelCursor++
	*keyCursor = 0
	return *modelCursor < totalModels || strategy == "round_robin"
}

// skipToNextModel (复用原有逻辑)
func (h *ProxyHandlerStateless) skipToNextModel(modelCursor, keyCursor *int, totalModels int, isPinned bool, strategy string) bool {
	if isPinned {
		return false
	}
	*modelCursor++
	*keyCursor = 0
	return *modelCursor < totalModels || strategy == "round_robin"
}

// HandleProxyRequest (Gin Handler wrapper)
func (h *ProxyHandlerStateless) HandleProxyRequest(router *StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var requestData models.ChatCompletionRequest
		if err := c.ShouldBindJSON(&requestData); err != nil {
			c.JSON(400, gin.H{"error": "Invalid JSON"})
			return
		}
		routing := h.router.ParseModelRouting(requestData.Model)
		h.ProxyRequest(c, routing, requestData)
	}
}