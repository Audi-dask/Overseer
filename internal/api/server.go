package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Audi-dask/Overseer/internal/auth"
	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/notify"
	"github.com/Audi-dask/Overseer/internal/pipeline"
	"github.com/Audi-dask/Overseer/internal/queue"
	"github.com/Audi-dask/Overseer/internal/runlog"
	"github.com/Audi-dask/Overseer/internal/secretbox"
	"github.com/Audi-dask/Overseer/internal/store"
	"github.com/Audi-dask/Overseer/internal/vcs"
	"github.com/Audi-dask/Overseer/internal/vcs/gitlab"
)

var Version = "dev"

type Server struct {
	Store  *store.Store
	Queue  *queue.DebouncedQueue
	Runner *pipeline.Runner
	Notify *notify.Sender
	Box    *secretbox.Box
	Auth   *auth.Service
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/setup", s.authSetup)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("GET /api/auth/me", s.authMe)
	mux.HandleFunc("POST /api/auth/password", s.changePassword)
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/instances", s.listInstances)
	mux.HandleFunc("POST /api/instances", s.createInstance)
	mux.HandleFunc("PUT /api/instances/{id}", s.updateInstance)
	mux.HandleFunc("DELETE /api/instances/{id}", s.deleteInstance)
	mux.HandleFunc("POST /api/instances/{id}/discover", s.discoverRepos)
	mux.HandleFunc("POST /api/instances/{id}/import", s.importRepos)
	mux.HandleFunc("POST /api/instances/{id}/sync", s.syncRepos)
	mux.HandleFunc("POST /api/instances/{id}/purge-inactive", s.purgeInactive)
	mux.HandleFunc("GET /api/repos", s.listRepos)
	mux.HandleFunc("DELETE /api/repos/{id}", s.deleteRepo)
	mux.HandleFunc("POST /api/repos/{id}/review", s.setReview)
	mux.HandleFunc("PUT /api/repos/{id}/trigger-mode", s.setTriggerMode)
	mux.HandleFunc("POST /api/repos/{id}/notify", s.setNotify)
	mux.HandleFunc("GET /api/llm-providers", s.listLLM)
	mux.HandleFunc("POST /api/llm-providers", s.upsertLLM)
	mux.HandleFunc("DELETE /api/llm-providers/{id}", s.deleteLLM)
	mux.HandleFunc("GET /api/prompts", s.getPrompt)
	mux.HandleFunc("PUT /api/prompts", s.savePrompt)
	mux.HandleFunc("GET /api/firewall", s.getFirewall)
	mux.HandleFunc("PUT /api/firewall", s.saveFirewall)
	mux.HandleFunc("GET /api/reviews", s.listReviews)
	mux.HandleFunc("GET /api/reviews/{id}/log", s.getReviewLog)
	mux.HandleFunc("POST /api/reviews/{id}/rerun", s.rerunReview)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.saveSettings)
	mux.HandleFunc("GET /api/notifications", s.getNotifications)
	mux.HandleFunc("POST /api/notifications/channels", s.upsertChannel)
	mux.HandleFunc("DELETE /api/notifications/channels/{id}", s.deleteChannel)
	mux.HandleFunc("POST /api/notifications/channels/{id}/test", s.testChannel)
	mux.HandleFunc("POST /api/notifications/groups", s.upsertGroup)
	mux.HandleFunc("DELETE /api/notifications/groups/{id}", s.deleteGroup)
	mux.HandleFunc("PUT /api/notifications/template", s.saveNotifyTemplate)
	mux.HandleFunc("POST /hooks/{instanceID}", s.webhook)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func settingsCallback(st *model.Settings) string {
	if st == nil {
		return ""
	}
	return st.CallbackBaseURL
}

