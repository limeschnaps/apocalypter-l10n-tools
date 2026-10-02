package index

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/textkind"
)

const (
	labelGUID = "11111111111111111111111111111111"
	dllGUID   = "22222222222222222222222222222222"
)

const uiPrefab = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!1 &100
GameObject:
  m_Name: Title
--- !u!114 &200
MonoBehaviour:
  m_GameObject: {fileID: 100}
  m_Enabled: 1
  m_Script: {fileID: 11500000, guid: ` + labelGUID + `, type: 3}
  m_EditorClassIdentifier: Assembly::Label
  m_text: Hello World
  m_count: 42
  m_items:
  - caption: First item
  - caption: Second item
--- !u!114 &300
MonoBehaviour:
  m_GameObject: {fileID: 0}
  m_Script: {fileID: 987654, guid: ` + dllGUID + `, type: 3}
  m_hint: hello from dll
--- !u!114 &400
MonoBehaviour:
  m_GameObject: {fileID: 0}
  m_Script: {fileID: 11500000, guid: 33333333333333333333333333333333, type: 3}
  m_note: orphan script
`

const settingsAsset = `%YAML 1.1
--- !u!114 &11400000
MonoBehaviour:
  m_Script: {fileID: 11500000, guid: ` + labelGUID + `, type: 3}
  m_Name: GameSettings
  greeting: 'Welcome, player'
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newProject(t *testing.T) (string, *Index) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Assets", "Scripts", "Label.cs.meta"), "fileFormatVersion: 2\nguid: "+labelGUID+"\n")
	writeFile(t, filepath.Join(root, "Library", "PackageCache", "pkg", "Vendor.dll.meta"), "fileFormatVersion: 2\nguid: "+dllGUID+"\n")
	writeFile(t, filepath.Join(root, "Assets", "Scripts", "NoGuid.cs.meta"), "fileFormatVersion: 2\n")
	writeFile(t, filepath.Join(root, "Assets", "UI", "Menu.prefab"), uiPrefab)
	writeFile(t, filepath.Join(root, "Packages", "local", "Settings.asset"), settingsAsset)
	writeFile(t, filepath.Join(root, "Assets", "Binary.asset"), "\x00\x01binary --- !u!114 ")
	writeFile(t, filepath.Join(root, "Assets", "Broken.prefab"), "%YAML 1.1\n--- !u!114 &1\nMonoBehaviour:\n  bad line\n")
	writeFile(t, filepath.Join(root, "Assets", "Mesh.asset"), "%YAML 1.1\n--- !u!43 &1\nMesh:\n  m_Name: m\n")
	writeFile(t, filepath.Join(root, "Assets", "Samples~", "Ignored.prefab"), uiPrefab)
	writeFile(t, filepath.Join(root, "Assets", ".hidden", "Ignored.prefab"), uiPrefab)
	writeFile(t, filepath.Join(root, "Assets", "readme.txt"), "--- !u!114 ")

	ix, err := New(root, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return root, ix
}

func search(t *testing.T, ix *Index, q Query) Result {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 100
	}
	// These tests cover the other filters; hiding service strings has
	// tests of its own.
	q.Service = true
	res, err := ix.Search(q)
	if err != nil {
		t.Fatalf("Search(%+v): %v", q, err)
	}
	return res
}

func TestNewRejectsNonUnityDir(t *testing.T) {
	if _, err := New(t.TempDir(), slog.Default()); !errors.Is(err, ErrNotUnityProject) {
		t.Errorf("err = %v", err)
	}
}

func TestRebuild(t *testing.T) {
	root, ix := newProject(t)
	if ix.Root() != root {
		t.Errorf("Root = %q", ix.Root())
	}
	stats := ix.Stats()
	if stats.Files != 2 || stats.Entries != 7 || stats.Scripts != 2 {
		t.Errorf("stats = %+v", stats)
	}

	res := search(t, ix, Query{Text: "hello world"})
	if res.Total != 1 {
		t.Fatalf("total = %d", res.Total)
	}
	e := res.Entries[0]
	want := Entry{File: "Assets/UI/Menu.prefab", Line: 12, DocID: 200, GameObject: "Title", Script: "Label", Path: "m_text", Value: "Hello World", Raw: " Hello World"}
	e.Start, e.End, e.lower = 0, 0, ""
	if e != want {
		t.Errorf("entry = %+v\nwant    %+v", e, want)
	}
}

