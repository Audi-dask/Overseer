package store

import (
	"context"
	"database/sql"
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

func TestReviewReportMigrationAndQueries(t *testing.T) {
	box, err := secretbox.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE reviews (
		id TEXT PRIMARY KEY, repo_id TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL,
		mr_id TEXT NOT NULL, commit_sha TEXT NOT NULL, status TEXT NOT NULL,
		duration_sec INTEGER NOT NULL DEFAULT 0, comments INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '', mr_url TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path, box)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var columnCount int
	if err := st.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('reviews') WHERE name='report_markdown'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 {
		t.Fatalf("report_markdown columns = %d, want 1", columnCount)
	}

	ctx := context.Background()
	review := &model.Review{ID: "report", Repo: "group/repo", MRID: "12", CommitSHA: "abcdef", Status: model.ReviewRunning}
	if err := st.InsertReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	report := "## Overseer Review\n\n- **严重程度**: high"
	if err := st.FinishReview(ctx, review.ID, model.ReviewSuccess, 9, 1, "", report); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetReview(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReportMarkdown != report {
		t.Fatalf("report = %q, want %q", got.ReportMarkdown, report)
	}
	list, err := st.ListReviews(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ReportMarkdown != "" {
		t.Fatalf("list reviews = %#v, want report omitted", list)
	}
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
