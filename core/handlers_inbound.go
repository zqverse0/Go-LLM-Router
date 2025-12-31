package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"llm-gateway/core/adapter"
	"llm-gateway/core/mapper"
	"llm-gateway/models"
	"net/http"
	"strings"

	stdPkgNet "net"

	"github.com/gin-gonic/gin"
)

// ResponseInterceptor captures the response from ProxyHandler
type ResponseInterceptor struct {
	gin.ResponseWriter
	body       *bytes.Buffer
	headers    http.Header
	statusCode int
	
	// Streaming support
	isStream   bool
	streamChan chan []byte
}

func NewResponseInterceptor(isStream bool) *ResponseInterceptor {
	return &ResponseInterceptor{
		body:       bytes.NewBufferString(""),
		headers:    make(http.Header),
		statusCode: 200,
		isStream:   isStream,
		streamChan: make(chan []byte, 1024), // [FIX-03] Increased buffer to avoid blocking
	}
}

func (w *ResponseInterceptor) Write(b []byte) (int, error) {
	if w.isStream {
		// Send a copy to channel
		// We must copy because b might be reused
		bCopy := make([]byte, len(b))
		copy(bCopy, b)
		w.streamChan <- bCopy
		return len(b), nil
	}
	return w.body.Write(b)
}

func (w *ResponseInterceptor) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *ResponseInterceptor) Header() http.Header {
	return w.headers
}

func (w *ResponseInterceptor) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *ResponseInterceptor) Status() int {
	return w.statusCode
}

func (w *ResponseInterceptor) Size() int {
	return w.body.Len()
}

func (w *ResponseInterceptor) Written() bool {
	return w.body.Len() > 0
}

func (w *ResponseInterceptor) Pusher() http.Pusher { return nil }
func (w *ResponseInterceptor) Hijack() (stdPkgNet.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }
func (w *ResponseInterceptor) Flush() {} 
func (w *ResponseInterceptor) CloseNotify() <-chan bool { return nil }

// HandleClaudeMessage handles incoming Claude API requests
func (h *ProxyHandler) HandleClaudeMessage(c *gin.Context) {
	var cReq adapter.ClaudeRequest
	if err := c.BindJSON(&cReq); err != nil {
		c.JSON(400, gin.H{"error": "Invalid Claude request"})
		return
	}

	// 1. Convert to OpenAI Request
	oReq, err := mapper.ClaudeRequestToOpenAI(cReq)
	if err != nil {
		c.JSON(400, gin.H{"error": "Failed to map request: " + err.Error()})
		return
	}

	// 2. Prepare Interceptor
	interceptor := NewResponseInterceptor(cReq.Stream)
	
	// Create a fake context that shares the Request but writes to Interceptor
	// We clone the request context to ensure cancellation works
	fakeC, _ := gin.CreateTestContext(interceptor)
	fakeC.Request = c.Request
	
	if cReq.Stream {
		// --- Streaming Mode ---
		
		// Run ProxyRequest in background
		go func() {
			defer close(interceptor.streamChan)
			h.ProxyRequest(fakeC, oReq)
		}()

		// Set Headers for Real Client
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
        c.Header("anthropic-version", "2023-06-01") // Optional

		// Read from channel and convert
		// OpenAI Stream (SSE) -> Claude Stream (SSE)
		
		// Buffer for incomplete lines
		var lineBuffer string

		for chunk := range interceptor.streamChan {
			lineBuffer += string(chunk)
			
			for {
				// [FIX-02] 支持 \n\n 和 \r\n\r\n 多种分隔符
				idx := strings.Index(lineBuffer, "\n\n")
				delimLen := 2
				if rIdx := strings.Index(lineBuffer, "\r\n\r\n"); rIdx != -1 && (idx == -1 || rIdx < idx) {
					idx = rIdx
					delimLen = 4
				}

				if idx == -1 {
					break
				}
				
				fullBlock := lineBuffer[:idx]
				lineBuffer = lineBuffer[idx+delimLen:]
				
				// Parse Block
				// Expected: "data: {...}" or "data: [DONE]"
				lines := strings.Split(strings.ReplaceAll(fullBlock, "\r\n", "\n"), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						dataStr := strings.TrimPrefix(line, "data: ")
						if dataStr == "[DONE]" {
                            // Claude done event
							c.Writer.Write([]byte("event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"))
							c.Writer.Flush()
							continue
						}
						
							var oResp models.ChatCompletionResponse
							if err := json.Unmarshal([]byte(dataStr), &oResp); err == nil {
								// Map to Claude Events
								// Since we don't track index easily in stateless mapper, we pass index 0
								events := mapper.OpenAIStreamToClaudeEvent(oResp, 0)
								for _, evt := range events {
									c.Writer.Write([]byte(evt))
								}
								c.Writer.Flush()
							}
						}
				}
			}
		}

	} else {
		// --- Normal Mode ---
		h.ProxyRequest(fakeC, oReq)

		// Check Status
		if interceptor.statusCode != 200 {
			// Propagate Error
			c.Data(interceptor.statusCode, "application/json", interceptor.body.Bytes())
			return
		}

		// Parse OpenAI Response
		var oResp models.ChatCompletionResponse
		if err := json.Unmarshal(interceptor.body.Bytes(), &oResp); err != nil {
			c.JSON(500, gin.H{"error": "Failed to parse upstream response"})
			return
		}

		// Convert to Claude Response
		cResp := mapper.OpenAIResponseToClaude(oResp)
		c.JSON(200, cResp)
	}
}

