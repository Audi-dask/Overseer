package gitlab

import (
	"net/http"
	"testing"
)

func TestGitLabWriteAllowed(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		want   bool
	}{
		{"read projects", http.MethodGet, "https://gitlab.example.com/api/v4/projects", true},
		{"create hook", http.MethodPost, "https://gitlab.example.com/api/v4/projects/12/hooks", true},
		{"delete hook", http.MethodDelete, "https://gitlab.example.com/api/v4/projects/12/hooks/34", true},
		{"update hook", http.MethodPut, "https://gitlab.example.com/api/v4/projects/12/hooks/34", true},
		{"post review note", http.MethodPost, "https://gitlab.example.com/api/v4/projects/12/merge_requests/1/notes", true},
		{"post commit comment", http.MethodPost, "https://gitlab.example.com/api/v4/projects/12/repository/commits/deadbeef/comments", true},
		{"post commit discussion", http.MethodPost, "https://gitlab.example.com/api/v4/projects/12/repository/commits/deadbeef/discussions", true},
		{"merge mr", http.MethodPut, "https://gitlab.example.com/api/v4/projects/12/merge_requests/1/merge", false},
		{"approve mr", http.MethodPost, "https://gitlab.example.com/api/v4/projects/12/merge_requests/1/approve", false},
		{"create branch", http.MethodPost, "https://gitlab.example.com/api/v4/projects/12/repository/branches", false},
		{"delete branch", http.MethodDelete, "https://gitlab.example.com/api/v4/projects/12/repository/branches/dev", false},
		{"edit project", http.MethodPut, "https://gitlab.example.com/api/v4/projects/12", false},
		{"delete all hooks", http.MethodDelete, "https://gitlab.example.com/api/v4/projects/12/hooks", false},
		{"nested hook path", http.MethodDelete, "https://gitlab.example.com/api/v4/projects/12/hooks/34/extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitLabWriteAllowed(tt.method, tt.url); got != tt.want {
				t.Fatalf("gitLabWriteAllowed(%q, %q) = %v, want %v", tt.method, tt.url, got, tt.want)
			}
		})
	}
}
