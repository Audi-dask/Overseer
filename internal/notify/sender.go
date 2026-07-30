package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
)

type Payload struct {
	Repo      string
	MRID      string
	MRURL     string
	CommitSHA string
	Status    string
	Summary   string
	Author    string
}

type Sender struct {
	HTTP *http.Client
}

func New() *Sender {
	return &Sender{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func Render(tmpl string, p Payload) string {
	repl := map[string]string{
		"{{repo}}":       p.Repo,
		"{{mr_id}}":      p.MRID,
		"{{mr_url}}":     p.MRURL,
		"{{commit_sha}}": p.CommitSHA,
		"{{status}}":     p.Status,
		"{{summary}}":    p.Summary,
		"{{author}}":     p.Author,
	}
	out := tmpl
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	return out
}

func (s *Sender) Send(ctx context.Context, ch model.NotifyChannel, text string, p Payload) error {
	if ch.Kind == model.NotifyFeishu || isFeishuWebhookURL(ch.Target) {
		return s.postJSON(ctx, ch.Target, feishuCard(p))
	}
	switch ch.Kind {
	case model.NotifyWebhook:
		return s.postJSON(ctx, ch.Target, map[string]any{
			"text":       text,
			"repo":       p.Repo,
			"mr_id":      p.MRID,
			"mr_url":     p.MRURL,
			"commit_sha": p.CommitSHA,
			"status":     p.Status,
			"summary":    p.Summary,
			"author":     p.Author,
		})
	default:
		return fmt.Errorf("unsupported notify kind: %s", ch.Kind)
	}
}

func isFeishuWebhookURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	return strings.Contains(u, "open.feishu.cn/open-apis/bot/v2/hook/") ||
		strings.Contains(u, "open.larksuite.com/open-apis/bot/v2/hook/")
}

// feishuCard builds an interactive card aligned with 参考/mr_note.py.
func feishuCard(p Payload) map[string]any {
	project := strings.TrimSpace(p.Repo)
	if project == "" {
		project = "Unknown Project"
	}
	author := strings.TrimSpace(p.Author)
	if author == "" {
		author = "Unknown Author"
	}

	elements := []any{
		map[string]any{
			"tag": "div",
			"fields": []any{
				map[string]any{
					"is_short": true,
					"text": map[string]any{
						"tag":     "lark_md",
						"content": fmt.Sprintf("**提交人：**\n%s", author),
					},
				},
				map[string]any{
					"is_short": true,
					"text": map[string]any{
						"tag":     "lark_md",
						"content": fmt.Sprintf("**项目：**\n%s", project),
					},
				},
			},
		},
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": fmt.Sprintf("**审查结论：**\n%s", cardSummary(p.Summary)),
			},
		},
	}
	if p.MRURL != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{
				map[string]any{
					"tag": "button",
					"text": map[string]any{
						"tag":     "plain_text",
						"content": "查看详细报告",
					},
					"type": "primary",
					"url":  p.MRURL,
				},
			},
		})
	}
	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "plain_text",
					"content": "Overseer · " + project,
				},
				"template": "blue",
			},
			"elements": elements,
		},
	}
}

func cardSummary(raw string) string {
	summary := strings.TrimSpace(raw)
	if summary == "" {
		summary = "审查完成"
	}
	if i := strings.IndexByte(summary, '\n'); i >= 0 {
		summary = strings.TrimSpace(summary[:i])
	}
	if !strings.HasPrefix(summary, "【总结】") {
		summary = "【自动截取】" + summary
	}
	runes := []rune(summary)
	if len(runes) > 50 {
		summary = string(runes[:50]) + "..."
	}
	return summary
}

func (s *Sender) postJSON(ctx context.Context, url string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notify %s: %s", resp.Status, string(raw))
	}
	// Feishu bot returns HTTP 200 with {"code":0} even on some errors.
	var feishu struct {
		Code       int    `json:"code"`
		Msg        string `json:"msg"`
		StatusCode int    `json:"StatusCode"`
		StatusMsg  string `json:"StatusMessage"`
	}
	if err := json.Unmarshal(raw, &feishu); err == nil {
		if feishu.Code != 0 {
			return fmt.Errorf("feishu code=%d msg=%s", feishu.Code, feishu.Msg)
		}
		if feishu.StatusCode != 0 {
			return fmt.Errorf("feishu StatusCode=%d msg=%s", feishu.StatusCode, feishu.StatusMsg)
		}
	}
	return nil
}
