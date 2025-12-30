package adapter

import (
	"llm-gateway/models"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ProviderAdapter 定义不同 LLM 提供商的适配接口
type ProviderAdapter interface {
	// ConvertRequest 将标准请求转换为特定提供商的 HTTP 请求
	ConvertRequest(ctx *gin.Context, originalReq models.ChatCompletionRequest, apiKey string, url string) (*http.Request, error)

	// HandleResponse 处理上游响应并将其转换为标准响应（支持流式和非流式）
	HandleResponse(c *gin.Context, resp *http.Response, isStream bool) error
}
