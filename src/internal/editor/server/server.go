// Package server exposes the index over a local HTTP API and serves the
// web UI.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"

	"apocalypter-l10n-tools/internal/editor/index"
)

const (
	defaultLimit = 200
	maxLimit     = 2000
	maxBodyBytes = 1 << 20
)

//go:embed static/index.html
var static embed.FS

// Source is what the editor searches and edits: an exported project
// (index.Index) or a player build (index.GameIndex).
type Source interface {
	Search(q index.Query) (index.Result, error)
	Stats() index.Stats
	Apply(e index.Edit) (index.Entry, error)
	Rebuild(ctx context.Context) error
}

type server struct {
	ix     Source
	logger *slog.Logger
}

// New returns the HTTP handler for the editor.
func New(ix Source, logger *slog.Logger) http.Handler {
	s := &server{ix: ix, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("POST /api/edit", s.edit)
	mux.HandleFunc("POST /api/rescan", s.rescan)
	return guard(mux)
}

// guard rejects requests that do not come from a same-origin page on a
// loopback host. The Host check defeats DNS rebinding; the Origin and
// Content-Type checks keep other sites from posting edits cross-origin.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "host not allowed")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
				writeError(w, http.StatusForbidden, "cross-origin request")
				return
			}
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "expected application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *server) page(w http.ResponseWriter, _ *http.Request) {
	data, err := static.ReadFile("static/index.html")
	if err != nil {
		s.fail(w, "page_read_failed", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'")
	_, _ = w.Write(data)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query()
	limit := defaultLimit
	if raw := p.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(maxLimit))
			return
		}
		limit = n
	}
	offset := 0
	if raw := p.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = n
	}
	sortBy, err := index.ParseSort(p.Get("sort"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.ix.Search(index.Query{
		Text:          p.Get("q"),
		Field:         p.Get("field"),
		GameObject:    p.Get("gameObject"),
		Script:        p.Get("script"),
		File:          p.Get("file"),
		Regex:         p.Get("regex") == "1",
		CaseSensitive: p.Get("case") == "1",
		Service:       p.Get("service") == "1",
		HideMaybe:     p.Get("hideMaybe") == "1",
		Sort:          sortBy,
		Desc:          p.Get("desc") == "1",
		Offset:        offset,
		Limit:         limit,
	})
	if errors.Is(err, index.ErrBadQuery) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.fail(w, "search_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.ix.Stats())
}

func (s *server) edit(w http.ResponseWriter, r *http.Request) {
	var e index.Edit
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := s.ix.Apply(e)
	switch {
	case errors.Is(err, index.ErrUnknownFile):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, index.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, index.ErrBadValue):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.fail(w, "edit_failed", err)
	default:
		writeJSON(w, http.StatusOK, updated)
	}
}

func (s *server) rescan(w http.ResponseWriter, r *http.Request) {
	if err := s.ix.Rebuild(r.Context()); err != nil {
		s.fail(w, "rescan_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, s.ix.Stats())
}

func (s *server) fail(w http.ResponseWriter, event string, err error) {
	s.logger.Error(event, "error", err.Error())
	writeError(w, http.StatusInternalServerError, "internal error, see server log")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
