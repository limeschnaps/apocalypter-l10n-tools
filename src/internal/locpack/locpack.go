// Package locpack reads and writes localization packages: zip archives
// that bundle the text patch journal, the font and sound replacement
// lists and the files the patcher needs.
//
// Layout:
//
//	patches.json  text patches: the editor journal, then the patches of
//	              the translated dictionary messages (optional)
//	fonts.json    font replacements (optional)
//	fonts/...     font files referenced by fonts.json
//	sounds.json   sound replacements (optional)
//	sounds/...    Ogg Vorbis files referenced by sounds.json
//
// At least one of patches.json, fonts.json and sounds.json must be
// present.
package locpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/ttf"
)

// Entry names inside a package.
const (
	PatchesName = "patches.json"
	FontsName   = "fonts.json"
	FontsDir    = "fonts/"
	SoundsName  = "sounds.json"
	SoundsDir   = "sounds/"
)

const (
	fontsVersion  = 1
	soundsVersion = 1
	// maxEntrySize bounds every decompressed entry, so a crafted archive
	// cannot exhaust memory.
	maxEntrySize = 64 << 20
)

// ErrFormat reports an invalid package, fonts.json or sounds.json.
var ErrFormat = errors.New("locpack: invalid package")

// FontSpec is one fonts.json entry: the Font object name in the game, the
// replacement font file and where the vertical metrics come from
// ("original", the default, or "font"; see gamepatch.Metrics).
type FontSpec struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Metrics string `json:"metrics,omitempty"`
}

type fontsFile struct {
	Version int        `json:"version"`
	Fonts   []FontSpec `json:"fonts"`
}

// SoundSpec is one sounds.json entry: the AudioClip name in the game and
// the replacement Ogg Vorbis file.
type SoundSpec struct {
	Name string `json:"name"`
	File string `json:"file"`
}

type soundsFile struct {
	Version int         `json:"version"`
	Sounds  []SoundSpec `json:"sounds"`
}

// Package is the content of a localization package.
type Package struct {
	Patches []patch.Patch
	Fonts   []gamepatch.FontReplacement
	Sounds  []gamepatch.SoundReplacement
}

// Read opens the package at path and loads everything it references.
func Read(path string) (*Package, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open package: %w", err)
	}
	defer zr.Close()

	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		name, err := cleanName(f.Name)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if _, dup := entries[name]; dup {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrFormat, name)
		}
		entries[name] = f
	}

	pkg := &Package{}
	if f, ok := entries[PatchesName]; ok {
		data, err := readEntry(f)
		if err != nil {
			return nil, err
		}
		if pkg.Patches, err = patch.Parse(data); err != nil {
			return nil, fmt.Errorf("%s: %w", PatchesName, err)
		}
	}
	if f, ok := entries[FontsName]; ok {
		data, err := readEntry(f)
		if err != nil {
			return nil, err
		}
		specs, err := parseFonts(data)
		if err != nil {
			return nil, err
		}
		for _, s := range specs {
			data, err := readReferenced(entries, FontsName, s.File)
			if err != nil {
				return nil, err
			}
			metrics, _ := gamepatch.ParseMetrics(s.Metrics)
			pkg.Fonts = append(pkg.Fonts, gamepatch.FontReplacement{Name: s.Name, Data: data, Metrics: metrics})
		}
	}
	if f, ok := entries[SoundsName]; ok {
		data, err := readEntry(f)
		if err != nil {
			return nil, err
		}
		specs, err := parseSounds(data)
		if err != nil {
			return nil, err
		}
		for _, s := range specs {
			data, err := readReferenced(entries, SoundsName, s.File)
			if err != nil {
				return nil, err
			}
			pkg.Sounds = append(pkg.Sounds, gamepatch.SoundReplacement{Name: s.Name, Data: data})
		}
	}
	if len(pkg.Patches) == 0 && len(pkg.Fonts) == 0 && len(pkg.Sounds) == 0 {
		return nil, fmt.Errorf("%w: none of %s, %s and %s has entries", ErrFormat, PatchesName, FontsName, SoundsName)
	}
	return pkg, nil
}

// readReferenced returns the entry that list (fonts.json or sounds.json)
// names as file.
func readReferenced(entries map[string]*zip.File, list, file string) ([]byte, error) {
	name, err := cleanName(file)
	if err != nil {
		return nil, err
	}
	f, ok := entries[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s references missing %q", ErrFormat, list, file)
	}
	return readEntry(f)
}

// Sources names the files a package is built from; any may be empty.
type Sources struct {
	// Patches is the patch journal written by the editor.
	Patches string
	// Map and PO are the translation.map and translation.po of a
	// dictionary, given together; translated messages become patches
	// applied after the journal.
	Map, PO string
	// Fonts is a fonts.json; font paths in it are relative to its
	// directory.
	Fonts string
	// Sounds is a sounds.json; sound paths in it are relative to its
	// directory.
	Sounds string
}

