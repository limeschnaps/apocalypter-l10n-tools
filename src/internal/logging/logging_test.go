package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandlerFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, nil)).With("run", 1).WithGroup("g")
	r := slog.NewRecord(time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC), slog.LevelWarn, "patch_not_found", 0)
	r.AddAttrs(
		slog.String("plain", "Свет"),
		slog.String("spaced", "a b"),
		slog.String("empty", ""),
		slog.String("quote", `say "hi"`),
		slog.Int("n", 3),
		slog.Any("err", errors.New("no match")),
		slog.Any("list", []string{"level0:2", "level1:3"}),
		slog.Time("at", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)),
		slog.Group("sub", slog.Bool("ok", true)),
		slog.Attr{},
	)
	if err := logger.Handler().Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	want := `2026-01-02 15:04:05 WARN  patch_not_found run=1 g.plain=Свет g.spaced="a b" g.empty="" g.quote="say \"hi\"" g.n=3 g.err="no match" g.list="[level0:2 level1:3]" g.at="2026-01-02 00:00:00" g.sub.ok=true` + "\n"
	if got := buf.String(); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestHandlerLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelWarn))
	logger.Info("hidden")
	logger.Error("shown")
	if got := buf.String(); strings.Contains(got, "hidden") || !strings.Contains(got, "ERROR shown\n") {
		t.Errorf("log = %q", got)
	}
	if h := NewHandler(&buf, nil); h.WithGroup("") != h {
		t.Error("empty group changed the handler")
	}
}

func TestNewWritesWarningsToFile(t *testing.T) {
	var console, file bytes.Buffer
	logger := New(&console, nil, &file)
	logger.Info("started")
	logger.Warn("careful")
	logger.Error("broken")
	if got := console.String(); !strings.Contains(got, "started") || !strings.Contains(got, "careful") || !strings.Contains(got, "broken") {
		t.Errorf("console = %q", got)
	}
	if got := file.String(); strings.Contains(got, "started") || !strings.Contains(got, "WARN  careful") || !strings.Contains(got, "ERROR broken") {
		t.Errorf("file = %q", got)
	}

	console.Reset()
	New(&console, nil, nil).Warn("alone")
	if !strings.Contains(console.String(), "alone") {
		t.Errorf("console = %q", console.String())
	}
}

func TestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	f := NewFile(path)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused file was created: %v", err)
	}
	for _, line := range []string{"one\n", "two\n"} {
		f := NewFile(path)
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "one\ntwo\n" {
		t.Errorf("file = %q, want appended lines", data)
	}

	bad := NewFile(filepath.Join(t.TempDir(), "missing", "run.log"))
	if _, err := bad.Write([]byte("x")); err == nil {
		t.Error("expected open error")
	}
}
