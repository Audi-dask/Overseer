package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Audi-dask/Overseer/internal/runlog"
	"github.com/Audi-dask/Overseer/internal/store"
)

func runReviewCleanup(ctx context.Context, st *store.Store) {
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		deleted, err := cleanupExpiredReviews(cleanupCtx, st, time.Now().UTC())
		if err != nil {
			log.Printf("review cleanup: %v", err)
			return
		}
		if deleted > 0 {
			log.Printf("review cleanup: deleted=%d", deleted)
		}
	}

	cleanup()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

func cleanupExpiredReviews(ctx context.Context, st *store.Store, now time.Time) (int, error) {
	settings, err := st.GetSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("load settings: %w", err)
	}
	if settings == nil || settings.ReviewRetentionDays == 0 {
		return 0, nil
	}
	if settings.ReviewRetentionDays < 0 {
		return 0, fmt.Errorf("invalid retention days: %d", settings.ReviewRetentionDays)
	}

	before := now.UTC().AddDate(0, 0, -settings.ReviewRetentionDays)
	ids, err := st.ListExpiredReviewIDs(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("list expired reviews: %w", err)
	}
	deleted := 0
	for _, id := range ids {
		if err := runlog.Remove(id); err != nil {
			return deleted, fmt.Errorf("remove log %s: %w", id, err)
		}
		if err := st.DeleteReview(ctx, id); err != nil {
			return deleted, fmt.Errorf("delete review %s: %w", id, err)
		}
		deleted++
	}
	return deleted, nil
}