// PackResult summarizes a written package.
type PackResult struct {
	// Patches counts the journal patches, DictionaryPatches the patches
	// made from the dictionary, Fuzzy the dictionary messages skipped as
	// fuzzy.
	Patches           int
	DictionaryPatches int
	Fuzzy             int
	Fonts             []PackedFont
	Sounds            []PackedSound
}

// PackedFont describes a font stored in the package.
type PackedFont struct {
	Name    string
	Entry   string
	Family  string
	Missing []rune
	Metrics gamepatch.Metrics
	// LineHeight is the line height of the font file in em.
	LineHeight float64
}

// PackedSound describes a sound stored in the package.
type PackedSound struct {
	Name     string
	Entry    string
	Channels int
	Rate     int
	Samples  int64
}

// Pack writes a package to w from the files in src. The journal must not
// be empty; a dictionary without translations adds nothing. Font and
// sound files are validated and stored under fonts/ and sounds/ by base
// name, and fonts.json and sounds.json are rewritten to point at them.
func Pack(w io.Writer, src Sources) (PackResult, error) {
	var res PackResult
	if src.Patches == "" && src.Map == "" && src.PO == "" && src.Fonts == "" && src.Sounds == "" {
		return res, fmt.Errorf("%w: nothing to pack", ErrFormat)
	}
	if (src.Map == "") != (src.PO == "") {
		return res, fmt.Errorf("%w: %s and %s go together", ErrFormat, dictionary.MapName, dictionary.POName)
	}
	var patches []patch.Patch
	if src.Patches != "" {
		data, err := os.ReadFile(src.Patches)
		if err != nil {
			return res, fmt.Errorf("read patches: %w", err)
		}
		if patches, err = patch.Parse(data); err != nil {
			return res, fmt.Errorf("%s: %w", src.Patches, err)
		}
		if len(patches) == 0 {
			return res, fmt.Errorf("%w: %s has no patches", ErrFormat, src.Patches)
		}
		res.Patches = len(patches)
	}
	if src.Map != "" {
		mapData, err := os.ReadFile(src.Map)
		if err != nil {
			return res, fmt.Errorf("read dictionary map: %w", err)
		}
		poData, err := os.ReadFile(src.PO)
		if err != nil {
			return res, fmt.Errorf("read dictionary translations: %w", err)
		}
		entries, fuzzy, err := dictionary.Decode(mapData, poData)
		if err != nil {
			return res, err
		}
		fromDict, err := dictionary.Patches(entries)
		if err != nil {
			return res, err
		}
		res.Fuzzy = fuzzy
		res.DictionaryPatches = len(fromDict)
		patches = append(patches, fromDict...)
	}
	if len(patches) == 0 && src.Fonts == "" && src.Sounds == "" {
		return res, fmt.Errorf("%w: no patches, fonts or sounds", ErrFormat)
	}
	zw := zip.NewWriter(w)
	if len(patches) > 0 {
		data, err := patch.Marshal(patches)
		if err != nil {
			return res, err
		}
		if err := writeEntry(zw, PatchesName, data); err != nil {
			return res, err
		}
	}
	if src.Fonts != "" {
		fonts, err := packFonts(zw, src.Fonts)
		if err != nil {
			return res, err
		}
		res.Fonts = fonts
	}
	if src.Sounds != "" {
		sounds, err := packSounds(zw, src.Sounds)
		if err != nil {
			return res, err
		}
		res.Sounds = sounds
	}
	if err := zw.Close(); err != nil {
		return res, fmt.Errorf("finish package: %w", err)
	}
	return res, nil
}

func packFonts(zw *zip.Writer, fontsPath string) ([]PackedFont, error) {
	data, err := os.ReadFile(fontsPath)
	if err != nil {
		return nil, fmt.Errorf("read fonts: %w", err)
	}
	specs, err := parseFonts(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fontsPath, err)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("%w: %s has no fonts", ErrFormat, fontsPath)
	}
	files := storedFiles{zw: zw, base: filepath.Dir(fontsPath), dir: FontsDir, kind: "font", stored: map[string][]byte{}}
	var packed []PackedFont
	out := fontsFile{Version: fontsVersion}
	for _, s := range specs {
		src := files.source(s.File)
		font, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("font %q: %w", s.Name, err)
		}
		parsed, err := ttf.Parse(font)
		if err != nil {
			return nil, fmt.Errorf("font %q (%s): %w", s.Name, src, err)
		}
		entry, err := files.store(src, font)
		if err != nil {
			return nil, err
		}
		out.Fonts = append(out.Fonts, FontSpec{Name: s.Name, File: entry, Metrics: s.Metrics})
		metrics, _ := gamepatch.ParseMetrics(s.Metrics)
		packed = append(packed, PackedFont{
			Name: s.Name, Entry: entry, Family: parsed.Family, Missing: parsed.Missing(gamepatch.RussianAlphabet),
			Metrics: metrics, LineHeight: parsed.LineHeight(),
		})
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode fonts: %w", err)
	}
	if err := writeEntry(zw, FontsName, append(encoded, '\n')); err != nil {
		return nil, err
	}
	return packed, nil
}

