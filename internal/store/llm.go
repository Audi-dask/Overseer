package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/secretbox"

	"github.com/google/uuid"
)

func (s *Store) ListLLMProviders(ctx context.Context) ([]model.LLMProvider, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, kind, base_url, model, api_key_enc, role, created_at, updated_at
FROM llm_providers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.LLMProvider
	for rows.Next() {
		var p model.LLMProvider
		var enc, created, updated string
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.Model, &enc, &p.Role, &created, &updated); err != nil {
			return nil, err
		}
		plain, _ := s.box.Decrypt(enc)
		p.APIKey = secretbox.Mask(plain)
		p.CreatedAt, _ = time.Parse(time.RFC3339, created)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

type UpsertLLMInput struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Kind    model.LLMKind `json:"kind"`
	BaseURL string        `json:"base_url"`
	Model   string        `json:"model"`
	APIKey  string        `json:"api_key"`
	Role    model.LLMRole `json:"role"`
}

func (s *Store) UpsertLLMProvider(ctx context.Context, in UpsertLLMInput) (*model.LLMProvider, error) {
	ts := now()
	id := in.ID
	if id == "" {
		id = "llm_" + uuid.NewString()[:8]
	}
	enc, err := s.box.Encrypt(in.APIKey)
	if err != nil {
		return nil, err
	}
	if in.Role == model.LLMRolePrimary || in.Role == model.LLMRoleFallback {
		_, _ = s.db.ExecContext(ctx, `UPDATE llm_providers SET role='none' WHERE role=? AND id!=?`, in.Role, id)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO llm_providers(id, name, kind, base_url, model, api_key_enc, role, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, kind=excluded.kind, base_url=excluded.base_url, model=excluded.model,
  api_key_enc=CASE WHEN excluded.api_key_enc='' THEN llm_providers.api_key_enc ELSE excluded.api_key_enc END,
  role=excluded.role, updated_at=excluded.updated_at`,
		id, in.Name, in.Kind, in.BaseURL, in.Model, enc, in.Role, ts, ts)
	if err != nil {
		return nil, err
	}
	return &model.LLMProvider{
		ID: id, Name: in.Name, Kind: in.Kind, BaseURL: in.BaseURL, Model: in.Model,
		APIKey: secretbox.Mask(in.APIKey), Role: in.Role,
	}, nil
}

func (s *Store) DeleteLLMProvider(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM llm_providers WHERE id=?`, id)
	return err
}

func (s *Store) GetLLMByRole(ctx context.Context, role model.LLMRole) (*model.LLMProvider, string, error) {
	var p model.LLMProvider
	var enc string
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, kind, base_url, model, api_key_enc, role FROM llm_providers WHERE role=? LIMIT 1`, role).
		Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.Model, &enc, &p.Role)
	if err != nil {
		return nil, "", err
	}
	plain, err := s.box.Decrypt(enc)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt api key: %w (MASTER_KEY 变更？请在 LLM 页重新填写 API Key)", err)
	}
	return &p, plain, nil
}
