// Package telemetry replaces open-code-review's OTel telemetry with plain
// logging, so the agent's phases, LLM calls and tool calls show up in the
// service log without pulling in OTel exporters.
package telemetry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Audi-dask/Overseer/internal/ocr/stdout"
)

// Attr is a key/value pair attached to spans and events.
type Attr struct {
	Key   string
	Value any
}

// Span is the subset of OTel span methods used by the OCR agent. Our
// implementation logs one line per span with its accumulated attributes.
type Span interface {
	SetStatus(code int, description string)
	RecordError(err error)
	End(...any)
}

const (
	StatusUnset = 0
	StatusError = 1
	StatusOK    = 2
)

type logSpan struct {
	name  string
	kind  string
	start time.Time
	// ctx carries the review's log sink so a span's closing line lands in the
	// same per-review log as the line that opened it.
	ctx context.Context

	mu    sync.Mutex
	attrs []Attr
	err   error
	done  bool
}

func (s *logSpan) SetStatus(code int, description string) {
	if code == StatusError && description != "" {
		s.mu.Lock()
		if s.err == nil {
			s.err = fmt.Errorf("%s", description)
		}
		s.mu.Unlock()
	}
}

func (s *logSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *logSpan) setAttr(key string, value any) {
	s.mu.Lock()
	s.attrs = append(s.attrs, Attr{Key: key, Value: value})
	s.mu.Unlock()
}

func (s *logSpan) End(...any) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	attrs := formatAttrs(s.attrs)
	err := s.err
	s.mu.Unlock()

	dur := time.Since(s.start).Round(time.Millisecond)
	switch {
	case err != nil:
		logf(s.ctx, "%s %s failed in %s: %v%s", s.kind, s.name, dur, err, attrs)
	default:
		logf(s.ctx, "%s %s done in %s%s", s.kind, s.name, dur, attrs)
	}
}

func newSpan(ctx context.Context, kind, name string) *logSpan {
	return &logSpan{name: name, kind: kind, start: time.Now(), ctx: ctx}
}

func StartSpan(ctx context.Context, name string, _ ...any) (context.Context, Span) {
	logf(ctx, "phase %s start", name)
	return ctx, newSpan(ctx, "phase", name)
}

// StartToolSpan stays quiet: tool calls are already logged by
// PrintToolCallStarted / PrintToolCallFinished.
func StartToolSpan(ctx context.Context, toolName string) (context.Context, Span) {
	return ctx, &logSpan{name: toolName, kind: "tool", start: time.Now(), done: true, ctx: ctx}
}

func StartLLMSpan(ctx context.Context, model string) (context.Context, Span) {
	logf(ctx, "llm request model=%s", model)
	return ctx, &logSpan{name: model, kind: "llm", start: time.Now(), done: true, ctx: ctx}
}

func EndSpan(span Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
	}
	span.End()
}

func SetAttr(span Span, key string, value any) {
	if s, ok := span.(*logSpan); ok {
		s.setAttr(key, value)
	}
}

func RecordToolResult(span Span, toolName string, durMs int64, err error) {
	if err != nil {
		logf(spanCtx(span), "tool %s failed in %dms: %v", toolName, durMs, err)
	}
}

func RecordLLMResult(span Span, dur time.Duration, tokens int64, err error) {
	model := ""
	if s, ok := span.(*logSpan); ok {
		model = s.name
	}
	if err != nil {
		logf(spanCtx(span), "llm failed model=%s in %s: %v", model, dur.Round(time.Millisecond), err)
		return
	}
	logf(spanCtx(span), "llm ok model=%s in %s tokens=%d", model, dur.Round(time.Millisecond), tokens)
}

func spanCtx(span Span) context.Context {
	if s, ok := span.(*logSpan); ok {
		return s.ctx
	}
	return nil
}

func AnyToAttr(k string, v any) Attr { return Attr{Key: k, Value: v} }

func Event(ctx context.Context, name string, attrs ...Attr) {
	logf(ctx, "event %s%s", name, formatAttrs(attrs))
}

func Eventf(ctx context.Context, name, message string, attrs ...Attr) {
	logf(ctx, "event %s: %s%s", name, message, formatAttrs(attrs))
}

func ErrorEvent(ctx context.Context, name string, err error, attrs ...Attr) {
	logf(ctx, "event %s error: %v%s", name, err, formatAttrs(attrs))
}

func PhaseEvent(ctx context.Context, name, message string, dur time.Duration, err error) {
	if err != nil {
		logf(ctx, "phase %s failed in %s: %v", name, dur.Round(time.Millisecond), err)
		return
	}
	logf(ctx, "phase %s %s in %s", name, message, dur.Round(time.Millisecond))
}

func FormatDuration(d time.Duration) string { return d.Round(time.Millisecond).String() }

func PrintTraceSummary(ctx context.Context, filesReviewed, commentsGenerated int64, inputTokens, outputTokens, totalTokens int64, cacheReadTokens, cacheWriteTokens int64, duration time.Duration) {
	logf(ctx, "summary files=%d comments=%d tokens=%d (in=%d out=%d cache_read=%d cache_write=%d) dur=%s",
		filesReviewed, commentsGenerated, totalTokens, inputTokens, outputTokens,
		cacheReadTokens, cacheWriteTokens, duration.Round(time.Millisecond))
}

func PrintToolCallStarted(ctx context.Context, toolName string, args map[string]any) {
	logf(ctx, "tool start %s %s", toolName, summarizeArgs(args))
}

func PrintToolCallFinished(ctx context.Context, toolName string, dur time.Duration) {
	logf(ctx, "tool done %s in %s", toolName, dur.Round(time.Millisecond))
}

func PrintToolCallError(ctx context.Context, toolName string, err error) {
	logf(ctx, "tool error %s: %v", toolName, err)
}

func RecordReviewDuration(context.Context, time.Duration) {}
func RecordFilesReviewed(context.Context, int64)          {}
func RecordCommentsGenerated(context.Context, int64)      {}
func RecordLLMRequest(context.Context, string, time.Duration, int64, string) {}
func RecordToolCall(context.Context, string, time.Duration, bool) {}

func Init(context.Context) bool { return false }
func IsEnabled() bool           { return false }
func ContentLogging() bool      { return false }

func Shutdown(context.Context) error                     { return nil }
func ShutdownWithTimeout(context.Context, time.Duration) {}

// logf writes one agent log line to the review log bound to ctx, falling back
// to the process-wide agent writer outside a review run.
func logf(ctx context.Context, format string, args ...any) {
	fmt.Fprintf(stdout.WriterCtx(ctx), "[ocr] "+format+"\n", args...)
}

// summarizeArgs renders tool arguments so logs show which file / range / query
// a call touched, while keeping long payloads from flooding the log.
func summarizeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+clip(args[k], 80))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func formatAttrs(attrs []Attr) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		parts = append(parts, a.Key+"="+clip(a.Value, 120))
	}
	return " " + strings.Join(parts, " ")
}

func clip(v any, max int) string {
	s := strings.Join(strings.Fields(fmt.Sprint(v)), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
