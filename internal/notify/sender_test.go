package notify

import (
	"encoding/json"
	"testing"
)

func TestFeishuCardInteractive(t *testing.T) {
	body := feishuCard(Payload{
		Repo:    "devops/test-go",
		MRURL:   "https://git.example.com/devops/test-go/-/merge_requests/1",
		Summary: "【总结】未发现阻塞问题",
		Author:  "Alice",
	})
	if body["msg_type"] != "interactive" {
		t.Fatalf("msg_type = %v, want interactive", body["msg_type"])
	}
	card, ok := body["card"].(map[string]any)
	if !ok {
		t.Fatal("missing card")
	}
	header, ok := card["header"].(map[string]any)
	if !ok {
		t.Fatal("missing header")
	}
	title, ok := header["title"].(map[string]any)
	if !ok || title["content"] != "Overseer · devops/test-go" {
		t.Fatalf("unexpected title: %#v", title)
	}
	if header["template"] != "blue" {
		t.Fatalf("template = %v, want blue", header["template"])
	}
	elements, ok := card["elements"].([]any)
	if !ok || len(elements) != 3 {
		t.Fatalf("elements = %#v, want 3 items", card["elements"])
	}
	b, _ := json.Marshal(body)
	if !json.Valid(b) {
		t.Fatal("invalid json")
	}
}

func TestCardSummary(t *testing.T) {
	got := cardSummary("第一行结论\n第二行忽略")
	if got != "【自动截取】第一行结论" {
		t.Fatalf("got %q", got)
	}
	long := cardSummary("【总结】" + stringsRepeat('长', 60))
	if len([]rune(long)) > 53 {
		t.Fatalf("summary not truncated: %q", long)
	}
}

func stringsRepeat(r rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}
