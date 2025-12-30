package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/models"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ClaudeAdapter struct{}

func NewClaudeAdapter() *ClaudeAdapter {
	return &ClaudeAdapter{}
}

// ConvertRequest OpenAI -> Claude
func (a *ClaudeAdapter) ConvertRequest(ctx *gin.Context, originalReq models.ChatCompletionRequest, apiKey string, baseURL string, upstreamModel string) (*http.Request, error) {
	claudeReq := ClaudeRequest{
		Model:    upstreamModel,
		Messages: make([]ClaudeMessage, 0),
		Stream:   originalReq.Stream,
	}

	// 1. Extract System Prompt
	var systemPromptBuilder strings.Builder
	for _, msg := range originalReq.Messages {
		if msg.Role == "system" {
			if systemPromptBuilder.Len() > 0 {
				systemPromptBuilder.WriteString("\n")
			}
			systemPromptBuilder.WriteString(msg.StringContent())
		}
	}
	if systemPromptBuilder.Len() > 0 {
		claudeReq.System = systemPromptBuilder.String()
	}

	// 2. Transform Messages
	for _, msg := range originalReq.Messages {
		if msg.Role == "system" {
			continue
		}

		cMsg := ClaudeMessage{
			Role: msg.Role,
		}
		if cMsg.Role == "tool" {
			cMsg.Role = "user" // Tool results are User messages in Claude
		}

		// Content Processing
		var blocks []ClaudeContentBlock

		// 2a. Handle Tool Results (OpenAI "tool" role)
		if msg.Role == "tool" {
			blocks = append(blocks, ClaudeContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.StringContent(),
			})
		} else {
			// 2b. Handle Normal Content (Text/Image)
			if strContent, ok := msg.Content.(string); ok && strContent != "" {
				blocks = append(blocks, ClaudeContentBlock{Type: "text", Text: strContent})
			} else if listContent, ok := msg.Content.([]interface{}); ok {
				for _, item := range listContent {
					itemMap, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					typeVal, _ := itemMap["type"].(string)
					if typeVal == "text" {
						if textVal, ok := itemMap["text"].(string); ok {
							blocks = append(blocks, ClaudeContentBlock{Type: "text", Text: textVal})
						}
					} else if typeVal == "image_url" {
						if imageUrlMap, ok := itemMap["image_url"].(map[string]interface{}); ok {
							if urlVal, ok := imageUrlMap["url"].(string); ok {
								if strings.HasPrefix(urlVal, "data:") {
									parts := strings.Split(urlVal, ",")
									if len(parts) == 2 {
										mimeType := strings.TrimSuffix(strings.TrimPrefix(parts[0], "data:"), ";base64")
										blocks = append(blocks, ClaudeContentBlock{
											Type: "image",
											Source: &ClaudeSource{
												Type:      "base64",
												MediaType: mimeType,
												Data:      parts[1],
											},
										})
									}
								}
								// TODO: Support URL images (download needed as Claude only supports base64 mostly)
							}
						}
					}
				}
			}

			// 2c. Handle Tool Calls (Assistant -> Tool Use)
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					var input map[string]interface{}
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = map[string]interface{}{}
					}
					blocks = append(blocks, ClaudeContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					})
				}
			}
		}

		if len(blocks) > 0 {
			cMsg.Content = blocks
			claudeReq.Messages = append(claudeReq.Messages, cMsg)
		}
	}

	// 3. Transform Tools
	if len(originalReq.Tools) > 0 {
		for _, tool := range originalReq.Tools {
			if tool.Type == "function" {
				claudeReq.Tools = append(claudeReq.Tools, ClaudeTool{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					InputSchema: tool.Function.Parameters,
				})
			}
		}
	}

	// 4. Transform Config
	if originalReq.MaxTokens != nil {
		claudeReq.MaxTokens = *originalReq.MaxTokens
	} else {
		claudeReq.MaxTokens = 4096 // Default safe limit
	}
	if originalReq.Temperature != nil {
		claudeReq.Temperature = *originalReq.Temperature
	}
	if originalReq.TopP != nil {
		claudeReq.TopP = *originalReq.TopP
	}

	// Build Request
	reqBodyBytes, err := json.Marshal(claudeReq)
	if err != nil {
		return nil, fmt.Errorf("marshal claude req error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.Request.Context(), "POST", baseURL+"/messages", bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create req error: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	return req, nil
}

// HandleResponse Claude -> OpenAI
func (a *ClaudeAdapter) HandleResponse(c *gin.Context, resp *http.Response, isStream bool) error {
	if isStream {
		return a.handleStreamResponse(c, resp)
	}
	return a.handleNormalResponse(c, resp)
}

func (a *ClaudeAdapter) handleNormalResponse(c *gin.Context, resp *http.Response) error {
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

	var claudeResp ClaudeResponse
	if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
		return err
	}

	openaiResp := models.ChatCompletionResponse{
		ID:      claudeResp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   claudeResp.Model,
		Choices: []models.ChatCompletionChoice{},
		Usage: &models.ChatCompletionUsage{
			PromptTokens:     claudeResp.Usage.InputTokens,
			CompletionTokens: claudeResp.Usage.OutputTokens,
			TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
		},
	}

	// Process Content & Tool Calls
	var contentBuilder strings.Builder
	var toolCalls []models.ChatToolCall

	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			contentBuilder.WriteString(block.Text)
		} else if block.Type == "tool_use" {
			argsBytes, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, models.ChatToolCall{
				ID:   block.ID,
				Type: "function",
				Function: models.ChatToolCallFunc{
					Name:      block.Name,
					Arguments: string(argsBytes),
				},
			})
		}
	}

	choice := models.ChatCompletionChoice{
		Index: 0,
		Message: models.ChatMessage{
			Role:    "assistant",
			Content: contentBuilder.String(),
		},
		FinishReason: mapStopReason(claudeResp.StopReason),
	}

	if len(toolCalls) > 0 {
		choice.Message.ToolCalls = toolCalls
	}

	openaiResp.Choices = append(openaiResp.Choices, choice)

	c.JSON(200, openaiResp)
	return nil
}

