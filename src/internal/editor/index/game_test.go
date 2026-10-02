package index

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/textkind"
	"apocalypter-l10n-tools/internal/unityfs"
	"apocalypter-l10n-tools/internal/unitytest"
)

// serializedFlag marks bundle nodes that hold serialized files.
const serializedFlag = 4

var tmpScript = patch.Script{Assembly: "Unity.TextMeshPro.dll", FileID: gamepatch.ScriptFileID("TMPro", "TextMeshProUGUI")}

func gameBundle() []byte {
	scripts := unitytest.Serialized([]unitytest.Object{
		{PathID: 7, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("TextMeshProUGUI", "TMPro", "Unity.TextMeshPro.dll")},
		{PathID: 8, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("Settings", "", "Assembly-CSharp.dll")},
	}, nil)
	ext := []string{"library/globalgamemanagers.assets"}
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 1, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Title", 2)},
		{PathID: 2, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(1, 1, 7, "", "Hello", "OK", "OK")},
		{PathID: 12, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(0, 1, 8, "Settings", "Welcome", "42")},
	}, ext)
	shared := unitytest.Serialized([]unitytest.Object{
		{PathID: 100, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Title", 101)},
		{PathID: 101, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(100, 1, 7, "", "Hello")},
	}, ext)
	return unitytest.Bundle([]unitytest.Node{
		{Path: "globalgamemanagers.assets", Flags: serializedFlag, Data: scripts},
		{Path: "level0", Flags: serializedFlag, Data: level},
		{Path: "sharedassets1.assets", Flags: serializedFlag, Data: shared},
	}, 64)
}

