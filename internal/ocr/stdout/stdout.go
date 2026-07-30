package stdout

import (
	"context"
	"io"
	"os"
	"sync"
)

var (
	w  io.Writer = os.Stdout
	mu sync.RWMutex
)

type writerKey struct{}

// WithWriter scopes agent output to dst for everything running under ctx.
// Concurrent reviews each bind their own writer, so their logs stay separate.
func WithWriter(ctx context.Context, dst io.Writer) context.Context {
	if dst == nil {
		return ctx
	}
	return context.WithValue(ctx, writerKey{}, dst)
}

// WriterCtx returns the writer bound to ctx, or the process-wide writer.
func WriterCtx(ctx context.Context) io.Writer {
	if ctx != nil {
		if dst, ok := ctx.Value(writerKey{}).(io.Writer); ok && dst != nil {
			return dst
		}
	}
	return Writer()
}

// Writer returns the current stdout writer (real stdout or discard).
func Writer() io.Writer {
	mu.RLock()
	defer mu.RUnlock()
	return w
}

// SetWriter redirects agent progress output, letting the host service send it
// through its own logger instead of raw stdout.
func SetWriter(dst io.Writer) {
	mu.Lock()
	w = dst
	mu.Unlock()
}

// Quiet replaces stdout with io.Discard and returns a cleanup function.
// Usage:
//
//	defer stdout.Quiet()()
//
// WARNING: Quiet must ONLY be called from the main goroutine, before spawning
// any concurrent work that writes to stdout, and its returned cleanup must be
// deferred in the same goroutine. Never call Quiet from multiple goroutines
// concurrently — it is not designed for nested or parallel silencing.
func Quiet() func() {
	mu.Lock()
	old := w
	w = io.Discard
	mu.Unlock()
	return func() {
		mu.Lock()
		w = old
		mu.Unlock()
	}
}
