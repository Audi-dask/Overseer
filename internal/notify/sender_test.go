package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/model"
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

func TestSendDingTalkActionCard(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	p := testPayload()
	sender := &Sender{HTTP: server.Client()}
	if err := sender.Send(context.Background(), model.NotifyChannel{Kind: model.NotifyDingTalk, Target: server.URL}, "ignored", p); err != nil {
		t.Fatal(err)
	}
	if got["msgtype"] != "actionCard" {
		t.Fatalf("msgtype = %v, want actionCard", got["msgtype"])
	}
	card := got["actionCard"].(map[string]any)
	if card["singleURL"] != p.MRURL || card["singleTitle"] != "查看详细报告" {
		t.Fatalf("unexpected actionCard action: %#v", card)
	}
	text := card["text"].(string)
	for _, want := range []string{p.Repo, p.Author, p.Status, cardSummary(p.Summary)} {
		if !strings.Contains(text, want) {
			t.Fatalf("actionCard text missing %q: %s", want, text)
		}
	}
}

func TestSendWeComTemplateCard(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	p := testPayload()
	sender := &Sender{HTTP: server.Client()}
	if err := sender.Send(context.Background(), model.NotifyChannel{Kind: model.NotifyWeCom, Target: server.URL}, "ignored", p); err != nil {
		t.Fatal(err)
	}
	if got["msgtype"] != "template_card" {
		t.Fatalf("msgtype = %v, want template_card", got["msgtype"])
	}
	card := got["template_card"].(map[string]any)
	if card["card_type"] != "text_notice" {
		t.Fatalf("card_type = %v, want text_notice", card["card_type"])
	}
	action := card["card_action"].(map[string]any)
	if action["url"] != p.MRURL || action["type"] != float64(1) {
		t.Fatalf("unexpected card_action: %#v", action)
	}
	jumps := card["jump_list"].([]any)
	if len(jumps) != 1 {
		t.Fatalf("jump_list = %#v, want 1 item", jumps)
	}
	jump := jumps[0].(map[string]any)
	if jump["title"] != "查看详细报告" || jump["url"] != p.MRURL || jump["type"] != float64(1) {
		t.Fatalf("unexpected jump_list item: %#v", jump)
	}
	if !strings.Contains(card["sub_title_text"].(string), cardSummary(p.Summary)) {
		t.Fatalf("unexpected summary: %v", card["sub_title_text"])
	}
}

func TestPlatformMessagesWithoutMRURLFallback(t *testing.T) {
	p := testPayload()
	p.MRURL = ""

	dingTalk := dingTalkCard(p)
	if dingTalk["msgtype"] != "markdown" || dingTalk["actionCard"] != nil {
		t.Fatalf("unexpected dingtalk fallback: %#v", dingTalk)
	}
	weCom := weComMessage(p)
	if weCom["msgtype"] != "markdown" || weCom["template_card"] != nil {
		t.Fatalf("unexpected wecom fallback: %#v", weCom)
	}
}

func TestSendPlatformBusinessErrors(t *testing.T) {
	tests := []struct {
		name string
		kind model.NotifyKind
		body string
		want string
	}{
		{name: "dingtalk", kind: model.NotifyDingTalk, body: `{"errcode":310000,"errmsg":"keywords not in content"}`, want: "dingtalk errcode=310000"},
		{name: "wecom", kind: model.NotifyWeCom, body: `{"errcode":93000,"errmsg":"invalid webhook url"}`, want: "wecom errcode=93000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			sender := &Sender{HTTP: server.Client()}
			err := sender.Send(context.Background(), model.NotifyChannel{Kind: tt.kind, Target: server.URL}, "ignored", testPayload())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestGenericWebhookDoesNotParseFeishuResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":123,"msg":"application response"}`))
	}))
	defer server.Close()
	sender := &Sender{HTTP: server.Client()}
	if err := sender.Send(context.Background(), model.NotifyChannel{Kind: model.NotifyWebhook, Target: server.URL}, "text", testPayload()); err != nil {
		t.Fatalf("generic webhook misclassified response: %v", err)
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

func testPayload() Payload {
	return Payload{
		Repo: "devops/test-go", MRID: "1",
		MRURL:     "https://git.example.com/devops/test-go/-/merge_requests/1",
		CommitSHA: "deadbeef", Status: "passed", Summary: "【总结】未发现阻塞问题", Author: "Alice",
	}
}

func stringsRepeat(r rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}
