package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/model"
)

func TestBranchHead(t *testing.T) {
	var gotPath, gotToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"commit":{"id":"abcdef123456","web_url":"https://gitlab.example/commit/abcdef"}}`))
	}))
	defer ts.Close()

	client := &Client{HTTP: ts.Client()}
	head, err := client.BranchHead(context.Background(), &model.Instance{BaseURL: ts.URL}, "secret-token", &model.Repo{ExternalID: "group/repo"}, "release/2.0")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v4/projects/group%2Frepo/repository/branches/release%2F2.0" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotToken != "secret-token" {
		t.Fatalf("token = %q", gotToken)
	}
	if head.CommitID != "abcdef123456" || head.WebURL != "https://gitlab.example/commit/abcdef" {
		t.Fatalf("head = %#v", head)
	}
}

func TestBranchHeadErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "upstream failed", http.StatusBadGateway)
		}))
		defer ts.Close()
		client := &Client{HTTP: ts.Client()}
		_, err := client.BranchHead(context.Background(), &model.Instance{BaseURL: ts.URL}, "token", &model.Repo{ExternalID: "12"}, "main")
		if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"commit":`))
		}))
		defer ts.Close()
		client := &Client{HTTP: ts.Client()}
		_, err := client.BranchHead(context.Background(), &model.Instance{BaseURL: ts.URL}, "token", &model.Repo{ExternalID: "12"}, "main")
		if err == nil || !strings.Contains(err.Error(), "decode json") {
			t.Fatalf("err = %v", err)
		}
	})
}

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
