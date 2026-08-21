package openairesponses

import "testing"

func TestWebsocketURL(t *testing.T) {
	tests := []struct{ base, want string }{
		{"", "wss://api.openai.com/v1/responses"},
		{"http://example.test/responses", "ws://example.test/responses"},
		{"https://example.test/responses", "wss://example.test/responses"},
	}
	for _, tt := range tests {
		if got := websocketURL(tt.base); got != tt.want {
			t.Errorf("websocketURL(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}
