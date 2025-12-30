package mapper

import (
	"encoding/json"
	"llm-gateway/core/adapter"
	"llm-gateway/models"
	"strings"
)

// === Gemini Inbound Mapper ===

// GeminiRequestToOpenAI converts an incoming Gemini API request to internal OpenAI format
func GeminiRequestToOpenAI(gReq adapter.GeminiRequest, model string) (models.ChatCompletionRequest, error) {
	req := models.ChatCompletionRequest{
		Model:    model,
		Messages: make([]models.ChatMessage, 0),
	}

	// 1. System Instruction -> System Message
	if gReq.SystemInstruction != nil {
		content := ""
		for _, part := range gReq.SystemInstruction.Parts {
			content += part.Text
		}
		if content != "" {
			req.Messages = append(req.Messages, models.ChatMessage{
				Role:    "system",
				Content: content,
			})
		}
	}

	// 2. Contents -> Messages
	for _, c := range gReq.Contents {
		role := "user"
		if c.Role == "model" {
			role = "assistant"
		} else if c.Role == "function" {
            role = "tool"
        }

		// Handle Parts
		var parts []interface{}
		var textBuilder strings.Builder

		for _, p := range c.Parts {
			if p.Text != "" {
				textBuilder.WriteString(p.Text)
				parts = append(parts, map[string]interface{}{
					"type": "text",
					"text": p.Text,
				})
			}
			if p.InlineData != nil {
				parts = append(parts, map[string]interface{}{
					"type": "image_url",
					"image_url": map[string]string{
						"url": "data:" + p.InlineData.MimeType + ";base64," + p.InlineData.Data,
					},
				})
			}
            // Handle FunctionResponse (Tool Output) from User to Model
            if p.FunctionResponse != nil {
                // OpenAI expects tool output in a separate message with role 'tool'
                // Here we are inside a content loop.
                // If it's a function response, we override the role to 'tool'.
                role = "tool"
                // Extract result JSON
                resBytes, _ := json.Marshal(p.FunctionResponse.Response)
                textBuilder.WriteString(string(resBytes)) // Tool output is string in OpenAI
            }
		}

		var content interface{}
		if len(parts) > 0 && len(parts) == 1 && parts[0].(map[string]interface{})["type"] == "text" {
			content = textBuilder.String()
		} else if len(parts) > 0 {
			content = parts
		} else {
			content = textBuilder.String()
		}

        // Special handling for tool response
        if role == "tool" {
            // We need to match the tool_call_id. Gemini sends 'name'.
            // This is tricky because OpenAI needs ID. We might need to fake it or map it if we had state.
            // For Stateless router, we assume Name map to Name if possible or just put name in Name field.
            msg := models.ChatMessage{
                Role: "tool",
                Content: content,
                Name: c.Parts[0].FunctionResponse.Name, // Use the name from first part
                ToolCallID: "call_" + c.Parts[0].FunctionResponse.Name, // Fake ID matching
            }
            req.Messages = append(req.Messages, msg)
        } else {
		    req.Messages = append(req.Messages, models.ChatMessage{
			    Role:    role,
			    Content: content,
		    })
        }
	}

	// 3. Config
	if gReq.GenerationConfig != nil {
		req.Temperature = &gReq.GenerationConfig.Temperature
		req.TopP = &gReq.GenerationConfig.TopP
		// req.TopK = gReq.GenerationConfig.TopK // OpenAI doesn't support TopK standardly
		req.MaxTokens = &gReq.GenerationConfig.MaxOutputTokens
		if len(gReq.GenerationConfig.StopSequences) > 0 {
			req.Stop = gReq.GenerationConfig.StopSequences
		}
	}
    
    // 4. Tools
    if len(gReq.Tools) > 0 {
        for _, tool := range gReq.Tools {
            for _, fd := range tool.FunctionDeclarations {
                req.Tools = append(req.Tools, models.ChatTool{
                    Type: "function",
                    Function: models.ChatToolFunction{
                        Name: fd.Name,
                        Description: fd.Description,
                        Parameters: fd.Parameters,
                    },
                })
            }
        }
    }

	return req, nil
}

// OpenAIResponseToGemini converts an OpenAI response to Gemini format
func OpenAIResponseToGemini(oResp models.ChatCompletionResponse) adapter.GeminiResponse {
	gResp := adapter.GeminiResponse{
		Candidates: make([]adapter.GeminiCandidate, 0),
	}

	if oResp.Usage != nil {
		gResp.UsageMetadata = &adapter.GeminiUsage{
			PromptTokenCount:     oResp.Usage.PromptTokens,
			CandidatesTokenCount: oResp.Usage.CompletionTokens,
			TotalTokenCount:      oResp.Usage.TotalTokens,
		}
	}

	if len(oResp.Choices) > 0 {
		choice := oResp.Choices[0]
		
		cand := adapter.GeminiCandidate{
			Index: 0,
			Content: adapter.GeminiContent{
				Role: "model",
				Parts: make([]adapter.GeminiPart, 0),
			},
		}

		// Finish Reason
		switch choice.FinishReason {
		case "stop":
			cand.FinishReason = "STOP"
		case "length":
			cand.FinishReason = "MAX_TOKENS"
		case "tool_calls":
			// Gemini uses STOP + FunctionCall part usually, or specific reason?
            // "STOP" is standard for function call too in some contexts, but let's check.
            // Actually Gemini API returns "STOP" when it generates a function call.
			cand.FinishReason = "STOP" 
		default:
			cand.FinishReason = "STOP"
		}

		// Content
		contentStr := choice.Message.StringContent()
		if contentStr != "" {
			cand.Content.Parts = append(cand.Content.Parts, adapter.GeminiPart{
				Text: contentStr,
			})
		}
        
        // Tool Calls -> FunctionCall
        for _, tc := range choice.Message.ToolCalls {
            var args map[string]interface{}
            json.Unmarshal([]byte(tc.Function.Arguments), &args)
            cand.Content.Parts = append(cand.Content.Parts, adapter.GeminiPart{
                FunctionCall: &adapter.GeminiFunctionCall{
                    Name: tc.Function.Name,
                    Args: args,
                },
            })
        }

		gResp.Candidates = append(gResp.Candidates, cand)
	}

	return gResp
}
