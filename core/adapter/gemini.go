package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/models"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Gemini Request Structures
type GeminiRequest struct {
	Contents          []GeminiContent `json:"contents"`
	SystemInstruction *GeminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiConfig   `json:"generationConfig,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *GeminiInlineData `json:"inline_data,omitempty"`
}

type GeminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type GeminiConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	TopK            int      `json:"topK,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// Gemini Response Structures
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

// GeminiAdapter Google Gemini 协议适配器
type GeminiAdapter struct{}

func NewGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{}
}

// ConvertRequest 将 OpenAI 请求转换为 Gemini 请求
func (a *GeminiAdapter) ConvertRequest(ctx *gin.Context, originalReq models.ChatCompletionRequest, apiKey string, baseURL string, upstreamModel string) (*http.Request, error) {
	geminiReq := GeminiRequest{
		Contents: make([]GeminiContent, 0),
	}

	// 1. 转换 Messages
	for _, msg := range originalReq.Messages {
		if msg.Role == "system" {
			// System message -> systemInstruction
			geminiReq.SystemInstruction = &GeminiContent{
				Parts: []GeminiPart{{Text: msg.StringContent()}},
			}
			continue
		}

		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}

		content := GeminiContent{
			Role:  role,
			Parts: make([]GeminiPart, 0),
		}

		// 处理多模态内容 (Text & Image)
		if strContent, ok := msg.Content.(string); ok {
			content.Parts = append(content.Parts, GeminiPart{Text: strContent})
		} else if listContent, ok := msg.Content.([]interface{}); ok {
			for _, item := range listContent {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				typeVal, _ := itemMap["type"].(string)
				if typeVal == "text" {
					if textVal, ok := itemMap["text"].(string); ok {
						content.Parts = append(content.Parts, GeminiPart{Text: textVal})
					}
				} else if typeVal == "image_url" {
					// 提取 Base64 图片
					if imageUrlMap, ok := itemMap["image_url"].(map[string]interface{}); ok {
						if urlVal, ok := imageUrlMap["url"].(string); ok {
							if strings.HasPrefix(urlVal, "data:") {
								// data:image/png;base64,xxxxxx
								parts := strings.Split(urlVal, ",")
								if len(parts) == 2 {
									mimeType := strings.TrimSuffix(strings.TrimPrefix(parts[0], "data:"), ";base64")
									data := parts[1]
									content.Parts = append(content.Parts, GeminiPart{
										InlineData: &GeminiInlineData{
											MimeType: mimeType,
											Data:     data,
										},
									})
								}
							} else {
								// TODO: 处理远程 URL (需要下载或使用 Vertex AI 格式)
								// 暂时仅支持 Base64
							}
						}
					}
				}
			}
		}

		geminiReq.Contents = append(geminiReq.Contents, content)
	}

	// 2. 转换配置参数
	config := &GeminiConfig{}
	if originalReq.Temperature != nil {
		config.Temperature = *originalReq.Temperature
	}
	if originalReq.TopP != nil {
		config.TopP = *originalReq.TopP
	}
	if originalReq.MaxTokens != nil {
		config.MaxOutputTokens = *originalReq.MaxTokens
	}
	if originalReq.Stop != nil {
		// OpenAI Stop 可以是 string 或 []string
		if s, ok := originalReq.Stop.(string); ok {
			config.StopSequences = []string{s}
		} else if arr, ok := originalReq.Stop.([]interface{}); ok {
			for _, s := range arr {
				if str, ok := s.(string); ok {
					config.StopSequences = append(config.StopSequences, str)
				}
			}
		}
	}
	geminiReq.GenerationConfig = config

	// 3. 构建 HTTP 请求
	reqBodyBytes, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	// 安全的 URL 构建
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream url: %w", err)
	}

	// 处理路径：替换 generateContent 为 streamGenerateContent (如果流式)
	if originalReq.Stream && strings.HasSuffix(u.Path, ":generateContent") {
		u.Path = strings.Replace(u.Path, ":generateContent", ":streamGenerateContent", 1)
	}

	// 使用 Query 方法构建参数
	q := u.Query()
	q.Set("key", apiKey) // 自动处理 URL 编码
	if originalReq.Stream {
		q.Set("alt", "sse")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx.Request.Context(), "POST", u.String(), bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// HandleResponse 处理 Gemini 响应
func (a *GeminiAdapter) HandleResponse(c *gin.Context, resp *http.Response, isStream bool) error {
	if isStream {
		return a.handleStreamResponse(c, resp)
	}
	return a.handleNormalResponse(c, resp)
}

func (a *GeminiAdapter) handleNormalResponse(c *gin.Context, resp *http.Response) error {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		c.Status(resp.StatusCode)
		c.Writer.Write(bodyBytes)
		return nil
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return err
	}

	// 转换为 OpenAI 响应
	openaiResp := models.ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "gemini-pro", // 或者从 request 获取
		Choices: []models.ChatCompletionChoice{},
	}

	if len(geminiResp.Candidates) > 0 {
		content := ""
		for _, part := range geminiResp.Candidates[0].Content.Parts {
			content += part.Text
		}

		openaiResp.Choices = append(openaiResp.Choices, models.ChatCompletionChoice{
			Index: 0,
			Message: models.ChatMessage{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: "stop", // 简化处理
		})
	}

	c.JSON(200, openaiResp)
	return nil
}

