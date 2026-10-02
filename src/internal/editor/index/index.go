// Package index keeps an in-memory index of MonoBehaviour string fields and
// applies edits: Index works on the asset files of a Unity project,
// GameIndex on the data.unity3d bundle of a player build.
package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"apocalypter-l10n-tools/internal/editor/unityyaml"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/textkind"
)

// Errors returned by Index methods.
var (
	ErrNotUnityProject = errors.New("not a Unity project: Assets directory is missing")
	ErrUnknownFile     = errors.New("file is not indexed")
	ErrConflict        = errors.New("asset changed since it was indexed")
	ErrBadQuery        = errors.New("bad query")
)

// monoScriptFileID is the fileID Unity uses in m_Script for scripts
// defined in a .cs file; other values point into a DLL.
const monoScriptFileID = 11500000

var (
	assetExts = map[string]bool{".prefab": true, ".unity": true, ".asset": true}
	// Asset directories scanned for .prefab/.unity/.asset files.
	assetDirs = []string{"Assets", "Packages"}
	// Directories scanned for script .meta files. Library/PackageCache
	// holds registry packages such as TextMeshPro.
	scriptDirs = []string{"Assets", "Packages", filepath.Join("Library", "PackageCache")}
	// Fields Unity maintains itself; editing them is never useful.
	ignoredFields = map[string]bool{"m_EditorClassIdentifier": true}
	numberRe      = regexp.MustCompile(`^-?\d+(\.\d+)?([eE][-+]?\d+)?$`)
	metaGUIDRe    = regexp.MustCompile(`(?m)^guid:\s*([0-9a-fA-F]{32})\s*$`)
)

// Entry is one searchable and editable string field.
type Entry struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	DocID      int64  `json:"docId,string"`
	GameObject string `json:"gameObject"`
	Script     string `json:"script"`
	Path       string `json:"path"`
	Value      string `json:"value"`
	// Start, End and Raw identify the exact bytes of the value in the file
	// at indexing time. Edits are rejected when they no longer match.
	Start int    `json:"start"`
	End   int    `json:"end"`
	Raw   string `json:"raw"`
	// Kind tells player-visible text from strings the game uses
	// internally.
	Kind textkind.Kind `json:"kind"`

	lower string
}

// Query describes a search. Empty filters match everything.
type Query struct {
	Text          string
	Field         string
	GameObject    string
	Script        string
	File          string
	Regex         bool
	CaseSensitive bool
	// Service includes strings of kind textkind.Service, which are hidden
	// by default.
	Service bool
	// HideMaybe leaves out strings of kind textkind.Maybe.
	HideMaybe bool
	// Sort orders the matches by a column, Desc in descending order.
	Sort Sort
	Desc bool
	// Offset skips that many matches; Limit caps the entries returned.
	Offset int
	Limit  int
}

// Result holds the first Query.Limit matches and the total match count.
// Hidden and HiddenMaybe count the strings that matched the other filters
// but were left out by kind: service strings without Query.Service, and
// Maybe strings with Query.HideMaybe.
type Result struct {
	Total       int     `json:"total"`
	Hidden      int     `json:"hidden"`
	HiddenMaybe int     `json:"hiddenMaybe"`
	Offset      int     `json:"offset"`
	Entries     []Entry `json:"entries"`

	// matches collects every match of a sorted query until finish.
	matches []*Entry
}

