package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/model"
)

func TestGetReviewReturnsReport(t *testing.T) {
	srv, st := newSettingsTestServer(t)
	review := &model.Review{
		ID:             "review-detail",
		Repo:           "group/repo",
		MRID:           "7",
		CommitSHA:      "abcdef",
		Status:         model.ReviewSuccess,
		ReportMarkdown: "## Full report\n\n- finding",
	}
	if err := st.InsertReview(context.Background(), review); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/reviews/"+review.ID, nil)
	req.SetPathValue("id", review.ID)
	res := httptest.NewRecorder()
	srv.getReview(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var got model.Review
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ReportMarkdown != review.ReportMarkdown {
		t.Fatalf("report = %q, want %q", got.ReportMarkdown, review.ReportMarkdown)
	}
}

func TestGetReviewNotFound(t *testing.T) {
	srv, _ := newSettingsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/reviews/missing", nil)
	req.SetPathValue("id", "missing")
	res := httptest.NewRecorder()
	srv.getReview(res, req)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "review not found") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}
