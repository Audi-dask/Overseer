package queue

import (
	"context"
	"sync"
	"time"

	"github.com/Audi-dask/Overseer/internal/model"
)

type Job struct {
	Trigger    model.ReviewTrigger
	InstanceID string
	Force      bool
}

type Handler func(ctx context.Context, job Job)

// DebouncedQueue coalesces jobs with the same repo+mr+commit within a window.
type DebouncedQueue struct {
	window  time.Duration
	handler Handler
	mu      sync.Mutex
	pending map[string]*time.Timer
	sem     chan struct{}
}

func New(window time.Duration, concurrency int, handler Handler) *DebouncedQueue {
	if concurrency < 1 {
		concurrency = 4
	}
	return &DebouncedQueue{
		window:  window,
		handler: handler,
		pending: map[string]*time.Timer{},
		sem:     make(chan struct{}, concurrency),
	}
}

func (q *DebouncedQueue) key(j Job) string {
	return j.InstanceID + "|" + j.Trigger.Repo + "|" + j.Trigger.MRID + "|" + j.Trigger.CommitSHA
}

// EnqueueNow bypasses the debounce window, used by manual re-runs from the UI.
func (q *DebouncedQueue) EnqueueNow(job Job) {
	q.sem <- struct{}{}
	go func() {
		defer func() { <-q.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				_ = r
			}
		}()
		q.handler(ctx, job)
	}()
}

func (q *DebouncedQueue) Enqueue(job Job) {
	k := q.key(job)
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.pending[k]; ok {
		t.Stop()
	}
	q.pending[k] = time.AfterFunc(q.window, func() {
		q.mu.Lock()
		delete(q.pending, k)
		q.mu.Unlock()
		q.sem <- struct{}{}
		go func() {
			defer func() { <-q.sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					_ = r
				}
			}()
			q.handler(ctx, job)
		}()
	})
}
