package index

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"apocalypter-l10n-tools/internal/editor/scriptlayout"
	"apocalypter-l10n-tools/internal/editor/textkind"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unityfs"
)

// managedDir holds the script assemblies of a Mono player build, next to
// data.unity3d.
const managedDir = "Managed"

// ErrBadValue reports a new value that the build index could not find
// again after saving. Only strings found without a script layout are
// restricted this way.
var ErrBadValue = errors.New("value must be at least 2 bytes of printable text with a letter")

type objectKey struct {
	file   string
	pathID int64
}

// GameIndex indexes the strings of MonoBehaviours in the data.unity3d
// bundle of a player build, without an exported project.
//
// The build has no type trees. When the Managed directory next to the
// bundle holds the script assemblies, each component is decoded with the
// layout scriptlayout derives from its class, and strings get the field
// paths Unity YAML uses, such as "fsm.states[1].name". Components without
// a usable layout fall back to serialized.ScanStrings, and their strings
// are named by position: str[0], str[1] and so on.
//
// Edits change only the in-memory copy and are recorded in the patch
// journal; the patcher writes them into the game. Rebuild loads the
// original bundle (data.unity3d.orig when present) and replays the
// journal, so the index shows what the patched game will contain.
type GameIndex struct {
	bundle string
	logger *slog.Logger

	// journal is the patch journal path; empty keeps edits in memory only.
	journal string

	// mu is held for writing during Rebuild, so an edit never races with
	// a journal replay.
	mu    sync.RWMutex
	build *gamepatch.Build
	comps []gamepatch.Component
	// layouts holds the script layout of each component, nil when its
	// strings come from the heuristic scan.
	layouts []*scriptlayout.Node
	entries [][]Entry
	byKey   map[objectKey]int
	// gen changes with every change of entries, under the write lock.
	gen    uint64
	sorted sortCache
}

// NewGame accepts the *_Data directory or the bundle itself and returns an
// empty index. Call Rebuild to fill it.
func NewGame(path string, logger *slog.Logger) (*GameIndex, error) {
	bundle, err := gamepatch.BundlePath(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(bundle)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle path: %w", err)
	}
	return &GameIndex{bundle: abs, logger: logger, byKey: map[objectKey]int{}}, nil
}

// SetJournal makes Apply record every edit in the patch journal at path.
func (g *GameIndex) SetJournal(path string) { g.journal = path }

// Root returns the absolute *_Data directory of the build.
func (g *GameIndex) Root() string { return filepath.Dir(g.bundle) }

// Rebuild reloads the bundle, replays the journal and rescans all strings.
func (g *GameIndex) Rebuild(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rebuildLocked(ctx)
}

func (g *GameIndex) rebuildLocked(ctx context.Context) error {
	source, err := gamepatch.PristinePath(g.bundle)
	if err != nil {
		return err
	}
	build, err := loadBuild(source)
	if err != nil {
		return err
	}
	var patches []patch.Patch
	if g.journal != "" {
		if patches, err = patch.Load(g.journal); err != nil {
			return err
		}
	}
	failed := 0
	for i, p := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if res := build.Apply(p, gamepatch.Options{}); res.Err != nil {
			failed++
			g.logger.Warn("journal_patch_failed", "index", i+1, "file", p.File, "path", p.Path, "owner", p.Owner, "error", res.Err.Error())
		}
	}

	comps := build.Components()
	layouts := g.loadLayouts(comps)
	entries, err := g.scanAll(ctx, build, comps, layouts)
	if err != nil {
		return err
	}
	byKey := make(map[objectKey]int, len(comps))
	decoded := 0
	for i, c := range comps {
		byKey[objectKey{c.File, c.PathID}] = i
		if layouts[i] != nil {
			decoded++
		}
	}
	g.build, g.comps, g.layouts, g.entries, g.byKey = build, comps, layouts, entries, byKey
	g.gen++

	stats := g.statsLocked()
	g.logger.Info("index_rebuilt",
		"bundle", source, "files", stats.Files, "entries", stats.Entries, "scripts", stats.Scripts,
		"components", len(comps), "decoded", decoded, "heuristic", len(comps)-decoded,
		"journal_patches", len(patches), "journal_failed", failed)
	return nil
}