// HandleGeminiGenerateContent handles incoming Gemini API requests
func (h *ProxyHandler) HandleGeminiGenerateContent(c *gin.Context) {
    // URL param: :model (e.g., "gemini-pro:generateContent")
    rawModel := c.Param("model")
    model := strings.Split(rawModel, ":")[0] // Extract "gemini-pro"
    
	var gReq adapter.GeminiRequest
	if err := c.BindJSON(&gReq); err != nil {
		c.JSON(400, gin.H{"error": "Invalid Gemini request"})
		return
	}

	// 1. Convert to OpenAI Request
	oReq, err := mapper.GeminiRequestToOpenAI(gReq, model)
	if err != nil {
		c.JSON(400, gin.H{"error": "Failed to map request: " + err.Error()})
		return
	}
    
    // Check if stream based on URL
    isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")
    oReq.Stream = isStream

	// 2. Prepare Interceptor
	interceptor := NewResponseInterceptor(isStream)
	fakeC, _ := gin.CreateTestContext(interceptor)
	fakeC.Request = c.Request

	if isStream {
		// --- Streaming Mode ---
        go func() {
			defer close(interceptor.streamChan)
			h.ProxyRequest(fakeC, oReq)
		}()
        
        c.Header("Content-Type", "text/event-stream")
        
        var lineBuffer string
        for chunk := range interceptor.streamChan {
            lineBuffer += string(chunk)
            
             for {
				// [FIX-02] 支持多种分隔符
				idx := strings.Index(lineBuffer, "\n\n")
				delimLen := 2
				if rIdx := strings.Index(lineBuffer, "\r\n\r\n"); rIdx != -1 && (idx == -1 || rIdx < idx) {
					idx = rIdx
					delimLen = 4
				}

				if idx == -1 {
					break
				}
				fullBlock := lineBuffer[:idx]
				lineBuffer = lineBuffer[idx+delimLen:]
                
                lines := strings.Split(strings.ReplaceAll(fullBlock, "\r\n", "\n"), "\n")
				for _, line := range lines {
                    line = strings.TrimSpace(line)
                    if strings.HasPrefix(line, "data: ") {
                        dataStr := strings.TrimPrefix(line, "data: ")
                        if dataStr == "[DONE]" { continue }
                        
                        var oResp models.ChatCompletionResponse
						if err := json.Unmarshal([]byte(dataStr), &oResp); err == nil {
                            // Map to Gemini Response
                            gResp := mapper.OpenAIResponseToGemini(oResp)
                            b, _ := json.Marshal(gResp)
                            c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
                            c.Writer.Flush()
                        }
                    }
                }
            }
        }

	} else {
		// --- Normal Mode ---
		h.ProxyRequest(fakeC, oReq)

		if interceptor.statusCode != 200 {
			c.Data(interceptor.statusCode, "application/json", interceptor.body.Bytes())
			return
		}

		var oResp models.ChatCompletionResponse
		if err := json.Unmarshal(interceptor.body.Bytes(), &oResp); err != nil {
			c.JSON(500, gin.H{"error": "Failed to parse upstream response"})
			return
		}

		gResp := mapper.OpenAIResponseToGemini(oResp)
		c.JSON(200, gResp)
	}
}