package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/secretbox"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	box, err := secretbox.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(filepath.Join(t.TempDir(), "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestReviewRetentionSettings(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	settings, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReviewRetentionDays != 30 {
		t.Fatalf("default retention = %d, want 30", settings.ReviewRetentionDays)
	}
	settings.ReviewRetentionDays = 0
	if err := st.SaveSettings(ctx, *settings); err != nil {
		t.Fatal(err)
	}
	saved, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ReviewRetentionDays != 0 {
		t.Fatalf("saved retention = %d, want 0", saved.ReviewRetentionDays)
	}
}

func TestListExpiredReviewIDs(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	reviews := []model.Review{
		{ID: "old-success", Repo: "repo", Status: model.ReviewSuccess, CreatedAt: now.Add(-31 * 24 * time.Hour)},
		{ID: "old-running", Repo: "repo", Status: model.ReviewRunning, CreatedAt: now.Add(-31 * 24 * time.Hour)},
		{ID: "recent-failed", Repo: "repo", Status: model.ReviewFailed, CreatedAt: now.Add(-29 * 24 * time.Hour)},
	}
	for i := range reviews {
		if err := st.InsertReview(ctx, &reviews[i]); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := st.ListExpiredReviewIDs(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "old-success" {
		t.Fatalf("expired ids = %v, want [old-success]", ids)
	}
	if err := st.DeleteReview(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetReview(ctx, ids[0]); err == nil {
		t.Fatal("deleted review still exists")
	}
}
