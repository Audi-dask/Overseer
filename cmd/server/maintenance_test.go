package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/runlog"
	"github.com/Audi-dask/Overseer/internal/secretbox"
	"github.com/Audi-dask/Overseer/internal/store"
)

func TestCleanupExpiredReviews(t *testing.T) {
	box, err := secretbox.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	logDir := t.TempDir()
	runlog.SetDir(logDir)
	ctx := context.Background()
	settings, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.ReviewRetentionDays = 30
	if err := st.SaveSettings(ctx, *settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	old := model.Review{ID: "old", Repo: "repo", Status: model.ReviewSuccess, CreatedAt: now.Add(-31 * 24 * time.Hour)}
	recent := model.Review{ID: "recent", Repo: "repo", Status: model.ReviewSuccess, CreatedAt: now.Add(-29 * 24 * time.Hour)}
	for _, review := range []*model.Review{&old, &recent} {
		if err := st.InsertReview(ctx, review); err != nil {
			t.Fatal(err)
		}
		sink, err := runlog.Open(review.ID)
		if err != nil {
			t.Fatal(err)
		}
		sink.Printf("test")
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := cleanupExpiredReviews(ctx, st, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := st.GetReview(ctx, old.ID); err == nil {
		t.Fatal("old review still exists")
	}
	if _, err := os.Stat(filepath.Join(logDir, old.ID+".log")); !os.IsNotExist(err) {
		t.Fatalf("old log still exists or stat failed: %v", err)
	}
	if _, err := st.GetReview(ctx, recent.ID); err != nil {
		t.Fatalf("recent review missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(logDir, recent.ID+".log")); err != nil {
		t.Fatalf("recent log missing: %v", err)
	}
}

func TestCleanupDisabled(t *testing.T) {
	box, err := secretbox.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	settings, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.ReviewRetentionDays = 0
	if err := st.SaveSettings(ctx, *settings); err != nil {
		t.Fatal(err)
	}
	review := &model.Review{ID: "keep", Repo: "repo", Status: model.ReviewSuccess, CreatedAt: time.Now().UTC().Add(-365 * 24 * time.Hour)}
	if err := st.InsertReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	deleted, err := cleanupExpiredReviews(ctx, st, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	if _, err := st.GetReview(ctx, review.ID); err != nil {
		t.Fatalf("review should be retained: %v", err)
	}
}
