package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/secretbox"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	box *secretbox.Box
}

func Open(path string, box *secretbox.Box) (*Store, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, box: box}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureDefaults(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS instances (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  base_url TEXT NOT NULL,
  token_enc TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'need_cred',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS repos (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL,
  external_id TEXT NOT NULL,
  full_name TEXT NOT NULL,
  private INTEGER NOT NULL DEFAULT 0,
  review_enabled INTEGER NOT NULL DEFAULT 0,
  notify_enabled INTEGER NOT NULL DEFAULT 0,
  notification_group_id TEXT NOT NULL DEFAULT '',
  webhook TEXT NOT NULL DEFAULT 'none',
  webhook_id TEXT NOT NULL DEFAULT '',
  default_branch TEXT NOT NULL DEFAULT '',
  trigger_mr_open INTEGER NOT NULL DEFAULT 1,
  trigger_mr_update INTEGER NOT NULL DEFAULT 1,
  trigger_mr_reopen INTEGER NOT NULL DEFAULT 1,
  trigger_mode TEXT NOT NULL DEFAULT 'mr',
  updated_at TEXT NOT NULL,
  UNIQUE(instance_id, external_id)
);

CREATE TABLE IF NOT EXISTS llm_providers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  base_url TEXT NOT NULL,
  model TEXT NOT NULL,
  api_key_enc TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'none',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS reviews (
  id TEXT PRIMARY KEY,
  repo_id TEXT NOT NULL DEFAULT '',
  repo TEXT NOT NULL,
  mr_id TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  status TEXT NOT NULL,
  duration_sec INTEGER NOT NULL DEFAULT 0,
  comments INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  mr_url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reviews_lookup ON reviews(repo, mr_id, commit_sha);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt (
  id TEXT PRIMARY KEY CHECK (id = 'default'),
  name TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS firewall (
  id TEXT PRIMARY KEY CHECK (id = 'default'),
  rules TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notify_channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  target_enc TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS notify_groups (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS notify_group_channels (
  group_id TEXT NOT NULL,
  channel_id TEXT NOT NULL,
  PRIMARY KEY(group_id, channel_id)
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	return s.ensureRepoTriggerColumns()
}

func (s *Store) ensureRepoTriggerColumns() error {
	cols := []string{
		`ALTER TABLE repos ADD COLUMN trigger_mr_open INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE repos ADD COLUMN trigger_mr_update INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE repos ADD COLUMN trigger_mr_reopen INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE repos ADD COLUMN trigger_mode TEXT NOT NULL DEFAULT 'mr'`,
	}
	for _, q := range cols {
		if _, err := s.db.Exec(q); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Store) ensureDefaults() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM prompt`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.db.Exec(
			`INSERT INTO prompt(id, name, body) VALUES('default', ?, ?)`,
			"默认审查提示词", defaultPrompt,
		); err != nil {
			return err
		}
	}
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM firewall`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.db.Exec(`INSERT INTO firewall(id, rules) VALUES('default', ?)`, defaultFirewall); err != nil {
			return err
		}
	}
	defaults := map[string]string{
		"callback_base_url":   "",
		"webhook_secret":      "",
		"max_concurrency":     "8",
		"debounce_sec":        "30",
		"admin_username":      "admin",
		"admin_password_hash": "",
	}
	for k, v := range defaults {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)`, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListInstances(ctx context.Context) ([]model.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT i.id, i.name, i.type, i.base_url, i.token_enc, i.status, i.created_at, i.updated_at,
       (SELECT COUNT(1) FROM repos r WHERE r.instance_id = i.id)
FROM instances i ORDER BY i.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Instance
	for rows.Next() {
		var it model.Instance
		var tokenEnc, created, updated string
		var repos int
		if err := rows.Scan(&it.ID, &it.Name, &it.Type, &it.BaseURL, &tokenEnc, &it.Status, &created, &updated, &repos); err != nil {
			return nil, err
		}
		it.HasCred = tokenEnc != ""
		it.Repos = repos
		it.CreatedAt, _ = time.Parse(time.RFC3339, created)
		it.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, it)
	}
	return out, rows.Err()
}

type CreateInstanceInput struct {
	Name    string
	Type    model.VCSType
	BaseURL string
	Token   string
}

func (s *Store) CreateInstance(ctx context.Context, in CreateInstanceInput) (*model.Instance, error) {
	id := "inst_" + uuid.NewString()[:8]
	enc, err := s.box.Encrypt(in.Token)
	if err != nil {
		return nil, err
	}
	status := model.InstanceNeedCred
	if in.Token != "" {
		status = model.InstanceConnected
	}
	ts := now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO instances(id, name, type, base_url, token_enc, status, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?)`, id, in.Name, in.Type, in.BaseURL, enc, status, ts, ts)
	if err != nil {
		return nil, err
	}
	return &model.Instance{
		ID: id, Name: in.Name, Type: in.Type, BaseURL: in.BaseURL,
		HasCred: in.Token != "", Status: status, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, nil
}

func (s *Store) GetInstanceToken(ctx context.Context, id string) (string, error) {
	var enc string
	err := s.db.QueryRowContext(ctx, `SELECT token_enc FROM instances WHERE id=?`, id).Scan(&enc)
	if err != nil {
		return "", err
	}
	return s.box.Decrypt(enc)
}

func (s *Store) GetInstance(ctx context.Context, id string) (*model.Instance, error) {
	var it model.Instance
	var tokenEnc, created, updated string
	var repos int
	err := s.db.QueryRowContext(ctx, `
SELECT i.id, i.name, i.type, i.base_url, i.token_enc, i.status, i.created_at, i.updated_at,
       (SELECT COUNT(1) FROM repos r WHERE r.instance_id = i.id)
FROM instances i WHERE i.id=?`, id).Scan(
		&it.ID, &it.Name, &it.Type, &it.BaseURL, &tokenEnc, &it.Status, &created, &updated, &repos,
	)
	if err != nil {
		return nil, err
	}
	it.HasCred = tokenEnc != ""
	it.Repos = repos
	it.CreatedAt, _ = time.Parse(time.RFC3339, created)
	it.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return &it, nil
}

type UpdateInstanceInput struct {
	Name    string
	BaseURL string
	Token   string // empty keeps existing token
}

func (s *Store) UpdateInstance(ctx context.Context, id string, in UpdateInstanceInput) (*model.Instance, error) {
	cur, err := s.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = cur.Name
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = cur.BaseURL
	}
	ts := now()
	if strings.TrimSpace(in.Token) != "" {
		enc, err := s.box.Encrypt(strings.TrimSpace(in.Token))
		if err != nil {
			return nil, err
		}
		_, err = s.db.ExecContext(ctx, `
UPDATE instances SET name=?, base_url=?, token_enc=?, status=?, updated_at=? WHERE id=?`,
			name, baseURL, enc, model.InstanceConnected, ts, id)
		if err != nil {
			return nil, err
		}
	} else {
		_, err = s.db.ExecContext(ctx, `
UPDATE instances SET name=?, base_url=?, updated_at=? WHERE id=?`,
			name, baseURL, ts, id)
		if err != nil {
			return nil, err
		}
	}
	return s.GetInstance(ctx, id)
}

func (s *Store) ListRepos(ctx context.Context, instanceID string) ([]model.Repo, error) {
	q := `
SELECT r.id, r.instance_id, i.name, r.external_id, r.full_name, r.private,
       r.review_enabled, r.notify_enabled, r.notification_group_id, r.webhook, r.webhook_id,
       r.default_branch, r.trigger_mode, r.updated_at
FROM repos r JOIN instances i ON i.id = r.instance_id`
	args := []any{}
	if instanceID != "" {
		q += ` WHERE r.instance_id=?`
		args = append(args, instanceID)
	}
	q += ` ORDER BY r.full_name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Repo
	for rows.Next() {
		var r model.Repo
		var priv, review, notify int
		var triggerMode string
		var updated string
		if err := rows.Scan(
			&r.ID, &r.InstanceID, &r.InstanceName, &r.ExternalID, &r.FullName, &priv,
			&review, &notify, &r.NotificationGroupID, &r.Webhook, &r.WebhookID,
			&r.DefaultBranch, &triggerMode, &updated,
		); err != nil {
			return nil, err
		}
		r.Private = priv == 1
		r.ReviewEnabled = review == 1
		r.NotifyEnabled = notify == 1
		r.TriggerMode = model.TriggerMode(triggerMode).OrDefault()
		r.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertRepos(ctx context.Context, instanceID string, repos []model.Repo) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	for _, r := range repos {
		id := r.ID
		if id == "" {
			id = "repo_" + uuid.NewString()[:8]
		}
		// Review/notify/webhook/trigger columns are only seeded on insert so re-syncing
		// never clobbers what the operator configured per repo.
		_, err := tx.ExecContext(ctx, `
INSERT INTO repos(id, instance_id, external_id, full_name, private, review_enabled, notify_enabled,
  notification_group_id, webhook, webhook_id, default_branch, trigger_mode, updated_at)
VALUES(?,?,?,?,?,0,0,'','none','',?,'mr',?)
ON CONFLICT(instance_id, external_id) DO UPDATE SET
  full_name=excluded.full_name,
  private=excluded.private,
  default_branch=excluded.default_branch,
  updated_at=excluded.updated_at`,
			id, instanceID, r.ExternalID, r.FullName, boolInt(r.Private), r.DefaultBranch, ts)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateRepoTriggerMode(ctx context.Context, repoID string, mode model.TriggerMode) error {
	if !mode.Valid() {
		return fmt.Errorf("trigger_mode 必须是 mr 或 push")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE repos SET trigger_mode=?, updated_at=? WHERE id=?`, string(mode), now(), repoID)
	return err
}

// DeleteRepo removes a local repo row. Caller is responsible for hook cleanup.
func (s *Store) DeleteRepo(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id=?`, id)
	return err
}

// DeleteInactiveRepos drops repos that never had review enabled (cleanup after full sync).
func (s *Store) DeleteInactiveRepos(ctx context.Context, instanceID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
DELETE FROM repos WHERE instance_id=? AND review_enabled=0`, instanceID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) SetRepoReview(ctx context.Context, repoID string, enabled bool) error {
	webhook := "none"
	if enabled {
		webhook = "pending"
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE repos SET review_enabled=?, webhook=?, notify_enabled=CASE WHEN ?=0 THEN 0 ELSE notify_enabled END,
  notification_group_id=CASE WHEN ?=0 THEN '' ELSE notification_group_id END, updated_at=?
WHERE id=?`, boolInt(enabled), webhook, boolInt(enabled), boolInt(enabled), now(), repoID)
	return err
}

// DeleteInstance removes an instance together with its repos. Existing VCS-side
// hooks are not revoked here; the caller decides whether to clean them up first.
func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM repos WHERE instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetRepoNotify(ctx context.Context, repoID string, enabled bool, groupID string) error {
	if !enabled {
		groupID = ""
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE repos SET notify_enabled=?, notification_group_id=?, updated_at=? WHERE id=? AND review_enabled=1`,
		boolInt(enabled), groupID, now(), repoID)
	return err
}

func (s *Store) UpdateRepoWebhook(ctx context.Context, repoID, webhookID string, status model.WebhookStatus) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE repos SET webhook_id=?, webhook=?, updated_at=? WHERE id=?`, webhookID, status, now(), repoID)
	return err
}

func (s *Store) GetRepo(ctx context.Context, id string) (*model.Repo, error) {
	list, err := s.ListRepos(ctx, "")
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *Store) GetSettings(ctx context.Context) (*model.Settings, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	sec := m["webhook_secret"]
	if sec != "" {
		if dec, err := s.box.Decrypt(sec); err == nil {
			sec = dec
		}
	}
	st := &model.Settings{
		CallbackBaseURL: m["callback_base_url"],
		WebhookSecret:   sec,
		MaxConcurrency:  atoiDefault(m["max_concurrency"], 8),
		DebounceSec:     atoiDefault(m["debounce_sec"], 30),
	}
	return st, nil
}

func atoiDefault(s string, d int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return d
	}
	return n
}

func (s *Store) SaveSettings(ctx context.Context, st model.Settings) error {
	cur, _ := s.GetSettings(ctx)
	secret := strings.TrimSpace(st.WebhookSecret)
	if secret == "" && cur != nil && cur.WebhookSecret != "" {
		// UI leaves secret blank to mean "keep existing".
		var enc string
		_ = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='webhook_secret'`).Scan(&enc)
		if enc != "" {
			pairs := map[string]string{
				"callback_base_url": st.CallbackBaseURL,
				"max_concurrency":   fmt.Sprintf("%d", st.MaxConcurrency),
				"debounce_sec":      fmt.Sprintf("%d", st.DebounceSec),
			}
			for k, v := range pairs {
				if _, err := s.db.ExecContext(ctx, `
INSERT INTO settings(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
					return err
				}
			}
			return nil
		}
	}
	encSecret, err := s.box.Encrypt(secret)
	if err != nil {
		return err
	}
	pairs := map[string]string{
		"callback_base_url": st.CallbackBaseURL,
		"webhook_secret":    encSecret,
		"max_concurrency":   fmt.Sprintf("%d", st.MaxConcurrency),
		"debounce_sec":      fmt.Sprintf("%d", st.DebounceSec),
	}
	for k, v := range pairs {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO settings(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetPrompt(ctx context.Context) (*model.PromptConfig, error) {
	var p model.PromptConfig
	err := s.db.QueryRowContext(ctx, `SELECT id, name, body FROM prompt WHERE id='default'`).
		Scan(&p.ID, &p.Name, &p.Body)
	if err != nil {
		return nil, err
	}
	p.DefaultBody = defaultPrompt
	p.Variables = promptVars
	return &p, nil
}

func (s *Store) SavePrompt(ctx context.Context, name, body string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE prompt SET name=?, body=? WHERE id='default'`, name, body)
	return err
}

func (s *Store) GetFirewall(ctx context.Context) (*model.FirewallConfig, error) {
	var f model.FirewallConfig
	err := s.db.QueryRowContext(ctx, `SELECT rules FROM firewall WHERE id='default'`).Scan(&f.Rules)
	if err != nil {
		return nil, err
	}
	f.DefaultRules = defaultFirewall
	return &f, nil
}

func (s *Store) SaveFirewall(ctx context.Context, rules string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE firewall SET rules=? WHERE id='default'`, rules)
	return err
}

func (s *Store) ListReviews(ctx context.Context, limit int) ([]model.Review, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, repo_id, repo, mr_id, commit_sha, status, duration_sec, comments, error, mr_url, created_at
FROM reviews ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Review
	for rows.Next() {
		var r model.Review
		var created string
		if err := rows.Scan(&r.ID, &r.RepoID, &r.Repo, &r.MRID, &r.CommitSHA, &r.Status, &r.DurationSec, &r.Comments, &r.Error, &r.MRURL, &created); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetReview(ctx context.Context, id string) (*model.Review, error) {
	var r model.Review
	var created string
	err := s.db.QueryRowContext(ctx, `
SELECT id, repo_id, repo, mr_id, commit_sha, status, duration_sec, comments, error, mr_url, created_at
FROM reviews WHERE id=?`, id).Scan(&r.ID, &r.RepoID, &r.Repo, &r.MRID, &r.CommitSHA, &r.Status,
		&r.DurationSec, &r.Comments, &r.Error, &r.MRURL, &created)
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &r, nil
}

func (s *Store) InsertReview(ctx context.Context, r *model.Review) error {
	if r.ID == "" {
		r.ID = "rv_" + uuid.NewString()[:8]
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO reviews(id, repo_id, repo, mr_id, commit_sha, status, duration_sec, comments, error, mr_url, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.RepoID, r.Repo, r.MRID, r.CommitSHA, r.Status, r.DurationSec, r.Comments, r.Error, r.MRURL, r.CreatedAt.Format(time.RFC3339))
	return err
}

// FinishReview closes out the row created when the run started, so one review
// attempt stays one row instead of leaving a dangling "running" record.
func (s *Store) FinishReview(ctx context.Context, id string, status model.ReviewStatus, durationSec, comments int, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE reviews SET status=?, duration_sec=?, comments=?, error=? WHERE id=?`,
		status, durationSec, comments, errMsg, id)
	return err
}

// ResolveStaleRunning marks reviews left in "running" by a crash or restart as
// failed; in-flight jobs do not survive a process exit.
func (s *Store) ResolveStaleRunning(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE reviews SET status=?, error=? WHERE status=?`,
		model.ReviewFailed, "服务重启，任务中断", model.ReviewRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) HasReviewedCommit(ctx context.Context, repo, mrID, commit string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM reviews WHERE repo=? AND mr_id=? AND commit_sha=? AND status='success'`,
		repo, mrID, commit).Scan(&n)
	return n > 0, err
}

func (s *Store) Overview(ctx context.Context) (map[string]any, error) {
	var instances, enabled int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM instances`).Scan(&instances)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM repos WHERE review_enabled=1`).Scan(&enabled)

	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var total24h, success24h int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM reviews WHERE created_at >= ?`, since).Scan(&total24h)
	_ = s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM reviews WHERE created_at >= ? AND status=?`, since, model.ReviewSuccess).Scan(&success24h)

	var avgDur sql.NullFloat64
	_ = s.db.QueryRowContext(ctx, `
SELECT AVG(duration_sec) FROM reviews WHERE created_at >= ? AND status=? AND duration_sec > 0`,
		since, model.ReviewSuccess).Scan(&avgDur)

	successRate := 0.0
	if total24h > 0 {
		successRate = float64(success24h) / float64(total24h)
	}
	avgSec := 0
	if avgDur.Valid {
		avgSec = int(avgDur.Float64 + 0.5)
	}

	reviews, err := s.ListReviews(ctx, 5)
	if err != nil {
		return nil, err
	}
	recent := make([]map[string]any, 0, len(reviews))
	for _, r := range reviews {
		recent = append(recent, map[string]any{
			"repo": r.Repo, "mr": r.MRID, "status": r.Status, "at": r.CreatedAt.Format("15:04"),
		})
	}
	return map[string]any{
		"instance_count":   instances,
		"review_enabled":   enabled,
		"triggers_24h":     total24h,
		"success_rate_24h": successRate,
		"avg_duration_sec": avgSec,
		"mode":             "agent",
		"mode_note":        "Agent · 本地 checkout + 工具调用",
		"recent":           recent,
	}, nil
}