// loadLayouts returns the script layout of every component, or nil entries
// when the assemblies are missing or a script is not supported.
func (g *GameIndex) loadLayouts(comps []gamepatch.Component) []*scriptlayout.Node {
	out := make([]*scriptlayout.Node, len(comps))
	dir := filepath.Join(g.Root(), managedDir)
	resolver, skipped, err := scriptlayout.LoadDir(dir)
	if err != nil {
		g.logger.Warn("script_assemblies_unavailable", "dir", dir, "error", err.Error(),
			"hint", "strings are found heuristically and named str[N]")
		return out
	}
	for _, e := range skipped {
		g.logger.Warn("script_assembly_skipped", "error", e.Error())
	}
	failed := map[string]bool{}
	for i, c := range comps {
		s := c.Script
		if s.ClassName == "" {
			continue
		}
		layout, err := resolver.Script(s.AssemblyName, s.Namespace, s.ClassName)
		if err != nil {
			if key := s.AssemblyName + " " + scriptLabel(s); !failed[key] {
				failed[key] = true
				g.logger.Warn("script_layout_failed", "script", key, "error", err.Error())
			}
			continue
		}
		out[i] = layout
	}
	return out
}

// scanAll lists the indexed strings of every component in parallel. A
// component whose data does not match its layout falls back to the
// heuristic scan, and its layout is cleared.
func (g *GameIndex) scanAll(ctx context.Context, build *gamepatch.Build, comps []gamepatch.Component, layouts []*scriptlayout.Node) ([][]Entry, error) {
	entries := make([][]Entry, len(comps))
	errs := make([]error, len(comps))
	next := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range next {
				all, err := componentStrings(build, comps[i], layouts[i])
				if err != nil {
					errs[i] = err
					all, _ = componentStrings(build, comps[i], nil)
				}
				entries[i] = visible(all)
			}
		})
	}
	for i := range comps {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	type failure struct {
		count int
		first error
	}
	failures := map[string]*failure{}
	var order []string
	for i, err := range errs {
		if err == nil {
			continue
		}
		layouts[i] = nil
		key := comps[i].Script.AssemblyName + " " + scriptLabel(comps[i].Script)
		f, ok := failures[key]
		if !ok {
			f = &failure{first: fmt.Errorf("%s:%d: %w", comps[i].File, comps[i].PathID, err)}
			failures[key] = f
			order = append(order, key)
		}
		f.count++
	}
	for _, key := range order {
		f := failures[key]
		g.logger.Warn("script_decode_failed", "script", key, "components", f.count, "error", f.first.Error())
	}
	return entries, nil
}

func loadBuild(path string) (*gamepatch.Build, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat bundle: %w", err)
	}
	b, err := unityfs.Open(f, info.Size())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Load reads every serialized file into memory, so the bundle file is
	// not needed afterwards.
	build, err := gamepatch.Load(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return build, nil
}

