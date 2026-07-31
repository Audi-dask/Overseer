package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/secretbox"
	"github.com/Audi-dask/Overseer/internal/store"
)

func newSettingsTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	box, err := secretbox.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{Store: st, Box: box}, st
}

func TestSaveSettingsKeepsRetentionWhenOmitted(t *testing.T) {
	srv, st := newSettingsTestServer(t)
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{
		"callback_base_url":"https://review.example.com",
		"webhook_secret":"",
		"max_concurrency":4,
		"debounce_sec":10
	}`))
	res := httptest.NewRecorder()
	srv.saveSettings(res, req)
	if res.Code != 200 {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	settings, err := st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReviewRetentionDays != 30 {
		t.Fatalf("retention = %d, want 30", settings.ReviewRetentionDays)
	}
}

func TestSaveSettingsAcceptsExplicitZeroRetention(t *testing.T) {
	srv, st := newSettingsTestServer(t)
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{
		"callback_base_url":"",
		"webhook_secret":"",
		"max_concurrency":8,
		"debounce_sec":30,
		"review_retention_days":0
	}`))
	res := httptest.NewRecorder()
	srv.saveSettings(res, req)
	if res.Code != 200 {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	settings, err := st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReviewRetentionDays != 0 {
		t.Fatalf("retention = %d, want 0", settings.ReviewRetentionDays)
	}
}

func TestSaveSettingsRejectsNegativeRetention(t *testing.T) {
	srv, st := newSettingsTestServer(t)
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{
		"review_retention_days":-1
	}`))
	res := httptest.NewRecorder()
	srv.saveSettings(res, req)
	if res.Code != 400 {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	settings, err := st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReviewRetentionDays != 30 {
		t.Fatalf("retention = %d, want 30", settings.ReviewRetentionDays)
	}
}
