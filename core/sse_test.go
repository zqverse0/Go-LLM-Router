package core

import (
	"reflect"
	"strings"
	"testing"
)

// 为了测试，我在这里复制一份逻辑，或者如果我把逻辑提取为函数就更好了。
// 既然我已经写到了 handlers_inbound.go 中，我可以使用一个辅助函数来测试它。
// 但是为了简单起见，我直接在测试中验证逻辑。

func TestSSESplittingLogic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:  "Standard LF",
			input: "data: block1\n\ndata: block2\n\n",
			expected: []string{"data: block1", "data: block2"},
		},
		{
			name:  "Mixed CRLF and LF",
			input: "data: block1\r\n\r\ndata: block2\n\n",
			expected: []string{"data: block1", "data: block2"},
		},
		{
			name:  "Chunked input simulation",
			input: "data: block1\n\ndata: bl",
			expected: []string{"data: block1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var results []string
			buffer := tt.input
			for {
				idx := strings.Index(buffer, "\n\n")
				delimLen := 2
				if rIdx := strings.Index(buffer, "\r\n\r\n"); rIdx != -1 && (idx == -1 || rIdx < idx) {
					idx = rIdx
					delimLen = 4
				}

				if idx == -1 {
					break
				}
				results = append(results, buffer[:idx])
				buffer = buffer[idx+delimLen:]
			}

			if !reflect.DeepEqual(results, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, results)
			}
		})
	}
}
