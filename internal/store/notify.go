package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Audi-dask/Overseer/internal/model"

	"github.com/google/uuid"
)

const defaultNotifyTemplate = "【Overseer】{{repo}} !{{mr_id}} 审查完成（{{status}}）\n{{mr_url}}"

var notifyVars = []string{"{{repo}}", "{{mr_id}}", "{{mr_url}}", "{{commit_sha}}", "{{status}}", "{{summary}}"}

func maskTarget(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 12 {
		return "••••••••"
	}
	return s[:8] + "••••••••" + s[len(s)-4:]
}

func (s *Store) ListNotifyChannels(ctx context.Context) ([]model.NotifyChannel, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, kind, target_enc, enabled FROM notify_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NotifyChannel
	for rows.Next() {
		var c model.NotifyChannel
		var enc string
		var enabled int
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &enc, &enabled); err != nil {
			return nil, err
		}
		plain, _ := s.box.Decrypt(enc)
		c.Target = plain
		c.TargetMasked = maskTarget(plain)
		c.Enabled = enabled == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

type UpsertChannelInput struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Kind    model.NotifyKind `json:"kind"`
	Target  string           `json:"target"`
	Enabled bool             `json:"enabled"`
}

func (s *Store) UpsertNotifyChannel(ctx context.Context, in UpsertChannelInput) (*model.NotifyChannel, error) {
	id := in.ID
	if id == "" {
		id = "ch_" + uuid.NewString()[:8]
	}
	enc, err := s.box.Encrypt(strings.TrimSpace(in.Target))
	if err != nil {
		return nil, err
	}
	// Empty target on update means "keep existing secret".
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO notify_channels(id, name, kind, target_enc, enabled) VALUES(?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, kind=excluded.kind, enabled=excluded.enabled,
  target_enc=CASE WHEN excluded.target_enc='' THEN notify_channels.target_enc ELSE excluded.target_enc END`,
		id, in.Name, in.Kind, enc, boolInt(in.Enabled)); err != nil {
		return nil, err
	}
	return s.GetNotifyChannel(ctx, id)
}

func (s *Store) GetNotifyChannel(ctx context.Context, id string) (*model.NotifyChannel, error) {
	var c model.NotifyChannel
	var enc string
	var enabled int
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, kind, target_enc, enabled FROM notify_channels WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Kind, &enc, &enabled)
	if err != nil {
		return nil, err
	}
	plain, _ := s.box.Decrypt(enc)
	c.Target = plain
	c.TargetMasked = maskTarget(plain)
	c.Enabled = enabled == 1
	return &c, nil
}

func (s *Store) DeleteNotifyChannel(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_group_channels WHERE channel_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_channels WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListNotifyGroups(ctx context.Context) ([]model.NotifyGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, enabled FROM notify_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NotifyGroup
	for rows.Next() {
		var g model.NotifyGroup
		var enabled int
		if err := rows.Scan(&g.ID, &g.Name, &enabled); err != nil {
			return nil, err
		}
		g.Enabled = enabled == 1
		g.ChannelIDs = []string{}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	links, err := s.db.QueryContext(ctx, `SELECT group_id, channel_id FROM notify_group_channels`)
	if err != nil {
		return nil, err
	}
	defer links.Close()
	byID := map[string]int{}
	for i := range out {
		byID[out[i].ID] = i
	}
	for links.Next() {
		var gid, cid string
		if err := links.Scan(&gid, &cid); err != nil {
			return nil, err
		}
		if i, ok := byID[gid]; ok {
			out[i].ChannelIDs = append(out[i].ChannelIDs, cid)
		}
	}
	return out, links.Err()
}

type UpsertGroupInput struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channel_ids"`
	Enabled    bool     `json:"enabled"`
}

func (s *Store) UpsertNotifyGroup(ctx context.Context, in UpsertGroupInput) (*model.NotifyGroup, error) {
	id := in.ID
	if id == "" {
		id = "ng_" + uuid.NewString()[:8]
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO notify_groups(id, name, enabled) VALUES(?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, enabled=excluded.enabled`,
		id, in.Name, boolInt(in.Enabled)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_group_channels WHERE group_id=?`, id); err != nil {
		return nil, err
	}
	for _, cid := range in.ChannelIDs {
		if strings.TrimSpace(cid) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO notify_group_channels(group_id, channel_id) VALUES(?,?)`, id, cid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &model.NotifyGroup{ID: id, Name: in.Name, ChannelIDs: in.ChannelIDs, Enabled: in.Enabled}, nil
}

func (s *Store) DeleteNotifyGroup(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_group_channels WHERE group_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_groups WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE repos SET notification_group_id='', notify_enabled=0 WHERE notification_group_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ChannelsForGroup returns enabled channels of an enabled group, ready to send.
func (s *Store) ChannelsForGroup(ctx context.Context, groupID string) ([]model.NotifyChannel, error) {
	var enabled int
	if err := s.db.QueryRowContext(ctx,
		`SELECT enabled FROM notify_groups WHERE id=?`, groupID).Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if enabled != 1 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.id, c.name, c.kind, c.target_enc
FROM notify_group_channels gc JOIN notify_channels c ON c.id = gc.channel_id
WHERE gc.group_id=? AND c.enabled=1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NotifyChannel
	for rows.Next() {
		var c model.NotifyChannel
		var enc string
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &enc); err != nil {
			return nil, err
		}
		plain, err := s.box.Decrypt(enc)
		if err != nil || plain == "" {
			continue
		}
		c.Target = plain
		c.Enabled = true
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetNotifyTemplate(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='notify_template'`).Scan(&v)
	if err == sql.ErrNoRows || v == "" {
		return defaultNotifyTemplate, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func (s *Store) SaveNotifyTemplate(ctx context.Context, tmpl string) error {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = defaultNotifyTemplate
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings(key, value) VALUES('notify_template', ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, tmpl)
	return err
}

func NotifyVars() []string { return notifyVars }

func DefaultNotifyTemplate() string { return defaultNotifyTemplate }
