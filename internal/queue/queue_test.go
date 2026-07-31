package queue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
)

func TestEnqueueCoalescesWhileRunning(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	q := New(5*time.Millisecond, 1, func(ctx context.Context, job Job) {
		calls.Add(1)
		close(started)
		<-release
	})
	job := Job{InstanceID: "inst", Trigger: model.ReviewTrigger{Repo: "group/repo", CommitSHA: "abc"}}

	q.Enqueue(job)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	q.Enqueue(job)
	q.EnqueueNow(job)
	time.Sleep(20 * time.Millisecond)
	close(release)
	time.Sleep(20 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestEnqueueNowReplacesPendingTimer(t *testing.T) {
	called := make(chan struct{}, 2)
	q := New(time.Hour, 1, func(ctx context.Context, job Job) {
		called <- struct{}{}
	})
	job := Job{InstanceID: "inst", Trigger: model.ReviewTrigger{Repo: "group/repo", CommitSHA: "abc"}}

	q.Enqueue(job)
	q.EnqueueNow(job)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("manual handler did not run")
	}
	select {
	case <-called:
		t.Fatal("pending timer was not replaced")
	case <-time.After(20 * time.Millisecond):
	}
}
