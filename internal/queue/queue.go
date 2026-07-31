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

// DebouncedQueue coalesces jobs with the same repo+mr+commit within a window
// and keeps the key occupied until the active handler returns.
type DebouncedQueue struct {
	window  time.Duration
	handler Handler
	mu      sync.Mutex
	active  map[string]*time.Timer
	sem     chan struct{}
}

func New(window time.Duration, concurrency int, handler Handler) *DebouncedQueue {
	if concurrency < 1 {
		concurrency = 4
	}
	return &DebouncedQueue{
		window:  window,
		handler: handler,
		active:  map[string]*time.Timer{},
		sem:     make(chan struct{}, concurrency),
	}
}

func (q *DebouncedQueue) key(j Job) string {
	return j.InstanceID + "|" + j.Trigger.Repo + "|" + j.Trigger.MRID + "|" + j.Trigger.CommitSHA
}

// EnqueueNow bypasses the debounce window, used by manual re-runs from the UI.
func (q *DebouncedQueue) EnqueueNow(job Job) {
	k := q.key(job)
	q.mu.Lock()
	if t, ok := q.active[k]; ok {
		if t == nil {
			q.mu.Unlock()
			return
		}
		t.Stop()
	}
	q.active[k] = nil
	q.mu.Unlock()
	go q.run(k, job)
}

func (q *DebouncedQueue) Enqueue(job Job) {
	k := q.key(job)
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.active[k]; ok {
		if t == nil {
			return
		}
		t.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(q.window, func() {
		q.mu.Lock()
		if q.active[k] != timer {
			q.mu.Unlock()
			return
		}
		q.active[k] = nil
		q.mu.Unlock()
		q.run(k, job)
	})
	q.active[k] = timer
}

func (q *DebouncedQueue) run(k string, job Job) {
	q.sem <- struct{}{}
	defer func() {
		<-q.sem
		q.mu.Lock()
		delete(q.active, k)
		q.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()
	q.handler(ctx, job)
}
