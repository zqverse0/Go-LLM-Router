package adapter

// Claude Request Structures

type ClaudeRequest struct {
	Model         string                 `json:"model"`
	Messages      []ClaudeMessage        `json:"messages"`
	System        string                 `json:"system,omitempty"`
	MaxTokens     int                    `json:"max_tokens,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	StopSequences []string               `json:"stop_sequences,omitempty"`
	Stream        bool                   `json:"stream,omitempty"`
	Temperature   float64                `json:"temperature,omitempty"`
	TopP          float64                `json:"top_p,omitempty"`
	TopK          int                    `json:"top_k,omitempty"`
	Tools         []ClaudeTool           `json:"tools,omitempty"`
	ToolChoice    interface{}            `json:"tool_choice,omitempty"` // map or string ("auto", "any")
}

type ClaudeMessage struct {
	Role    string      `json:"role"`    // "user" or "assistant"
	Content interface{} `json:"content"` // string or []ClaudeContentBlock
}

type ClaudeContentBlock struct {
	Type   string        `json:"type"`             // "text", "image", "tool_use", "tool_result"
	Text   string        `json:"text,omitempty"`   // for "text"
	Source *ClaudeSource `json:"source,omitempty"` // for "image"
	
	// Tool Use
	ID    string      `json:"id,omitempty"`
	Name  string      `json:"name,omitempty"`
	Input interface{} `json:"input,omitempty"` // JSON object
	
	// Tool Result
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"` // string or []ClaudeContentBlock
	IsError   bool        `json:"is_error,omitempty"`
}

type ClaudeSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type ClaudeTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"` // JSON Schema
}

// Claude Response Structures

type ClaudeResponse struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"` // "message"
	Role         string               `json:"role"`
	Content      []ClaudeContentBlock `json:"content"`
	Model        string               `json:"model"`
	StopReason   *string              `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence"`
	Usage        ClaudeUsage          `json:"usage"`
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Streaming Events

type ClaudeStreamEvent struct {
	Type         string              `json:"type"` // message_start, content_block_start, ...
	Message      *ClaudeResponse     `json:"message,omitempty"`
	Index        int                 `json:"index,omitempty"`
	ContentBlock *ClaudeContentBlock `json:"content_block,omitempty"`
	Delta        *ClaudeDelta        `json:"delta,omitempty"`
	Usage        *ClaudeUsage        `json:"usage,omitempty"` // in message_delta
}

type ClaudeDelta struct {
	Type         string  `json:"type"` // text_delta, input_json_delta
	Text         string  `json:"text,omitempty"`
	PartialJson  string  `json:"partial_json,omitempty"`
	StopReason   *string `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}
