// Command editor serves a local web UI for searching and
// editing string fields of MonoBehaviour components in Unity assets.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/editor/index"
	"apocalypter-l10n-tools/internal/editor/server"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/po"
	"apocalypter-l10n-tools/internal/textkind"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	if len(os.Args) > 1 && os.Args[1] == "dump" {
		err = runDump(ctx, os.Args[2:], os.Stderr)
	} else {
		err = run(ctx, os.Args[1:], os.Stderr, nil)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// sourceFlags are the flags that choose what the editor opens.
type sourceFlags struct {
	root, level, game, journal string
}

func (f *sourceFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.root, "root", ".", "Unity project root (the directory containing Assets/)")
	fs.StringVar(&f.level, "log-level", "info", "log level: debug, info, warn, error")
	fs.StringVar(&f.game, "game", "", "player build to edit directly: the *_Data directory or data.unity3d; replaces -root")
	fs.StringVar(&f.journal, "patches", "", "patch journal for patcher (default <root>/patches.json, or patches.json in the *_Data directory with -game)")
}

// open validates the parsed flags and returns the logger, the journal path
// and an index with that journal set, not yet built.
func (f *sourceFlags) open(fs *flag.FlagSet, logOut io.Writer) (*slog.Logger, source, string, error) {
	switch fs.NArg() {
	case 0:
	case 1:
		f.root = fs.Arg(0)
	default:
		return nil, nil, "", fmt.Errorf("expected at most one project root, got %d arguments", fs.NArg())
	}
	rootSet := fs.NArg() > 0
	fs.Visit(func(fl *flag.Flag) { rootSet = rootSet || fl.Name == "root" })
	if f.game != "" && rootSet {
		return nil, nil, "", errors.New("-game cannot be combined with a project root")
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(f.level)); err != nil {
		return nil, nil, "", fmt.Errorf("invalid -log-level: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: lvl}))

	ix, err := newSource(f.root, f.game, logger)
	if err != nil {
		return nil, nil, "", err
	}
	journal := f.journal
	if journal == "" {
		journal = filepath.Join(ix.Root(), "patches.json")
	}
	ix.SetJournal(journal)
	logger.Info("journal_enabled", "path", journal)
	return logger, ix, journal, nil
}

// run starts the server and blocks until ctx is cancelled. When ready is
// not nil, it receives the listening address once the server accepts
// connections.
func run(ctx context.Context, args []string, logOut io.Writer, ready chan<- string) error {
	fs := flag.NewFlagSet("editor", flag.ContinueOnError)
	fs.SetOutput(logOut)
	var src sourceFlags
	src.register(fs)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address; only loopback hosts are accepted by the UI")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: editor [flags] [project-root]")
		fmt.Fprintln(fs.Output(), "       editor [flags] -game <Game_Data dir | data.unity3d>")
		fmt.Fprintln(fs.Output(), "       editor dump -o l10n/ru [flags] (project-root | -game <game>)")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger, ix, _, err := src.open(fs, logOut)
	if err != nil {
		return err
	}

	started := time.Now()
	if err := ix.Rebuild(ctx); err != nil {
		return fmt.Errorf("build index: %w", err)
	}
	logger.Info("index_ready", "duration_ms", time.Since(started).Milliseconds())

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{
		Handler:           server.New(ix, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Rescanning a large project can take a while.
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Info("server_started", "url", "http://"+ln.Addr().String(), "root", ix.Root())
	if ready != nil {
		ready <- ln.Addr().String()
	}

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	logger.Info("server_stopped")
	return nil
}

type source interface {
	server.Source
	SetJournal(path string)
	Root() string
	Records(kind textkind.Kind) ([]patch.Patch, error)
}

func newSource(root, game string, logger *slog.Logger) (source, error) {
	if game != "" {
		return index.NewGame(game, logger)
	}
	return index.New(root, logger)
}

// runDump writes translation.map and translation.po of the on-screen
// strings into the -o directory. The strings are read with the journal
// applied, because the patcher applies dictionary patches after the
// journal; strings equal to a translation from the journal are already
// translated and left out. Translations already in translation.po carry
// over by msgid.
func runDump(ctx context.Context, args []string, logOut io.Writer) error {
	fs := flag.NewFlagSet("editor dump", flag.ContinueOnError)
	fs.SetOutput(logOut)
	var src sourceFlags
	src.register(fs)
	out := fs.String("o", "", "localization directory for "+dictionary.MapName+" and "+dictionary.POName+"; translations already in it are kept (required)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: editor dump -o l10n/ru [flags] [project-root]")
		fmt.Fprintln(fs.Output(), "       editor dump -o l10n/ru [flags] -game <Game_Data dir | data.unity3d>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		fs.Usage()
		return errors.New("-o is required")
	}
	previous, err := loadPO(filepath.Join(*out, dictionary.POName))
	if err != nil {
		return err
	}
	logger, ix, journal, err := src.open(fs, logOut)
	if err != nil {
		return err
	}
	journaled, err := patch.Load(journal)
	if err != nil {
		return err
	}
	if err := ix.Rebuild(ctx); err != nil {
		return fmt.Errorf("build index: %w", err)
	}
	found, err := ix.Records(textkind.Screen)
	if err != nil {
		return err
	}
	translatedByJournal := make(map[string]bool, len(journaled))
	for _, p := range journaled {
		translatedByJournal[p.New] = true
	}
	all := len(found)
	found = slices.DeleteFunc(found, func(p patch.Patch) bool { return translatedByJournal[p.Old] })

	entries := dictionary.Build(found)
	abs, err := filepath.Abs(*out)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", *out, err)
	}
	header := dictionary.Header{Project: strings.TrimSuffix(filepath.Base(ix.Root()), "_Data"), Language: filepath.Base(abs)}
	mapData, poData, obsoleted, err := dictionary.Encode(entries, previous, header)
	if err != nil {
		return err
	}
	for _, m := range obsoleted {
		logger.Warn("translation_obsolete", "msgid", m.ID, "msgstr", m.Str)
	}
	if err := dictionary.Save(*out, mapData, poData); err != nil {
		return err
	}
	translated := 0
	if previous != nil {
		done := map[string]bool{}
		for _, m := range previous.Messages {
			done[m.ID] = done[m.ID] || m.Str != ""
		}
		for _, e := range entries {
			if done[e.Old] {
				translated++
			}
		}
	}
	logger.Info("dictionary_written", "dir", *out, "strings", len(entries), "translated", translated,
		"locations", len(found), "journal_translated", all-len(found), "obsoleted", len(obsoleted))
	return nil
}

// loadPO reads the PO file at path; a missing file yields nil.
func loadPO(path string) (*po.File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := po.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
