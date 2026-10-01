package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/editor/index"
	"apocalypter-l10n-tools/internal/editor/textkind"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unitytest"
)

const prefab = `%YAML 1.1
--- !u!114 &200
MonoBehaviour:
  m_GameObject: {fileID: 0}
  m_Script: {fileID: 11500000, guid: 11111111111111111111111111111111, type: 3}
  m_text: Hello World
`

func newHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	return newHandlerWith(t, prefab)
}

func newHandlerWith(t *testing.T, content string) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "Assets", "Menu.prefab")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ix, err := index.New(root, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(ix, logger), path
}

func do(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestPage(t *testing.T) {
	h, _ := newHandler(t)
	rec := do(t, h, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>apocalypter-l10n-tools editor</title>") {
		t.Errorf("page: %d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("missing CSP header")
	}
	if rec := do(t, h, http.MethodGet, "/missing", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: %d", rec.Code)
	}
}

func TestSearchAndStats(t *testing.T) {
	h, _ := newHandler(t)
	rec := do(t, h, http.MethodGet, "/api/search?q=hello&limit=5", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	res := decode[index.Result](t, rec)
	if res.Total != 1 || res.Entries[0].Value != "Hello World" || res.Entries[0].DocID != 200 {
		t.Errorf("result = %+v", res)
	}

	for _, target := range []string{"/api/search?q=(&regex=1", "/api/search?limit=0", "/api/search?limit=x", "/api/search?offset=-1", "/api/search?offset=x", "/api/search?sort=size"} {
		if rec := do(t, h, http.MethodGet, target, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
	if res := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=HELLO&case=1", "", nil)); res.Total != 0 {
		t.Errorf("case-sensitive search = %+v", res)
	}
	if res := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=hello&gameObject=absent", "", nil)); res.Total != 0 {
		t.Errorf("game object filter = %+v", res)
	}

	stats := decode[index.Stats](t, do(t, h, http.MethodGet, "/api/stats", "", nil))
	if stats.Files != 1 || stats.Entries != 1 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestEdit(t *testing.T) {
	h, path := newHandler(t)
	entry := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=hello", "", nil)).Entries[0]

	edit := func(value string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(index.Edit{File: entry.File, Start: entry.Start, End: entry.End, Raw: entry.Raw, Value: value})
		return do(t, h, http.MethodPost, "/api/edit", string(body), map[string]string{"Origin": "http://127.0.0.1:8080"})
	}
	rec := edit("Bye")
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	if updated := decode[index.Entry](t, rec); updated.Value != "Bye" {
		t.Errorf("updated = %+v", updated)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "  m_text: Bye\n") {
		t.Errorf("file = %s", data)
	}

	if rec := edit("again"); rec.Code != http.StatusConflict {
		t.Errorf("stale edit: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/edit", `{"file":"Assets/None.prefab"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown file: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/edit", `{"bogus":1}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}

	// Directory permissions cannot make a folder unwritable on Windows.
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(filepath.Dir(path), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0o755) })
	fresh := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=bye", "", nil)).Entries[0]
	body, _ := json.Marshal(index.Edit{File: fresh.File, Start: fresh.Start, End: fresh.End, Raw: fresh.Raw, Value: "x"})
	if rec := do(t, h, http.MethodPost, "/api/edit", string(body), nil); rec.Code != http.StatusInternalServerError && os.Geteuid() != 0 {
		t.Errorf("unwritable dir: %d %s", rec.Code, rec.Body)
	}
}

func TestRescan(t *testing.T) {
	h, path := newHandler(t)
	if err := os.WriteFile(path, []byte(strings.Replace(prefab, "Hello World", "Changed", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPost, "/api/rescan", "{}", nil); rec.Code != http.StatusOK {
		t.Fatalf("rescan: %d", rec.Code)
	}
	if res := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=changed", "", nil)); res.Total != 1 {
		t.Errorf("rescan did not pick up change: %+v", res)
	}

	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(filepath.Dir(path), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0o755) })
	if rec := do(t, h, http.MethodPost, "/api/rescan", "{}", nil); rec.Code != http.StatusInternalServerError && os.Geteuid() != 0 {
		t.Errorf("unreadable project: %d", rec.Code)
	}
}

func TestGuard(t *testing.T) {
	h, _ := newHandler(t)
	cases := []struct {
		name    string
		method  string
		host    string
		headers map[string]string
		want    int
	}{
		{"foreign host", http.MethodGet, "evil.example:8080", nil, http.StatusForbidden},
		{"localhost", http.MethodGet, "localhost:8080", nil, http.StatusOK},
		{"ipv6 loopback", http.MethodGet, "[::1]:8080", nil, http.StatusOK},
		{"no port", http.MethodGet, "127.0.0.1", nil, http.StatusOK},
		{"cross origin", http.MethodPost, "127.0.0.1:8080", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"form post", http.MethodPost, "127.0.0.1:8080", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/api/stats"
			if tc.method == http.MethodPost {
				target = "/api/rescan"
			}
			req := httptest.NewRequest(tc.method, target, strings.NewReader("{}"))
			req.Host = tc.host
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("code = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestEditGame(t *testing.T) {
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 1, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Title", 2)},
		{PathID: 2, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(1, 0, 0, "", "Hello World")},
	}, nil)
	dir := t.TempDir()
	bundle := unitytest.Bundle([]unitytest.Node{{Path: "level0", Flags: 4, Data: level}}, 64)
	if err := os.WriteFile(filepath.Join(dir, "data.unity3d"), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	g, err := index.NewGame(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	g.SetJournal(filepath.Join(dir, "patches.json"))
	if err := g.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := New(g, logger)

	entry := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=hello", "", nil)).Entries[0]
	// The UI sends docId back as the string it received.
	edit := func(value string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(index.Edit{File: entry.File, DocID: entry.DocID, Start: entry.Start, End: entry.End, Raw: entry.Raw, Value: value})
		if !strings.Contains(string(body), `"docId":"2"`) {
			t.Fatalf("docId encoding: %s", body)
		}
		return do(t, h, http.MethodPost, "/api/edit", string(body), nil)
	}
	if rec := edit("1"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad value: %d %s", rec.Code, rec.Body)
	}
	rec := edit("Привет")
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	if updated := decode[index.Entry](t, rec); updated.Value != "Привет" || updated.DocID != 2 {
		t.Errorf("updated = %+v", updated)
	}
	if rec := edit("again"); rec.Code != http.StatusConflict {
		t.Errorf("stale edit: %d", rec.Code)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "patches.json")); !strings.Contains(string(data), `"new": "Привет"`) {
		t.Errorf("journal = %s", data)
	}
}

func TestSearchService(t *testing.T) {
	h, _ := newHandlerWith(t, strings.Replace(prefab, "  m_text: Hello World\n", "  m_Name: MenuPanel\n  m_text: Hello World\n", 1))
	res := decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=", "", nil))
	if res.Total != 1 || res.Hidden != 1 || res.Entries[0].Kind != textkind.Maybe {
		t.Errorf("default = %+v", res)
	}
	res = decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=&service=1", "", nil))
	if res.Total != 2 || res.Hidden != 0 {
		t.Errorf("with service = %+v", res)
	}
	res = decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=&service=1&offset=1&limit=1", "", nil))
	if res.Total != 2 || res.Offset != 1 || len(res.Entries) != 1 || res.Entries[0].Value != "Hello World" {
		t.Errorf("second page = %+v", res)
	}
	res = decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=&service=1&sort=value&desc=1", "", nil))
	if len(res.Entries) != 2 || res.Entries[0].Value != "MenuPanel" || res.Entries[1].Value != "Hello World" {
		t.Errorf("sorted = %+v", res.Entries)
	}
	res = decode[index.Result](t, do(t, h, http.MethodGet, "/api/search?q=&hideMaybe=1", "", nil))
	if res.Total != 0 || res.HiddenMaybe != 1 || res.Hidden != 1 {
		t.Errorf("hide maybe = %+v", res)
	}
	page := do(t, h, http.MethodGet, "/", "", nil).Body.String()
	for _, id := range []string{`id="service"`, `id="hideMaybe"`, `id="pager"`, `id="page-size"`} {
		if !strings.Contains(page, id) {
			t.Errorf("page lacks %s", id)
		}
	}
}