func TestSearchFilters(t *testing.T) {
	_, ix := newProject(t)
	cases := []struct {
		name string
		q    Query
		want []string
	}{
		{"case insensitive", Query{Text: "HELLO"}, []string{"Hello World", "hello from dll"}},
		{"case sensitive", Query{Text: "hello", CaseSensitive: true}, []string{"hello from dll"}},
		{"regex", Query{Text: `^(first|second) item$`, Regex: true}, []string{"First item", "Second item"}},
		{"regex case sensitive", Query{Text: `^S`, Regex: true, CaseSensitive: true}, []string{"Second item"}},
		{"field", Query{Field: "items["}, []string{"First item", "Second item"}},
		{"game object", Query{GameObject: "TITLE"}, []string{"Hello World", "First item", "Second item"}},
		{"game object and field", Query{GameObject: "title", Field: "m_text"}, []string{"Hello World"}},
		{"script dll", Query{Script: "vendor.dll#987654"}, []string{"hello from dll"}},
		{"unknown script by guid", Query{Script: "3333"}, []string{"orphan script"}},
		{"file", Query{File: "settings"}, []string{"GameSettings", "Welcome, player"}},
		{"no match", Query{Text: "absent"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := search(t, ix, tc.q)
			var got []string
			for _, e := range res.Entries {
				got = append(got, e.Value)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") || res.Total != len(tc.want) {
				t.Errorf("got %q (total %d), want %q", got, res.Total, tc.want)
			}
		})
	}
}

func TestSearchLimitAndBadRegex(t *testing.T) {
	_, ix := newProject(t)
	res := search(t, ix, Query{Limit: 2})
	if res.Total != 7 || len(res.Entries) != 2 {
		t.Errorf("total=%d entries=%d", res.Total, len(res.Entries))
	}
	if _, err := ix.Search(Query{Text: "(", Regex: true}); !errors.Is(err, ErrBadQuery) {
		t.Errorf("err = %v", err)
	}
}

func editFor(e Entry, value string) Edit {
	return Edit{File: e.File, Start: e.Start, End: e.End, Raw: e.Raw, Value: value}
}

