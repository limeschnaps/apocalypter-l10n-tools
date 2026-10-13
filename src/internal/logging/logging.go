// Package logging provides the human-readable slog handler of the
// command-line tools and a log file that is created on the first write.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// TimeFormat is the timestamp layout of every line.
const TimeFormat = "2006-01-02 15:04:05"

// Handler writes each record as one line:
//
//	2026-01-02 15:04:05 WARN  patch_not_found index=3 owner=Title error="patch matches no object"
//
// Values with spaces, quotes or control characters are quoted.
type Handler struct {
	w     io.Writer
	mu    *sync.Mutex
	level slog.Leveler
	// attrs holds the preformatted attributes added by WithAttrs.
	attrs  string
	prefix string
}

// NewHandler returns a Handler that writes records at level or above to w.
// A nil level means slog.LevelInfo.
func NewHandler(w io.Writer, level slog.Leveler) *Handler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &Handler{w: w, mu: &sync.Mutex{}, level: level}
}

// Enabled reports whether records at l are written.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle writes r as one line.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if !r.Time.IsZero() {
		b.WriteString(r.Time.Format(TimeFormat))
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "%-5s %s", r.Level.String(), r.Message)
	b.WriteString(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&b, h.prefix, a)
		return true
	})
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

// WithAttrs returns a Handler that adds attrs to every record.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	for _, a := range attrs {
		appendAttr(&b, h.prefix, a)
	}
	c := *h
	c.attrs += b.String()
	return &c
}

// WithGroup returns a Handler that prefixes the keys of later attributes
// with name and a dot.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix += name + "."
	return &c
}

func appendAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			appendAttr(b, prefix, g)
		}
		return
	}
	b.WriteByte(' ')
	b.WriteString(prefix)
	b.WriteString(a.Key)
	b.WriteByte('=')
	b.WriteString(quote(formatValue(a.Value)))
}

func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindTime:
		return v.Time().Format(TimeFormat)
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return x.Error()
		case []string:
			return "[" + strings.Join(x, " ") + "]"
		}
	}
	return v.String()
}

func quote(s string) string {
	needs := s == "" || strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r)
	})
	if needs {
		return strconv.Quote(s)
	}
	return s
}

// File appends to the file at path, which is created on the first write,
// so that a run without warnings leaves no log behind.
type File struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

// NewFile returns a File writing to path.
func NewFile(path string) *File {
	return &File{path: path}
}

// Write appends p to the file, opening it first if needed.
func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.f == nil {
		file, err := os.OpenFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return 0, fmt.Errorf("open log file: %w", err)
		}
		f.f = file
	}
	return f.f.Write(p)
}

// Close closes the file if it was opened.
func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.f == nil {
		return nil
	}
	err := f.f.Close()
	f.f = nil
	return err
}

// New returns a logger that writes records at level or above to console
// and, when file is not nil, warnings and errors to file as well.
func New(console io.Writer, level slog.Leveler, file io.Writer) *slog.Logger {
	handlers := []slog.Handler{NewHandler(console, level)}
	if file != nil {
		handlers = append(handlers, NewHandler(file, slog.LevelWarn))
	}
	return slog.New(slog.NewMultiHandler(handlers...))
}
