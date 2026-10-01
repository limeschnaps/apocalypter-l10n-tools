package locpack

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/po"
	"apocalypter-l10n-tools/internal/unitytest"
)

func cyrillicTTF(family string) []byte {
	chars := map[rune]uint16{'A': 1}
	for i, r := range gamepatch.RussianAlphabet {
		chars[r] = uint16(i + 2)
	}
	return unitytest.TTF(unitytest.TTFSpec{Family: family, UnitsPerEm: 1000, Ascender: 800, Descender: -200, Chars: chars})
}

func write(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// sources creates a localization directory with a journal, fonts.json
// and fonts; it returns the journal and fonts.json paths.
func sources(t *testing.T, fontsJSON string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	journal := filepath.Join(dir, "patches.json")
	if err := patch.Append(journal, patch.Patch{Owner: "Title", Old: "NEW GAME", New: "НОВАЯ ИГРА"}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "fonts", "Cyr.ttf"), cyrillicTTF("Cyr"))
	write(t, filepath.Join(dir, "other", "Latin.ttf"), unitytest.TTF(unitytest.TTFSpec{Family: "Latin", UnitsPerEm: 1000, Chars: map[rune]uint16{'A': 1}}))
	return journal, write(t, filepath.Join(dir, "fonts.json"), []byte(fontsJSON))
}

func pack(t *testing.T, patches, fonts string) (string, PackResult) {
	t.Helper()
	var buf bytes.Buffer
	res, err := Pack(&buf, Sources{Patches: patches, Fonts: fonts})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return write(t, filepath.Join(t.TempDir(), "loc.zip"), buf.Bytes()), res
}

const twoFonts = `{"version": 1, "fonts": [
	{"name": "Helveticrap", "file": "fonts/Cyr.ttf", "metrics": "font"},
	{"name": "Apocalypse Grunge", "file": "fonts/Cyr.ttf"},
	{"name": "forcedSquare", "file": "other/Latin.ttf"}
]}`

