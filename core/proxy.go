package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/models"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// RetryCursor 重试游标 - 完全无状态
type RetryCursor struct {
	GroupID           string
	CurrentModelIndex int    // 当前模型索引
	CurrentKeyIndex   int    // 当前模型内的Key索引
	Strategy          string // 组策略：round_robin 或 fallback
	IsPinned          bool   // 是否锁定模式（禁止切换模型）
}

// NewRetryCursor 创建新的重试��标
func NewRetryCursor(groupID, strategy string) *RetryCursor {
	return &RetryCursor{
		GroupID:           groupID,
		CurrentModelIndex: 0,
		CurrentKeyIndex:   0,
		Strategy:          strategy,
		IsPinned:          false, // 默认不锁定
	}
}

// NewPinnedRetryCursor 创建锁定模式的重试游标
func NewPinnedRetryCursor(groupID string, modelIndex int) *RetryCursor {
	return &RetryCursor{
		GroupID:           groupID,
		CurrentModelIndex: modelIndex,
		CurrentKeyIndex:   0,
		Strategy:          "direct",
		IsPinned:          true, // 锁定模式
	}
}

// AdvanceCursor 推进游标 - 核心故障转移逻辑
func (c *RetryCursor) AdvanceCursor(totalModels, totalKeys int) bool {
	if c.CurrentKeyIndex < totalKeys-1 {
		// 还有更多Key，推进Key索引
		c.CurrentKeyIndex++
		return true // Key推进成功
	} else {
		// Key用完了，需要切换到下一个模型
		if c.IsPinned {
			// 🔒 锁定模式：禁止切换模型，Key用完就返回失败
			c.CurrentKeyIndex = 0 // 重置以便重试（如果需要）
			return false // 告诉调用者无法推进
		} else {
			// 非锁定模式：切换到下一个模型
			if c.CurrentModelIndex < totalModels-1 {
				c.CurrentModelIndex++
				c.CurrentKeyIndex = 0 // 重置Key索引
				return true // 模型切换成功
			} else {
				// 模型也用完了，从头开始
				if c.Strategy == "round_robin" {
					c.CurrentModelIndex = 0
					c.CurrentKeyIndex = 0
					return true // 轮询模式可以重新开始
				}
				return false // 故障转移模式结束
			}
		}
	}
}

// ProxyHandlerStateless 无状态代理处理器
type ProxyHandlerStateless struct {
	router *StatelessModelRouter
	logger *logrus.Logger
	client *http.Client
}

