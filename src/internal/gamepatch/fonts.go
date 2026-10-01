package gamepatch

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/ttf"
	"apocalypter-l10n-tools/internal/unityfs"
)

// RussianAlphabet is the character set checked by font reports.
const RussianAlphabet = "АБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯабвгдеёжзийклмнопрстуфхцчшщъыьэюя"

// ErrUnknownFont reports a font replacement whose name matches no Font.
var ErrUnknownFont = errors.New("no Font object with this name")

// Metrics selects where a replaced font takes its vertical metrics
// (m_Ascent, m_Descent, m_LineSpacing) from.
type Metrics string

const (
	// MetricsOriginal keeps the metrics of the font being replaced, so
	// text layout stays exactly as the game was designed. UI.Text with
	// vertical truncation hides a line that is taller than its rect, so a
	// replacement with a taller line would make such labels disappear.
	MetricsOriginal Metrics = "original"
	// MetricsFont derives the metrics from the new font file, as Unity's
	// importer would.
	MetricsFont Metrics = "font"
)

// ErrUnknownMetrics reports an unsupported Metrics value.
var ErrUnknownMetrics = errors.New(`metrics must be "original" or "font"`)

// ParseMetrics validates s; an empty string means MetricsOriginal.
func ParseMetrics(s string) (Metrics, error) {
	switch m := Metrics(s); m {
	case "":
		return MetricsOriginal, nil
	case MetricsOriginal, MetricsFont:
		return m, nil
	default:
		return "", fmt.Errorf("%w, got %q", ErrUnknownMetrics, s)
	}
}

// FontReplacement swaps the TTF data of every Font object named Name.
// The zero Metrics value means MetricsOriginal.
type FontReplacement struct {
	Name    string
	Data    []byte
	Metrics Metrics
}

// FontInfo describes one Font object of the build.
type FontInfo struct {
	File     string
	PathID   int64
	Name     string
	Family   string
	DataSize int
	// LineHeight is the line height the game uses, in em.
	LineHeight float64
	// Missing lists the RussianAlphabet characters the font lacks; it is
	// nil when the font data could not be parsed.
	Missing []rune
	Err     error
}

// FontResult describes the outcome of one font replacement.
type FontResult struct {
	Name    string
	Family  string
	Missing []rune
	Targets []Target
	Metrics Metrics
	// OriginalLineHeight is the line height of the replaced font and
	// FontLineHeight that of the new font file, both in em.
	OriginalLineHeight float64
	FontLineHeight     float64
}

// ListFonts reports every Font object in the bundle.
func ListFonts(b *unityfs.Bundle) ([]FontInfo, error) {
	st, err := load(b)
	if err != nil {
		return nil, err
	}
	var out []FontInfo
	for _, name := range st.fileNames() {
		f := st.files[name]
		for _, o := range f.Objects {
			if o.ClassID != serialized.ClassFont {
				continue
			}
			info := FontInfo{File: name, PathID: o.PathID}
			font, err := serialized.ReadFont(f.Data(o), f.ByteOrder())
			if err != nil {
				info.Err = err
				out = append(out, info)
				continue
			}
			info.Name, info.DataSize, info.LineHeight = font.Name, len(font.FontData), lineHeight(font)
			if parsed, err := ttf.Parse(font.FontData); err != nil {
				info.Err = err
			} else {
				info.Family, info.Missing = parsed.Family, parsed.Missing(RussianAlphabet)
			}
			out = append(out, info)
		}
	}
	return out, nil
}

