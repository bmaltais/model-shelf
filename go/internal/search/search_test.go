package search

import "testing"

func TestContainsFormatKeyword(t *testing.T) {
	tests := []struct {
		query  string
		format string
		want   bool
	}{
		{"qwen 3 4b", "mlx", false},
		{"qwen3 mlx 4bit", "mlx", true},
		{"qwen3 MLX", "mlx", true},
		{"qwen3 gguf", "gguf", true},
		{"qwen3 4b", "gguf", false},
		{"test safetensors model", "safetensors", true},
		{"test model", "safetensors", false},
		{"", "mlx", false},
	}
	for _, tc := range tests {
		got := containsFormatKeyword(tc.query, tc.format)
		if got != tc.want {
			t.Errorf("containsFormatKeyword(%q, %q) = %v, want %v", tc.query, tc.format, got, tc.want)
		}
	}
}
