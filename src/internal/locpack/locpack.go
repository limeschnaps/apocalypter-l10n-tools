// Package locpack reads and writes localization packages: zip archives
// that bundle the text patch journal, the font replacement list and the
// font files the patcher needs.
//
// Layout:
//
//	patches.json  text patches: the editor journal, then the patches of
//	              the translated dictionary messages (optional)
//	fonts.json    font replacements (optional)
//	fonts/...     font files referenced by fonts.json
//
// At least one of patches.json and fonts.json must be present.
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
)

const (
	fontsVersion = 1
	// maxEntrySize bounds every decompressed entry, so a crafted archive
	// cannot exhaust memory.
	maxEntrySize = 64 << 20
)

// ErrFormat reports an invalid package or fonts.json.
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

// Package is the content of a localization package.
type Package struct {
	Patches []patch.Patch
	Fonts   []gamepatch.FontReplacement
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
			name, err := cleanName(s.File)
			if err != nil {
				return nil, err
			}
			ff, ok := entries[name]
			if !ok {
				return nil, fmt.Errorf("%w: %s references missing %q", ErrFormat, FontsName, s.File)
			}
			data, err := readEntry(ff)
			if err != nil {
				return nil, err
			}
			metrics, _ := gamepatch.ParseMetrics(s.Metrics)
			pkg.Fonts = append(pkg.Fonts, gamepatch.FontReplacement{Name: s.Name, Data: data, Metrics: metrics})
		}
	}
	if len(pkg.Patches) == 0 && len(pkg.Fonts) == 0 {
		return nil, fmt.Errorf("%w: neither %s nor %s has entries", ErrFormat, PatchesName, FontsName)
	}
	return pkg, nil
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

// Pack writes a package to w from the files in src. The journal must not
// be empty; a dictionary without translations adds nothing. Font files are
// validated and stored under fonts/ by base name, and fonts.json is
// rewritten to point at them.
func Pack(w io.Writer, src Sources) (PackResult, error) {
	var res PackResult
	if src.Patches == "" && src.Map == "" && src.PO == "" && src.Fonts == "" {
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
	if len(patches) == 0 && src.Fonts == "" {
		return res, fmt.Errorf("%w: no patches and no fonts", ErrFormat)
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
	base := filepath.Dir(fontsPath)
	stored := map[string][]byte{}
	var packed []PackedFont
	out := fontsFile{Version: fontsVersion}
	for _, s := range specs {
		src := s.File
		if !filepath.IsAbs(src) {
			src = filepath.Join(base, filepath.FromSlash(src))
		}
		font, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("font %q: %w", s.Name, err)
		}
		parsed, err := ttf.Parse(font)
		if err != nil {
			return nil, fmt.Errorf("font %q (%s): %w", s.Name, src, err)
		}
		entry := FontsDir + filepath.Base(src)
		if prev, ok := stored[entry]; ok && !bytes.Equal(prev, font) {
			return nil, fmt.Errorf("%w: two different font files named %q", ErrFormat, filepath.Base(src))
		} else if !ok {
			if err := writeEntry(zw, entry, font); err != nil {
				return nil, err
			}
			stored[entry] = font
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