// componentStrings returns every string of a component, including empty
// ones and numbers, each with its textkind. With a layout, strings are
// decoded and named by field path; without one, they are found by
// serialized.ScanStrings.
func componentStrings(build *gamepatch.Build, c gamepatch.Component, layout *scriptlayout.Node) ([]Entry, error) {
	data := build.Data(c)
	order := build.ByteOrder(c.File)
	h, err := serialized.ReadMonoBehaviourHeader(data, order)
	if err != nil {
		return nil, err
	}
	script := scriptLabel(c.Script)
	entry := func(path string, offset, size int, value string) Entry {
		return Entry{
			File:       c.File,
			DocID:      c.PathID,
			GameObject: c.Owner,
			Script:     script,
			Path:       path,
			Value:      value,
			Start:      offset,
			End:        offset + size,
			Raw:        value,
			lower:      strings.ToLower(value),
		}
	}
	nameSize := len(serialized.EncodeString(h.Name, order))
	out := []Entry{entry("m_Name", h.FieldsOffset-nameSize, nameSize, h.Name)}
	var raw map[string][]byte
	if layout == nil {
		for i, s := range serialized.ScanStrings(data, h.FieldsOffset, order) {
			out = append(out, entry("str["+strconv.Itoa(i)+"]", s.Offset, s.Size, s.Value))
		}
	} else {
		needed := func(path string) bool { ok, _ := textkind.NumberField(path); return ok }
		strs, fields, err := scriptlayout.DecodeWith(layout, data, h.FieldsOffset, order, needed)
		if err != nil {
			return nil, err
		}
		for _, s := range strs {
			out = append(out, entry(s.Path, s.Offset, s.Size, s.Value))
		}
		raw = fields
	}
	classify(c, out, raw, order)
	return out, nil
}

// classify sets the textkind of every entry of a component.
func classify(c gamepatch.Component, entries []Entry, raw map[string][]byte, order serialized.ByteOrder) {
	comp := textkind.Component{
		Script:  textkind.Script{Assembly: c.Script.AssemblyName, Namespace: c.Script.Namespace, Class: c.Script.ClassName},
		Strings: make([]textkind.Field, len(entries)),
	}
	for i, e := range entries {
		comp.Strings[i] = textkind.Field{Path: e.Path, Value: e.Value}
	}
	if len(raw) > 0 {
		comp.Numbers = make(map[string][]int32, len(raw))
		for path, b := range raw {
			_, array := textkind.NumberField(path)
			comp.Numbers[path] = textkind.ParseRawNumber(b, array, order)
		}
	}
	for i, k := range textkind.Classify(comp) {
		entries[i].Kind = k
	}
}

// visible drops the strings the index does not show: empty values and
// numbers, as for YAML assets.
func visible(all []Entry) []Entry {
	var out []Entry
	for _, e := range all {
		if e.Value != "" && !numberRe.MatchString(e.Value) {
			out = append(out, e)
		}
	}
	return out
}

func scriptLabel(s serialized.MonoScript) string {
	if s.Namespace == "" {
		return s.ClassName
	}
	return s.Namespace + "." + s.ClassName
}

// Stats returns index counters. Files counts serialized files of the
// bundle that have at least one string.
func (g *GameIndex) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.statsLocked()
}

func (g *GameIndex) statsLocked() Stats {
	var s Stats
	if g.build != nil {
		s.Scripts = g.build.Scripts()
	}
	files := map[string]bool{}
	for i, entries := range g.entries {
		if len(entries) > 0 {
			files[g.comps[i].File] = true
			s.Entries += len(entries)
		}
	}
	s.Files = len(files)
	return s
}

