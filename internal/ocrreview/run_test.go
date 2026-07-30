package ocrreview

import (
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/ocr/config/template"
)

func TestFenceLang(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"wallet/wallet.go", "go"},
		{"src/App.tsx", "typescript"},
		{"lib/util.js", "javascript"},
		{"main.py", "python"},
		{"Dockerfile", "dockerfile"},
		{"Makefile", "makefile"},
		{"go.mod", "go"},
		{"unknown.xyz", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := fenceLang(tt.path); got != tt.want {
			t.Fatalf("fenceLang(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestApplyReviewPromptReplacesEmbeddedPolicy(t *testing.T) {
	tpl, err := template.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const custom = "只关注 SQL 注入，并使用中文。"
	if err := applyReviewPrompt(tpl, custom); err != nil {
		t.Fatal(err)
	}

	for _, conv := range []*template.LlmConversation{&tpl.MainTask, tpl.PlanTask} {
		if conv == nil {
			continue
		}
		for _, msg := range conv.Messages {
			if msg.Role != "system" {
				continue
			}
			if !strings.Contains(msg.Content, custom) {
				t.Fatalf("custom review prompt missing from system message")
			}
			if strings.Contains(msg.Content, "developed by Alibaba") ||
				strings.Contains(msg.Content, "expert in code review task planning") {
				t.Fatalf("embedded review policy was not replaced: %q", msg.Content)
			}
		}
	}
	if tpl.ReviewFilterTask != nil {
		t.Fatal("embedded review filter must be disabled")
	}
}

func TestApplyReviewPromptRejectsEmpty(t *testing.T) {
	tpl, err := template.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyReviewPrompt(tpl, " \n "); err == nil {
		t.Fatal("expected empty custom prompt to fail")
	}
}
