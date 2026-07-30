package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/notify"
	"github.com/Audi-dask/Overseer/internal/ocrreview"
	"github.com/Audi-dask/Overseer/internal/queue"
	"github.com/Audi-dask/Overseer/internal/runlog"
	"github.com/Audi-dask/Overseer/internal/store"
	"github.com/Audi-dask/Overseer/internal/vcs"
	"github.com/Audi-dask/Overseer/internal/vcs/gitlab"
	"github.com/Audi-dask/Overseer/internal/workspace"
)

type Runner struct {
	Store  *store.Store
	GL     *gitlab.Client
	Notify *notify.Sender
}

func (r *Runner) ProviderFor(t model.VCSType) vcs.Provider {
	switch t {
	case model.VCSGitLab:
		return r.GL
	default:
		return nil
	}
}

func (r *Runner) Handle(ctx context.Context, job queue.Job) {
	start := time.Now()
	tr := job.Trigger
	inst, err := r.Store.GetInstance(ctx, job.InstanceID)
	if err != nil {
		return
	}
	repos, err := r.Store.ListRepos(ctx, job.InstanceID)
	if err != nil {
		return
	}
	var repo *model.Repo
	for i := range repos {
		if repos[i].ExternalID == tr.ExternalID || repos[i].FullName == tr.Repo {
			repo = &repos[i]
			break
		}
	}
	if repo == nil || !repo.ReviewEnabled {
		return
	}

	if !job.Force {
		ok, _ := r.Store.HasReviewedCommit(ctx, repo.FullName, tr.MRID, tr.CommitSHA)
		if ok {
			_ = r.Store.InsertReview(ctx, &model.Review{
				RepoID: repo.ID, Repo: repo.FullName, MRID: tr.MRID, CommitSHA: tr.CommitSHA,
				Status: model.ReviewSkipped, Error: "commit 已审查过", MRURL: tr.MRURL,
			})
			return
		}
	}

	settings, _ := r.Store.GetSettings(ctx)
	concurrency := 4
	if settings != nil {
		if settings.MaxConcurrency > 0 {
			concurrency = settings.MaxConcurrency
		}
	}

	rec := &model.Review{
		RepoID: repo.ID, Repo: repo.FullName, MRID: tr.MRID, CommitSHA: tr.CommitSHA,
		Status: model.ReviewRunning, MRURL: tr.MRURL,
	}
	_ = r.Store.InsertReview(ctx, rec)

	// From here on the whole run logs into this review's own log file; the
	// server terminal keeps only access logs.
	if sink, err := runlog.Open(rec.ID); err == nil {
		defer sink.Close()
		ctx = runlog.With(ctx, sink)
	}
	runlog.Printf(ctx, "pipeline: start %s !%s %s mode=agent event=%s force=%t",
		repo.FullName, tr.MRID, shortSHA(tr.CommitSHA), tr.EventType, job.Force)

	token, err := r.Store.GetInstanceToken(ctx, inst.ID)
	if err != nil || token == "" {
		r.fail(ctx, rec.ID, repo, tr, start, "missing instance token")
		return
	}
	prov := r.ProviderFor(inst.Type)
	if prov == nil {
		r.fail(ctx, rec.ID, repo, tr, start, "unsupported vcs: "+string(inst.Type))
		return
	}

	content, inline, commentCount, err := r.runAgent(ctx, inst, token, repo, tr, concurrency)
	if err != nil {
		r.fail(ctx, rec.ID, repo, tr, start, err.Error())
		return
	}
	postMR := shouldPostMRComment(repo, tr)
	if postMR {
		if err := prov.PostComment(ctx, inst, token, repo, tr.MRID, content); err != nil {
			r.fail(ctx, rec.ID, repo, tr, start, "post comment: "+err.Error())
			return
		}
		if len(inline) > 0 {
			if err := prov.PostInlineComments(ctx, inst, token, repo, tr.MRID, inline); err != nil {
				runlog.Printf(ctx, "pipeline: inline discussions: %v", err)
			} else {
				runlog.Printf(ctx, "pipeline: inline discussions ok count=%d", len(inline))
			}
		}
	} else if tr.CommitSHA != "" {
		if err := prov.PostCommitComment(ctx, inst, token, repo, tr.CommitSHA, content); err != nil {
			r.fail(ctx, rec.ID, repo, tr, start, "post commit comment: "+err.Error())
			return
		}
		runlog.Printf(ctx, "pipeline: commit summary comment ok sha=%s", shortSHA(tr.CommitSHA))
		if len(inline) > 0 {
			if err := prov.PostCommitInlineComments(ctx, inst, token, repo, tr.CommitSHA, inline); err != nil {
				runlog.Printf(ctx, "pipeline: commit inline comments: %v", err)
			} else {
				runlog.Printf(ctx, "pipeline: commit inline comments ok count=%d", len(inline))
			}
		}
	} else {
		runlog.Printf(ctx, "pipeline: 无 MR / Commit，跳过 GitLab 评论回填")
	}
	_ = r.Store.FinishReview(ctx, rec.ID, model.ReviewSuccess,
		int(time.Since(start).Seconds()), commentCount, "")
	runlog.Printf(ctx, "pipeline: ok %s !%s %s mode=agent comments=%d",
		repo.FullName, tr.MRID, shortSHA(tr.CommitSHA), commentCount)
	summary := fmt.Sprintf("共 %d 条意见，详见 MR", commentCount)
	if !postMR && tr.CommitSHA != "" {
		summary = fmt.Sprintf("共 %d 条意见，详见 Commit 评论", commentCount)
	} else if !postMR {
		summary = fmt.Sprintf("共 %d 条意见，详见审查日志", commentCount)
	}
	r.sendNotify(ctx, repo, tr, "success", summary)
}