// Search returns entries matching q ordered by file, object and offset.
func (g *GameIndex) Search(q Query) (Result, error) {
	match, err := q.matcher()
	if err != nil {
		return Result{}, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sorted.search(g.gen, q, func(res *Result, q Query) {
		for _, entries := range g.entries {
			res.add(entries, match, q)
		}
	}), nil
}

// Apply replaces a string in memory and records the change in the
// journal. Like the patcher, the change reaches every component with the
// same owner and script that holds the old value at the same occurrence,
// such as copies of a prefab baked into scenes. It fails with ErrConflict
// when e does not describe a current entry.
func (g *GameIndex) Apply(e Edit) (Entry, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	i, ok := g.byKey[objectKey{e.File, e.DocID}]
	if !ok {
		return Entry{}, fmt.Errorf("%s:%d: %w", e.File, e.DocID, ErrUnknownFile)
	}
	c := g.comps[i]
	k := slices.IndexFunc(g.entries[i], func(x Entry) bool { return x.Start == e.Start && x.End == e.End })
	if k < 0 || g.entries[i][k].Raw != e.Raw {
		return Entry{}, fmt.Errorf("%s:%d@%d: %w", e.File, e.DocID, e.Start, ErrConflict)
	}
	old := g.entries[i][k]
	if e.Value == old.Value {
		return old, nil
	}
	order := g.build.ByteOrder(c.File)
	if g.layouts[i] == nil && !scannable(e.Value, order) {
		return Entry{}, ErrBadValue
	}
	p, err := g.recordLocked(i, old)
	if err != nil {
		return Entry{}, err
	}
	p.New = e.Value
	res := g.build.Apply(p, gamepatch.Options{})
	if res.Err != nil {
		// A failed patch may have changed some of the matched objects.
		return Entry{}, errors.Join(fmt.Errorf("apply edit: %w", res.Err), g.rebuildLocked(context.Background()))
	}
	if g.journal != "" {
		if err := patch.Append(g.journal, p); err != nil {
			// The build in memory must match the journal: reload it without
			// the edit that was not recorded.
			return Entry{}, errors.Join(err, g.rebuildLocked(context.Background()))
		}
	}
	var edited []Entry
	for _, t := range res.Targets {
		j := g.byKey[objectKey{t.File, t.PathID}]
		all := g.rescanLocked(j)
		g.entries[j] = visible(all)
		g.gen++
		if j == i {
			edited = all
		}
	}
	k = slices.IndexFunc(edited, func(x Entry) bool { return x.Start == e.Start })
	if k < 0 || edited[k].Value != e.Value {
		return Entry{}, fmt.Errorf("edited value does not round-trip at %s:%d@%d", e.File, e.DocID, e.Start)
	}
	g.logger.Info("field_updated", "file", e.File, "doc_id", e.DocID, "path", old.Path, "objects", len(res.Targets))
	return edited[k], nil
}

// recordLocked describes entry e of component i as a journal record with
// an empty New.
func (g *GameIndex) recordLocked(i int, e Entry) (patch.Patch, error) {
	c := g.comps[i]
	occurrence, ok := gamepatch.Occurrence(g.build.Data(c), e.Start, e.Value, g.build.ByteOrder(c.File))
	if !ok {
		return patch.Patch{}, fmt.Errorf("%s:%d@%d: indexed string not found in object data", c.File, c.PathID, e.Start)
	}
	return patch.Patch{
		File:       c.File,
		Path:       e.Path,
		Owner:      c.Owner,
		Script:     patchScriptFor(c),
		Occurrence: occurrence,
		Old:        e.Value,
	}, nil
}

// Records describes every indexed string of the given kind as the journal
// record an edit of it would produce, with an empty New. The records
// match the build with the journal applied.
func (g *GameIndex) Records(kind textkind.Kind) ([]patch.Patch, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []patch.Patch
	for i, entries := range g.entries {
		for _, e := range entries {
			if e.Kind != kind {
				continue
			}
			p, err := g.recordLocked(i, e)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// rescanLocked returns all strings of component j after a change. The
// heuristic scan replaces a layout that no longer matches the data.
func (g *GameIndex) rescanLocked(j int) []Entry {
	c := g.comps[j]
	all, err := componentStrings(g.build, c, g.layouts[j])
	if err != nil {
		g.logger.Warn("script_decode_failed", "script", c.Script.AssemblyName+" "+scriptLabel(c.Script), "components", 1,
			"error", fmt.Sprintf("%s:%d: %v", c.File, c.PathID, err))
		g.layouts[j] = nil
		all, _ = componentStrings(g.build, c, nil)
	}
	return all
}

// scannable reports whether ScanStrings finds v, so that an edited value
// stays in the index.
func scannable(v string, order serialized.ByteOrder) bool {
	found := serialized.ScanStrings(serialized.EncodeString(v, order), 0, order)
	return len(found) == 1 && found[0].Value == v
}

// patchScriptFor identifies the script of c in a journal record. An
// unresolved script matches any component of the owner.
func patchScriptFor(c gamepatch.Component) patch.Script {
	if c.FileID == 0 {
		return patch.Script{}
	}
	return patch.Script{Assembly: c.Script.AssemblyName, FileID: c.FileID}
}