func hookPrereqWarning(inst *model.Instance, token string, tokenErr error, settings *model.Settings, providerOK bool) string {
	if !providerOK {
		return "不支持的 VCS 类型"
	}
	if settings == nil || strings.TrimSpace(settings.CallbackBaseURL) == "" {
		return "请先在「服务设置」填写对外回调 Base URL"
	}
	if inst != nil && inst.HasCred && token == "" {
		if tokenErr != nil {
			return "实例 Token 无法解密（MASTER_KEY 变更或未设置？），请在「实例」页重新填写 GitLab Token"
		}
	}
	if token == "" {
		return "请在「实例」页填写 GitLab Token（Personal Access Token）"
	}
	return ""
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.Overview(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListInstances(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.Instance{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string        `json:"name"`
		Type    model.VCSType `json:"type"`
		BaseURL string        `json:"base_url"`
		Token   string        `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	inst, err := s.Store.CreateInstance(r.Context(), store.CreateInstanceInput{
		Name: in.Name, Type: in.Type, BaseURL: strings.TrimRight(in.BaseURL, "/"), Token: in.Token,
	})
	if err != nil {
		log.Printf("create instance failed: %v", err)
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("instance created: id=%s name=%s base=%s token=%t", inst.ID, inst.Name, inst.BaseURL, in.Token != "")
	writeJSON(w, 201, inst)
}

func (s *Server) updateInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if _, err := s.Store.GetInstance(r.Context(), id); err != nil {
		writeErr(w, 404, "instance not found")
		return
	}
	inst, err := s.Store.UpdateInstance(r.Context(), id, store.UpdateInstanceInput{
		Name: in.Name, BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), Token: in.Token,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("instance updated: id=%s name=%s base=%s token_replaced=%t",
		inst.ID, inst.Name, inst.BaseURL, strings.TrimSpace(in.Token) != "")
	writeJSON(w, 200, inst)
}

func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, err := s.Store.GetInstance(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "instance not found")
		return
	}
	// Best-effort hook cleanup so we don't leave orphan hooks pointing at us.
	if token, _ := s.Store.GetInstanceToken(r.Context(), id); token != "" {
		if p := s.provider(inst.Type); p != nil {
			repos, _ := s.Store.ListRepos(r.Context(), id)
			for i := range repos {
				if repos[i].Webhook == model.WebhookOK {
					_ = p.DeleteWebhook(r.Context(), inst, token, &repos[i])
				}
			}
		}
	}
	if err := s.Store.DeleteInstance(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) provider(t model.VCSType) vcs.Provider {
	switch t {
	case model.VCSGitLab:
		return gitlab.New()
	default:
		return nil
	}
}

func (s *Server) discoverRepos(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Query string `json:"q"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Query) == "" {
		writeErr(w, 400, "q (group / project keyword) required")
		return
	}
	inst, token, p, ok := s.instanceProvider(w, r, id)
	if !ok {
		return
	}
	log.Printf("discover repos: instance=%s q=%q", inst.ID, in.Query)
	found, err := p.SearchRepos(r.Context(), inst, token, in.Query)
	if err != nil {
		log.Printf("discover failed: instance=%s: %v", inst.ID, err)
		writeErr(w, 502, err.Error())
		return
	}
	existing, _ := s.Store.ListRepos(r.Context(), id)
	have := map[string]bool{}
	for _, er := range existing {
		have[er.ExternalID] = true
	}
	type candidate struct {
		model.Repo
		Imported bool `json:"imported"`
	}
	out := make([]candidate, 0, len(found))
	for _, repo := range found {
		out = append(out, candidate{Repo: repo, Imported: have[repo.ExternalID]})
	}
	log.Printf("discover ok: instance=%s q=%q hits=%d", inst.ID, in.Query, len(out))
	writeJSON(w, 200, map[string]any{"query": in.Query, "candidates": out, "count": len(out)})
}

func (s *Server) importRepos(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		ExternalIDs []string `json:"external_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.ExternalIDs) == 0 {
		writeErr(w, 400, "external_ids required")
		return
	}
	if len(in.ExternalIDs) > 200 {
		writeErr(w, 400, "import at most 200 repos per request")
		return
	}
	inst, token, _, ok := s.instanceProvider(w, r, id)
	if !ok {
		return
	}
	gl, ok := s.provider(inst.Type).(*gitlab.Client)
	if !ok || gl == nil {
		writeErr(w, 400, "unsupported vcs type in phase1 (gitlab only)")
		return
	}
	log.Printf("import repos: instance=%s count=%d", inst.ID, len(in.ExternalIDs))
	repos, err := gl.GetProjectsByIDs(r.Context(), inst, token, in.ExternalIDs)
	if err != nil {
		log.Printf("import fetch failed: instance=%s: %v", inst.ID, err)
		writeErr(w, 502, err.Error())
		return
	}
	if err := s.Store.UpsertRepos(r.Context(), id, repos); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	list, _ := s.Store.ListRepos(r.Context(), id)
	log.Printf("import ok: instance=%s imported=%d total=%d", inst.ID, len(repos), len(list))
	writeJSON(w, 200, map[string]any{"imported": len(repos), "total": len(list)})
}

// syncRepos refreshes metadata for already-imported repos only (no new imports).
func (s *Server) syncRepos(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, token, p, ok := s.instanceProvider(w, r, id)
	if !ok {
		return
	}
	existing, err := s.Store.ListRepos(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(existing) == 0 {
		writeJSON(w, 200, map[string]any{"synced": 0, "message": "no imported repos; use discover+import"})
		return
	}
	want := map[string]struct{}{}
	for _, er := range existing {
		want[er.ExternalID] = struct{}{}
	}
	log.Printf("sync repos: instance=%s refresh_want=%d", inst.ID, len(want))
	all, err := p.ListRepos(r.Context(), inst, token)
	if err != nil {
		log.Printf("sync repos failed: instance=%s: %v", inst.ID, err)
		writeErr(w, 502, err.Error())
		return
	}
	var refresh []model.Repo
	for _, repo := range all {
		if _, ok := want[repo.ExternalID]; ok {
			refresh = append(refresh, repo)
		}
	}
	if err := s.Store.UpsertRepos(r.Context(), id, refresh); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("sync repos ok: instance=%s refreshed=%d", inst.ID, len(refresh))
	writeJSON(w, 200, map[string]any{"synced": len(refresh), "instance_id": id})
}

func (s *Server) purgeInactive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Store.GetInstance(r.Context(), id); err != nil {
		writeErr(w, 404, "instance not found")
		return
	}
	n, err := s.Store.DeleteInactiveRepos(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("purge inactive: instance=%s deleted=%d", id, n)
	writeJSON(w, 200, map[string]any{"deleted": n})
}

func (s *Server) deleteRepo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	repo, err := s.Store.GetRepo(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "repo not found")
		return
	}
	if repo.ReviewEnabled {
		writeErr(w, 409, "disable review first")
		return
	}
	if err := s.Store.DeleteRepo(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) instanceProvider(w http.ResponseWriter, r *http.Request, id string) (*model.Instance, string, vcs.Provider, bool) {
	inst, err := s.Store.GetInstance(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "instance not found")
		return nil, "", nil, false
	}
	token, err := s.Store.GetInstanceToken(r.Context(), id)
	if err != nil || token == "" {
		writeErr(w, 400, "instance token missing")
		return nil, "", nil, false
	}
	p := s.provider(inst.Type)
	if p == nil {
		writeErr(w, 400, "unsupported vcs type in phase1 (gitlab only)")
		return nil, "", nil, false
	}
	return inst, token, p, true
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	list, err := s.Store.ListRepos(r.Context(), instanceID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.Repo{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) ensureRepoWebhook(ctx context.Context, repo *model.Repo, inst *model.Instance, token string, settings *model.Settings) (string, error) {
	p := s.provider(inst.Type)
	if p == nil || token == "" || settings == nil || settings.CallbackBaseURL == "" {
		return "", fmt.Errorf("missing provider/token/callback")
	}
	cb := strings.TrimRight(settings.CallbackBaseURL, "/") + "/hooks/" + inst.ID
	return p.EnsureWebhook(ctx, inst, token, repo, cb, settings.WebhookSecret)
}

// syncReviewWebhooks pushes callback URL / secret / trigger flags to GitLab for all enabled repos.
func (s *Server) syncReviewWebhooks(ctx context.Context, settings *model.Settings) []string {
	if settings == nil || settings.CallbackBaseURL == "" {
		return nil
	}
	instances, err := s.Store.ListInstances(ctx)
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, inst := range instances {
		token, tokenErr := s.Store.GetInstanceToken(ctx, inst.ID)
		repos, err := s.Store.ListRepos(ctx, inst.ID)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		for i := range repos {
			repo := repos[i]
			if !repo.ReviewEnabled {
				continue
			}
			if msg := hookPrereqWarning(&inst, token, tokenErr, settings, s.provider(inst.Type) != nil); msg != "" {
				_ = s.Store.UpdateRepoWebhook(ctx, repo.ID, repo.WebhookID, model.WebhookPending)
				warnings = append(warnings, fmt.Sprintf("%s: %s", repo.FullName, msg))
				continue
			}
			instCopy := inst
			wid, err := s.ensureRepoWebhook(ctx, &repo, &instCopy, token, settings)
			if err != nil {
				_ = s.Store.UpdateRepoWebhook(ctx, repo.ID, repo.WebhookID, model.WebhookPending)
				warnings = append(warnings, fmt.Sprintf("%s: %v", repo.FullName, err))
				continue
			}
			_ = s.Store.UpdateRepoWebhook(ctx, repo.ID, wid, model.WebhookOK)
			log.Printf("sync hook ok: repo=%s hook=%s mode=%s", repo.FullName, wid, repo.TriggerMode)
		}
	}
	return warnings
}

func (s *Server) setReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	repo, err := s.Store.GetRepo(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "repo not found")
		return
	}
	inst, err := s.Store.GetInstance(r.Context(), repo.InstanceID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	token, tokenErr := s.Store.GetInstanceToken(r.Context(), inst.ID)
	settings, _ := s.Store.GetSettings(r.Context())
	p := s.provider(inst.Type)

	if err := s.Store.SetRepoReview(r.Context(), id, in.Enabled); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	repo, _ = s.Store.GetRepo(r.Context(), id)

	log.Printf("set review: repo=%s enabled=%t", repo.FullName, in.Enabled)
	if in.Enabled {
		if msg := hookPrereqWarning(inst, token, tokenErr, settings, p != nil); msg != "" {
			_ = s.Store.UpdateRepoWebhook(r.Context(), id, "", model.WebhookPending)
			log.Printf("hook pending: repo=%s token_err=%v callback=%q", repo.FullName, tokenErr, settingsCallback(settings))
			writeJSON(w, 200, map[string]any{"repo": repo, "warning": msg})
			return
		}
		wid, err := s.ensureRepoWebhook(r.Context(), repo, inst, token, settings)
		if err != nil {
			_ = s.Store.UpdateRepoWebhook(r.Context(), id, "", model.WebhookPending)
			log.Printf("ensure hook failed: repo=%s: %v", repo.FullName, err)
			writeErr(w, 502, "ensure webhook: "+err.Error())
			return
		}
		log.Printf("ensure hook ok: repo=%s hook=%s mode=%s", repo.FullName, wid, repo.TriggerMode)
		_ = s.Store.UpdateRepoWebhook(r.Context(), id, wid, model.WebhookOK)
	} else if p != nil && token != "" {
		if err := p.DeleteWebhook(r.Context(), inst, token, repo); err != nil {
			log.Printf("delete hook failed: repo=%s: %v", repo.FullName, err)
		}
		_ = s.Store.UpdateRepoWebhook(r.Context(), id, "", model.WebhookNone)
	}
	repo, _ = s.Store.GetRepo(r.Context(), id)
	writeJSON(w, 200, repo)
}

func (s *Server) setTriggerMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Mode model.TriggerMode `json:"trigger_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if !in.Mode.Valid() {
		writeErr(w, 400, "trigger_mode 必须是 mr 或 push")
		return
	}
	repo, err := s.Store.GetRepo(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "repo not found")
		return
	}
	if err := s.Store.UpdateRepoTriggerMode(r.Context(), id, in.Mode); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	repo, _ = s.Store.GetRepo(r.Context(), id)

	if repo.ReviewEnabled {
		inst, err := s.Store.GetInstance(r.Context(), repo.InstanceID)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		token, tokenErr := s.Store.GetInstanceToken(r.Context(), inst.ID)
		settings, _ := s.Store.GetSettings(r.Context())
		if msg := hookPrereqWarning(inst, token, tokenErr, settings, s.provider(inst.Type) != nil); msg != "" {
			_ = s.Store.UpdateRepoWebhook(r.Context(), id, repo.WebhookID, model.WebhookPending)
			repo, _ = s.Store.GetRepo(r.Context(), id)
			writeJSON(w, 200, map[string]any{
				"repo":    repo,
				"warning": "触发来源已保存，但钩子待配置：" + msg,
			})
			return
		}
		wid, err := s.ensureRepoWebhook(r.Context(), repo, inst, token, settings)
		if err != nil {
			_ = s.Store.UpdateRepoWebhook(r.Context(), id, repo.WebhookID, model.WebhookPending)
			repo, _ = s.Store.GetRepo(r.Context(), id)
			writeJSON(w, 200, map[string]any{
				"repo":    repo,
				"warning": "触发来源已保存，但更新 GitLab 钩子失败: " + err.Error(),
			})
			return
		}
		_ = s.Store.UpdateRepoWebhook(r.Context(), id, wid, model.WebhookOK)
		repo, _ = s.Store.GetRepo(r.Context(), id)
	}

	log.Printf("set trigger mode: repo=%s mode=%s webhook=%s", repo.FullName, repo.TriggerMode, repo.Webhook)
	writeJSON(w, 200, repo)
}