func TestApply(t *testing.T) {
	root, ix := newProject(t)
	path := filepath.Join(root, "Assets", "UI", "Menu.prefab")
	target := search(t, ix, Query{Text: "First item"}).Entries[0]

	updated, err := ix.Apply(editFor(target, "Первый пункт"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if updated.Value != "Первый пункт" || updated.Path != "m_items[0].caption" {
		t.Errorf("updated = %+v", updated)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "  - caption: \"" + `\` + "u041F"
	if !strings.Contains(string(data), wantLine) {
		t.Errorf("file does not contain escaped value:\n%s", data)
	}
	if strings.Replace(string(data), updated.Raw, " First item", 1) != uiPrefab {
		t.Errorf("edit touched other bytes:\n%s", data)
	}
	// Windows has no Unix permission bits to preserve.
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", info.Mode())
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "Assets", "UI", ".*"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left: %v", leftovers)
	}

	// The index reflects the new content and shifted offsets.
	if res := search(t, ix, Query{Text: "первый"}); res.Total != 1 || res.Entries[0].Start != updated.Start {
		t.Errorf("search after edit = %+v", res)
	}
	second := search(t, ix, Query{Text: "Second item"}).Entries[0]
	if second.Start == 0 || second.Start <= updated.End {
		t.Errorf("offset of following field not refreshed: %+v", second)
	}
	if _, err := ix.Apply(editFor(second, "")); err != nil {
		t.Fatalf("Apply empty: %v", err)
	}
	if res := search(t, ix, Query{Text: "Second item"}); res.Total != 0 {
		t.Errorf("empty value still indexed: %+v", res)
	}
}

func TestApplyConflicts(t *testing.T) {
	root, ix := newProject(t)
	target := search(t, ix, Query{Text: "Hello World"}).Entries[0]

	if _, err := ix.Apply(Edit{File: "Assets/Other.prefab", Raw: target.Raw}); !errors.Is(err, ErrUnknownFile) {
		t.Errorf("unknown file err = %v", err)
	}
	bogus := editFor(target, "x")
	bogus.Start--
	if _, err := ix.Apply(bogus); !errors.Is(err, ErrConflict) {
		t.Errorf("unindexed range err = %v", err)
	}

	path := filepath.Join(root, "Assets", "UI", "Menu.prefab")
	writeFile(t, path, strings.Replace(uiPrefab, "Hello World", "Hello Unity", 1))
	if _, err := ix.Apply(editFor(target, "x")); !errors.Is(err, ErrConflict) {
		t.Errorf("stale raw err = %v", err)
	}
	if res := search(t, ix, Query{Text: "Hello Unity"}); res.Total != 1 {
		t.Errorf("conflict did not reindex the file: %+v", res)
	}

	writeFile(t, path, "%YAML 1.1\n")
	if _, err := ix.Apply(editFor(target, "x")); !errors.Is(err, ErrConflict) {
		t.Errorf("truncated file err = %v", err)
	}

	writeFile(t, path, "%YAML 1.1\n--- !u!114 &1\nMonoBehaviour:\n  bad line\n")
	if _, err := ix.Apply(editFor(target, "x")); !errors.Is(err, ErrConflict) {
		t.Errorf("broken file err = %v", err)
	}
	if res := search(t, ix, Query{File: "Menu"}); res.Total != 0 {
		t.Errorf("broken file still indexed: %+v", res)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Apply(editFor(target, "x")); err == nil || errors.Is(err, ErrConflict) {
		t.Errorf("missing file err = %v", err)
	}
}

func TestRebuildCancelled(t *testing.T) {
	_, ix := newProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ix.Rebuild(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestApplyRecordsJournal(t *testing.T) {
	root, ix := newProject(t)
	journal := filepath.Join(root, "patches.json")
	ix.SetJournal(journal)

	apply := func(q Query, value string) {
		t.Helper()
		if _, err := ix.Apply(editFor(search(t, ix, q).Entries[0], value)); err != nil {
			t.Fatalf("Apply(%q): %v", value, err)
		}
	}
	apply(Query{Text: "Hello World"}, "Привет")
	apply(Query{Text: "hello from dll"}, "dll text")
	apply(Query{Text: "orphan script"}, "orphan text")
	apply(Query{Text: "Welcome, player"}, "Добро пожаловать")
	apply(Query{Text: "Second item"}, "First item")
	apply(Query{Field: "m_items[1]"}, "Third item")
	apply(Query{Text: "Third item"}, "Third item")

	got, err := patch.Load(journal)
	if err != nil {
		t.Fatal(err)
	}
	want := []patch.Patch{
		{File: "Assets/UI/Menu.prefab", Path: "m_text", Owner: "Title", Script: patch.Script{Class: "Label"}, Kind: "maybe", Old: "Hello World", New: "Привет"},
		{File: "Assets/UI/Menu.prefab", Path: "m_hint", Script: patch.Script{Assembly: "Vendor.dll", FileID: 987654}, Kind: "service", Old: "hello from dll", New: "dll text"},
		{File: "Assets/UI/Menu.prefab", Path: "m_note", Kind: "maybe", Old: "orphan script", New: "orphan text"},
		{File: "Packages/local/Settings.asset", Path: "greeting", Owner: "GameSettings", Script: patch.Script{Class: "Label"}, Kind: "maybe", Old: "Welcome, player", New: "Добро пожаловать"},
		{File: "Assets/UI/Menu.prefab", Path: "m_items[1].caption", Owner: "Title", Script: patch.Script{Class: "Label"}, Kind: "maybe", Old: "Second item", New: "First item"},
		{File: "Assets/UI/Menu.prefab", Path: "m_items[1].caption", Owner: "Title", Script: patch.Script{Class: "Label"}, Occurrence: 1, Kind: "maybe", Old: "First item", New: "Third item"},
	}
	if len(got) != len(want) {
		t.Fatalf("journal has %d patches, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("patch %d = %+v\nwant      %+v", i, got[i], want[i])
		}
	}
}

func TestApplyJournalFailureRestoresAsset(t *testing.T) {
	root, ix := newProject(t)
	ix.SetJournal(filepath.Join(root, "missing-dir", "patches.json"))
	target := search(t, ix, Query{Text: "Hello World"}).Entries[0]
	if _, err := ix.Apply(editFor(target, "changed")); err == nil {
		t.Fatal("expected journal error")
	}
	data, err := os.ReadFile(filepath.Join(root, "Assets", "UI", "Menu.prefab"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != uiPrefab {
		t.Error("asset was not restored after the journal failed")
	}
}

func TestSearchKinds(t *testing.T) {
	root, ix := newProject(t)
	// A UI.Text from UnityEngine.UI.dll, which YAML references by DLL GUID
	// and the fileID Unity derives from the class name.
	const uiGUID = "44444444444444444444444444444444"
	writeFile(t, filepath.Join(root, "Assets", "Plugins", "UnityEngine.UI.dll.meta"), "fileFormatVersion: 2\nguid: "+uiGUID+"\n")
	writeFile(t, filepath.Join(root, "Assets", "UI", "Label.prefab"), `%YAML 1.1
--- !u!114 &500
MonoBehaviour:
  m_GameObject: {fileID: 0}
  m_Script: {fileID: 708705254, guid: `+uiGUID+`, type: 3}
  m_Text: Start game
  m_FontData:
    m_Font: {fileID: 0}
`)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}

	res, err := ix.Search(Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, e := range res.Entries {
		kinds[e.Value] = e.Kind.String()
	}
	want := map[string]string{
		"Start game": "screen", "Hello World": "maybe", "First item": "maybe", "Second item": "maybe",
		"orphan script": "maybe", "Welcome, player": "maybe",
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("visible kinds = %v", kinds)
	}
	// The third-party DLL string and the ScriptableObject's m_Name.
	if res.Hidden != 2 || res.Total != len(want) {
		t.Errorf("total = %d, hidden = %d", res.Total, res.Hidden)
	}

	all, err := ix.Search(Query{Service: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != len(want)+2 || all.Hidden != 0 {
		t.Errorf("with service: total = %d, hidden = %d", all.Total, all.Hidden)
	}
	onlyScreen, err := ix.Search(Query{HideMaybe: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if onlyScreen.Total != 1 || onlyScreen.Entries[0].Value != "Start game" || onlyScreen.HiddenMaybe != len(want)-1 || onlyScreen.Hidden != 2 {
		t.Errorf("hide maybe: total = %d, hiddenMaybe = %d, hidden = %d", onlyScreen.Total, onlyScreen.HiddenMaybe, onlyScreen.Hidden)
	}
	// Other filters apply before hiding: only matching service strings count.
	if res, _ := ix.Search(Query{Text: "hello", Limit: 100}); res.Total != 1 || res.Hidden != 1 {
		t.Errorf("filtered: total = %d, hidden = %d", res.Total, res.Hidden)
	}

	// An edit keeps the kind of the field.
	target := search(t, ix, Query{Text: "Start game"}).Entries[0]
	updated, err := ix.Apply(editFor(target, "Начать игру"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != textkind.Screen {
		t.Errorf("edited kind = %v", updated.Kind)
	}
}

func TestSearchOffset(t *testing.T) {
	_, ix := newProject(t)
	all := search(t, ix, Query{})
	var pages []string
	for offset := 0; offset < all.Total; offset += 3 {
		res := search(t, ix, Query{Offset: offset, Limit: 3})
		if res.Total != all.Total || res.Offset != offset {
			t.Errorf("offset %d: total = %d, offset = %d", offset, res.Total, res.Offset)
		}
		for _, e := range res.Entries {
			pages = append(pages, e.Value)
		}
	}
	var want []string
	for _, e := range all.Entries {
		want = append(want, e.Value)
	}
	if !reflect.DeepEqual(pages, want) {
		t.Errorf("pages = %q, want %q", pages, want)
	}
	if res := search(t, ix, Query{Offset: all.Total + 5, Limit: 3}); len(res.Entries) != 0 || res.Total != all.Total {
		t.Errorf("past the end = %+v", res)
	}
}

// Records describe strings exactly as the journal would record an edit.
func TestRecordsMatchJournal(t *testing.T) {
	root, ix := newProject(t)
	const uiGUID = "44444444444444444444444444444444"
	writeFile(t, filepath.Join(root, "Assets", "Plugins", "UnityEngine.UI.dll.meta"), "fileFormatVersion: 2\nguid: "+uiGUID+"\n")
	writeFile(t, filepath.Join(root, "Assets", "UI", "Label.prefab"), `%YAML 1.1
--- !u!1 &100
GameObject:
  m_Name: Start
--- !u!114 &500
MonoBehaviour:
  m_GameObject: {fileID: 100}
  m_Script: {fileID: 708705254, guid: `+uiGUID+`, type: 3}
  m_Text: Start game
`)
	journal := filepath.Join(root, "patches.json")
	ix.SetJournal(journal)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err := ix.Records(textkind.Screen)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	if _, err := ix.Apply(editFor(search(t, ix, Query{Text: "Start game"}).Entries[0], "Начать игру")); err != nil {
		t.Fatal(err)
	}
	patches, err := patch.Load(journal)
	if err != nil || len(patches) != 1 {
		t.Fatalf("journal = %+v, %v", patches, err)
	}
	want := patches[0]
	want.New = ""
	if records[0] != want {
		t.Errorf("record = %+v, journal = %+v", records[0], want)
	}

	if err := os.Remove(filepath.Join(root, "Assets", "UI", "Label.prefab")); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Records(textkind.Screen); err == nil {
		t.Error("records of a deleted asset succeeded")
	}
}