// NewProxyHandlerStateless 创建新的无状态代理处理器
func NewProxyHandlerStateless(router *StatelessModelRouter, logger *logrus.Logger) *ProxyHandlerStateless {
	return &ProxyHandlerStateless{
		router: router,
		logger: logger,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// getClientIP 获取客户端真实IP地址
func getClientIP(c *gin.Context) string {
	// 检查 X-Forwarded-For 头
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For 可能包含多个IP，取第��个
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// 检查 X-Real-IP 头
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// 检查 X-Forwarded 头
	if xf := c.GetHeader("X-Forwarded"); xf != "" {
		return strings.TrimSpace(xf)
	}

	// 使用 RemoteAddr（可能包含端口）
	if ip, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		return ip
	}

	return c.Request.RemoteAddr
}

// ProxyRequest 处理代理请求 - 重构为基于游标的迭代
func (h *ProxyHandlerStateless) ProxyRequest(c *gin.Context, routing *models.RoutingInfo, requestData models.ChatCompletionRequest) {
	startTime := time.Now()
	clientIP := getClientIP(c)

	// 生成请求ID（简单的时间戳 + 随机数）
	requestID := fmt.Sprintf("%d", time.Now().UnixNano())

	h.logger.Infof("🚀 Request: ID=%s | Model=%s | IP=%s", requestID, routing.GroupID, clientIP)

	// 获取模型组信息
	group, err := h.router.GetModelGroup(routing.GroupID)
	if err != nil {
		h.logger.Errorf("Failed to get model group %s: %v", routing.GroupID, err)
		h.sendFinalErrorResponse(c, 404, nil, fmt.Errorf("model group '%s' not found", routing.GroupID))
		return
	}

	maxAttempts := h.router.CalculateMaxRetries(routing.GroupID)

	// 步骤 1: 初始化游标（基于策略）
	var modelCursor, keyCursor int

	// 🔥 新增：预检查是否有可用的模型（避免所有模型都没Key的死循环）
	hasAvailableKeys := false
	for _, model := range group.Models {
		if keys, err := h.router.GetModelKeys(model.ID); err == nil && len(keys) > 0 {
			hasAvailableKeys = true
			break
		}
	}

	if !hasAvailableKeys {
		h.logger.Errorf("💀 ALL MODELS HAVE NO KEYS in group %s", routing.GroupID)
		h.sendFinalErrorResponse(c, 503, nil, fmt.Errorf("no models in group '%s' have API keys configured", routing.GroupID))
		return
	}
	if routing.IsPinned && routing.ModelIndex != nil {
		// 🔒 锁定模式：使用指定模型
		if *routing.ModelIndex >= 0 && *routing.ModelIndex < len(group.Models) {
			modelCursor = *routing.ModelIndex
			keyCursor = 0
			h.logger.Infof("PROXY: Using pinned model index %d", *routing.ModelIndex)
		} else {
			h.logger.Errorf("PROXY: Invalid pinned model index %d for group %s (total: %d)",
				*routing.ModelIndex, routing.GroupID, len(group.Models))
			h.sendFinalErrorResponse(c, 400, nil,
				fmt.Errorf("model index %d out of bounds for group '%s'", *routing.ModelIndex, routing.GroupID))
			return
		}
	} else {
		// 策略模式：根据策略获取初始索引
		modelCursor = h.router.GetInitialModelIndex(routing.GroupID)
		// 对于 Key，我们需要根据当前模型获取初始索引
		if len(group.Models) > 0 {
			initialModel := group.Models[modelCursor%len(group.Models)]
			// 获取当前组的计数器来计算 Key 索引
			keyCursor = h.router.GetInitialKeyIndex(initialModel.ID)
		}
		}

	// 步骤 2: 基于游标的迭代循环
	for attempt := 0; attempt < maxAttempts; attempt++ {

		// 步骤 3: 选择模型（基于游标）
		selectedModelIndex := modelCursor % len(group.Models)
		selectedModel := group.Models[selectedModelIndex]

		// 获取模型的 API Keys
		modelKeys, err := h.router.GetModelKeys(selectedModel.ID)
		if err != nil {
			h.logger.Errorf("PROXY: Failed to get keys for model %s: %v", selectedModel.ProviderName, err)
			// 推进游标并继续
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), 0, routing.IsPinned, group.Strategy)
			continue
		}

		if len(modelKeys) == 0 {
			h.logger.Infof("⏭️ Skipping model [%s] (No keys available)", selectedModel.ProviderName)

			// 关键修复：处理无Key模型的情况
			if routing.IsPinned {
				// 🔒 如果是定向路由且没Key，直接报错退出
				h.sendFinalErrorResponse(c, 503, nil, fmt.Errorf("pinned model %s has no API keys configured", selectedModel.ProviderName))
				return
			}

			// 如果是普通/轮询模式，直接跳到下一个模型（绕过advanceCursors）
			// 🔥 防止无限循环：检查是否已经遍历过所有模型
			originalModelIndex := modelCursor
			for {
				modelCursor = (modelCursor + 1) % len(group.Models)
				keyCursor = 0

				// 找到有Key的模型就停止
				if nextKeys, err := h.router.GetModelKeys(group.Models[modelCursor].ID); err == nil && len(nextKeys) > 0 {
					break
				}

				// 如果又回到原点，说明所有模型都没Key（理论上不会触发，因为前面有预检查）
				if modelCursor == originalModelIndex {
					h.sendFinalErrorResponse(c, 503, nil, fmt.Errorf("no available models with API keys"))
					return
				}
			}

			// 立即进入下一次循环
			continue
		}

		// 步骤 4: 选择 Key（基于游标）
		selectedKeyIndex := keyCursor % len(modelKeys)
		selectedKey := modelKeys[selectedKeyIndex]

		// 规范化URL
		originalURL := selectedModel.UpstreamURL
		targetURL := h.normalizeURL(originalURL)

		h.logger.Infof("🎯 Attempt %d/%d: Using [%s] (Key: %s) -> %s",
			attempt+1, maxAttempts, selectedModel.UpstreamModel, maskKey(selectedKey), targetURL)

		// 步骤 5: 执行请求
		requestData.Model = selectedModel.UpstreamModel
		reqBodyBytes, err := json.Marshal(requestData)
		if err != nil {
			h.logger.Errorf("PROXY: Failed to marshal request: %v", err)
			// 推进游标并继续
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			continue
		}

		// 发送 HTTP 请求
		ctx, cancel := h.router.ContextTimeout(time.Duration(selectedModel.Timeout) * time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBuffer(reqBodyBytes))
		if err != nil {
			h.logger.Errorf("PROXY: Failed to create request: %v", err)
			// 推进游标并继续
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			continue
		}

		// 设置请求头
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+selectedKey)
		req.Header.Set("User-Agent", "LLM-Gateway/2.0")

		// 发送请求
		resp, err := h.client.Do(req)
		latency := time.Since(startTime).Seconds() * 1000 // ms

		if err != nil {
			h.logger.Warnf("⚠️ Attempt %d Failed: Network error - %v", attempt+1, err)
			h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			continue
		}

		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			// 成功！
			h.router.UpdateStats(routing.GroupID, selectedModelIndex, true, latency)
			h.logger.Infof("✅ Success: [%s] | Status: 200 | Latency: %.0fms", selectedModel.UpstreamModel, latency)

			// 复制响应头
			for k, v := range resp.Header {
				for _, val := range v {
					c.Header(k, val)
				}
			}
			c.Status(resp.StatusCode)
			io.Copy(c.Writer, resp.Body)
			return
		} else {
			// 失败，记录错误
			h.router.UpdateStats(routing.GroupID, selectedModelIndex, false, latency)

			// 读取错误信息
			errorBody, _ := io.ReadAll(resp.Body)
			errorText := string(errorBody)
			if len(errorText) > 200 {
				errorText = errorText[:200]
			}

			// 🔥 模型熔断：智能错误判断
			if h.router.IsHardError(resp.StatusCode, nil) {
				h.logger.Warnf("❌ Attempt %d Failed: %d %s (Hard Error) - skipping model", attempt+1, resp.StatusCode, getHTTPStatusText(resp.StatusCode))
				h.skipToNextModel(&modelCursor, &keyCursor, len(group.Models), routing.IsPinned, group.Strategy)
			} else if h.router.IsAuthError(resp.StatusCode) {
				h.logger.Warnf("⚠️ Attempt %d Failed: %d %s (Auth Error) - retrying...", attempt+1, resp.StatusCode, getHTTPStatusText(resp.StatusCode))
				h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			} else if h.router.IsServerError(resp.StatusCode) {
				h.logger.Warnf("⚠️ Attempt %d Failed: %d %s (Server Error) - switching model", attempt+1, resp.StatusCode, getHTTPStatusText(resp.StatusCode))
				h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			} else {
				h.logger.Warnf("⚠️ Attempt %d Failed: %d %s - retrying...", attempt+1, resp.StatusCode, getHTTPStatusText(resp.StatusCode))
				h.advanceCursors(&modelCursor, &keyCursor, len(group.Models), len(modelKeys), routing.IsPinned, group.Strategy)
			}
	}
}

	// 所有尝试都失败了
	h.logger.Errorf("💀 Failed: All %d attempts exhausted", maxAttempts)
	h.sendFinalErrorResponse(c, 502, nil, fmt.Errorf("all models unavailable after %d attempts", maxAttempts))
}

