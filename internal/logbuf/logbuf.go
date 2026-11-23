// Package logbuf provides a slog handler that also keeps recent records in
// memory for the dashboard's log view.
package logbuf

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry is one log line.
type Entry struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
	Attrs string    `json:"attrs,omitempty"`
}

// Buffer is a fixed-size ring of entries.
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	max     int
}

// New creates a buffer holding up to max entries.
func New(max int) *Buffer { return &Buffer{max: max} }

func (b *Buffer) add(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, e)
	if len(b.entries) > b.max {
		b.entries = append([]Entry(nil), b.entries[len(b.entries)-b.max:]...)
	}
}

// Entries returns a copy, newest last.
func (b *Buffer) Entries() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Entry(nil), b.entries...)
}

// Handler tees records to next and into the buffer.
type Handler struct {
	next  slog.Handler
	buf   *Buffer
	level slog.Leveler
	attrs []slog.Attr
	group string
}

// NewHandler wraps next.
func NewHandler(next slog.Handler, buf *Buffer, level slog.Leveler) *Handler {
	return &Handler{next: next, buf: buf, level: level}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder
	write := func(a slog.Attr) {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		fmt.Fprintf(&sb, "%s=%v", key, a.Value.Any())
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(func(a slog.Attr) bool { write(a); return true })
	h.buf.add(Entry{Time: r.Time.UTC(), Level: r.Level.String(), Msg: r.Message, Attrs: sb.String()})
	if h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	c.next = h.next.WithAttrs(attrs)
	return &c
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	c := *h
	c.group = name
	c.next = h.next.WithGroup(name)
	return &c
}
