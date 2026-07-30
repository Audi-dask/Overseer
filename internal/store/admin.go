package store

import (
	"context"
	"os"
	"strings"

	"github.com/Audi-dask/Overseer/internal/auth"
)

type AdminAccount struct {
	Username      string
	PasswordHash  string
	SetupRequired bool
}

func (s *Store) GetAdminAccount(ctx context.Context) (*AdminAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('admin_username','admin_password_hash')`)
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
	username := strings.TrimSpace(m["admin_username"])
	if username == "" {
		username = auth.DefaultUsername
	}
	hash := m["admin_password_hash"]
	return &AdminAccount{
		Username:      username,
		PasswordHash:  hash,
		SetupRequired: hash == "",
	}, nil
}

func (s *Store) SetAdminPassword(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		username = auth.DefaultUsername
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	pairs := map[string]string{
		"admin_username":      username,
		"admin_password_hash": hash,
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

// BootstrapAdminIfNeeded initializes the single admin account once.
// Priority: existing hash > ADMIN_PASSWORD env > leave setup_required for UI.
func (s *Store) BootstrapAdminIfNeeded(ctx context.Context) (setupRequired bool, err error) {
	acc, err := s.GetAdminAccount(ctx)
	if err != nil {
		return false, err
	}
	if !acc.SetupRequired {
		return false, nil
	}
	if pwd := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")); pwd != "" {
		if err := s.SetAdminPassword(ctx, auth.DefaultUsername, pwd); err != nil {
			return true, err
		}
		return false, nil
	}
	return true, nil
}