// Edit replaces the value of one indexed field.
type Edit struct {
	File string `json:"file"`
	// DocID selects the object inside File; only GameIndex uses it.
	DocID int64  `json:"docId,string,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Raw   string `json:"raw"`
	Value string `json:"value"`
}

// Stats summarizes the index contents.
type Stats struct {
	Files   int `json:"files"`
	Entries int `json:"entries"`
	Scripts int `json:"scripts"`
}

type script struct {
	name string
	dll  bool
}

// Index is safe for concurrent use.
type Index struct {
	root   string
	logger *slog.Logger

	// journal is the patch journal path; empty disables recording.
	journal string

	mu      sync.RWMutex
	scripts map[string]script
	files   map[string][]Entry
	order   []string
	// gen changes with every change of files, under the write lock.
	gen    uint64
	sorted sortCache
}

// New validates root and returns an empty index. Call Rebuild to fill it.
func New(root string, logger *slog.Logger) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	info, err := os.Stat(filepath.Join(abs, "Assets"))
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s: %w", abs, ErrNotUnityProject)
	}
	return &Index{root: abs, logger: logger, scripts: map[string]script{}, files: map[string][]Entry{}}, nil
}

// SetJournal makes Apply record every edit in the patch journal at path.
func (ix *Index) SetJournal(path string) { ix.journal = path }

// Root returns the absolute project root.
func (ix *Index) Root() string { return ix.root }

// Rebuild rescans the project and replaces the index contents.
func (ix *Index) Rebuild(ctx context.Context) error {
	scripts, err := ix.scanScripts(ctx)
	if err != nil {
		return err
	}
	paths, err := ix.listAssets(ctx)
	if err != nil {
		return err
	}
	files, err := ix.parseAll(ctx, paths, scripts)
	if err != nil {
		return err
	}

	order := make([]string, 0, len(files))
	for rel := range files {
		order = append(order, rel)
	}
	slices.Sort(order)

	ix.mu.Lock()
	ix.scripts, ix.files, ix.order = scripts, files, order
	ix.gen++
	ix.mu.Unlock()

	stats := ix.Stats()
	ix.logger.Info("index_rebuilt", "root", ix.root, "files", stats.Files, "entries", stats.Entries, "scripts", stats.Scripts)
	if stats.Files == 0 {
		ix.logger.Warn("index_empty",
			"root", ix.root,
			"assets_found", len(paths),
			"hint", "no text-serialized MonoBehaviours found; enable Force Text serialization or export the project as YAML")
	}
	return nil
}

// Stats returns index counters.
func (ix *Index) Stats() Stats {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	s := Stats{Files: len(ix.files), Scripts: len(ix.scripts)}
	for _, entries := range ix.files {
		s.Entries += len(entries)
	}
	return s
}

// Search returns entries matching q in file order.
func (ix *Index) Search(q Query) (Result, error) {
	match, err := q.matcher()
	if err != nil {
		return Result{}, err
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.sorted.search(ix.gen, q, func(res *Result, q Query) {
		for _, rel := range ix.order {
			res.add(ix.files[rel], match, q)
		}
	}), nil
}

// add appends the entries that match q to r: at most q.Limit of them,
// after skipping the first q.Offset matches. Total counts every match.
//
// A sorted query only collects the matches here; finish orders them and
// cuts out the page.
func (r *Result) add(entries []Entry, match func(Entry) bool, q Query) {
	r.Offset = q.Offset
	for i := range entries {
		e := &entries[i]
		if !match(*e) {
			continue
		}
		switch {
		case e.Kind == textkind.Service && !q.Service:
			r.Hidden++
			continue
		case e.Kind == textkind.Maybe && q.HideMaybe:
			r.HiddenMaybe++
			continue
		}
		r.Total++
		switch {
		case q.Sort != SortNone:
			r.matches = append(r.matches, e)
		case r.Total > q.Offset && len(r.Entries) < q.Limit:
			r.Entries = append(r.Entries, *e)
		}
	}
}

// matcher combines all filters of q.
func (q Query) matcher() (func(Entry) bool, error) {
	text, err := textMatcher(q)
	if err != nil {
		return nil, err
	}
	field, gameObject, scriptName, file := strings.ToLower(q.Field), strings.ToLower(q.GameObject), strings.ToLower(q.Script), strings.ToLower(q.File)
	contains := func(s, sub string) bool { return sub == "" || strings.Contains(strings.ToLower(s), sub) }
	return func(e Entry) bool {
		return contains(e.File, file) && contains(e.Path, field) && contains(e.GameObject, gameObject) && contains(e.Script, scriptName) && text(e)
	}, nil
}

func textMatcher(q Query) (func(Entry) bool, error) {
	switch {
	case q.Text == "":
		return func(Entry) bool { return true }, nil
	case q.Regex:
		expr := q.Text
		if !q.CaseSensitive {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadQuery, err)
		}
		return func(e Entry) bool { return re.MatchString(e.Value) }, nil
	case q.CaseSensitive:
		return func(e Entry) bool { return strings.Contains(e.Value, q.Text) }, nil
	default:
		text := strings.ToLower(q.Text)
		return func(e Entry) bool { return strings.Contains(e.lower, text) }, nil
	}
}

// Apply writes e to disk and returns the updated field. It fails with
// ErrConflict when the file no longer contains the expected bytes; the
// file is reindexed in that case, so a repeated search shows fresh data.
func (ix *Index) Apply(e Edit) (Entry, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	entries, ok := ix.files[e.File]
	if !ok {
		return Entry{}, fmt.Errorf("%q: %w", e.File, ErrUnknownFile)
	}
	abs := filepath.Join(ix.root, filepath.FromSlash(e.File))
	info, err := os.Stat(abs)
	if err != nil {
		return Entry{}, fmt.Errorf("stat asset: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Entry{}, fmt.Errorf("read asset: %w", err)
	}

	known := slices.ContainsFunc(entries, func(x Entry) bool { return x.Start == e.Start && x.End == e.End })
	if !known || e.End > len(data) || string(data[e.Start:e.End]) != e.Raw {
		ix.reindexLocked(e.File, data)
		return Entry{}, fmt.Errorf("%s:%d: %w", e.File, e.Start, ErrConflict)
	}

	out := make([]byte, 0, len(data)+len(e.Value)+3)
	out = append(out, data[:e.Start]...)
	out = append(out, ' ')
	out = append(out, unityyaml.Encode(e.Value)...)
	out = append(out, data[e.End:]...)

	asset, err := unityyaml.Parse(out)
	if err != nil {
		return Entry{}, fmt.Errorf("edited asset does not parse: %w", err)
	}
	updated, ok := findField(e.File, out, asset, ix.scripts, e.Start)
	if !ok || updated.Value != e.Value {
		return Entry{}, fmt.Errorf("edited value does not round-trip at %s:%d", e.File, e.Start)
	}
	var rec *patch.Patch
	if ix.journal != "" {
		if rec, err = ix.patchFor(e.File, data, e.Start, e.Value); err != nil {
			return Entry{}, err
		}
	}
	if err := writeAtomic(abs, out, info.Mode().Perm()); err != nil {
		return Entry{}, err
	}
	if rec != nil {
		if err := patch.Append(ix.journal, *rec); err != nil {
			// Keep the asset and the journal consistent: an edit that is not
			// journaled would never reach the game.
			if restoreErr := writeAtomic(abs, data, info.Mode().Perm()); restoreErr != nil {
				return Entry{}, errors.Join(err, fmt.Errorf("restore asset: %w", restoreErr))
			}
			return Entry{}, err
		}
	}
	ix.files[e.File] = buildEntries(e.File, out, asset, ix.scripts)
	ix.gen++
	ix.logger.Info("field_updated", "file", e.File, "path", updated.Path, "line", updated.Line)
	return updated, nil
}

// patchFor describes the edit of the field at start in the original data
// as a journal record. It returns nil when the value does not change.
func (ix *Index) patchFor(rel string, data []byte, start int, value string) (*patch.Patch, error) {
	asset, err := unityyaml.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse original asset: %w", err)
	}
	p, ok := ix.record(rel, asset, start)
	switch {
	case !ok:
		return nil, fmt.Errorf("no field at %s:%d", rel, start)
	case p.Old == value:
		return nil, nil
	}
	entries := buildEntries(rel, data, asset, ix.scripts)
	if i := slices.IndexFunc(entries, func(e Entry) bool { return e.Start == start }); i >= 0 {
		p.Kind = entries[i].Kind.String()
	}
	p.New = value
	return &p, nil
}

// record describes the field at start as a journal record with an empty
// New.
func (ix *Index) record(rel string, asset *unityyaml.Asset, start int) (patch.Patch, bool) {
	for _, mb := range asset.MonoBehaviours {
		i := slices.IndexFunc(mb.Fields, func(f unityyaml.Field) bool { return f.Start == start })
		if i < 0 {
			continue
		}
		field := mb.Fields[i]
		p := patch.Patch{File: rel, Path: field.Path, Old: field.Value, Script: ix.patchScript(mb)}
		for _, f := range mb.Fields[:i] {
			if f.Value == field.Value && !ignoredFields[f.Path] {
				p.Occurrence++
			}
		}
		if mb.GameObjectID != 0 {
			p.Owner = asset.GameObjectNames[mb.GameObjectID]
		} else if j := slices.IndexFunc(mb.Fields, func(f unityyaml.Field) bool { return f.Path == "m_Name" }); j >= 0 {
			p.Owner = mb.Fields[j].Value
		}
		return p, true
	}
	return patch.Patch{}, false
}

// Records describes every indexed string of the given kind as the journal
// record an edit of it would produce, with an empty New. Assets are read
// again, so the records match the files on disk.
func (ix *Index) Records(kind textkind.Kind) ([]patch.Patch, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []patch.Patch
	for _, rel := range ix.order {
		if !slices.ContainsFunc(ix.files[rel], func(e Entry) bool { return e.Kind == kind }) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ix.root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("read asset: %w", err)
		}
		asset, err := unityyaml.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, e := range buildEntries(rel, data, asset, ix.scripts) {
			if e.Kind != kind {
				continue
			}
			p, ok := ix.record(rel, asset, e.Start)
			if !ok {
				return nil, fmt.Errorf("no field at %s:%d", rel, e.Start)
			}
			p.Kind = kind.String()
			out = append(out, p)
		}
	}
	return out, nil
}

func (ix *Index) patchScript(mb unityyaml.MonoBehaviour) patch.Script {
	s, ok := ix.scripts[mb.ScriptGUID]
	switch {
	case !ok:
		return patch.Script{}
	case s.dll || mb.ScriptFileID != monoScriptFileID:
		return patch.Script{Assembly: s.name, FileID: mb.ScriptFileID}
	default:
		return patch.Script{Class: s.name}
	}
}

func (ix *Index) reindexLocked(rel string, data []byte) {
	asset, err := unityyaml.Parse(data)
	if err != nil {
		ix.logger.Warn("asset_reindex_failed", "file", rel, "error", err.Error())
		ix.files[rel] = nil
		ix.gen++
		return
	}
	ix.files[rel] = buildEntries(rel, data, asset, ix.scripts)
	ix.gen++
}

func (ix *Index) scanScripts(ctx context.Context) (map[string]script, error) {
	scripts := map[string]script{}
	err := ix.walk(ctx, scriptDirs, func(path string) error {
		var s script
		switch {
		case strings.HasSuffix(path, ".cs.meta"):
			s.name = strings.TrimSuffix(filepath.Base(path), ".cs.meta")
		case strings.HasSuffix(path, ".dll.meta"):
			s.name, s.dll = strings.TrimSuffix(filepath.Base(path), ".meta"), true
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			ix.logger.Warn("meta_read_failed", "path", path, "error", err.Error())
			return nil
		}
		if m := metaGUIDRe.FindSubmatch(data); m != nil {
			scripts[strings.ToLower(string(m[1]))] = s
		}
		return nil
	})
	return scripts, err
}

func (ix *Index) listAssets(ctx context.Context) ([]string, error) {
	var paths []string
	err := ix.walk(ctx, assetDirs, func(path string) error {
		if assetExts[filepath.Ext(path)] {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

// walk visits regular files under the given project subdirectories,
// skipping the hidden and "~"-suffixed folders Unity ignores.
func (ix *Index) walk(ctx context.Context, dirs []string, visit func(string) error) error {
	for _, dir := range dirs {
		base := filepath.Join(ix.root, dir)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && path == base {
					return fs.SkipDir
				}
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if path != base && (strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~")) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			return visit(path)
		})
		if err != nil {
			return fmt.Errorf("scan %s: %w", dir, err)
		}
	}
	return nil
}

func (ix *Index) parseAll(ctx context.Context, paths []string, scripts map[string]script) (map[string][]Entry, error) {
	type parsed struct {
		rel     string
		entries []Entry
	}
	jobs := make(chan string)
	results := make(chan parsed)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for path := range jobs {
				rel, entries, ok := ix.parseFile(path, scripts)
				if ok {
					results <- parsed{rel, entries}
				}
			}
		})
	}
	go func() {
		defer close(jobs)
		for _, p := range paths {
			select {
			case jobs <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	files := map[string][]Entry{}
	for r := range results {
		files[r.rel] = r.entries
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

// parseFile returns ok=false for files that are binary, have no
// MonoBehaviours or cannot be parsed.
func (ix *Index) parseFile(path string, scripts map[string]script) (string, []Entry, bool) {
	rel, err := filepath.Rel(ix.root, path)
	if err != nil {
		return "", nil, false
	}
	rel = filepath.ToSlash(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		ix.logger.Warn("asset_read_failed", "file", rel, "error", err.Error())
		return "", nil, false
	}
	if !bytes.Contains(data, []byte("--- !u!114 ")) {
		return "", nil, false
	}
	asset, err := unityyaml.Parse(data)
	if errors.Is(err, unityyaml.ErrNotText) {
		return "", nil, false
	}
	if err != nil {
		ix.logger.Warn("asset_parse_failed", "file", rel, "error", err.Error())
		return "", nil, false
	}
	return rel, buildEntries(rel, data, asset, scripts), true
}

func buildEntries(rel string, data []byte, asset *unityyaml.Asset, scripts map[string]script) []Entry {
	var entries []Entry
	for _, mb := range asset.MonoBehaviours {
		kinds := classifyFields(mb, scripts)
		for i, f := range mb.Fields {
			if f.Value == "" || ignoredFields[f.Path] || numberRe.MatchString(f.Value) {
				continue
			}
			entries = append(entries, newEntry(rel, data, asset, mb, f, scripts, kinds[i]))
		}
	}
	return entries
}

func findField(rel string, data []byte, asset *unityyaml.Asset, scripts map[string]script, start int) (Entry, bool) {
	for _, mb := range asset.MonoBehaviours {
		for i, f := range mb.Fields {
			if f.Start == start {
				return newEntry(rel, data, asset, mb, f, scripts, classifyFields(mb, scripts)[i]), true
			}
		}
	}
	return Entry{}, false
}

// classifyFields returns the textkind of every field of mb, in order.
func classifyFields(mb unityyaml.MonoBehaviour, scripts map[string]script) []textkind.Kind {
	c := textkind.Component{Script: componentScript(mb, scripts), Strings: make([]textkind.Field, len(mb.Fields))}
	for i, f := range mb.Fields {
		c.Strings[i] = textkind.Field{Path: f.Path, Value: f.Value}
		if needed, array := textkind.NumberField(f.Path); needed {
			if c.Numbers == nil {
				c.Numbers = map[string][]int32{}
			}
			c.Numbers[f.Path] = textkind.ParseYAMLNumber(f.Value, array)
		}
	}
	return textkind.Classify(c)
}

// knownByFileID maps the fileID Unity gives a script in a DLL to its
// class, for the classes textkind recognizes. YAML references DLL scripts
// only by DLL and fileID.
var knownByFileID = func() map[int64]textkind.Script {
	out := map[int64]textkind.Script{}
	for full := range textkind.TextComponents {
		i := strings.LastIndex(full, ".")
		s := textkind.Script{Namespace: full[:max(i, 0)], Class: full[i+1:]}
		out[gamepatch.ScriptFileID(s.Namespace, s.Class)] = s
	}
	return out
}()

// componentScript describes the script of mb for textkind. A .cs script
// of the project has no known assembly and counts as game code.
func componentScript(mb unityyaml.MonoBehaviour, scripts map[string]script) textkind.Script {
	s, ok := scripts[mb.ScriptGUID]
	switch {
	case !ok:
		return textkind.Script{}
	case s.dll || mb.ScriptFileID != monoScriptFileID:
		known := knownByFileID[mb.ScriptFileID]
		known.Assembly = s.name
		return known
	default:
		return textkind.Script{Class: s.name}
	}
}

func newEntry(rel string, data []byte, asset *unityyaml.Asset, mb unityyaml.MonoBehaviour, f unityyaml.Field, scripts map[string]script, kind textkind.Kind) Entry {
	return Entry{
		File:       rel,
		Line:       f.Line,
		DocID:      mb.ID,
		GameObject: asset.GameObjectNames[mb.GameObjectID],
		Script:     scriptName(mb, scripts),
		Path:       f.Path,
		Value:      f.Value,
		Start:      f.Start,
		End:        f.End,
		Raw:        string(data[f.Start:f.End]),
		Kind:       kind,
		lower:      strings.ToLower(f.Value),
	}
}

func scriptName(mb unityyaml.MonoBehaviour, scripts map[string]script) string {
	s, ok := scripts[mb.ScriptGUID]
	switch {
	case !ok:
		return mb.ScriptGUID
	case s.dll || mb.ScriptFileID != monoScriptFileID:
		return fmt.Sprintf("%s#%d", s.name, mb.ScriptFileID)
	default:
		return s.name
	}
}

func writeAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	// A dot-prefixed temp file keeps Unity from importing it.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace asset: %w", err)
	}
	return nil
}