func TestPackAndRead(t *testing.T) {
	journal, fonts := sources(t, twoFonts)
	zipPath, res := pack(t, journal, fonts)
	if res.Patches != 1 || len(res.Fonts) != 3 {
		t.Fatalf("result = %+v", res)
	}
	if res.Fonts[0].Entry != "fonts/Cyr.ttf" || res.Fonts[0].Family != "Cyr" || len(res.Fonts[0].Missing) != 0 {
		t.Errorf("font 0 = %+v", res.Fonts[0])
	}
	if res.Fonts[2].Entry != "fonts/Latin.ttf" || len(res.Fonts[2].Missing) != 66 {
		t.Errorf("font 2 = %+v", res.Fonts[2])
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	_ = zr.Close()
	if got := strings.Join(names, ","); got != "patches.json,fonts/Cyr.ttf,fonts/Latin.ttf,fonts.json" {
		t.Errorf("entries = %s", got)
	}

	pkg, err := Read(zipPath)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(pkg.Patches) != 1 || pkg.Patches[0].New != "НОВАЯ ИГРА" {
		t.Errorf("patches = %+v", pkg.Patches)
	}
	if len(pkg.Fonts) != 3 || pkg.Fonts[1].Name != "Apocalypse Grunge" || !bytes.Equal(pkg.Fonts[1].Data, cyrillicTTF("Cyr")) {
		t.Errorf("fonts = %d", len(pkg.Fonts))
	}
	// The metrics choice survives packing; unset means original.
	if pkg.Fonts[0].Metrics != gamepatch.MetricsFont || pkg.Fonts[1].Metrics != gamepatch.MetricsOriginal {
		t.Errorf("metrics = %q, %q", pkg.Fonts[0].Metrics, pkg.Fonts[1].Metrics)
	}
	if res.Fonts[0].Metrics != gamepatch.MetricsFont || res.Fonts[0].LineHeight != 1 {
		t.Errorf("packed font = %+v", res.Fonts[0])
	}

	// Packing is deterministic.
	again, _ := pack(t, journal, fonts)
	a, _ := os.ReadFile(zipPath)
	b, _ := os.ReadFile(again)
	if !bytes.Equal(a, b) {
		t.Error("packing the same inputs produced different archives")
	}
}

func TestPackPartial(t *testing.T) {
	journal, fonts := sources(t, twoFonts)
	onlyPatches, _ := pack(t, journal, "")
	if pkg, err := Read(onlyPatches); err != nil || len(pkg.Patches) != 1 || len(pkg.Fonts) != 0 {
		t.Errorf("patches only = %+v, %v", pkg, err)
	}
	onlyFonts, _ := pack(t, "", fonts)
	if pkg, err := Read(onlyFonts); err != nil || len(pkg.Patches) != 0 || len(pkg.Fonts) != 3 {
		t.Errorf("fonts only = %+v, %v", pkg, err)
	}

	abs := filepath.Join(filepath.Dir(fonts), "fonts", "Cyr.ttf")
	_, absFonts := sources(t, `{"version":1,"fonts":[{"name":"X","file":"`+filepath.ToSlash(abs)+`"}]}`)
	if _, res := pack(t, "", absFonts); res.Fonts[0].Entry != "fonts/Cyr.ttf" {
		t.Errorf("absolute path entry = %+v", res.Fonts)
	}
}

func TestPackErrors(t *testing.T) {
	journal, _ := sources(t, twoFonts)
	empty := write(t, filepath.Join(t.TempDir(), "empty.json"), []byte(`{"version":1,"patches":[]}`))
	cases := map[string]struct{ patches, fonts string }{
		"nothing":         {"", ""},
		"missing journal": {"/nonexistent/patches.json", ""},
		"bad journal":     {write(t, filepath.Join(t.TempDir(), "p.json"), []byte("{")), ""},
		"empty journal":   {empty, ""},
		"missing fonts":   {"", "/nonexistent/fonts.json"},
	}
	fontsCases := map[string]string{
		"unknown field":  `{"version":1,"fonts":[],"extra":1}`,
		"version":        `{"version":2,"fonts":[]}`,
		"no fonts":       `{"version":1,"fonts":[]}`,
		"empty name":     `{"version":1,"fonts":[{"name":"","file":"fonts/Cyr.ttf"}]}`,
		"bad metrics":    `{"version":1,"fonts":[{"name":"A","file":"fonts/Cyr.ttf","metrics":"tall"}]}`,
		"duplicate name": `{"version":1,"fonts":[{"name":"A","file":"fonts/Cyr.ttf"},{"name":"A","file":"fonts/Cyr.ttf"}]}`,
		"missing file":   `{"version":1,"fonts":[{"name":"A","file":"fonts/None.ttf"}]}`,
		"not a font":     `{"version":1,"fonts":[{"name":"A","file":"patches.json"}]}`,
		"name collision": `{"version":1,"fonts":[{"name":"A","file":"fonts/Cyr.ttf"},{"name":"B","file":"twin/Cyr.ttf"}]}`,
	}
	for name, content := range fontsCases {
		_, fonts := sources(t, content)
		write(t, filepath.Join(filepath.Dir(fonts), "twin", "Cyr.ttf"), cyrillicTTF("Twin"))
		cases[name] = struct{ patches, fonts string }{journal, fonts}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Pack(&bytes.Buffer{}, Sources{Patches: tc.patches, Fonts: tc.fonts}); err == nil {
				t.Error("expected error")
			}
		})
	}
}

type entry struct {
	name string
	data string
}

func rawZip(t *testing.T, entries ...entry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return write(t, filepath.Join(t.TempDir(), "raw.zip"), buf.Bytes())
}

func TestReadErrors(t *testing.T) {
	journal := `{"version":1,"patches":[{"owner":"T","old":"a","new":"b"}]}`
	fontsRef := `{"version":1,"fonts":[{"name":"A","file":"fonts/a.ttf"}]}`
	cases := map[string]string{
		"parent path":    rawZip(t, entry{name: "../patches.json", data: journal}),
		"absolute path":  rawZip(t, entry{name: "/patches.json", data: journal}),
		"backslash":      rawZip(t, entry{name: `fonts\a.ttf`, data: "x"}, entry{name: "patches.json", data: journal}),
		"duplicate":      rawZip(t, entry{name: "patches.json", data: journal}, entry{name: "./patches.json", data: journal}),
		"bad journal":    rawZip(t, entry{name: "patches.json", data: "{"}),
		"bad fonts.json": rawZip(t, entry{name: "fonts.json", data: "{"}),
		"missing font":   rawZip(t, entry{name: "fonts.json", data: fontsRef}),
		"unsafe ref":     rawZip(t, entry{name: "fonts.json", data: `{"version":1,"fonts":[{"name":"A","file":"../a.ttf"}]}`}),
		"empty":          rawZip(t, entry{name: "readme.txt", data: "hi"}, entry{name: "fonts/", data: ""}),
		"empty journal":  rawZip(t, entry{name: "patches.json", data: `{"version":1,"patches":[]}`}),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(path); !errors.Is(err, ErrFormat) && !errors.Is(err, patch.ErrFormat) {
				t.Errorf("err = %v", err)
			}
		})
	}
	if _, err := Read(write(t, filepath.Join(t.TempDir(), "x.zip"), []byte("not a zip"))); err == nil {
		t.Error("expected error for a non-zip file")
	}

	ok := rawZip(t, entry{name: "fonts.json", data: fontsRef}, entry{name: "fonts/a.ttf", data: "font bytes"}, entry{name: "fonts/", data: ""})
	pkg, err := Read(ok)
	if err != nil || len(pkg.Fonts) != 1 || string(pkg.Fonts[0].Data) != "font bytes" {
		t.Errorf("valid package = %+v, %v", pkg, err)
	}
}