func (r *Runner) runAgent(ctx context.Context, inst *model.Instance, token string, repo *model.Repo, tr model.ReviewTrigger, concurrency int) (string, []vcs.InlineComment, int, error) {
	if tr.CommitSHA == "" {
		return "", nil, 0, errors.New("agent mode requires commit sha from webhook")
	}
	cacheRoot := filepath.Join("data", "workspaces")
	if v := os.Getenv("WORKSPACE_DIR"); v != "" {
		cacheRoot = v
	}
	dir, cleanup, err := workspace.Prepare(ctx, inst.BaseURL, repo.FullName, token, tr.CommitSHA, cacheRoot)
	if err != nil {
		return "", nil, 0, fmt.Errorf("workspace: %s", workspace.RedactPaths(dir, err.Error()))
	}
	defer cleanup()
	runlog.Printf(ctx, "pipeline: agent workspace ready %s", workspace.Label(dir))

	firewallRules := ""
	if fw, fwErr := r.Store.GetFirewall(ctx); fwErr == nil && fw != nil {
		firewallRules = fw.Rules
	}
	res, err := ocrreview.Run(ctx, r.Store, ocrreview.Options{
		RepoDir:       dir,
		CommitSHA:     tr.CommitSHA,
		Concurrency:   concurrency,
		Guidance:      r.reviewGuidance(ctx, repo, tr),
		FirewallRules: firewallRules,
	})
	if err != nil {
		return "", nil, 0, errors.New("agent: " + err.Error())
	}
	n := len(res.Comments)
	if n == 0 {
		n = 1 // still posted a summary note
	}
	inline := make([]vcs.InlineComment, 0, len(res.Comments))
	for _, c := range res.Comments {
		if c.StartLine <= 0 || strings.TrimSpace(c.Path) == "" {
			continue
		}
		inline = append(inline, vcs.InlineComment{
			Path:           c.Path,
			Content:        c.Content,
			SuggestionCode: c.SuggestionCode,
			StartLine:      c.StartLine,
			EndLine:        c.EndLine,
			Category:       c.Category,
			Severity:       c.Severity,
		})
	}
	return res.Markdown, inline, n, nil
}

// reviewGuidance renders the configured review prompt for agent mode. The
// {{diff}} placeholder is dropped because the agent reads the diff through its
// own tools instead of receiving it inline.
func (r *Runner) reviewGuidance(ctx context.Context, repo *model.Repo, tr model.ReviewTrigger) string {
	prompt, err := r.Store.GetPrompt(ctx)
	if err != nil || prompt == nil {
		if err != nil {
			runlog.Printf(ctx, "pipeline: load prompt: %v", err)
		}
		return ""
	}
	project := tr.Project
	if project == "" {
		project = repo.FullName
	}
	// Manual re-runs replay a stored review, which carries no branch name.
	branch := tr.Branch
	if branch == "" {
		branch = repo.DefaultBranch
	}
	body := renderPrompt(prompt.Body, map[string]string{
		"project_name": project,
		"branch":       branch,
		"commit_sha":   tr.CommitSHA,
		"mr_id":        tr.MRID,
		"diff":         "",
	})
	return strings.TrimSpace(body)
}

func (r *Runner) fail(ctx context.Context, reviewID string, repo *model.Repo, tr model.ReviewTrigger, start time.Time, msg string) {
	runlog.Printf(ctx, "pipeline: fail %s !%s: %s", repo.FullName, tr.MRID, msg)
	_ = r.Store.FinishReview(ctx, reviewID, model.ReviewFailed,
		int(time.Since(start).Seconds()), 0, msg)
	r.sendNotify(ctx, repo, tr, "failed", msg)
}

func (r *Runner) sendNotify(ctx context.Context, repo *model.Repo, tr model.ReviewTrigger, status, summary string) {
	if repo == nil || !repo.NotifyEnabled || repo.NotificationGroupID == "" {
		return
	}
	channels, err := r.Store.ChannelsForGroup(ctx, repo.NotificationGroupID)
	if err != nil {
		runlog.Printf(ctx, "notify: load group %s: %v", repo.NotificationGroupID, err)
		return
	}
	if len(channels) == 0 {
		runlog.Printf(ctx, "notify: skip %s group=%s (no enabled channels)", repo.FullName, repo.NotificationGroupID)
		return
	}
	tmpl, _ := r.Store.GetNotifyTemplate(ctx)
	p := notify.Payload{
		Repo: repo.FullName, MRID: tr.MRID, MRURL: tr.MRURL,
		CommitSHA: tr.CommitSHA, Status: status, Summary: summary, Author: tr.Author,
	}
	text := notify.Render(tmpl, p)
	if r.Notify == nil {
		r.Notify = notify.New()
	}
	for _, ch := range channels {
		if err := r.Notify.Send(ctx, ch, text, p); err != nil {
			runlog.Printf(ctx, "notify fail: repo=%s channel=%s: %v", repo.FullName, ch.Name, err)
			continue
		}
		runlog.Printf(ctx, "notify ok: repo=%s channel=%s status=%s", repo.FullName, ch.Name, status)
	}
}

func renderPrompt(tmpl string, vars map[string]string) string {
	out := tmpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// shouldPostMRComment is true only for MR trigger mode with a numeric GitLab MR IID.
func shouldPostMRComment(repo *model.Repo, tr model.ReviewTrigger) bool {
	if repo == nil || repo.TriggerMode.OrDefault() == model.TriggerPush {
		return false
	}
	if tr.EventType == "push" {
		return false
	}
	return isGitLabMRIID(tr.MRID)
}

func isGitLabMRIID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
