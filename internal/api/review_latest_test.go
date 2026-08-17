package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/queue"
	"github.com/Audi-dask/Overseer/internal/store"
)

func setupReviewLatestTest(t *testing.T, upstream http.HandlerFunc, reviewEnabled bool, defaultBranch string) (*Server, *model.Repo, <-chan queue.Job) {
	t.Helper()
	gitlabServer := httptest.NewServer(upstream)
	t.Cleanup(gitlabServer.Close)
	srv, st := newSettingsTestServer(t)
	inst, err := st.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name: "GitLab", Type: model.VCSGitLab, BaseURL: gitlabServer.URL, Token: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if upsertErr := st.UpsertRepos(context.Background(), inst.ID, []model.Repo{{
		ExternalID: "42", FullName: "group/repo", Private: true, DefaultBranch: defaultBranch,
	}}); upsertErr != nil {
		t.Fatal(upsertErr)
	}
	repos, err := st.ListRepos(context.Background(), inst.ID)
	if err != nil || len(repos) != 1 {
		t.Fatalf("repos = %#v, err = %v", repos, err)
	}
	repo := repos[0]
	if reviewEnabled {
		if reviewErr := st.SetRepoReview(context.Background(), repo.ID, true); reviewErr != nil {
			t.Fatal(reviewErr)
		}
		updated, err := st.GetRepo(context.Background(), repo.ID)
		if err != nil {
			t.Fatal(err)
		}
		repo = *updated
	}
	jobs := make(chan queue.Job, 1)
	srv.Queue = queue.New(time.Millisecond, 1, func(ctx context.Context, job queue.Job) { jobs <- job })
	return srv, &repo, jobs
}

func callReviewLatest(srv *Server, repoID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/repos/"+repoID+"/review-latest", nil)
	req.SetPathValue("id", repoID)
	res := httptest.NewRecorder()
	srv.reviewLatest(res, req)
	return res
}

func TestReviewLatestSuccess(t *testing.T) {
	srv, repo, jobs := setupReviewLatestTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/42/repository/branches/develop" {
			t.Errorf("path = %q", r.URL.EscapedPath())
		}
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("token = %q", r.Header.Get("PRIVATE-TOKEN"))
		}
		_, _ = w.Write([]byte(`{"commit":{"id":"abcdef123456","web_url":"https://gitlab.example/group/repo/-/commit/abcdef123456"}}`))
	}, true, "develop")

	res := callReviewLatest(srv, repo.ID)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["repo_id"] != repo.ID || body["branch"] != "develop" || body["commit"] != "abcdef123456" {
		t.Fatalf("body = %#v", body)
	}
	select {
	case job := <-jobs:
		tr := job.Trigger
		if !job.Force || tr.EventType != "push" || tr.CommitSHA != "abcdef123456" || tr.Branch != "develop" || tr.MRID != "develop" || tr.Author != "Overseer Manual" {
			t.Fatalf("job = %#v", job)
		}
	case <-time.After(time.Second):
		t.Fatal("job not enqueued")
	}
}

func TestReviewLatestRepoNotFound(t *testing.T) {
	srv, _, _ := setupReviewLatestTest(t, func(w http.ResponseWriter, r *http.Request) {}, true, "main")
	res := callReviewLatest(srv, "missing")
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestReviewLatestRequiresReviewEnabled(t *testing.T) {
	srv, repo, _ := setupReviewLatestTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called")
	}, false, "main")
	res := callReviewLatest(srv, repo.ID)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestReviewLatestRejectsEmptyDefaultBranch(t *testing.T) {
	srv, repo, _ := setupReviewLatestTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called")
	}, true, "")
	res := callReviewLatest(srv, repo.ID)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestReviewLatestUpstreamError(t *testing.T) {
	srv, repo, _ := setupReviewLatestTest(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}, true, "main")
	res := callReviewLatest(srv, repo.ID)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}