// replaceFont swaps the font data of every Font named r.Name and derives
// the metrics and kerning the way Unity's font importer does.
func (st *state) replaceFont(r FontReplacement) (FontResult, error) {
	parsed, err := ttf.Parse(r.Data)
	if err != nil {
		return FontResult{}, fmt.Errorf("font %q: %w", r.Name, err)
	}
	metrics, err := ParseMetrics(string(r.Metrics))
	if err != nil {
		return FontResult{}, fmt.Errorf("font %q: %w", r.Name, err)
	}
	res := FontResult{
		Name: r.Name, Family: parsed.Family, Missing: parsed.Missing(RussianAlphabet),
		Metrics: metrics, FontLineHeight: parsed.LineHeight(),
	}
	for _, name := range st.fileNames() {
		f := st.files[name]
		for _, o := range f.Objects {
			if o.ClassID != serialized.ClassFont {
				continue
			}
			font, err := serialized.ReadFont(st.objectData(name, o.PathID), f.ByteOrder())
			if err != nil {
				return res, fmt.Errorf("%s Font %d: %w", name, o.PathID, err)
			}
			if font.Name != r.Name {
				continue
			}
			res.OriginalLineHeight = lineHeight(font)
			ApplyFontData(font, r.Data, parsed, metrics)
			st.set(name, o.PathID, font.Encode(f.ByteOrder()))
			res.Targets = append(res.Targets, Target{File: name, PathID: o.PathID})
		}
	}
	if len(res.Targets) == 0 {
		return res, fmt.Errorf("%q: %w", r.Name, ErrUnknownFont)
	}
	return res, nil
}

// ApplyFontData stores data in font and recomputes the fields Unity
// derives from the font file at import. Kerning pairs are always
// recomputed, as FreeType's FT_Get_Kerning reports them in its default
// mode (grid-fitted to whole pixels and damped below 25 ppem), sorted by
// character pair; pairs that come out as zero are dropped, as the importer
// does. With MetricsFont, ascent, descent and line spacing are also
// recomputed, scaled to the import size in float32; with MetricsOriginal
// they are kept.
func ApplyFontData(font *serialized.Font, data []byte, parsed *ttf.Font, metrics Metrics) {
	font.FontData = data
	if metrics == MetricsFont {
		scale := font.FontSize / float32(parsed.UnitsPerEm)
		font.Ascent = float32(parsed.Ascender) * scale
		font.Descent = float32(parsed.Descender) * scale
		// Unity multiplies before dividing here, unlike ascent and descent;
		// the float32 results differ in the last bit for some fonts.
		font.LineSpacing = float32(int(parsed.Ascender)-int(parsed.Descender)+int(parsed.LineGap)) * font.FontSize / float32(parsed.UnitsPerEm)
	}
	ppem := int64(math.Round(float64(font.FontSize)))
	font.Kerning = font.Kerning[:0]
	for _, p := range parsed.KerningPairs() {
		if p.Left > 0xffff || p.Right > 0xffff {
			continue
		}
		v := freetypeKerning(int64(p.Value), ppem, int64(parsed.UnitsPerEm))
		if v == 0 {
			continue
		}
		font.Kerning = append(font.Kerning, serialized.KerningValue{Left: uint16(p.Left), Right: uint16(p.Right), Value: float32(v)})
	}
	slices.SortFunc(font.Kerning, func(a, b serialized.KerningValue) int {
		if a.Left != b.Left {
			return int(a.Left) - int(b.Left)
		}
		return int(a.Right) - int(b.Right)
	})
}

// freetypeKerning mirrors FT_Get_Kerning with FT_KERNING_DEFAULT for a
// face sized to ppem pixels: scale to 26.6 pixels, damp by ppem/25 for
// small sizes, round to a whole pixel. It returns whole pixels.
func freetypeKerning(v, ppem, upem int64) int64 {
	xScale := divFix(ppem*64, upem)
	k := mulFix(v, xScale)
	if ppem < 25 {
		k = mulDiv(k, ppem, 25)
	}
	return ((k + 32) &^ 63) / 64
}

// mulFix, divFix and mulDiv follow FreeType's rounding: they work on
// magnitudes and reapply the sign.
func mulFix(a, b int64) int64 {
	return withSign(a, b, (abs(a)*abs(b)+0x8000)>>16)
}

func divFix(a, b int64) int64 {
	return withSign(a, b, ((abs(a)<<16)+abs(b)/2)/abs(b))
}

func mulDiv(a, b, c int64) int64 {
	return withSign(a, b, (abs(a)*abs(b)+c/2)/c)
}

func withSign(a, b, v int64) int64 {
	if (a < 0) != (b < 0) {
		return -v
	}
	return v
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// lineHeight returns the stored line spacing in em.
func lineHeight(font *serialized.Font) float64 {
	if font.FontSize == 0 {
		return 0
	}
	return float64(font.LineSpacing) / float64(font.FontSize)
}

func (st *state) fileNames() []string {
	names := make([]string, 0, len(st.files))
	for n := range st.files {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