func (s *Server) setNotify(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Enabled bool   `json:"enabled"`
		GroupID string `json:"notification_group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	repo, err := s.Store.GetRepo(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "repo not found")
		return
	}
	if !repo.ReviewEnabled {
		writeErr(w, 409, "enable review first")
		return
	}
	if in.GroupID == "" {
		in.GroupID = repo.NotificationGroupID
	}
	if err := s.Store.SetRepoNotify(r.Context(), id, in.Enabled, in.GroupID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	repo, _ = s.Store.GetRepo(r.Context(), id)
	writeJSON(w, 200, repo)
}

func (s *Server) listLLM(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListLLMProviders(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.LLMProvider{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) upsertLLM(w http.ResponseWriter, r *http.Request) {
	var in store.UpsertLLMInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	p, err := s.Store.UpsertLLMProvider(r.Context(), in)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deleteLLM(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteLLMProvider(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) getPrompt(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetPrompt(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) savePrompt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		writeErr(w, 400, "审查提示词不能为空")
		return
	}
	if err := s.Store.SavePrompt(r.Context(), in.Name, in.Body); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) getFirewall(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.GetFirewall(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

func (s *Server) saveFirewall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules string `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if err := s.Store.SaveFirewall(r.Context(), in.Rules); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListReviews(r.Context(), 100)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.Review{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) getReviewLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Store.GetReview(r.Context(), id); err != nil {
		writeErr(w, 404, "review not found")
		return
	}
	text, err := runlog.Read(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "log": text})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	acc, _ := s.Store.GetAdminAccount(r.Context())
	username := "admin"
	if acc != nil {
		username = acc.Username
	}
	writeJSON(w, 200, map[string]any{
		"callback_base_url":     st.CallbackBaseURL,
		"webhook_secret":        st.WebhookSecret,
		"master_key_configured": true,
		"max_concurrency":       st.MaxConcurrency,
		"debounce_sec":          st.DebounceSec,
		"review_retention_days": st.ReviewRetentionDays,
		"admin_username":        username,
		"admin_auth_enabled":    true,
		"runtime": map[string]any{
			"version": Version,
			"listen":  ":8080",
		},
	})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CallbackBaseURL     string `json:"callback_base_url"`
		WebhookSecret       string `json:"webhook_secret"`
		MaxConcurrency      int    `json:"max_concurrency"`
		DebounceSec         int    `json:"debounce_sec"`
		ReviewRetentionDays *int   `json:"review_retention_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	st := model.Settings{
		CallbackBaseURL: in.CallbackBaseURL,
		WebhookSecret:   in.WebhookSecret,
		MaxConcurrency:  in.MaxConcurrency,
		DebounceSec:     in.DebounceSec,
	}
	if in.ReviewRetentionDays == nil {
		current, err := s.Store.GetSettings(r.Context())
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		st.ReviewRetentionDays = current.ReviewRetentionDays
	} else {
		st.ReviewRetentionDays = *in.ReviewRetentionDays
	}
	if st.ReviewRetentionDays < 0 {
		writeErr(w, 400, "审查日志保留天数不能小于 0")
		return
	}
	if err := s.Store.SaveSettings(r.Context(), st); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	saved, _ := s.Store.GetSettings(r.Context())
	warnings := s.syncReviewWebhooks(r.Context(), saved)
	out := map[string]any{"status": "ok"}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	writeJSON(w, 200, out)
}

func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	channels, err := s.Store.ListNotifyChannels(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	groups, err := s.Store.ListNotifyGroups(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if channels == nil {
		channels = []model.NotifyChannel{}
	}
	if groups == nil {
		groups = []model.NotifyGroup{}
	}
	// Never leak plaintext webhook URLs to the UI.
	for i := range channels {
		channels[i].Target = ""
	}
	tmpl, err := s.Store.GetNotifyTemplate(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"template":  tmpl,
		"variables": store.NotifyVars(),
		"channels":  channels,
		"groups":    groups,
	})
}

func (s *Server) upsertChannel(w http.ResponseWriter, r *http.Request) {
	var in store.UpsertChannelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if strings.TrimSpace(in.Name) == "" || in.Kind == "" {
		writeErr(w, 400, "name and kind required")
		return
	}
	if in.ID == "" && strings.TrimSpace(in.Target) == "" {
		writeErr(w, 400, "target required")
		return
	}
	if in.Kind != model.NotifyFeishu && in.Kind != model.NotifyWebhook &&
		in.Kind != model.NotifyWeCom && in.Kind != model.NotifyDingTalk {
		writeErr(w, 400, "kind must be feishu, webhook, wecom, or dingtalk")
		return
	}
	ch, err := s.Store.UpsertNotifyChannel(r.Context(), in)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ch.Target = ""
	log.Printf("notify channel saved: id=%s kind=%s", ch.ID, ch.Kind)
	writeJSON(w, 200, ch)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteNotifyChannel(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("notify channel deleted: %s", id)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ch, err := s.Store.GetNotifyChannel(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "channel not found")
		return
	}
	if ch.Target == "" {
		writeErr(w, 400, "channel target empty")
		return
	}
	tmpl, _ := s.Store.GetNotifyTemplate(r.Context())
	p := notify.Payload{
		Repo: "devops/test-go", MRID: "0", MRURL: "https://example.com/mr/0",
		CommitSHA: "deadbeef", Status: "test", Summary: "这是一条测试通知", Author: "Overseer Test",
	}
	text := notify.Render(tmpl, p)
	if s.Notify == nil {
		s.Notify = notify.New()
	}
	if err := s.Notify.Send(r.Context(), *ch, text, p); err != nil {
		log.Printf("notify test failed: channel=%s: %v", id, err)
		writeErr(w, 502, err.Error())
		return
	}
	log.Printf("notify test ok: channel=%s", id)
	writeJSON(w, 200, map[string]string{"status": "sent"})
}

func (s *Server) upsertGroup(w http.ResponseWriter, r *http.Request) {
	var in store.UpsertGroupInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	if len(in.ChannelIDs) == 0 {
		writeErr(w, 400, "channel_ids required")
		return
	}
	g, err := s.Store.UpsertNotifyGroup(r.Context(), in)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("notify group saved: id=%s channels=%d", g.ID, len(g.ChannelIDs))
	writeJSON(w, 200, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteNotifyGroup(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("notify group deleted: %s", id)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) saveNotifyTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if err := s.Store.SaveNotifyTemplate(r.Context(), in.Template); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// rerunReview replays a stored review with Force=true so the same commit can be
// reviewed again without opening a new MR.
func (s *Server) rerunReview(w http.ResponseWriter, r *http.Request) {
	rv, err := s.Store.GetReview(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "review not found")
		return
	}
	repo, err := s.Store.GetRepo(r.Context(), rv.RepoID)
	if err != nil {
		writeErr(w, 404, "repo not found")
		return
	}
	if rv.CommitSHA == "" {
		writeErr(w, 400, "该记录缺少 commit，无法重跑")
		return
	}
	eventType := "manual.rerun"
	if repo.TriggerMode.OrDefault() == model.TriggerPush {
		eventType = "push"
	}
	job := queue.Job{
		InstanceID: repo.InstanceID,
		Force:      true,
		Trigger: model.ReviewTrigger{
			InstanceID: repo.InstanceID,
			Repo:       repo.FullName,
			ExternalID: repo.ExternalID,
			MRID:       rv.MRID,
			CommitSHA:  rv.CommitSHA,
			EventType:  eventType,
			MRURL:      rv.MRURL,
			Project:    repo.FullName,
			Branch:     rv.MRID, // push 记录里 MRID 存的是分支名
		},
	}
	s.Queue.EnqueueNow(job)
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceID")
	inst, err := s.Store.GetInstance(r.Context(), instanceID)
	if err != nil {
		writeErr(w, 404, "instance not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, 400, "read body")
		return
	}
	headers := map[string]string{}
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
			headers[strings.ToLower(k)] = v[0]
		}
	}
	settings, _ := s.Store.GetSettings(r.Context())
	secret := ""
	if settings != nil {
		secret = settings.WebhookSecret
	}
	p := s.provider(inst.Type)
	if p == nil {
		writeErr(w, 400, "unsupported vcs")
		return
	}
	tr, err := p.ParseEvent(body, headers, secret)
	if err != nil {
		// ACK ignored events to avoid GitLab retries storm
		log.Printf("webhook ignore %s: %v", instanceID, err)
		writeJSON(w, 202, map[string]string{"status": "ignored", "reason": err.Error()})
		return
	}
	tr.InstanceID = instanceID

	// Per-repo trigger policy (default: all MR actions). Skip early to avoid queue noise.
	repos, _ := s.Store.ListRepos(r.Context(), instanceID)
	var matched *model.Repo
	for i := range repos {
		if repos[i].ExternalID == tr.ExternalID || repos[i].FullName == tr.Repo {
			matched = &repos[i]
			break
		}
	}
	if matched == nil || !matched.ReviewEnabled {
		log.Printf("webhook ignore %s: repo not enabled (%s)", instanceID, tr.Repo)
		writeJSON(w, 202, map[string]string{"status": "ignored", "reason": "repo not enabled"})
		return
	}
	if !matched.TriggerMode.MatchesEvent(tr.EventType) {
		log.Printf("webhook ignore %s: trigger mode=%s event=%s repo=%s",
			instanceID, matched.TriggerMode, tr.EventType, tr.Repo)
		writeJSON(w, 202, map[string]string{"status": "ignored", "reason": "trigger mode mismatch"})
		return
	}

	log.Printf("hook accepted: instance=%s event=%s repo=%s mr=%s sha=%s",
		instanceID, tr.EventType, tr.Repo, tr.MRID, tr.CommitSHA)
	s.Queue.Enqueue(queue.Job{Trigger: *tr, InstanceID: instanceID})
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}