func mapStopReason(reason *string) string {
	if reason == nil {
		return ""
	}
	switch *reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return *reason
	}
}

// Streaming

type ClaudeStreamScanner struct {
	scanner      *bufio.Scanner
	requestID    string
	created      int64
	current      []byte
	err          error
	currentIdx   int
	isFirstChunk bool
}

func NewClaudeStreamScanner(r io.Reader) *ClaudeStreamScanner {
	return &ClaudeStreamScanner{
		scanner:      bufio.NewScanner(r),
		created:      time.Now().Unix(),
		currentIdx:   0,
		isFirstChunk: true,
	}
}

func (s *ClaudeStreamScanner) Scan() bool {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		eventType := strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		
		if !s.scanner.Scan() {
			break
		}
		dataLine := s.scanner.Text()
		if !strings.HasPrefix(dataLine, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(dataLine, "data: ")

		var event ClaudeStreamEvent
		if err := json.Unmarshal([]byte(dataStr), &event); err != nil {
			continue
		}

		// Handle Events
		chunk := models.ChatCompletionResponse{
			Object:  "chat.completion.chunk",
			Created: s.created,
			Choices: []models.ChatCompletionChoice{
				{Index: 0},
			},
		}
		hasContent := false

		switch eventType {
		case "message_start":
			if event.Message != nil {
				s.requestID = event.Message.ID
				chunk.ID = s.requestID
				chunk.Model = event.Message.Model
				chunk.Choices[0].Delta.Role = "assistant"
				hasContent = true
			}
		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				// Start of tool use
				chunk.ID = s.requestID
				chunk.Choices[0].Delta.ToolCalls = []models.ChatToolCall{
					{
						Index: &event.Index,
						ID:    event.ContentBlock.ID,
						Type:  "function",
						Function: models.ChatToolCallFunc{
							Name:      event.ContentBlock.Name,
							Arguments: "",
						},
					},
				}
				hasContent = true
			}
		case "content_block_delta":
			if event.Delta != nil {
				chunk.ID = s.requestID
				if event.Delta.Type == "text_delta" {
					chunk.Choices[0].Delta.Content = event.Delta.Text
					hasContent = true
				} else if event.Delta.Type == "input_json_delta" {
					chunk.Choices[0].Delta.ToolCalls = []models.ChatToolCall{
						{
							Index: &event.Index,
							Function: models.ChatToolCallFunc{
								Arguments: event.Delta.PartialJson,
							},
						},
					}
					hasContent = true
				}
			}
		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != nil {
				chunk.ID = s.requestID
				chunk.Choices[0].FinishReason = mapStopReason(event.Delta.StopReason)
				hasContent = true
			}
			if event.Usage != nil {
				// Usage in stream is usually handled by a final chunk in OpenAI
				// We can emit it here
				// Note: standard OpenAI clients might expect usage in a separate chunk with empty choices, 
				// or attached to the last chunk. Let's attach to this one if possible, or emit separate.
				// But here we are inside a single loop iteration.
				// Let's just set it.
				chunk.Usage = &models.ChatCompletionUsage{
					PromptTokens:     event.Usage.InputTokens,
					CompletionTokens: event.Usage.OutputTokens,
					TotalTokens:      event.Usage.InputTokens + event.Usage.OutputTokens,
				}
			}
		case "message_stop":
			return false // End of stream
		case "ping":
			continue
		}

		if hasContent {
			chunkBytes, _ := json.Marshal(chunk)
			s.current = []byte(fmt.Sprintf("data: %s\n\n", chunkBytes))
			return true
		}
	}

	if s.scanner.Err() != nil {
		s.err = s.scanner.Err()
	}
	return false
}

func (s *ClaudeStreamScanner) Bytes() []byte {
	return s.current
}

func (s *ClaudeStreamScanner) Err() error {
	return s.err
}

func (a *ClaudeAdapter) handleStreamResponse(c *gin.Context, resp *http.Response) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
	c.Writer.Flush()

	var scanner StreamScanner = NewClaudeStreamScanner(resp.Body)

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
