package vcs

import (
	"context"

	"github.com/Audi-dask/Overseer/internal/model"
)

// InlineComment is a line-anchored review finding for VCS diff discussions.
type InlineComment struct {
	Path           string
	Content        string
	SuggestionCode string
	StartLine      int
	EndLine        int
	Category       string
	Severity       string
}

// Provider deliberately exposes only two categories of VCS mutations:
// webhook lifecycle management and posting AI review comments. Implementations
// must keep all other repository, branch, commit and merge-request operations
// read-only.
type Provider interface {
	ListRepos(ctx context.Context, instance *model.Instance, token string) ([]model.Repo, error)
	// SearchRepos finds projects by keyword (group path/name or project name). Results are
	// candidates only — callers decide what to persist.
	SearchRepos(ctx context.Context, instance *model.Instance, token, query string) ([]model.Repo, error)
	EnsureWebhook(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, callbackURL, secret string) (webhookID string, err error)
	DeleteWebhook(ctx context.Context, instance *model.Instance, token string, repo *model.Repo) error
	ParseEvent(payload []byte, headers map[string]string, secret string) (*model.ReviewTrigger, error)
	PostComment(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, mrID, content string) error
	// PostInlineComments posts diff discussions for line-anchored findings (best-effort).
	PostInlineComments(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, mrID string, comments []InlineComment) error
	// PostCommitComment posts a summary note on a commit (Push 触发等无 MR 场景).
	PostCommitComment(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, commitSHA, content string) error
	// PostCommitInlineComments posts line-anchored notes on a commit diff (best-effort).
	PostCommitInlineComments(ctx context.Context, instance *model.Instance, token string, repo *model.Repo, commitSHA string, comments []InlineComment) error
}
