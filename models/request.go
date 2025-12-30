package models

import (
	"encoding/json"
	"strings"
	"time"
)

// ChatCompletionRequest OpenAI 聊天请求
type ChatCompletionRequest struct {
	Model            string                 `json:"model" binding:"required"`
	Messages         []ChatMessage          `json:"messages" binding:"required"`
	Stream           bool                   `json:"stream,omitempty"`
	Temperature      *float64               `json:"temperature,omitempty"`
	TopP             *float64               `json:"top_p,omitempty"`
	N                *int                   `json:"n,omitempty"`
	StreamOptions    *StreamOptions         `json:"stream_options,omitempty"`
	Stop             interface{}            `json:"stop,omitempty"`
	MaxTokens        *int                   `json:"max_tokens,omitempty"`
	PresencePenalty  *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64               `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]interface{} `json:"logit_bias,omitempty"`
	User             string                 `json:"user,omitempty"`
	Seed             *int                   `json:"seed,omitempty"`
	Tools            []ChatTool             `json:"tools,omitempty"`
	ToolChoice       interface{}            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                 `json:"parallel_tool_calls,omitempty"`
	ResponseFormat   *ResponseFormat        `json:"response_format,omitempty"`
}

// ChatMessage 聊天消息
type ChatMessage struct {
	Role             string        `json:"role,omitempty" binding:"required,oneof=system user assistant tool"`
	Content          interface{}   `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"` // For DeepSeek reasoning models
	Name             string        `json:"name,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"`
}

// ChatTool 工具定义
type ChatTool struct {
	Type     string           `json:"type" binding:"required,oneof=function"`
	Function ChatToolFunction `json:"function"`
}

// ChatToolFunction 工具函数
type ChatToolFunction struct {
	Description string                 `json:"description,omitempty"`
	Name        string                 `json:"name" binding:"required"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Strict      *bool                  `json:"strict,omitempty"`
}

// ChatToolCall 工具调用
type ChatToolCall struct {
	Index    *int              `json:"index,omitempty"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type" binding:"required,oneof=function"`
	Function ChatToolCallFunc  `json:"function"`
}

// ChatToolCallFunc 工具调用函数
type ChatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamOptions 流式选项
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ResponseFormat 响应格式
type ResponseFormat struct {
	Type       string `json:"type" binding:"required,oneof=text json_object json_schema"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

// JSONSchema JSON Schema
type JSONSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Strict      *bool                  `json:"strict,omitempty"`
	Schema      map[string]interface{} `json:"schema"`
}

// ChatCompletionResponse OpenAI 聊天响应
type ChatCompletionResponse struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []ChatCompletionChoice  `json:"choices"`
	Usage   *ChatCompletionUsage    `json:"usage,omitempty"`
	SystemFingerprint string        `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChoice 聊天选择
type ChatCompletionChoice struct {
	Index        int              `json:"index"`
	Message      ChatMessage      `json:"message,omitempty"`
	Delta        ChatMessage      `json:"delta,omitempty"`
	FinishReason string           `json:"finish_reason,omitempty"`
	Logprobs     interface{}      `json:"logprobs,omitempty"`
}

// ChatCompletionUsage 使用统计
type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 错误详情
type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

// HealthResponse 健康检查响应
type HealthResponse struct {
	Status      string   `json:"status"`
	Gateway     string   `json:"gateway"`
	ModelGroups []string `json:"model_groups"`
	Timestamp   int64    `json:"timestamp"`
}

// AdminStatsResponse 管理员统计响应
type AdminStatsResponse struct {
	GroupID    string                 `json:"group_id"`
	Strategy   string                 `json:"strategy"`
	Models     []AdminModelStats      `json:"models"`
	TotalRequests int                 `json:"total_requests"`
	Timestamp  int64                  `json:"timestamp"`
}

// AdminModelStats 管理员模型统计
type AdminModelStats struct {
	Index         int     `json:"index"`
	Provider      string  `json:"provider"`
	UpstreamModel string  `json:"upstream_model"`
	Success       int     `json:"success"`
	Error         int     `json:"error"`
	AvgLatency    float64 `json:"avg_latency"`
	TotalRequests int     `json:"total_requests"`
}

// CreateModelGroupRequest 创建模型组请求
type CreateModelGroupRequest struct {
	GroupID  string           `json:"group_id" binding:"required"`
	Strategy string           `json:"strategy" binding:"oneof=fallback round_robin"`
	Models   []CreateModelRequest `json:"models"`
}

// CreateModelRequest 创建模型请求
type CreateModelRequest struct {
	ProviderName  string   `json:"provider_name" binding:"required"`
	UpstreamURL   string   `json:"upstream_url" binding:"required,url"`
	UpstreamModel string   `json:"upstream_model" binding:"required"`
	Keys          []string `json:"keys" binding:"required,min=1"`
	Timeout       int      `json:"timeout" binding:"min=1,max=300"`
}

// UpdateModelGroupRequest 更新模型组请求
type UpdateModelGroupRequest struct {
	Strategy *string `json:"strategy" binding:"omitempty,oneof=fallback round_robin"`
}

// UpdateModelRequest 更新模型请求
type UpdateModelRequest struct {
	ProviderName  *string  `json:"provider_name"`
	UpstreamURL   *string  `json:"upstream_url"`
	UpstreamModel *string  `json:"upstream_model"`
	Keys          []string `json:"keys"`
	Timeout       *int     `json:"timeout" binding:"omitempty,min=1,max=300"`
}

// APIResponse 通用API响应
type APIResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// MaskAPIKey 脱敏API Key
func MaskAPIKey(key string) string {
	if key == "" {
		return "***"
	}

	if len(key) <= 4 {
		return key[:1] + "***"
	}

	if len(key) <= 8 {
		return key[:2] + "***" + key[len(key)-2:]
	}

	return key[:3] + "***" + key[len(key)-4:]
}

// NewSuccessResponse 创建成功响应
func NewSuccessResponse(message string, data interface{}) *APIResponse {
	return &APIResponse{
		Success:   true,
		Message:   message,
		Data:      data,
		Timestamp: time.Now().Unix(),
	}
}

// NewErrorResponse 创建错误响应
func NewErrorResponse(message string) *APIResponse {
	return &APIResponse{
		Success:   false,
		Message:   message,
		Timestamp: time.Now().Unix(),
	}
}

// StringContent 从ChatMessage.Content提取字符串内容
// 支持普通字符串和多模态数组格式
func (m *ChatMessage) StringContent() string {
	if m.Content == nil {
		return ""
	}

	// 情况1: 直接是字符串
	if str, ok := m.Content.(string); ok {
		return str
	}

	// 情况2: 多模态数组格式 [{"type": "text", "text": "..."}, ...]
	if arr, ok := m.Content.([]interface{}); ok {
		var result strings.Builder
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				// 查找type="text"的项
				if itemType, exists := itemMap["type"]; exists && itemType == "text" {
					if text, exists := itemMap["text"]; exists {
						if textStr, ok := text.(string); ok {
							if result.Len() > 0 {
								result.WriteString(" ")
							}
							result.WriteString(textStr)
						}
					}
				}
			}
		}
		return result.String()
	}

	// 情况3: 其他类型，尝试转换为JSON字符串
	if jsonBytes, err := json.Marshal(m.Content); err == nil {
		return string(jsonBytes)
	}

	return ""
}