func TestReadEntryLimit(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "patches.json", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	// Highly compressible data just over the limit.
	chunk := bytes.Repeat([]byte{' '}, 1<<20)
	for range maxEntrySize>>20 + 1 {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := write(t, filepath.Join(t.TempDir(), "big.zip"), buf.Bytes())
	if _, err := Read(path); !errors.Is(err, ErrFormat) {
		t.Errorf("oversized entry err = %v", err)
	}
}

func TestPackDictionary(t *testing.T) {
	journal, _ := sources(t, twoFonts)
	loc := dictionary.Location{File: "level0", Path: "m_Text", Owner: "Title"}
	// dict writes translation.map and translation.po for entries.
	dict := func(entries ...dictionary.Entry) Sources {
		t.Helper()
		dir := t.TempDir()
		mapData, poData, _, err := dictionary.Encode(entries, nil, dictionary.Header{Language: "ru"})
		if err != nil {
			t.Fatal(err)
		}
		f, err := po.Parse(poData)
		if err != nil {
			t.Fatal(err)
		}
		for i := range f.Messages {
			f.Messages[i].Str = entries[i].New
		}
		if err := dictionary.Save(dir, mapData, po.Marshal(f)); err != nil {
			t.Fatal(err)
		}
		return Sources{Map: filepath.Join(dir, dictionary.MapName), PO: filepath.Join(dir, dictionary.POName)}
	}
	withJournal := func(src Sources) Sources {
		src.Patches = journal
		return src
	}

	// Dictionary patches follow the journal.
	var buf bytes.Buffer
	res, err := Pack(&buf, withJournal(dict(dictionary.Entry{Old: "LOAD", New: "ЗАГРУЗИТЬ", FoundIn: []dictionary.Location{loc}})))
	if err != nil {
		t.Fatal(err)
	}
	if res.Patches != 1 || res.DictionaryPatches != 1 {
		t.Errorf("result = %+v", res)
	}
	pkg, err := Read(write(t, filepath.Join(t.TempDir(), "loc.zip"), buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Patches) != 2 || pkg.Patches[0].Old != "NEW GAME" || pkg.Patches[1].New != "ЗАГРУЗИТЬ" {
		t.Errorf("patches = %+v", pkg.Patches)
	}

	untranslated := dict(dictionary.Entry{Old: "LOAD", FoundIn: []dictionary.Location{loc}})
	chained := dict(
		dictionary.Entry{Old: "OK", New: "Ок", FoundIn: []dictionary.Location{loc}},
		dictionary.Entry{Old: "Ok", New: "OK", FoundIn: []dictionary.Location{loc}},
	)
	for name, src := range map[string]Sources{
		"only untranslated": untranslated,
		"chained":           withJournal(chained),
		"map without po":    {Map: untranslated.Map},
		"po without map":    {Patches: journal, PO: untranslated.PO},
		"missing map":       {Map: "/nonexistent/translation.map", PO: untranslated.PO},
		"missing po":        {Map: untranslated.Map, PO: "/nonexistent/translation.po"},
		"malformed":         {Map: write(t, filepath.Join(t.TempDir(), "m.map"), []byte("{")), PO: untranslated.PO},
	} {
		if _, err := Pack(&bytes.Buffer{}, src); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	// An untranslated dictionary next to a journal adds nothing.
	if res, err := Pack(&bytes.Buffer{}, withJournal(untranslated)); err != nil || res.DictionaryPatches != 0 {
		t.Errorf("untranslated with journal = %+v, %v", res, err)
	}
}
