package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/models"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// OpenAIAdapter OpenAI 协议（透传模式）
type OpenAIAdapter struct{}

func NewOpenAIAdapter() *OpenAIAdapter {
	return &OpenAIAdapter{}
}

func (a *OpenAIAdapter) ConvertRequest(ctx *gin.Context, originalReq models.ChatCompletionRequest, apiKey string, url string) (*http.Request, error) {
	// 直接序列化原始请求
	reqBodyBytes, err := json.Marshal(originalReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.Request.Context(), "POST", url, bytes.NewBuffer(reqBodyBytes))
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
