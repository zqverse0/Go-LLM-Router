package adapter

import (
	"llm-gateway/models"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ProviderAdapter 定义不同 LLM 提供商的适配接口
type ProviderAdapter interface {
	// ConvertRequest 将标准请求转换为特定提供商的 HTTP 请求
	// 增加了 upstreamModel 参数，确保转发给上游的是正确的模型名称
	ConvertRequest(ctx *gin.Context, originalReq models.ChatCompletionRequest, apiKey string, url string, upstreamModel string) (*http.Request, error)

	// HandleResponse 处理上游响应并将其转换为标准响应（支持流式和非流式）
	HandleResponse(c *gin.Context, resp *http.Response, isStream bool) error
}

// StreamScanner 通用流式解析接口 (Task 3)
// 用于统一处理 OpenAI 和 Gemini 的 SSE 响应
type StreamScanner interface {
	Scan() bool
	Bytes() []byte
	Err() error
}