func packSounds(zw *zip.Writer, soundsPath string) ([]PackedSound, error) {
	data, err := os.ReadFile(soundsPath)
	if err != nil {
		return nil, fmt.Errorf("read sounds: %w", err)
	}
	specs, err := parseSounds(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", soundsPath, err)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("%w: %s has no sounds", ErrFormat, soundsPath)
	}
	files := storedFiles{zw: zw, base: filepath.Dir(soundsPath), dir: SoundsDir, kind: "sound", stored: map[string][]byte{}}
	var packed []PackedSound
	out := soundsFile{Version: soundsVersion}
	for _, s := range specs {
		src := files.source(s.File)
		sound, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("sound %q: %w", s.Name, err)
		}
		_, stream, err := gamepatch.SoundBank(sound)
		if err != nil {
			return nil, fmt.Errorf("sound %q (%s): %w", s.Name, src, err)
		}
		entry, err := files.store(src, sound)
		if err != nil {
			return nil, err
		}
		out.Sounds = append(out.Sounds, SoundSpec{Name: s.Name, File: entry})
		packed = append(packed, PackedSound{
			Name: s.Name, Entry: entry, Channels: stream.Channels, Rate: stream.Rate, Samples: stream.Samples,
		})
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sounds: %w", err)
	}
	if err := writeEntry(zw, SoundsName, append(encoded, '\n')); err != nil {
		return nil, err
	}
	return packed, nil
}

// storedFiles writes the files a replacement list references under dir
// by base name, each once.
type storedFiles struct {
	zw     *zip.Writer
	base   string
	dir    string
	kind   string
	stored map[string][]byte
}

// source resolves a path from the list against the list's directory.
func (s storedFiles) source(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(s.base, filepath.FromSlash(file))
}

// store writes data read from src unless an equal file with the same
// base name is already stored, and returns its entry name.
func (s storedFiles) store(src string, data []byte) (string, error) {
	entry := s.dir + filepath.Base(src)
	if prev, ok := s.stored[entry]; ok {
		if !bytes.Equal(prev, data) {
			return "", fmt.Errorf("%w: two different %s files named %q", ErrFormat, s.kind, filepath.Base(src))
		}
		return entry, nil
	}
	if err := writeEntry(s.zw, entry, data); err != nil {
		return "", err
	}
	s.stored[entry] = data
	return entry, nil
}

func parseSounds(data []byte) ([]SoundSpec, error) {
	var f soundsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFormat, SoundsName, err)
	}
	if f.Version != soundsVersion {
		return nil, fmt.Errorf("%w: %s version %d", ErrFormat, SoundsName, f.Version)
	}
	var names []string
	for i, s := range f.Sounds {
		if s.Name == "" || s.File == "" {
			return nil, fmt.Errorf("%w: %s entry %d needs name and file", ErrFormat, SoundsName, i+1)
		}
		if slices.Contains(names, s.Name) {
			return nil, fmt.Errorf("%w: %s lists sound %q twice", ErrFormat, SoundsName, s.Name)
		}
		names = append(names, s.Name)
	}
	return f.Sounds, nil
}

func parseFonts(data []byte) ([]FontSpec, error) {
	var f fontsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFormat, FontsName, err)
	}
	if f.Version != fontsVersion {
		return nil, fmt.Errorf("%w: %s version %d", ErrFormat, FontsName, f.Version)
	}
	var names []string
	for i, s := range f.Fonts {
		if s.Name == "" || s.File == "" {
			return nil, fmt.Errorf("%w: %s entry %d needs name and file", ErrFormat, FontsName, i+1)
		}
		if _, err := gamepatch.ParseMetrics(s.Metrics); err != nil {
			return nil, fmt.Errorf("%w: %s entry %d: %w", ErrFormat, FontsName, i+1, err)
		}
		if slices.Contains(names, s.Name) {
			return nil, fmt.Errorf("%w: %s lists font %q twice", ErrFormat, FontsName, s.Name)
		}
		names = append(names, s.Name)
	}
	return f.Fonts, nil
}

// cleanName validates an archive path: relative, forward slashes, no
// parent references.
func cleanName(name string) (string, error) {
	clean := path.Clean(strings.TrimSuffix(name, "/"))
	if name == "" || strings.Contains(name, `\`) || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: unsafe entry name %q", ErrFormat, name)
	}
	return clean, nil
}

func readEntry(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxEntrySize {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrFormat, f.Name, maxEntrySize)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer rc.Close()
	// The header size may lie; the limit guards the actual stream.
	data, err := io.ReadAll(io.LimitReader(rc, maxEntrySize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	if len(data) > maxEntrySize {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrFormat, f.Name, maxEntrySize)
	}
	return data, nil
}

// packageTime is the fixed modification time of every entry, so packing
// the same inputs yields the same archive.
var packageTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func writeEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: packageTime})
	if err != nil {
		return fmt.Errorf("add %s: %w", name, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
