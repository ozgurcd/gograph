package parser

import (
	"testing"
)

func TestExtractStaticSegments(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		expected []string
	}{
		{
			name:     "simple path",
			rawURL:   "https://api.example.com/users",
			expected: []string{"users"},
		},
		{
			name:     "multi-segment path",
			rawURL:   "https://user-svc/api/v2/users",
			expected: []string{"api", "v2", "users"},
		},
		{
			name:     "with query string (ignored)",
			rawURL:   "https://api.example.com/users?page=1&limit=10",
			expected: []string{"users"},
		},
		{
			name:     "with fragment (ignored)",
			rawURL:   "https://api.example.com/users#section",
			expected: []string{"users"},
		},
		{
			name:     "trailing slash",
			rawURL:   "https://api.example.com/",
			expected: nil,
		},
		{
			name:     "root path only",
			rawURL:   "https://api.example.com",
			expected: nil,
		},
		{
			name:     "empty URL",
			rawURL:   "",
			expected: nil,
		},
		{
			name:     "deeply nested path",
			rawURL:   "https://svc.example.com/a/b/c/d/e",
			expected: []string{"a", "b", "c", "d", "e"},
		},
		{
			name:     "path with trailing slash",
			rawURL:   "https://svc.example.com/api/v2/",
			expected: []string{"api", "v2"},
		},
		{
			name:     "malformed URL",
			rawURL:   "://invalid",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStaticSegments(tt.rawURL)
			if len(got) == 0 && len(tt.expected) == 0 {
				return
			}
			if len(got) != len(tt.expected) {
				t.Errorf("extractStaticSegments(%q) = %v, want %v", tt.rawURL, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("extractStaticSegments(%q)[%d] = %q, want %q", tt.rawURL, i, got[i], tt.expected[i])
				}
			}
		})
	}
}