// GeminiStreamScanner 实现 StreamScanner 接口
type GeminiStreamScanner struct {
	scanner   *bufio.Scanner
	requestID string
	created   int64
	current   []byte // 当前帧的 OpenAI 格式数据
	err       error
}

func NewGeminiStreamScanner(r io.Reader) *GeminiStreamScanner {
	return &GeminiStreamScanner{
		scanner:   bufio.NewScanner(r),
		requestID: fmt.Sprintf("chatcmpl-%d", time.Now().Unix()),
		created:   time.Now().Unix(),
	}
}

func (s *GeminiStreamScanner) Scan() bool {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		dataStr := strings.TrimPrefix(line, "data: ")
		if strings.TrimSpace(dataStr) == "[DONE]" {
			return false
		}

		var geminiResp GeminiResponse
		if err := json.Unmarshal([]byte(dataStr), &geminiResp); err != nil {
			continue
		}

		if len(geminiResp.Candidates) > 0 {
			candidate := geminiResp.Candidates[0]
			content := ""
			for _, part := range candidate.Content.Parts {
				content += part.Text
			}

			if content != "" {
				// 构造 OpenAI Delta
				chunk := models.ChatCompletionResponse{
					ID:      s.requestID,
					Object:  "chat.completion.chunk",
					Created: s.created,
					Model:   "gemini-pro",
					Choices: []models.ChatCompletionChoice{
						{
							Index: 0,
							Delta: models.ChatMessage{
								Role:    "assistant",
								Content: content,
							},
							FinishReason: "",
						},
					},
				}
				
				chunkBytes, _ := json.Marshal(chunk)
				// 格式化为 SSE 消息
				s.current = []byte(fmt.Sprintf("data: %s\n\n", chunkBytes))
				return true
			}
		}
	}
	if s.scanner.Err() != nil {
		s.err = s.scanner.Err()
	}
	return false
}

func (s *GeminiStreamScanner) Bytes() []byte {
	return s.current
}

func (s *GeminiStreamScanner) Err() error {
	return s.err
}

// handleStreamResponse 处理 Gemini SSE 流式响应
func (a *GeminiAdapter) handleStreamResponse(c *gin.Context, resp *http.Response) error {
	// 设置 SSE 头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
	c.Writer.Flush()

	// 使用 Scanner 接口
	var scanner StreamScanner = NewGeminiStreamScanner(resp.Body)

	for scanner.Scan() {
		if _, err := c.Writer.Write(scanner.Bytes()); err != nil {
			return err
		}
		c.Writer.Flush()
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
	return nil
}
