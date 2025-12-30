package mapper

import (
	"encoding/json"
	"fmt"
	"llm-gateway/core/adapter"
	"llm-gateway/models"
	"strings"
)

// === Claude Inbound Mapper ===

// ClaudeRequestToOpenAI converts an incoming Claude API request to internal OpenAI format
func ClaudeRequestToOpenAI(cReq adapter.ClaudeRequest) (models.ChatCompletionRequest, error) {
	req := models.ChatCompletionRequest{
		Model:       cReq.Model,
		Stream:      cReq.Stream,
		Temperature: &cReq.Temperature,
		TopP:        &cReq.TopP,
		MaxTokens:   &cReq.MaxTokens,
	}

	// 1. System Prompt
	if cReq.System != "" {
		req.Messages = append(req.Messages, models.ChatMessage{
			Role:    "system",
			Content: cReq.System,
		})
	}

	// 2. Messages
	for _, msg := range cReq.Messages {
		role := msg.Role
		if role == "user" && len(req.Messages) == 0 && cReq.System == "" {
			// First user message
		}

		// Handle Content (String or Blocks)
		// OpenAI supports string or []interface{} (multimodal)
		// We map Claude blocks to OpenAI multimodal content
		var content interface{}
		
		// If it's simple string content
		if str, ok := msg.Content.(string); ok {
			content = str
		} else if blocks, ok := msg.Content.([]interface{}); ok {
			// Check if it's a list of blocks
			// We need to convert Claude blocks map[string]interface{} to OpenAI parts
			// But since we are inside the gateway, we can just pass the blocks if we implement the converter right.
			// However, adapter.ClaudeContentBlock is struct.
			// Let's assume msg.Content was unmarshaled into []interface{} or []ClaudeContentBlock if we control the struct.
			// In gin binding, it will be []interface{} or []map[string]interface{}
			
			// Simple approach: Convert text blocks to string, others to OpenAI image_url
			var textBuilder strings.Builder
			var parts []interface{}

			for _, b := range blocks {
				blockMap, _ := b.(map[string]interface{})
				bType, _ := blockMap["type"].(string)

				if bType == "text" {
					if t, ok := blockMap["text"].(string); ok {
						textBuilder.WriteString(t)
							parts = append(parts, map[string]interface{}{
								"type": "text",
								"text": t,
							})
					}
				} else if bType == "image" {
					if src, ok := blockMap["source"].(map[string]interface{}); ok {
						if data, ok := src["data"].(string); ok {
							mediaType := src["media_type"].(string)
							parts = append(parts, map[string]interface{}{
								"type": "image_url",
								"image_url": map[string]string{
									"url": fmt.Sprintf("data:%s;base64,%s", mediaType, data),
								},
							})
						}
					}
				}
			}
			
			if len(parts) > 0 {
				// If strictly text, use string
				// But to support images, use parts
				content = parts
			} else {
				content = textBuilder.String()
			}
		}

		req.Messages = append(req.Messages, models.ChatMessage{
			Role:    role,
			Content: content,
		})
	}

	// 3. Tools
	// Map Claude Tools to OpenAI Tools
	// OpenAI: tools: [{type: function, function: {name, description, parameters}}]
	// Claude: tools: [{name, description, input_schema}]
	for _, t := range cReq.Tools {
		req.Tools = append(req.Tools, models.ChatTool{
			Type: "function",
			Function: models.ChatToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return req, nil
}

// OpenAIResponseToClaude converts an OpenAI response to Claude format
func OpenAIResponseToClaude(oResp models.ChatCompletionResponse) adapter.ClaudeResponse {
	cResp := adapter.ClaudeResponse{
		ID:    oResp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: oResp.Model,
		Usage: adapter.ClaudeUsage{
			InputTokens:  0,
			OutputTokens: 0,
		},
		Content: make([]adapter.ClaudeContentBlock, 0),
	}

	if oResp.Usage != nil {
		cResp.Usage.InputTokens = oResp.Usage.PromptTokens
		cResp.Usage.OutputTokens = oResp.Usage.CompletionTokens
	}

	if len(oResp.Choices) > 0 {
		choice := oResp.Choices[0]
		
		// Stop Reason Mapping
		switch choice.FinishReason {
		case "stop":
			s := "end_turn"
			cResp.StopReason = &s
		case "length":
			s := "max_tokens"
			cResp.StopReason = &s
		case "tool_calls":
			s := "tool_use"
			cResp.StopReason = &s
		default:
			s := "end_turn"
			cResp.StopReason = &s
		}

		// Content Mapping
		if choice.Message.Content != nil {
			contentStr := choice.Message.StringContent()
			if contentStr != "" {
				cResp.Content = append(cResp.Content, adapter.ClaudeContentBlock{
					Type: "text",
					Text: contentStr,
				})
			}
		}

		// Tool Calls Mapping
		for _, tc := range choice.Message.ToolCalls {
			var input interface{}
			json.Unmarshal([]byte(tc.Function.Arguments), &input)
			
			cResp.Content = append(cResp.Content, adapter.ClaudeContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
	}

	return cResp
}

// OpenAIStreamToClaudeEvent converts OpenAI chunk to Claude SSE event(s)
// Returns a list of events because one OpenAI chunk might split into multiple Claude events
func OpenAIStreamToClaudeEvent(chunk models.ChatCompletionResponse, index int) []string {
	var events []string

	// 1. Message Start (Only on first chunk or if ID changes - simplified for now)
	// Ideally we handle state outside, but here we just process deltas.
	// We assume the caller handles "message_start".

	choice := chunk.Choices[0]
	delta := choice.Delta

	// Content Delta
	contentStr := delta.StringContent()
	if contentStr != "" {
		evt := adapter.ClaudeStreamEvent{
			Type:  "content_block_delta",
			Index: 0, // Assume single text block for now
			Delta: &adapter.ClaudeDelta{
				Type: "text_delta",
				Text: contentStr,
			},
		}
		b, _ := json.Marshal(evt)
		events = append(events, fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", string(b)))
	}

	// Tool Calls
	// OpenAI sends partial tool calls. Claude needs content_block_start (tool_use) and then input_json_delta.
	// Complex state handling required.
	// For MVP, we might skip complex tool streaming or handle basic cases.

	// Message Delta (Stop Reason & Usage)
	if choice.FinishReason != "" || chunk.Usage != nil {
		evt := adapter.ClaudeStreamEvent{
			Type:  "message_delta",
			Delta: &adapter.ClaudeDelta{},
			Usage: &adapter.ClaudeUsage{},
		}
		
		if choice.FinishReason != "" {
			reason := "end_turn"
			if choice.FinishReason == "length" { reason = "max_tokens" }
			if choice.FinishReason == "tool_calls" { reason = "tool_use" }
			evt.Delta.StopReason = &reason
			evt.Delta.StopSequence = nil
		}

		if chunk.Usage != nil {
			evt.Usage.OutputTokens = chunk.Usage.CompletionTokens
		}
		
		b, _ := json.Marshal(evt)
		events = append(events, fmt.Sprintf("event: message_delta\ndata: %s\n\n", string(b)))
	}

	return events
}
