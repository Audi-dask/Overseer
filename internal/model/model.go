package model

import (
	"strings"
	"time"
)

type VCSType string

const (
	VCSGitLab VCSType = "gitlab"
	VCSGitHub VCSType = "github"
	VCSGitea  VCSType = "gitea"
)

type InstanceStatus string

const (
	InstanceConnected InstanceStatus = "connected"
	InstanceNeedCred  InstanceStatus = "need_cred"
)

type Instance struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      VCSType        `json:"type"`
	BaseURL   string         `json:"base_url"`
	HasCred   bool           `json:"has_cred"`
	Repos     int            `json:"repos"`
	Status    InstanceStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type WebhookStatus string

const (
	WebhookOK      WebhookStatus = "ok"
	WebhookNone    WebhookStatus = "none"
	WebhookPending WebhookStatus = "pending"
)

type Repo struct {
	ID                  string        `json:"id"`
	InstanceID          string        `json:"instance_id"`
	InstanceName        string        `json:"instance_name,omitempty"`
	ExternalID          string        `json:"external_id"`
	FullName            string        `json:"full_name"`
	Private             bool          `json:"private"`
	ReviewEnabled       bool          `json:"review_enabled"`
	NotifyEnabled       bool          `json:"notify_enabled"`
	NotificationGroupID string        `json:"notification_group_id,omitempty"`
	Webhook             WebhookStatus `json:"webhook"`
	WebhookID           string        `json:"webhook_id,omitempty"`
	DefaultBranch       string        `json:"default_branch,omitempty"`
	// TriggerMode selects which GitLab project hook events start review: mr or push.
	TriggerMode TriggerMode `json:"trigger_mode"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// TriggerMode is the per-repo webhook trigger source (mutually exclusive).
type TriggerMode string

const (
	TriggerMR   TriggerMode = "mr"
	TriggerPush TriggerMode = "push"
)

func DefaultTriggerMode() TriggerMode { return TriggerMR }

func (m TriggerMode) Valid() bool {
	return m == TriggerMR || m == TriggerPush
}

func (m TriggerMode) OrDefault() TriggerMode {
	if m.Valid() {
		return m
	}
	return TriggerMR
}

// MatchesEvent reports whether an incoming webhook event type fits this mode.
func (m TriggerMode) MatchesEvent(eventType string) bool {
	switch m.OrDefault() {
	case TriggerPush:
		return eventType == "push"
	default:
		return strings.HasPrefix(eventType, "merge_request.")
	}
}

type LLMKind string

const (
	LLMOpenAICompatible LLMKind = "openai_compatible"
	LLMAnthropic        LLMKind = "anthropic"
)

type LLMRole string

const (
	LLMRoleNone     LLMRole = "none"
	LLMRolePrimary  LLMRole = "primary"
	LLMRoleFallback LLMRole = "fallback"
)

type LLMProvider struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      LLMKind   `json:"kind"`
	BaseURL   string    `json:"base_url"`
	Model     string    `json:"model"`
	APIKey    string    `json:"api_key"` // masked when returned to UI
	Role      LLMRole   `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ReviewStatus string

const (
	ReviewSuccess ReviewStatus = "success"
	ReviewFailed  ReviewStatus = "failed"
	ReviewRunning ReviewStatus = "running"
	ReviewSkipped ReviewStatus = "skipped"
)

type Review struct {
	ID          string       `json:"id"`
	RepoID      string       `json:"repo_id,omitempty"`
	Repo        string       `json:"repo"`
	MRID        string       `json:"mr"`
	CommitSHA   string       `json:"commit"`
	Status      ReviewStatus `json:"status"`
	DurationSec int          `json:"duration_sec"`
	Comments    int          `json:"comments"`
	Error       string       `json:"error,omitempty"`
	MRURL       string       `json:"mr_url,omitempty"`
	CreatedAt   time.Time    `json:"at"`
}

type ReviewTrigger struct {
	InstanceID string
	Repo       string
	ExternalID string
	MRID       string
	CommitSHA  string
	EventType  string
	MRURL      string
	Project    string
	Branch     string
	Author     string
}

type Settings struct {
	CallbackBaseURL string `json:"callback_base_url"`
	WebhookSecret   string `json:"webhook_secret"`
	MaxConcurrency  int    `json:"max_concurrency"`
	DebounceSec     int    `json:"debounce_sec"`
}

type PromptConfig struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Body        string              `json:"body"`
	DefaultBody string              `json:"default_body"`
	Variables   []map[string]string `json:"variables"`
}

type FirewallConfig struct {
	Rules        string `json:"rules"`
	DefaultRules string `json:"default_rules"`
}

type NotifyKind string

const (
	NotifyFeishu  NotifyKind = "feishu"
	NotifyWebhook NotifyKind = "webhook"
)

type NotifyChannel struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Kind         NotifyKind `json:"kind"`
	Target       string     `json:"target,omitempty"` // plaintext, only for internal use
	TargetMasked string     `json:"target_masked"`
	Enabled      bool       `json:"enabled"`
}

type NotifyGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channel_ids"`
	Enabled    bool     `json:"enabled"`
}
