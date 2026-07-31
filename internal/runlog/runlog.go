// Package runlog captures the per-review agent log. Agent and pipeline output
// is written to one file per review instead of the process stdout, so the
// server terminal keeps only HTTP access logs and the admin UI can show the
// full trace of a single review.
package runlog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Audi-dask/Overseer/internal/ocr/stdout"
)

// maxBytes caps one review log so a runaway agent cannot fill the disk.
const maxBytes = 4 << 20

var (
	dirMu sync.RWMutex
	dir   = filepath.Join("data", "reviewlogs")
)

// SetDir configures where review logs are stored.
func SetDir(d string) {
	if strings.TrimSpace(d) == "" {
		return
	}
	dirMu.Lock()
	dir = d
	dirMu.Unlock()
}

func baseDir() string {
	dirMu.RLock()
	defer dirMu.RUnlock()
	return dir
}

func pathFor(reviewID string) string {
	return filepath.Join(baseDir(), reviewID+".log")
}

// Sink is an io.Writer that prefixes every line with a timestamp and appends
// it to the review's log file.
type Sink struct {
	mu       sync.Mutex
	f        *os.File
	written  int
	overflow bool
	pending  []byte
}

// Open creates (or truncates) the log file for a review.
func Open(reviewID string) (*Sink, error) {
	if strings.TrimSpace(reviewID) == "" {
		return nil, fmt.Errorf("runlog: empty review id")
	}
	if err := os.MkdirAll(baseDir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(pathFor(reviewID))
	if err != nil {
		return nil, err
	}
	return &Sink{f: f}, nil
}

func (s *Sink) Write(p []byte) (int, error) {
	if s == nil {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overflow {
		return len(p), nil
	}
	// Agent output arrives in line chunks; buffer partial lines so the
	// timestamp lands at the start of a real line.
	s.pending = append(s.pending, p...)
	for {
		i := bytes.IndexByte(s.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(s.pending[:i]), "\r")
		s.pending = s.pending[i+1:]
		s.writeLine(line)
	}
	return len(p), nil
}

// Printf writes one formatted line to the sink.
func (s *Sink) Printf(format string, args ...any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overflow {
		return
	}
	s.writeLine(fmt.Sprintf(format, args...))
}

// writeLine must be called with s.mu held.
func (s *Sink) writeLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	out := time.Now().Format("2006/01/02 15:04:05") + " " + line + "\n"
	if s.written+len(out) > maxBytes {
		s.overflow = true
		_, _ = s.f.WriteString(time.Now().Format("2006/01/02 15:04:05") +
			" [runlog] 日志超过上限，后续输出已截断\n")
		return
	}
	n, err := s.f.WriteString(out)
	if err == nil {
		s.written += n
	}
}

func (s *Sink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) > 0 {
		s.writeLine(strings.TrimRight(string(s.pending), "\r\n"))
		s.pending = nil
	}
	return s.f.Close()
}

// Read returns the stored log of a review. Missing logs yield an empty string.
func Read(reviewID string) (string, error) {
	b, err := os.ReadFile(pathFor(reviewID))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Remove deletes a review log. Missing logs are already clean.
func Remove(reviewID string) error {
	err := os.Remove(pathFor(reviewID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// With binds a sink to ctx so agent and pipeline code can find it.
func With(ctx context.Context, s *Sink) context.Context {
	if s == nil {
		return ctx
	}
	return stdout.WithWriter(ctx, s)
}

// Printf writes a line to the sink bound to ctx, falling back to the process
// stdout writer when a call happens outside a review run.
func Printf(ctx context.Context, format string, args ...any) {
	fmt.Fprintf(stdout.WriterCtx(ctx), format+"\n", args...)
}