// advanceCursors 推进游标的统一逻辑（基于您的优化思路）
func (h *ProxyHandlerStateless) advanceCursors(modelCursor, keyCursor *int, totalModels, totalKeys int, isPinned bool, strategy string) bool {
	// 边界检查
	if totalKeys == 0 {
		h.logger.Warn("No keys available, cannot advance cursor")
		return false
	}

	// 1. 优先尝试切换 Key（Pinned 和 Normal 模式逻辑一致）
	if *keyCursor < totalKeys-1 {
		// 还有 Key 没试完，移动到下一个 Key
		*keyCursor++
		h.logger.Infof("🔄 Advancing to next key %d/%d", *keyCursor+1, totalKeys)
		return true // 继续重试
	}

	// 2. Key 用完了，判断是否允许切换模型
	if isPinned {
		// 🔒 Pinned 模式：Key 用完了，不允许切模型 -> 彻底失败
		h.logger.Warn("🔒 Pinned model exhausted all keys. Stopping.")
		return false // 退出循环，返回错误
	}

	// 3. Normal 模式：Key 用完了，切换到下一个模型
	if totalModels == 0 {
		h.logger.Warn("No models available for switching")
		return false
	}

	*modelCursor++
	*keyCursor = 0 // 重置 Key 索引

	h.logger.Infof("🔄 Switched to next model %d/%d, reset key index to 0", *modelCursor+1, totalModels)

	// 检查 modelCursor 是否越界
	if *modelCursor < totalModels {
		return true
	} else if strategy == "round_robin" {
		// 轮询模式可以重新开始
		*modelCursor = 0
		h.logger.Infof("🔄 Round-robin: wrapped around to first model")
		return true
	}

	h.logger.Warn("No more models available after exhausting all options")
	return false
}