// gameDir writes a *_Data directory with the test bundle.
func gameDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Game_Data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, gamepatch.BundleName), gameBundle(), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newGame(t *testing.T, path, journal string, log io.Writer) *GameIndex {
	t.Helper()
	g, err := NewGame(path, slog.New(slog.NewJSONHandler(log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	g.SetJournal(journal)
	if err := g.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	return g
}

func openBundle(t *testing.T, data []byte) *unityfs.Bundle {
	t.Helper()
	b, err := unityfs.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func find(t *testing.T, g *GameIndex, q Query) []Entry {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 100
	}
	// These tests cover the other filters; hiding service strings has
	// tests of its own.
	q.Service = true
	res, err := g.Search(q)
	if err != nil {
		t.Fatal(err)
	}
	return res.Entries
}

func editOf(e Entry, value string) Edit {
	return Edit{File: e.File, DocID: e.DocID, Start: e.Start, End: e.End, Raw: e.Raw, Value: value}
}

func TestGameIndexSearch(t *testing.T) {
	dir := gameDir(t)
	g := newGame(t, dir, "", io.Discard)
	if g.Root() != dir {
		t.Errorf("root = %s, want %s", g.Root(), dir)
	}
	if s := g.Stats(); s != (Stats{Files: 2, Entries: 6, Scripts: 2}) {
		t.Errorf("stats = %+v", s)
	}

	hello := find(t, g, Query{Text: "hello"})
	if len(hello) != 2 {
		t.Fatalf("hello = %+v", hello)
	}
	e := hello[0]
	if e.File != "level0" || e.DocID != 2 || e.GameObject != "Title" || e.Script != "TMPro.TextMeshProUGUI" || e.Path != "str[0]" || e.Line != 0 {
		t.Errorf("entry = %+v", e)
	}
	if hello[1].File != "sharedassets1.assets" {
		t.Errorf("baked copy = %+v", hello[1])
	}
	if got := find(t, g, Query{Field: "m_name"}); len(got) != 1 || got[0].Value != "Settings" || got[0].Script != "Settings" {
		t.Errorf("m_Name entries = %+v", got)
	}
	if got := find(t, g, Query{Text: "42"}); len(got) != 0 {
		t.Errorf("number indexed: %+v", got)
	}
	if got := find(t, g, Query{Script: "tmpro", File: "level"}); len(got) != 3 {
		t.Errorf("script and file filter = %+v", got)
	}
	if got := find(t, g, Query{GameObject: "title"}); len(got) == 0 || slices.ContainsFunc(got, func(e Entry) bool { return e.GameObject != "Title" }) {
		t.Errorf("game object filter = %+v", got)
	}
	if _, err := g.Search(Query{Text: "(", Regex: true}); !errors.Is(err, ErrBadQuery) {
		t.Errorf("bad regex: %v", err)
	}
}

func TestGameIndexApply(t *testing.T) {
	dir := gameDir(t)
	bundle := filepath.Join(dir, gamepatch.BundleName)
	original, _ := os.ReadFile(bundle)
	journal := filepath.Join(dir, "patches.json")
	g := newGame(t, dir, journal, io.Discard)

	hello := find(t, g, Query{Text: "Hello", File: "level0"})[0]
	updated, err := g.Apply(editOf(hello, "Привет"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value != "Привет" || updated.Start != hello.Start || updated.Path != "str[0]" {
		t.Errorf("updated = %+v", updated)
	}
	if got := find(t, g, Query{Text: "привет"}); len(got) != 2 {
		t.Errorf("baked copy not updated: %+v", got)
	}
	if data, _ := os.ReadFile(bundle); !bytes.Equal(data, original) {
		t.Error("bundle on disk changed")
	}

	// Offsets after the edited string moved; the second "OK" is found anew.
	oks := find(t, g, Query{Text: "OK", CaseSensitive: true})
	if len(oks) != 2 {
		t.Fatalf("OK entries = %+v", oks)
	}
	if _, err := g.Apply(editOf(oks[1], "Ладно")); err != nil {
		t.Fatal(err)
	}
	if same, err := g.Apply(editOf(oks[0], "OK")); err != nil || same.Value != "OK" {
		t.Errorf("unchanged edit = %+v, %v", same, err)
	}

	patches, err := patch.Load(journal)
	if err != nil {
		t.Fatal(err)
	}
	want := []patch.Patch{
		{File: "level0", Path: "str[0]", Owner: "Title", Script: tmpScript, Occurrence: 0, Kind: "maybe", Old: "Hello", New: "Привет"},
		{File: "level0", Path: "str[2]", Owner: "Title", Script: tmpScript, Occurrence: 1, Kind: "maybe", Old: "OK", New: "Ладно"},
	}
	if len(patches) != len(want) {
		t.Fatalf("journal = %+v", patches)
	}
	for i := range want {
		if patches[i] != want[i] {
			t.Errorf("journal[%d] = %+v, want %+v", i, patches[i], want[i])
		}
	}

	// The journal must replay against the original bundle.
	b := openBundle(t, original)
	if _, _, err := gamepatch.Apply(b, patches, gamepatch.Options{}); err != nil {
		t.Errorf("journal does not apply to the bundle: %v", err)
	}

	cases := map[string]struct {
		edit Edit
		want error
	}{
		"stale":        {editOf(hello, "x"), ErrConflict},
		"unknown":      {Edit{File: "level0", DocID: 99}, ErrUnknownFile},
		"wrong offset": {Edit{File: "level0", DocID: 2, Start: 3, End: 9, Raw: "Hello"}, ErrConflict},
		"number":       {editOf(updated, "1"), ErrBadValue},
		"control":      {editOf(updated, "a\x01b"), ErrBadValue},
	}
	for name, tc := range cases {
		if _, err := g.Apply(tc.edit); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

func TestGameIndexRebuildReplaysJournal(t *testing.T) {
	dir := gameDir(t)
	bundle := filepath.Join(dir, gamepatch.BundleName)
	journal := filepath.Join(dir, "patches.json")
	for _, p := range []patch.Patch{
		{Owner: "Title", Script: tmpScript, Old: "Hello", New: "Привет"},
		{Owner: "Nobody", Old: "x", New: "y"},
	} {
		if err := patch.Append(journal, p); err != nil {
			t.Fatal(err)
		}
	}
	// A patched game keeps the original next to the bundle; the index
	// must start from it. WriteTo produces the patched copy, which keeps
	// the original blocks.
	original, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(bundle, bundle+gamepatch.BackupSuffix); err != nil {
		t.Fatal(err)
	}
	var patched bytes.Buffer
	if _, err := openBundle(t, original).WriteTo(&patched, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, patched.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	g := newGame(t, bundle, journal, &log)
	if got := find(t, g, Query{Text: "привет"}); len(got) != 2 {
		t.Errorf("journal not replayed: %+v", got)
	}
	if !strings.Contains(log.String(), "journal_patch_failed") || !strings.Contains(log.String(), `"journal_failed":1`) {
		t.Errorf("log = %s", log.String())
	}
}

func TestGameIndexRejectsStaleBackup(t *testing.T) {
	dir := gameDir(t)
	bundle := filepath.Join(dir, gamepatch.BundleName)
	// A backup from an older game version: the current bundle was not
	// produced from it.
	older := unitytest.Bundle([]unitytest.Node{{Path: "level0", Flags: 4, Data: unitytest.Serialized(nil, nil)}}, 64)
	if err := os.WriteFile(bundle+gamepatch.BackupSuffix, older, 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := NewGame(dir, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Rebuild(context.Background()); !errors.Is(err, gamepatch.ErrStaleBackup) {
		t.Errorf("err = %v", err)
	}
}

func TestGameIndexJournalFailure(t *testing.T) {
	dir := gameDir(t)
	g := newGame(t, dir, filepath.Join(dir, "missing", "patches.json"), io.Discard)
	hello := find(t, g, Query{Text: "Hello", File: "level0"})[0]
	if _, err := g.Apply(editOf(hello, "Привет")); err == nil {
		t.Fatal("edit without a writable journal succeeded")
	}
	if got := find(t, g, Query{Text: "привет"}); len(got) != 0 {
		t.Errorf("unrecorded edit stayed in memory: %+v", got)
	}
	if got := find(t, g, Query{Text: "hello"}); len(got) != 2 {
		t.Errorf("index not restored: %+v", got)
	}
}

func TestGameIndexErrors(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if _, err := NewGame(filepath.Join(t.TempDir(), "missing"), logger); err == nil {
		t.Error("missing path accepted")
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, gamepatch.BundleName)
	if err := os.WriteFile(bundle, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := NewGame(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Rebuild(context.Background()); err == nil {
		t.Error("broken bundle loaded")
	}
	if _, err := g.Apply(Edit{File: "level0", DocID: 2}); !errors.Is(err, ErrUnknownFile) {
		t.Errorf("apply before rebuild: %v", err)
	}

	good := gameDir(t)
	journal := filepath.Join(good, "patches.json")
	if err := os.WriteFile(journal, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err = NewGame(good, logger)
	if err != nil {
		t.Fatal(err)
	}
	g.SetJournal(journal)
	if err := g.Rebuild(context.Background()); err == nil {
		t.Error("malformed journal accepted")
	}
	g.SetJournal("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Rebuild(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled rebuild: %v", err)
	}
}

// scriptGameDir writes a *_Data directory whose Managed directory holds
// the unitytest script assemblies. level0 has a Game.Dialog on "Menu", a
// Game.Dialog whose data does not match the layout and a Game.Broken;
// sharedassets1.assets has a baked copy of the first Dialog.
func scriptGameDir(t *testing.T) string {
	t.Helper()
	scripts := unitytest.Serialized([]unitytest.Object{
		{PathID: 7, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("Dialog", "Game", "Assembly-CSharp.dll")},
		{PathID: 9, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("Broken", "Game", "Assembly-CSharp.dll")},
	}, nil)
	ext := []string{"library/globalgamemanagers.assets"}
	dialog := func(gameObject int64) []byte {
		return append(unitytest.MonoBehaviour(gameObject, 1, 7, ""), unitytest.DialogFields("Hello")...)
	}
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 1, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Menu", 2)},
		{PathID: 2, ClassID: serialized.ClassMonoBehaviour, Data: dialog(1)},
		{PathID: 3, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(0, 1, 7, "Mismatch", "Lost")},
		{PathID: 4, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(0, 1, 9, "Broken", "Words")},
	}, ext)
	shared := unitytest.Serialized([]unitytest.Object{
		{PathID: 100, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Menu", 101)},
		{PathID: 101, ClassID: serialized.ClassMonoBehaviour, Data: dialog(100)},
	}, ext)
	bundle := unitytest.Bundle([]unitytest.Node{
		{Path: "globalgamemanagers.assets", Flags: serializedFlag, Data: scripts},
		{Path: "level0", Flags: serializedFlag, Data: level},
		{Path: "sharedassets1.assets", Flags: serializedFlag, Data: shared},
	}, 64)

	dir := filepath.Join(t.TempDir(), "Game_Data")
	if err := os.MkdirAll(filepath.Join(dir, gamepatch.ManagedDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, gamepatch.BundleName), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range unitytest.ScriptAssemblies() {
		if err := os.WriteFile(filepath.Join(dir, gamepatch.ManagedDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGameIndexScriptLayouts(t *testing.T) {
	dir := scriptGameDir(t)
	journal := filepath.Join(dir, "patches.json")
	var log bytes.Buffer
	g := newGame(t, dir, journal, &log)
	for _, want := range []string{`"decoded":2`, `"heuristic":2`, `"msg":"script_decode_failed"`, `"msg":"script_layout_failed"`} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %s: %s", want, log.String())
		}
	}

	hello := find(t, g, Query{Text: "Hello"})
	if len(hello) != 2 || hello[0].Path != "text" || hello[0].Script != "Game.Dialog" || hello[0].GameObject != "Menu" {
		t.Fatalf("hello = %+v", hello)
	}
	if got := find(t, g, Query{Field: "items[1]", File: "level0"}); len(got) != 1 || got[0].Value != "Bow" || got[0].Path != "items[1].title" {
		t.Errorf("items[1] = %+v", got)
	}
	if got := find(t, g, Query{Text: "Lost"}); len(got) != 1 || got[0].Path != "str[0]" {
		t.Errorf("mismatched component = %+v", got)
	}
	words := find(t, g, Query{Text: "Words"})
	if len(words) != 1 || words[0].Path != "str[0]" {
		t.Fatalf("broken script = %+v", words)
	}

	// A decoded field accepts any value, even one the heuristic scan would
	// not find again.
	updated, err := g.Apply(editOf(hello[0], ""))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value != "" || updated.Path != "text" {
		t.Errorf("updated = %+v", updated)
	}
	if got := find(t, g, Query{Text: "Hello"}); len(got) != 0 {
		t.Errorf("baked copy not updated: %+v", got)
	}
	if got := find(t, g, Query{Field: "pair.b"}); len(got) != 2 {
		t.Errorf("fields after the edit moved out of the index: %+v", got)
	}
	if _, err := g.Apply(editOf(words[0], "1")); !errors.Is(err, ErrBadValue) {
		t.Errorf("heuristic value: %v", err)
	}

	patches, err := patch.Load(journal)
	if err != nil || len(patches) != 1 {
		t.Fatalf("journal = %+v, %v", patches, err)
	}
	want := patch.Patch{
		File: "level0", Path: "text", Owner: "Menu",
		Script: patch.Script{Assembly: "Assembly-CSharp.dll", FileID: gamepatch.ScriptFileID("Game", "Dialog")},
		Kind:   "maybe", Old: "Hello", New: "",
	}
	if patches[0] != want {
		t.Errorf("journal = %+v, want %+v", patches[0], want)
	}

	// Rebuilding replays the journal with the layouts in place.
	if err := g.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := find(t, g, Query{Text: "Hello"}); len(got) != 0 {
		t.Errorf("journal not replayed: %+v", got)
	}
	if got := find(t, g, Query{Field: "node.children[0].name"}); len(got) != 2 {
		t.Errorf("nested fields = %+v", got)
	}
}

func TestGameIndexSkipsForeignAssemblies(t *testing.T) {
	dir := scriptGameDir(t)
	if err := os.WriteFile(filepath.Join(dir, gamepatch.ManagedDir, "native.dll"), []byte("not managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	g := newGame(t, dir, "", &log)
	if !strings.Contains(log.String(), "script_assembly_skipped") {
		t.Errorf("log = %s", log.String())
	}
	if got := find(t, g, Query{Text: "Hello"}); len(got) != 2 || got[0].Path != "text" {
		t.Errorf("hello = %+v", got)
	}
}

func TestGameIndexKinds(t *testing.T) {
	g := newGame(t, scriptGameDir(t), "", io.Discard)
	res, err := g.Search(Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		// Game.Dialog is game code; decoded and heuristic strings alike.
		if e.Kind != textkind.Maybe {
			t.Errorf("%s %s = %v", e.Script, e.Path, e.Kind)
		}
	}
	// m_Name of the "Mismatch" and "Broken" components.
	if res.Hidden != 2 {
		t.Errorf("hidden = %d", res.Hidden)
	}
	all, _ := g.Search(Query{Service: true, Limit: 100})
	if all.Total != res.Total+2 {
		t.Errorf("with service: %d, without: %d", all.Total, res.Total)
	}
}

// Records feed the dictionary: translating every string must yield
// patches that replay without a miss, including the same string in two
// files and two equal strings inside one component.
func TestGameIndexRecordsReplayAsDictionary(t *testing.T) {
	dir := gameDir(t)
	g := newGame(t, dir, "", io.Discard)
	records, err := g.Records(textkind.Maybe)
	if err != nil {
		t.Fatal(err)
	}
	want := []patch.Patch{
		{File: "level0", Path: "str[0]", Owner: "Title", Script: tmpScript, Kind: "maybe", Old: "Hello"},
		{File: "level0", Path: "str[1]", Owner: "Title", Script: tmpScript, Kind: "maybe", Old: "OK"},
		{File: "level0", Path: "str[2]", Owner: "Title", Script: tmpScript, Occurrence: 1, Kind: "maybe", Old: "OK"},
		{File: "level0", Path: "str[0]", Owner: "Settings", Script: patch.Script{Assembly: "Assembly-CSharp.dll", FileID: gamepatch.ScriptFileID("", "Settings")}, Kind: "maybe", Old: "Welcome"},
		{File: "sharedassets1.assets", Path: "str[0]", Owner: "Title", Script: tmpScript, Kind: "maybe", Old: "Hello"},
	}
	if !slices.Equal(records, want) {
		t.Fatalf("records = %+v\nwant %+v", records, want)
	}

	entries := dictionary.Build(records)
	for i := range entries {
		entries[i].New = "ru " + entries[i].Old
	}
	patches, err := dictionary.Patches(entries)
	if err != nil {
		t.Fatal(err)
	}
	data, err := patch.Marshal(patches)
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(dir, "patches.json")
	if err := os.WriteFile(journal, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	g = newGame(t, dir, journal, &log)
	if strings.Contains(log.String(), "journal_patch_failed") {
		t.Errorf("dictionary patches failed:\n%s", log.String())
	}
	for _, e := range find(t, g, Query{}) {
		if e.Path != "m_Name" && !strings.HasPrefix(e.Value, "ru ") {
			t.Errorf("untranslated %s %s %s = %q", e.File, e.GameObject, e.Path, e.Value)
		}
	}
}
