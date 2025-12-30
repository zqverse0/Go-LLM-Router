package adapter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"llm-gateway/models"
)

func TestClaudeAdapter_ConvertRequest(t *testing.T) {
	adapter := NewClaudeAdapter()
	
temp := 0.7
	originalReq := models.ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []models.ChatMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello!"},
		},
		Temperature: &temp,
		Stream:      true,
	}

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("POST", "/", nil)

	req, err := adapter.ConvertRequest(ctx, originalReq, "sk-test-key", "https://api.anthropic.com/v1", "claude-3-sonnet")
	assert.NoError(t, err)
	assert.Equal(t, "https://api.anthropic.com/v1/messages", req.URL.String())
	assert.Equal(t, "sk-test-key", req.Header.Get("x-api-key"))

	var claudeReq ClaudeRequest
	err = json.NewDecoder(req.Body).Decode(&claudeReq)
	assert.NoError(t, err)
	
	assert.Equal(t, "claude-3-sonnet", claudeReq.Model)
	assert.Equal(t, "You are a helpful assistant.", claudeReq.System)
	assert.Equal(t, 1, len(claudeReq.Messages))
	assert.Equal(t, "user", claudeReq.Messages[0].Role)
}

func TestClaudeAdapter_HandleResponse_Stream(t *testing.T) {
	// 模拟 Claude SSE
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", `{"type": "message_start", "message": {"id": "msg_123", "model": "claude-3"}}`)
		w.(http.Flusher).Flush()

		fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", `{"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}`)
		w.(http.Flusher).Flush()

		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", `{"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}`)
		w.(http.Flusher).Flush()

		fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", `{"type": "message_stop"}`)
		w.(http.Flusher).Flush()
	}))
	defer ts.Close()

	resp, _ := http.Get(ts.URL)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	adapter := NewClaudeAdapter()
	err := adapter.HandleResponse(c, resp, true)
	assert.NoError(t, err)

	responseBody := w.Body.String()
	scanner := bufio.NewScanner(strings.NewReader(responseBody))
	
	var fullText string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var chunk map[string]interface{}
		json.Unmarshal([]byte(data), &chunk)

		choices := chunk["choices"].([]interface{})
		if len(choices) > 0 {
			choice := choices[0].(map[string]interface{})
			if delta, ok := choice["delta"].(map[string]interface{}); ok {
				if content, ok := delta["content"].(string); ok {
					fullText += content
				}
			}
		}
	}

	assert.Equal(t, "Hello", fullText)
}