// 🔥 skipToNextModel 模型熔断：直接跳到下一个模型，跳过剩余的 Keys
func (h *ProxyHandlerStateless) skipToNextModel(modelCursor, keyCursor *int, totalModels int, isPinned bool, strategy string) bool {
	if isPinned {
		// 锁定模式：不能跳模型，返回失败
		h.logger.Warn("🔒 Cannot skip model in pinned mode")
		return false
	}

	h.logger.Infof("🔥 Circuit breaker triggered: skipping to next model (was at model %d)", *modelCursor)

	// 直接跳到下一个模型，重置 Key 游标
	*modelCursor++
	*keyCursor = 0

	if *modelCursor < totalModels {
		return true
	} else if strategy == "round_robin" {
		// 轮询模式可以重新开始
		*modelCursor = 0
		h.logger.Infof("🔄 Round-robin: wrapped around to first model")
		return true
	}

	h.logger.Warn("🔥 No more models available after circuit breaker")
	return false
}

// sendFinalErrorResponse 发送最终错误响应
func (h *ProxyHandlerStateless) sendFinalErrorResponse(c *gin.Context, statusCode int, resp *http.Response, err error) {
	if resp != nil {
		// 如果有上游响应，尝试转发
		for k, v := range resp.Header {
			for _, val := range v {
				c.Header(k, val)
			}
		}
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
		resp.Body.Close()
		return
	}

	// 否则发送标准错误响应
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": err.Error(),
			"type":    "service_unavailable",
		},
	})
}

// maskKey 脱敏 API Key
func maskKey(key string) string {
	if len(key) <= 4 {
		return key
	}
	return key[:3] + "***" + key[len(key)-4:]
}

// getHTTPStatusText 获取HTTP状态码的描述文本
func getHTTPStatusText(statusCode int) string {
	switch statusCode {
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	case 504:
		return "Gateway Timeout"
	default:
		return fmt.Sprintf("HTTP %d", statusCode)
	}
}

// normalizeURL 仅做基本清理，不进行自动拼接，完全信任用户配置的完整 URL
func (h *ProxyHandlerStateless) normalizeURL(originalURL string) string {
	return strings.TrimSpace(originalURL)
}

// handleStreamingRequestStateless 处理流式请求
func (h *ProxyHandlerStateless) handleStreamingRequestStateless(
	c *gin.Context,
	routing *models.RoutingInfo,
	requestData models.ChatCompletionRequest,
	startTime time.Time,
	maxAttempts int,
) {
	h.logger.Infof("=== PROXY STREAMING REQUEST START ===")

	// 流式请求的实现类似于普通请求，但使用 WebSocket 或 Server-Sent Events
	// 这里暂时简化，复用普通请求的逻辑
	h.ProxyRequest(c, routing, requestData)
}

// HandleProxyRequest 适配器函数，符合 gin.HandlerFunc 接口
func (h *ProxyHandlerStateless) HandleProxyRequest(router *StatelessModelRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析请求体
		var requestData models.ChatCompletionRequest
		if err := c.ShouldBindJSON(&requestData); err != nil {
			c.JSON(400, gin.H{
				"error": gin.H{
					"message": "Invalid request body: " + err.Error(),
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// 解析路由信息
		routing := h.router.ParseModelRouting(requestData.Model)

		// 调用实际的代理处理函数
		h.ProxyRequest(c, routing, requestData)
	}
}