package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/vcs"
)

type Client struct {
	HTTP *http.Client
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 45 * time.Second}}
}

func (c *Client) apiBase(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/api/v4") {
		return base
	}
	return base + "/api/v4"
}

func (c *Client) do(ctx context.Context, method, rawURL, token string, body any) (*http.Response, error) {
	if !gitLabWriteAllowed(method, rawURL) {
		return nil, fmt.Errorf("gitlab write blocked by safety policy: %s %s", method, rawURL)
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

// gitLabWriteAllowed enforces the service's VCS safety boundary at the HTTP
// transport layer. Repository access is read-only except for webhook lifecycle
// management and posting AI review notes to merge requests.
func gitLabWriteAllowed(method, rawURL string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.TrimSuffix(u.Path, "/")
	switch method {
	case http.MethodPost:
		if strings.HasSuffix(path, "/hooks") {
			return true
		}
		if strings.Contains(path, "/repository/commits/") &&
			(strings.HasSuffix(path, "/comments") || strings.HasSuffix(path, "/discussions")) {
			return true
		}
		return strings.Contains(path, "/merge_requests/") &&
			(strings.HasSuffix(path, "/notes") || strings.HasSuffix(path, "/discussions"))
	case http.MethodPut:
		const marker = "/hooks/"
		i := strings.LastIndex(path, marker)
		if i < 0 {
			return false
		}
		hookID := path[i+len(marker):]
		return hookID != "" && !strings.Contains(hookID, "/")
	case http.MethodDelete:
		const marker = "/hooks/"
		i := strings.LastIndex(path, marker)
		if i < 0 {
			return false
		}
		hookID := path[i+len(marker):]
		return hookID != "" && !strings.Contains(hookID, "/")
	default:
		return false
	}
}

type glProject struct {
	ID            int64  `json:"id"`
	PathWithNS    string `json:"path_with_namespace"`
	Visibility    string `json:"visibility"`
	DefaultBranch string `json:"default_branch"`
}

func (c *Client) toRepos(items []glProject) []model.Repo {
	out := make([]model.Repo, 0, len(items))
	for _, it := range items {
		out = append(out, model.Repo{
			ExternalID:    strconv.FormatInt(it.ID, 10),
			FullName:      it.PathWithNS,
			Private:       it.Visibility != "public",
			DefaultBranch: it.DefaultBranch,
			Webhook:       model.WebhookNone,
		})
	}
	return out
}

func (c *Client) fetchProjects(ctx context.Context, token, listURL string) ([]glProject, error) {
	var out []glProject
	page := 1
	for {
		u := listURL
		if strings.Contains(u, "?") {
			u += fmt.Sprintf("&per_page=100&page=%d", page)
		} else {
			u += fmt.Sprintf("?per_page=100&page=%d", page)
		}
		resp, err := c.do(ctx, http.MethodGet, u, token, nil)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("gitlab list projects: %s: %s", resp.Status, string(b))
		}
		var items []glProject
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		out = append(out, items...)
		if len(items) < 100 {
			break
		}
		page++
		if page > 50 {
			break
		}
	}
	return out, nil
}

func (c *Client) ListRepos(ctx context.Context, instance *model.Instance, token string) ([]model.Repo, error) {
	base := c.apiBase(instance.BaseURL)
	items, err := c.fetchProjects(ctx, token, base+"/projects?membership=true&simple=true")
	if err != nil {
		return nil, err
	}
	return c.toRepos(items), nil
}

func (c *Client) BranchHead(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, branch string) (*vcs.BranchHead, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, fmt.Errorf("default branch required")
	}
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/repository/branches/%s", base, url.PathEscape(repo.ExternalID), url.PathEscape(branch))
	resp, err := c.do(ctx, http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab get branch head: %s: %s", resp.Status, string(b))
	}
	var out struct {
		Commit struct {
			ID     string `json:"id"`
			WebURL string `json:"web_url"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("gitlab get branch head: decode json: %w", err)
	}
	if strings.TrimSpace(out.Commit.ID) == "" {
		return nil, fmt.Errorf("gitlab get branch head: commit id missing")
	}
	return &vcs.BranchHead{CommitID: out.Commit.ID, WebURL: out.Commit.WebURL}, nil
}

// SearchRepos matches GitLab groups and projects by keyword.
// Prefer group hits: if any group matches, expand its (sub)projects; also merge
// direct project-name matches. Caps at 200 candidates to keep the UI usable.
func (c *Client) SearchRepos(ctx context.Context, instance *model.Instance, token, query string) ([]model.Repo, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("search query required")
	}
	base := c.apiBase(instance.BaseURL)
	seen := map[string]struct{}{}
	var out []model.Repo

	add := func(items []glProject) {
		for _, it := range c.toRepos(items) {
			if _, ok := seen[it.ExternalID]; ok {
				continue
			}
			seen[it.ExternalID] = struct{}{}
			out = append(out, it)
		}
	}

	// 1) Groups whose name/path match → import their projects (incl. subgroups).
	groupsURL := fmt.Sprintf("%s/groups?search=%s&min_access_level=10", base, url.QueryEscape(q))
	resp, err := c.do(ctx, http.MethodGet, groupsURL+"&per_page=20", token, nil)
	if err != nil {
		return nil, err
	}
	gb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab search groups: %s: %s", resp.Status, string(gb))
	}
	var groups []struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
		Name     string `json:"name"`
	}
	_ = json.Unmarshal(gb, &groups)
	for _, g := range groups {
		pu := fmt.Sprintf("%s/groups/%d/projects?include_subgroups=true&simple=true&with_shared=false", base, g.ID)
		items, err := c.fetchProjects(ctx, token, pu)
		if err != nil {
			return nil, fmt.Errorf("group %s projects: %w", g.FullPath, err)
		}
		add(items)
		if len(out) >= 200 {
			return out[:200], nil
		}
	}

	// 2) Direct project search (name / path_with_namespace substring).
	projURL := fmt.Sprintf("%s/projects?search=%s&membership=true&simple=true", base, url.QueryEscape(q))
	items, err := c.fetchProjects(ctx, token, projURL)
	if err != nil {
		return nil, err
	}
	add(items)
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

func (c *Client) GetProjectsByIDs(ctx context.Context, instance *model.Instance, token string, externalIDs []string) ([]model.Repo, error) {
	base := c.apiBase(instance.BaseURL)
	var out []model.Repo
	for _, id := range externalIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		u := fmt.Sprintf("%s/projects/%s?simple=true", base, url.PathEscape(id))
		resp, err := c.do(ctx, http.MethodGet, u, token, nil)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("gitlab get project %s: %s: %s", id, resp.Status, string(b))
		}
		var it glProject
		if err := json.Unmarshal(b, &it); err != nil {
			return nil, err
		}
		out = append(out, c.toRepos([]glProject{it})...)
	}
	return out, nil
}

func hookEventFlags(mode model.TriggerMode) (pushEvents, mrEvents bool) {
	switch mode.OrDefault() {
	case model.TriggerPush:
		return true, false
	default:
		return false, true
	}
}

func (c *Client) hookBody(callbackURL, secret string, mode model.TriggerMode) map[string]any {
	pushEvents, mrEvents := hookEventFlags(mode)
	return map[string]any{
		"url":                     callbackURL,
		"token":                   secret,
		"push_events":             pushEvents,
		"merge_requests_events":   mrEvents,
		"enable_ssl_verification": strings.HasPrefix(callbackURL, "https://"),
		"note_events":             false,
	}
}

func (c *Client) EnsureWebhook(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, callbackURL, secret string) (string, error) {
	base := c.apiBase(instance.BaseURL)
	pid := url.PathEscape(repo.ExternalID)
	listURL := fmt.Sprintf("%s/projects/%s/hooks", base, pid)
	resp, err := c.do(ctx, http.MethodGet, listURL, token, nil)
	if err != nil {
		return "", err
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gitlab list hooks: %s: %s", resp.Status, string(b))
	}
	var hooks []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	_ = json.Unmarshal(b, &hooks)
	body := c.hookBody(callbackURL, secret, repo.TriggerMode)
	for _, h := range hooks {
		if h.URL == callbackURL {
			hookID := strconv.FormatInt(h.ID, 10)
			updateURL := fmt.Sprintf("%s/projects/%s/hooks/%s", base, pid, hookID)
			resp, err = c.do(ctx, http.MethodPut, updateURL, token, body)
			if err != nil {
				return "", err
			}
			b, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 300 {
				return "", fmt.Errorf("gitlab update hook: %s: %s", resp.Status, string(b))
			}
			return hookID, nil
		}
	}
	resp, err = c.do(ctx, http.MethodPost, listURL, token, body)
	if err != nil {
		return "", err
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gitlab create hook: %s: %s", resp.Status, string(b))
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(b, &created); err != nil {
		return "", err
	}
	return strconv.FormatInt(created.ID, 10), nil
}

func (c *Client) DeleteWebhook(ctx context.Context, instance *model.Instance, token string, repo *model.Repo) error {
	if repo.WebhookID == "" {
		return nil
	}
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/hooks/%s", base, url.PathEscape(repo.ExternalID), repo.WebhookID)
	resp, err := c.do(ctx, http.MethodDelete, u, token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab delete hook: %s: %s", resp.Status, string(b))
	}
	return nil
}

func (c *Client) ParseEvent(payload []byte, headers map[string]string, secret string) (*model.ReviewTrigger, error) {
	if secret != "" {
		token := headers["x-gitlab-token"]
		if token == "" {
			token = headers["X-Gitlab-Token"]
		}
		if token != secret {
			return nil, fmt.Errorf("invalid gitlab webhook token")
		}
	}
	event := headers["x-gitlab-event"]
	if event == "" {
		event = headers["X-Gitlab-Event"]
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	objKind, _ := raw["object_kind"].(string)
	if event == "" {
		event = objKind
	}

	switch {
	case objKind == "push" || event == "Push Hook":
		return c.parsePushEvent(raw)
	case objKind == "merge_request" || event == "Merge Request Hook":
		return c.parseMREvent(raw)
	default:
		return nil, fmt.Errorf("ignore event: %s/%s", event, objKind)
	}
}

func (c *Client) parsePushEvent(raw map[string]any) (*model.ReviewTrigger, error) {
	after, _ := raw["after"].(string)
	if after == "" || after == strings.Repeat("0", 40) {
		return nil, fmt.Errorf("ignore push: branch deleted or empty after")
	}
	ref, _ := raw["ref"].(string)
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if branch == ref {
		branch = ref
	}
	project, _ := raw["project"].(map[string]any)
	extID := fmt.Sprint(project["id"])
	path, _ := project["path_with_namespace"].(string)
	webURL, _ := project["web_url"].(string)
	commitURL := webURL
	if commitURL != "" {
		commitURL = strings.TrimRight(commitURL, "/") + "/-/commit/" + after
	}
	return &model.ReviewTrigger{
		Repo:       path,
		ExternalID: extID,
		MRID:       branch,
		CommitSHA:  after,
		EventType:  "push",
		MRURL:      commitURL,
		Project:    path,
		Branch:     branch,
		Author:     eventAuthor(raw),
	}, nil
}

func (c *Client) parseMREvent(raw map[string]any) (*model.ReviewTrigger, error) {
	attrs, _ := raw["object_attributes"].(map[string]any)
	if attrs == nil {
		return nil, fmt.Errorf("missing object_attributes")
	}
	action, _ := attrs["action"].(string)
	switch action {
	case "open", "reopen", "update", "sync":
	default:
		return nil, fmt.Errorf("ignore mr action: %s", action)
	}
	project, _ := raw["project"].(map[string]any)
	mrIID := fmt.Sprint(attrs["iid"])
	commit := ""
	if ls, ok := attrs["last_commit"].(map[string]any); ok {
		commit, _ = ls["id"].(string)
	}
	if commit == "" {
		if sha, ok := attrs["merge_commit_sha"].(string); ok {
			commit = sha
		}
	}
	extID := fmt.Sprint(project["id"])
	path, _ := project["path_with_namespace"].(string)
	webURL, _ := attrs["url"].(string)
	branch, _ := attrs["source_branch"].(string)
	return &model.ReviewTrigger{
		Repo:       path,
		ExternalID: extID,
		MRID:       mrIID,
		CommitSHA:  commit,
		EventType:  "merge_request." + action,
		MRURL:      webURL,
		Project:    path,
		Branch:     branch,
		Author:     eventAuthor(raw),
	}, nil
}

func eventAuthor(raw map[string]any) string {
	if u, ok := raw["user"].(map[string]any); ok {
		if name, _ := u["name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	if name, _ := raw["user_name"].(string); strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return ""
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func (c *Client) PostComment(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, mrID, content string) error {
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes", base, url.PathEscape(repo.ExternalID), mrID)
	resp, err := c.do(ctx, http.MethodPost, u, token, map[string]any{"body": content})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab post note: %s: %s", resp.Status, string(b))
	}
	return nil
}

func (c *Client) PostCommitComment(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, commitSHA, content string) error {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return fmt.Errorf("commit sha required")
	}
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/repository/commits/%s/comments",
		base, url.PathEscape(repo.ExternalID), url.PathEscape(commitSHA))
	resp, err := c.do(ctx, http.MethodPost, u, token, map[string]any{"note": content})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab post commit comment: %s: %s", resp.Status, string(b))
	}
	return nil
}

// PostCommitInlineComments adds line-anchored notes on a commit diff via the
// commit comments API (Push 触发；无 MR discussion 线程).
func (c *Client) PostCommitInlineComments(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, commitSHA string, comments []vcs.InlineComment) error {
	if len(comments) == 0 {
		return nil
	}
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return fmt.Errorf("commit sha required")
	}
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/repository/commits/%s/comments",
		base, url.PathEscape(repo.ExternalID), url.PathEscape(commitSHA))
	var errs []string
	posted := 0
	for _, cm := range comments {
		if cm.StartLine <= 0 || strings.TrimSpace(cm.Path) == "" {
			continue
		}
		body := formatInlineBody(cm)
		resp, err := c.do(ctx, http.MethodPost, u, token, map[string]any{
			"note":      body,
			"path":      cm.Path,
			"line":      cm.StartLine,
			"line_type": "new",
		})
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			errs = append(errs, fmt.Sprintf("%s:%d %s", cm.Path, cm.StartLine, string(b)))
			continue
		}
		posted++
	}
	if posted == 0 && len(errs) > 0 {
		return fmt.Errorf("gitlab commit inline failed: %s", strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("gitlab commit inline partial (%d ok): %s", posted, strings.Join(errs, "; "))
	}
	return nil
}

type mrDiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	StartSHA string `json:"start_sha"`
	HeadSHA  string `json:"head_sha"`
}

func (c *Client) getMRDiffRefs(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, mrID string) (*mrDiffRefs, error) {
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/merge_requests/%s", base, url.PathEscape(repo.ExternalID), mrID)
	resp, err := c.do(ctx, http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab get mr: %s: %s", resp.Status, string(b))
	}
	var out struct {
		DiffRefs mrDiffRefs `json:"diff_refs"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out.DiffRefs.BaseSHA == "" || out.DiffRefs.HeadSHA == "" {
		return nil, fmt.Errorf("gitlab mr missing diff_refs")
	}
	if out.DiffRefs.StartSHA == "" {
		out.DiffRefs.StartSHA = out.DiffRefs.BaseSHA
	}
	return &out.DiffRefs, nil
}

// PostInlineComments creates diff discussions for line-anchored review findings.
// Comments without StartLine are skipped. Failures are returned as a joined error
// after attempting all comments (summary note should already be posted).
func (c *Client) PostInlineComments(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, mrID string, comments []vcs.InlineComment) ([]vcs.InlineComment, error) {
	if len(comments) == 0 {
		return nil, nil
	}
	refs, err := c.getMRDiffRefs(ctx, instance, token, repo, mrID)
	if err != nil {
		return comments, err
	}
	base := c.apiBase(instance.BaseURL)
	u := fmt.Sprintf("%s/projects/%s/merge_requests/%s/discussions", base, url.PathEscape(repo.ExternalID), mrID)
	var errs []string
	var failed []vcs.InlineComment
	posted := 0
	for _, cm := range comments {
		if cm.StartLine <= 0 || strings.TrimSpace(cm.Path) == "" {
			failed = append(failed, cm)
			continue
		}
		body := formatInlineBody(cm)
		pos := map[string]any{
			"base_sha":      refs.BaseSHA,
			"start_sha":     refs.StartSHA,
			"head_sha":      refs.HeadSHA,
			"position_type": "text",
			"new_path":      cm.Path,
			"old_path":      cm.Path,
			"new_line":      cm.StartLine,
		}
		resp, err := c.do(ctx, http.MethodPost, u, token, map[string]any{
			"body":     body,
			"position": pos,
		})
		if err != nil {
			failed = append(failed, cm)
			errs = append(errs, err.Error())
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			failed = append(failed, cm)
			errs = append(errs, fmt.Sprintf("%s:%d %s", cm.Path, cm.StartLine, string(b)))
			continue
		}
		posted++
	}
	if len(errs) > 0 {
		return failed, fmt.Errorf("gitlab inline partial (%d ok): %s", posted, strings.Join(errs, "; "))
	}
	return failed, nil
}

func formatInlineBody(cm vcs.InlineComment) string {
	var b strings.Builder
	sev := cm.Severity
	if sev == "" {
		sev = "info"
	}
	cat := cm.Category
	if cat == "" {
		cat = "review"
	}
	b.WriteString(fmt.Sprintf("**[%s / %s]**\n\n", sev, cat))
	b.WriteString(strings.TrimSpace(cm.Content))
	if code := strings.TrimSpace(cm.SuggestionCode); code != "" {
		b.WriteString("\n\n")
		// GitLab Apply suggestion: offsets relative to the anchored line.
		end := cm.EndLine
		if end < cm.StartLine {
			end = cm.StartLine
		}
		extra := end - cm.StartLine
		b.WriteString(fmt.Sprintf("```suggestion:-0+%d\n", extra))
		b.WriteString(code)
		if !strings.HasSuffix(code, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("```")
	}
	return b.String()
}
