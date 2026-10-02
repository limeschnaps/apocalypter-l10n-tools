package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/locpack"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unityfs"
	"apocalypter-l10n-tools/internal/unitytest"
)

func gameDir(t *testing.T) string {
	t.Helper()
	scripts := unitytest.Serialized([]unitytest.Object{
		{PathID: 7, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("Menu", "", "Assembly-CSharp.dll")},
	}, nil)
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 1, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Title", 2)},
		{PathID: 2, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(1, 1, 7, "", "NEW GAME", "LOAD GAME")},
		{PathID: 3, ClassID: serialized.ClassFont, Data: unitytest.Font("Helveticrap", 16, latinTTF(), nil)},
		{PathID: 4, ClassID: serialized.ClassAudioClip, Data: unitytest.AudioClip("enemy_human_single_1", "sharedassets1.resource", 0, 64)},
	}, []string{"globalgamemanagers.assets"})
	bundle := unitytest.Bundle([]unitytest.Node{
		{Path: "globalgamemanagers.assets", Flags: 4, Data: scripts},
		{Path: "level0", Flags: 4, Data: level},
	}, 128)
	dir := filepath.Join(t.TempDir(), "Game_Data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bundleName), bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func journal(t *testing.T, patches ...patch.Patch) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "patches.json")
	for _, p := range patches {
		if err := patch.Append(path, p); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func levelStrings(t *testing.T, bundlePath string) string {
	t.Helper()
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := unityfs.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := b.Node("level0")
	level, err := b.ReadNode(n)
	if err != nil {
		t.Fatal(err)
	}
	f, err := serialized.Parse(level)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := f.Object(2)
	return string(f.Data(o))
}

func latinTTF() []byte {
	return unitytest.TTF(unitytest.TTFSpec{Family: "Latin", UnitsPerEm: 1000, Ascender: 800, Descender: -200, Chars: map[rune]uint16{'A': 1}})
}

func cyrillicTTF(t *testing.T) string {
	t.Helper()
	chars := map[rune]uint16{}
	for i, r := range gamepatch.RussianAlphabet {
		chars[r] = uint16(i + 1)
	}
	path := filepath.Join(t.TempDir(), "cyr.ttf")
	// A 1.25 em line, taller than the 1 em of the fixture Font.
	data := unitytest.TTF(unitytest.TTFSpec{Family: "Cyr Sans", UnitsPerEm: 1000, Ascender: 950, Descender: -300, Chars: chars})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func listFonts(t *testing.T, path string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run([]string{"-list-fonts", path}, &out, io.Discard); err != nil {
		t.Fatalf("list fonts: %v", err)
	}
	return out.String()
}

var newGame = patch.Patch{Owner: "Title", Script: patch.Script{Class: "Menu"}, Old: "NEW GAME", New: "НОВАЯ ИГРА"}

func TestRunOut(t *testing.T) {
	dir := gameDir(t)
	out := filepath.Join(t.TempDir(), "patched.unity3d")
	if err := run([]string{"-patches", journal(t, newGame), "-out", out, dir}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if s := levelStrings(t, out); !strings.Contains(s, "НОВАЯ ИГРА") || !strings.Contains(s, "LOAD GAME") {
		t.Errorf("patched object = %q", s)
	}
	// Windows has no Unix permission bits to preserve.
	if info, _ := os.Stat(out); runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", info.Mode())
	}
	if s := levelStrings(t, filepath.Join(dir, bundleName)); !strings.Contains(s, "NEW GAME") {
		t.Error("source bundle was modified")
	}
}

func TestRunInPlace(t *testing.T) {
	dir := gameDir(t)
	target := filepath.Join(dir, bundleName)
	original, _ := os.ReadFile(target)

	if err := run([]string{"-patches", journal(t, newGame), "-in-place", dir}, io.Discard, io.Discard); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if backup, _ := os.ReadFile(target + backupSuffix); !bytes.Equal(backup, original) {
		t.Fatal("backup does not hold the original bundle")
	}

	// A second run with a longer journal starts from the backup again.
	load := patch.Patch{Owner: "Title", Old: "LOAD GAME", New: "ЗАГРУЗИТЬ"}
	if err := run([]string{"-patches", journal(t, newGame, load), "-in-place", target}, io.Discard, io.Discard); err != nil {
		t.Fatalf("second run: %v", err)
	}
	s := levelStrings(t, target)
	if !strings.Contains(s, "НОВАЯ ИГРА") || !strings.Contains(s, "ЗАГРУЗИТЬ") {
		t.Errorf("patched object = %q", s)
	}
	if backup, _ := os.ReadFile(target + backupSuffix); !bytes.Equal(backup, original) {
		t.Error("backup changed on the second run")
	}
}

func TestRunDryRunAndFailures(t *testing.T) {
	dir := gameDir(t)
	target := filepath.Join(dir, bundleName)
	before, _ := os.ReadFile(target)
	var log bytes.Buffer
	if err := run([]string{"-patches", journal(t, newGame), "-dry-run", dir}, io.Discard, &log); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(log.String(), `"objects":["level0:2"]`) {
		t.Errorf("log does not list targets: %s", log.String())
	}

	missing := patch.Patch{Owner: "Nobody", Old: "x", New: "y"}
	empty := filepath.Join(t.TempDir(), "empty.json")
	notGame := t.TempDir()
	broken := filepath.Join(t.TempDir(), bundleName)
	if err := os.WriteFile(broken, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"no args":         {},
		"no patches flag": {dir},
		"bad flag":        {"-nope"},
		"out and inplace": {"-patches", journal(t, newGame), "-out", "x", "-in-place", dir},
		"no destination":  {"-patches", journal(t, newGame), dir},
		"missing path":    {"-patches", journal(t, newGame), "-dry-run", filepath.Join(dir, "nope")},
		"no bundle":       {"-patches", journal(t, newGame), "-dry-run", notGame},
		"empty journal":   {"-patches", empty, "-dry-run", dir},
		"broken bundle":   {"-patches", journal(t, newGame), "-dry-run", broken},
		"failing patch":   {"-patches", journal(t, newGame, missing), "-in-place", dir},
		"out dir missing": {"-patches", journal(t, newGame), "-out", filepath.Join(dir, "no", "out"), dir},
	}
	for name, args := range cases {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if after, _ := os.ReadFile(target); !bytes.Equal(after, before) {
		t.Error("failed runs modified the bundle")
	}
	if _, err := os.Stat(target + backupSuffix); err == nil {
		t.Error("failed run created a backup")
	}
}

func TestRunFonts(t *testing.T) {
	dir := gameDir(t)
	if list := listFonts(t, dir); !strings.Contains(list, "Helveticrap") || !strings.Contains(list, "missing 66/66") {
		t.Errorf("list = %s", list)
	}

	out := filepath.Join(t.TempDir(), "patched.unity3d")
	var log bytes.Buffer
	if err := run([]string{"-font", "Helveticrap=" + cyrillicTTF(t), "-out", out, dir}, io.Discard, &log); err != nil {
		t.Fatalf("run: %v", err)
	}
	if list := listFonts(t, out); !strings.Contains(list, "Cyr Sans") || !strings.Contains(list, " ok") {
		t.Errorf("patched list = %s", list)
	}
	if !strings.Contains(log.String(), `"msg":"font_replaced"`) || strings.Contains(log.String(), "font_missing_characters") {
		t.Errorf("log = %s", log.String())
	}

	// Text patches and fonts in one run; a Latin-only font triggers a
	// coverage warning.
	latin := filepath.Join(t.TempDir(), "latin.ttf")
	if err := os.WriteFile(latin, latinTTF(), 0o644); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := run([]string{"-patches", journal(t, newGame), "-font", "Helveticrap=" + latin, "-dry-run", dir}, io.Discard, &log); err != nil {
		t.Fatalf("combined run: %v", err)
	}
	if !strings.Contains(log.String(), "font_missing_characters") {
		t.Errorf("no coverage warning: %s", log.String())
	}

	failures := map[string][]string{
		"no equals":    {"-font", "Helveticrap", "-dry-run", dir},
		"missing file": {"-font", "Helveticrap=/nonexistent.ttf", "-dry-run", dir},
		"unknown font": {"-font", "Nope=" + cyrillicTTF(t), "-dry-run", dir},
		"bad ttf":      {"-font", "Helveticrap=" + journal(t, newGame), "-dry-run", dir},
		"nothing":      {"-dry-run", dir},
	}
	for name, args := range failures {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := run([]string{"-list-fonts", filepath.Join(dir, "nope")}, io.Discard, io.Discard); err == nil {
		t.Error("list-fonts on a missing path: expected error")
	}
}

func TestRunPackage(t *testing.T) {
	dir := gameDir(t)
	loc := t.TempDir()
	journalPath := journal(t, newGame)
	font := cyrillicTTF(t)
	if err := os.MkdirAll(filepath.Join(loc, "fonts"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(font)
	if err := os.WriteFile(filepath.Join(loc, "fonts", "cyr.ttf"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	fontsJSON := filepath.Join(loc, "fonts.json")
	if err := os.WriteFile(fontsJSON, []byte(`{"version":1,"fonts":[{"name":"Helveticrap","file":"fonts/cyr.ttf"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(t.TempDir(), "localization.zip")
	var log bytes.Buffer
	if err := run([]string{"pack", "-o", zipPath, "-patches", journalPath, "-fonts", fontsJSON}, io.Discard, &log); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(log.String(), `"msg":"package_written"`) || !strings.Contains(log.String(), `"patches":1`) {
		t.Errorf("pack log = %s", log.String())
	}

	if err := run([]string{"-package", zipPath, "-in-place", dir}, io.Discard, io.Discard); err != nil {
		t.Fatalf("apply package: %v", err)
	}
	target := filepath.Join(dir, bundleName)
	if s := levelStrings(t, target); !strings.Contains(s, "НОВАЯ ИГРА") {
		t.Errorf("text not patched: %q", s)
	}
	if list := listFonts(t, target); !strings.Contains(list, "Cyr Sans") {
		t.Errorf("font not replaced: %s", list)
	}

	latin := filepath.Join(loc, "fonts", "latin.ttf")
	if err := os.WriteFile(latin, latinTTF(), 0o644); err != nil {
		t.Fatal(err)
	}
	latinJSON := filepath.Join(loc, "latin.json")
	if err := os.WriteFile(latinJSON, []byte(`{"version":1,"fonts":[{"name":"Helveticrap","file":"fonts/latin.ttf"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := run([]string{"pack", "-o", filepath.Join(t.TempDir(), "l.zip"), "-fonts", latinJSON}, io.Discard, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "font_missing_characters") {
		t.Errorf("no coverage warning: %s", log.String())
	}

	failures := map[string][]string{
		"package and patches": {"-package", zipPath, "-patches", journalPath, "-dry-run", dir},
		"package and font":    {"-package", zipPath, "-font", "Helveticrap=" + font, "-dry-run", dir},
		"bad package":         {"-package", journalPath, "-dry-run", dir},
		"pack without -o":     {"pack", "-patches", journalPath},
		"pack extra args":     {"pack", "-o", zipPath, "extra"},
		"pack bad flag":       {"pack", "-nope"},
		"pack nothing":        {"pack", "-o", filepath.Join(t.TempDir(), "x.zip")},
		"pack bad dest":       {"pack", "-o", filepath.Join(t.TempDir(), "no", "x.zip"), "-patches", journalPath},
	}
	for name, args := range failures {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRunPackSourceDir(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "l10n", "ru")
	if err := os.MkdirAll(filepath.Join(src, "fonts"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(journal(t, newGame))
	if err := os.WriteFile(filepath.Join(src, "patches.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	font, _ := os.ReadFile(cyrillicTTF(t))
	if err := os.WriteFile(filepath.Join(src, "fonts", "cyr.ttf"), font, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "fonts.json"), []byte(`{"version":1,"fonts":[{"name":"Helveticrap","file":"fonts/cyr.ttf"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The default output lands in the working directory, named after the
	// source directory; a trailing slash does not change the name.
	t.Chdir(root)
	if err := run([]string{"pack", "-s", "./l10n/ru/"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("pack -s: %v", err)
	}
	pkg, err := locpack.Read(filepath.Join(root, "ru.lang"))
	if err != nil {
		t.Fatalf("read ru.lang: %v", err)
	}
	if len(pkg.Patches) != 1 || len(pkg.Fonts) != 1 {
		t.Errorf("package = %d patches, %d fonts", len(pkg.Patches), len(pkg.Fonts))
	}

	custom := filepath.Join(root, "custom.lang")
	if err := run([]string{"pack", "-s", src, "-o", custom}, io.Discard, io.Discard); err != nil {
		t.Fatalf("pack -s -o: %v", err)
	}
	a, _ := os.ReadFile(filepath.Join(root, "ru.lang"))
	b, _ := os.ReadFile(custom)
	if !bytes.Equal(a, b) {
		t.Error("-o changed the package content")
	}

	// A directory with only fonts.json is a valid source.
	if err := os.Remove(filepath.Join(src, "patches.json")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pack", "-s", src, "-o", filepath.Join(root, "fonts-only.lang")}, io.Discard, io.Discard); err != nil {
		t.Fatalf("fonts-only source: %v", err)
	}

	emptyDir := t.TempDir()
	notDir := filepath.Join(root, "ru.lang")
	failures := map[string][]string{
		"with -patches": {"pack", "-s", src, "-patches", "x.json"},
		"with -fonts":   {"pack", "-s", src, "-fonts", "x.json"},
		"missing dir":   {"pack", "-s", filepath.Join(root, "nope")},
		"not a dir":     {"pack", "-s", notDir},
		"empty dir":     {"pack", "-s", emptyDir},
		"no -o, no -s":  {"pack", "-patches", "x.json"},
	}
	for name, args := range failures {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRunFontMetrics(t *testing.T) {
	dir := gameDir(t)
	font := cyrillicTTF(t)
	if list := listFonts(t, dir); !strings.Contains(list, "LINE_EM") || !strings.Contains(list, "1.000") {
		t.Errorf("list = %s", list)
	}

	for metrics, want := range map[string]string{"original": "1.000", "font": "1.250"} {
		out := filepath.Join(t.TempDir(), "patched.unity3d")
		var log bytes.Buffer
		if err := run([]string{"-font", "Helveticrap=" + font, "-font-metrics", metrics, "-out", out, dir}, io.Discard, &log); err != nil {
			t.Fatalf("%s: %v", metrics, err)
		}
		if list := listFonts(t, out); !strings.Contains(list, want) {
			t.Errorf("%s: list = %s", metrics, list)
		}
		grows := strings.Contains(log.String(), "font_line_height_grows")
		if grows != (metrics == "font") || !strings.Contains(log.String(), `"font_line_em":1.25`) {
			t.Errorf("%s: log = %s", metrics, log.String())
		}
	}
	if err := run([]string{"-font", "Helveticrap=" + font, "-font-metrics", "tall", "-dry-run", dir}, io.Discard, io.Discard); err == nil {
		t.Error("invalid -font-metrics: expected error")
	}
}

func TestRunRejectsStaleBackup(t *testing.T) {
	dir := gameDir(t)
	target := filepath.Join(dir, bundleName)
	if err := run([]string{"-patches", journal(t, newGame), "-in-place", dir}, io.Discard, io.Discard); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// A game update writes a new, unpatched bundle next to the old backup.
	updated := unitytest.Bundle([]unitytest.Node{{Path: "level0", Flags: 4, Data: unitytest.Serialized(nil, nil)}}, 64)
	if err := os.WriteFile(target, updated, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"-in-place", "-dry-run"} {
		err := run([]string{"-patches", journal(t, newGame), mode, dir}, io.Discard, io.Discard)
		if !errors.Is(err, gamepatch.ErrStaleBackup) {
			t.Errorf("%s: err = %v", mode, err)
		}
	}
	if data, _ := os.ReadFile(target); !bytes.Equal(data, updated) {
		t.Error("the updated bundle was modified")
	}
	// Listing still shows the current bundle.
	if err := run([]string{"-list-fonts", dir}, io.Discard, io.Discard); err != nil {
		t.Errorf("list-fonts: %v", err)
	}
}

func TestRunPackDictionary(t *testing.T) {
	src := t.TempDir()
	data, _ := os.ReadFile(journal(t, newGame))
	if err := os.WriteFile(filepath.Join(src, locpack.PatchesName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	menu := patch.Script{Class: "Menu"}
	entries := []dictionary.Entry{
		{Old: "LOAD GAME", FoundIn: []dictionary.Location{{File: "level0", Path: "str[1]", Owner: "Title", Script: menu}}},
		{Old: "QUIT", FoundIn: []dictionary.Location{{File: "level0", Path: "str[2]", Owner: "Title", Script: menu}}},
	}
	mapData, poData, _, err := dictionary.Encode(entries, nil, dictionary.Header{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	poData = []byte(strings.Replace(string(poData), "msgid \"LOAD GAME\"\nmsgstr \"\"", "msgid \"LOAD GAME\"\nmsgstr \"ЗАГРУЗИТЬ\"", 1))
	if err := dictionary.Save(src, mapData, poData); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(t.TempDir(), "ru.lang")
	var log bytes.Buffer
	if err := run([]string{"pack", "-s", src, "-o", pkgPath}, io.Discard, &log); err != nil {
		t.Fatalf("pack: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), `"patches":1,"dictionary_patches":1`) {
		t.Errorf("log = %s", log.String())
	}

	// The journal and the dictionary both reach the game.
	game := gameDir(t)
	out := filepath.Join(t.TempDir(), "data.unity3d")
	if err := run([]string{"-package", pkgPath, "-out", out, game}, io.Discard, io.Discard); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if s := levelStrings(t, out); !strings.Contains(s, "НОВАЯ ИГРА") || !strings.Contains(s, "ЗАГРУЗИТЬ") {
		t.Errorf("patched strings = %q", s)
	}

	// A dictionary alone is a valid source; fuzzy messages are skipped.
	mapPath, poPath := filepath.Join(src, dictionary.MapName), filepath.Join(src, dictionary.POName)
	fuzzyPO := filepath.Join(t.TempDir(), dictionary.POName)
	if err := os.WriteFile(fuzzyPO, []byte(strings.Replace(string(poData), "msgid \"LOAD GAME\"", "#, fuzzy\nmsgid \"LOAD GAME\"", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	dictOnly := filepath.Join(t.TempDir(), "d.lang")
	if err := run([]string{"pack", "-o", dictOnly, "-map", mapPath, "-po", poPath}, io.Discard, io.Discard); err != nil {
		t.Fatalf("dictionary only: %v", err)
	}
	if pkg, err := locpack.Read(dictOnly); err != nil || len(pkg.Patches) != 1 {
		t.Errorf("dictionary-only package = %+v, %v", pkg, err)
	}
	log.Reset()
	if err := run([]string{"pack", "-o", dictOnly, "-patches", filepath.Join(src, locpack.PatchesName), "-map", mapPath, "-po", fuzzyPO}, io.Discard, &log); err != nil {
		t.Fatalf("fuzzy: %v", err)
	}
	if !strings.Contains(log.String(), "dictionary_fuzzy_skipped") || !strings.Contains(log.String(), `"dictionary_patches":0`) {
		t.Errorf("fuzzy log = %s", log.String())
	}

	failures := map[string][]string{
		"with -map":      {"pack", "-s", src, "-map", mapPath},
		"map without po": {"pack", "-o", dictOnly, "-map", mapPath},
		"stale po":       {"pack", "-o", dictOnly, "-map", filepath.Join(t.TempDir(), "none.map"), "-po", poPath},
	}
	for name, args := range failures {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRunLoadsScriptAssemblies(t *testing.T) {
	dir := gameDir(t)
	managed := filepath.Join(dir, gamepatch.ManagedDir)
	if err := os.Mkdir(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	files := unitytest.ScriptAssemblies()
	files["native.dll"] = []byte("not managed")
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(managed, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Menu is missing from the assemblies, so its patch falls back to the
	// occurrence.
	path := journal(t, patch.Patch{Owner: "Title", Path: "title", Old: "NEW GAME", New: "НОВАЯ ИГРА"})
	var log bytes.Buffer
	if err := run([]string{"-patches", path, "-dry-run", dir}, io.Discard, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	if strings.Contains(log.String(), "script_assemblies_unavailable") {
		t.Errorf("assemblies not loaded:\n%s", log.String())
	}

	if err := os.RemoveAll(managed); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := run([]string{"-patches", path, "-dry-run", dir}, io.Discard, &log); err != nil {
		t.Fatalf("run without assemblies: %v", err)
	}
	if !strings.Contains(log.String(), "script_assemblies_unavailable") {
		t.Errorf("missing warning:\n%s", log.String())
	}
}

func TestRunPackageSounds(t *testing.T) {
	src := filepath.Join(t.TempDir(), "ru")
	if err := os.MkdirAll(filepath.Join(src, "sounds"), 0o755); err != nil {
		t.Fatal(err)
	}
	ogg := unitytest.OggVorbis(unitytest.OggSpec{Channels: 1, Rate: 44100, Samples: 22050, Setup: []byte("s"), Packets: [][]byte{{0, 1}}})
	if err := os.WriteFile(filepath.Join(src, "sounds", "shot.ogg"), ogg, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sounds.json"), []byte(`{"version":1,"sounds":[{"name":"enemy_human_single_1","file":"sounds/shot.ogg"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(t.TempDir(), "ru.lang")
	var packLog bytes.Buffer
	if err := run([]string{"pack", "-s", src, "-o", pkgPath}, io.Discard, &packLog); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(packLog.String(), `"msg":"sound_packed"`) {
		t.Errorf("pack log = %s", packLog.String())
	}

	game := gameDir(t)
	out := filepath.Join(t.TempDir(), "patched.unity3d")
	var log bytes.Buffer
	if err := run([]string{"-package", pkgPath, "-out", out, game}, io.Discard, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), `"msg":"sound_replaced"`) {
		t.Errorf("log = %s", log.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := unityfs.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Node(gamepatch.SoundsNode); !ok {
		t.Errorf("no %s node", gamepatch.SoundsNode)
	}
	n, _ := b.Node("level0")
	level, _ := b.ReadNode(n)
	f, err := serialized.Parse(level)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := f.Object(4)
	clip, err := serialized.ReadAudioClip(f.Data(o), f.ByteOrder())
	if err != nil {
		t.Fatal(err)
	}
	if clip.Resource.Source != gamepatch.SoundsNode || clip.Frequency != 44100 || clip.Length != 0.5 {
		t.Errorf("clip = %+v", clip)
	}
}
