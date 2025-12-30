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
)

func TestGeminiAdapter_HandleResponse_Stream(t *testing.T) {
	// 1. 模拟 Gemini SSE 服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		
		// 发送第一块数据
		fmt.Fprintf(w, "data: %s\n\n", `{"candidates": [{"content": {"parts": [{"text": "Hello"}]}, "finishReason": ""}]}`)
		w.(http.Flusher).Flush()

		// 发送第二块数据
		fmt.Fprintf(w, "data: %s\n\n", `{"candidates": [{"content": {"parts": [{"text": " World"}]}, "finishReason": "STOP"}]}`)
		w.(http.Flusher).Flush()

		// 发送结束标记
		fmt.Fprintf(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer ts.Close()

	// 2. 模拟请求上游
	resp, err := http.Get(ts.URL)
	assert.NoError(t, err)

	// 3. 准备 Recorder 和 Context
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// 4. 调用 Adapter 处理
	adapter := NewGeminiAdapter()
	err = adapter.HandleResponse(c, resp, true)
	assert.NoError(t, err)

	// 5. 验证输出
	// 解析 SSE 输出
	responseBody := w.Body.String()
	scanner := bufio.NewScanner(strings.NewReader(responseBody))
	
	var fullText string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// 验证是 OpenAI 格式
		var chunk map[string]interface{}
		err := json.Unmarshal([]byte(data), &chunk)
		assert.NoError(t, err, "Should be valid JSON")

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

	assert.Equal(t, "Hello World", fullText, "Content should be reassembled correctly")
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
}
