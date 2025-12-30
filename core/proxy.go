package core

import (
	"fmt"
	"llm-gateway/core/adapter"
	"llm-gateway/models"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const (
	MaxRetries = 3
)

// ProxyHandler 代理请求处理器
type ProxyHandler struct {
	lb          *LoadBalancer
	httpClient  *http.Client
	logger      *logrus.Logger
	asyncLogger *AsyncRequestLogger
}

// NewProxyHandler 创建新的代理处理器
func NewProxyHandler(lb *LoadBalancer, client *http.Client, logger *logrus.Logger, asyncLogger *AsyncRequestLogger) *ProxyHandler {
	return &ProxyHandler{
		lb:          lb,
		httpClient:  client,
		logger:      logger,
		asyncLogger: asyncLogger,
	}
}

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
	return c.ClientIP()
}

func (h *ProxyHandler) getAdapter(provider string) adapter.ProviderAdapter {
	switch strings.ToLower(provider) {
	case "gemini":
		return adapter.NewGeminiAdapter()
	case "claude", "anthropic":
		return adapter.NewClaudeAdapter()
	default:
		return adapter.NewOpenAIAdapter()
	}
}

// HandleProxyRequest 返回 Gin 处理函数
func (h *ProxyHandler) HandleProxyRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req models.ChatCompletionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		// 将路由和执行逻辑下沉到 ProxyRequest 中
		h.ProxyRequest(c, req)
	}
}

// ProxyRequest 处理代理请求 (包含重试逻辑)
func (h *ProxyHandler) ProxyRequest(c *gin.Context, requestData models.ChatCompletionRequest) {
	startTime := time.Now()
	clientIP := getClientIP(c)
	requestID := fmt.Sprintf("req_%d", startTime.UnixNano())

	var lastErr error
	var finalRespStatusCode int
	var routing *models.RoutingInfo
	
	// --- 重试循环 ---
	for i := 0; i < MaxRetries; i++ {
		// 1. 获取路由 (每次重试都重新获取，以避开已标记为 Cooldown 的 Key)
		var err error
		routing, err = h.lb.Route(requestData.Model)
		if err != nil {
			// 如果连路由都找不到（比如所有 Key 都挂了），直接退出
			h.logger.Warnf("[Attempt %d] Routing failed: %v", i+1, err)
			lastErr = err
			break
		}

		h.logger.Infof("[Attempt %d] Selected upstream: %s (%s) | Key: ...%s", 
			i+1, routing.UpstreamURL, routing.UpstreamModel,  safeKeyMask(routing.APIKey))

		// 2. 获取适配器
		adp := h.getAdapter(routing.Provider)
		
		// 3. 转换请求
		req, err := adp.ConvertRequest(c, requestData, routing.APIKey, routing.UpstreamURL, routing.UpstreamModel)
		if err != nil {
			h.logger.Errorf("Request conversion failed: %v", err)
			c.JSON(500, gin.H{"error": "Internal Adapter Error"})
			return // 内部错误不重试
		}

		// 4. 发起请求
		resp, err := h.httpClient.Do(req)
		
		// --- 错误处理与状态反馈 ---
		if err != nil {
			// 网络层面错误 (DNS, Timeout, Refused)
			h.logger.Warnf("Upstream network error: %v", err)
			h.lb.keyManager.MarkCooldown(routing.APIKey, 10*time.Second) // 短暂冷却
			lastErr = err
			continue // 立即重试
		}
		
		finalRespStatusCode = resp.StatusCode

		// 429 Too Many Requests
		if resp.StatusCode == 429 {
			resp.Body.Close()
			h.logger.Warnf("Upstream 429 (Rate Limit). Marking key cooldown.")
			h.lb.keyManager.MarkCooldown(routing.APIKey, 60*time.Second) // 标准冷却
			lastErr = fmt.Errorf("upstream rate limit (429)")
			continue // 重试
		}

		// 401/403 Auth Error
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			resp.Body.Close()
			h.logger.Errorf("Upstream Auth Error (%d). Marking key dead.", resp.StatusCode)
			h.lb.keyManager.MarkDead(routing.APIKey) // 永久拉黑
			lastErr = fmt.Errorf("upstream auth error (%d)", resp.StatusCode)
			continue // 重试
		}

		// 5xx Server Error (Optional: 可以选择重试)
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			h.logger.Warnf("Upstream Server Error (%d).", resp.StatusCode)
			h.lb.keyManager.MarkCooldown(routing.APIKey, 30*time.Second) // 避开故障节点
			lastErr = fmt.Errorf("upstream server error (%d)", resp.StatusCode)
			continue // 重试
		}

		// --- 成功 (200 OK 或其他非重试状态码) ---
		defer resp.Body.Close()
		
		// 处理响应
		err = adp.HandleResponse(c, resp, requestData.Stream)
		if err != nil {
			h.logger.Errorf("Failed to handle response: %v", err)
		}
		
		// 记录日志并退出
		recordLog(h.asyncLogger, requestID, c, clientIP, startTime, routing, resp.StatusCode)
		return
	}

	// --- 重试耗尽 ---
	h.logger.Errorf("All %d retries failed. Last error: %v", MaxRetries, lastErr)
	c.JSON(502, gin.H{
		"error": fmt.Sprintf("Upstream unavailable after %d retries. Last error: %v", MaxRetries, lastErr),
	})
	
	// 记录失败日志
	recordLog(h.asyncLogger, requestID, c, clientIP, startTime, routing, finalRespStatusCode)
}

func safeKeyMask(k string) string {
	if len(k) < 8 {
		return "***"
	}
	return k[len(k)-4:]
}

func recordLog(logger *AsyncRequestLogger, reqID string, c *gin.Context, ip string, start time.Time, routing *models.RoutingInfo, status int) {
	if logger == nil {
		return
	}
	group := ""
	model := ""
	provider := ""
	if routing != nil {
		group = routing.GroupID
		model = routing.UpstreamModel
		provider = routing.Provider
	}
	
	logger.Log(&models.RequestLog{
		RequestID:        reqID,
		CreatedAt:        start,
		Method:           c.Request.Method,
		Path:             c.Request.URL.Path,
		StatusCode:       status,
		Duration:         time.Since(start).Milliseconds(),
		IP:               ip,
		ModelGroup:       group,
		UsedModel:        model,
		Provider:         provider,
		UserAgent:        c.Request.UserAgent(),
	})
}