package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/models"
		"net/http"
		"net/url"
		"strings"
	
		"github.com/gin-gonic/gin"
	)

// OpenAIAdapter OpenAI 协议（透传模式）
type OpenAIAdapter struct{}

func NewOpenAIAdapter() *OpenAIAdapter {
	return &OpenAIAdapter{}
}

func (a *OpenAIAdapter) ConvertRequest(ctx *gin.Context, originalReq models.ChatCompletionRequest, apiKey string, baseURL string, upstreamModel string) (*http.Request, error) {
	// 关键修复：将请求中的模型名替换为上游识别的名称
	originalReq.Model = upstreamModel
	
	// [Sanitization]
	// If this looks like an image request (has Prompt), ensure Messages is nil
	// so omitempty kicks in. Empty slice [] != nil.
	if originalReq.Prompt != nil {
		originalReq.Messages = nil
	} else if len(originalReq.Messages) == 0 {
		// If both are empty, it's a weird request, but let's default to nil Messages to be safe
		originalReq.Messages = nil
	}

	// 直接序列化原始请求
	reqBodyBytes, err := json.Marshal(originalReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	// 验证 URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream url: %w", err)
	}

	// [Debug] Log the actual payload being sent
	// WARNING: This logs sensitive data (prompt/messages). Disable in production.
	// fmt.Printf("\n[OpenAIAdapter] Upstream: %s\n", u.String())
	// fmt.Printf("[OpenAIAdapter] Prompt Type: %T\n", originalReq.Prompt)
	// fmt.Printf("[OpenAIAdapter] Payload: %s\n\n", string(reqBodyBytes))

	// [Auto-Append Endpoint]
	// Only append /chat/completions if it looks like a base URL.
	// We avoid appending if the user has already specified a specific endpoint (like /v1/images/generations).
	path := u.Path
	if !strings.Contains(path, "/chat/completions") && 
	   !strings.Contains(path, "/images/") && 
	   !strings.Contains(path, "/audio/") && 
	   !strings.Contains(path, "/embeddings") {
		
		// If path is empty, root, or ends in /v1, append chat completions
		if path == "" || path == "/" || strings.HasSuffix(path, "/v1") || strings.HasSuffix(path, "/v1/") {
			basePath := strings.TrimSuffix(path, "/")
			u.Path = basePath + "/chat/completions"
		}
	}

	req, err := http.NewRequestWithContext(ctx.Request.Context(), "POST", u.String(), bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "LLM-Gateway/2.0")

	return req, nil
}

func (a *OpenAIAdapter) HandleResponse(c *gin.Context, resp *http.Response, isStream bool) error {
	// 复制响应头
	for k, v := range resp.Header {
		if k == "Content-Length" || k == "Content-Encoding" || k == "Connection" {
			continue
		}
		for _, val := range v {
			c.Header(k, val)
		}
	}
	
	c.Status(resp.StatusCode)

	// 对于非 200 响应，直接透传 Body
	if resp.StatusCode != 200 {
		_, err := io.Copy(c.Writer, resp.Body)
		return err
	}

	if isStream {
		// 设置 SSE 头
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Writer.Flush()

		// 这里可以复用 ProxyHandler 中的流式处理逻辑，或者直接 Copy
		// 为了简单起见，这里直接 Copy，实际项目中应该调用 shared utility 来处理 DeepSeek reasoning 等特殊逻辑
		_, err := io.Copy(c.Writer, resp.Body)
		return err
	} else {
		// 普通响应直接 Copy
		_, err := io.Copy(c.Writer, resp.Body)
		return err
	}
}