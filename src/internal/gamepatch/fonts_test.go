package gamepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/ttf"
	"apocalypter-l10n-tools/internal/unitytest"
)

var le = binary.LittleEndian

func TestFreetypeKerning(t *testing.T) {
	// Raw values from LiberationSans (2048 units/em) and the pixels Unity
	// stored for them at font size 16.
	cases := map[int64]int64{-387: -2, -272: -1, -113: -1, -98: 0, -23: 0, 23: 0, 98: 0, 121: 1, 188: 1}
	for v, want := range cases {
		if got := freetypeKerning(v, 16, 2048); got != want {
			t.Errorf("freetypeKerning(%d) = %d, want %d", v, got, want)
		}
	}
	// No damping from 25 ppem on.
	if got := freetypeKerning(-113, 32, 2048); got != -2 {
		t.Errorf("32 ppem = %d", got)
	}
}

func cyrillicTTF(family string) []byte {
	chars := map[rune]uint16{'A': 1, 'V': 2}
	for i, r := range RussianAlphabet {
		chars[r] = uint16(10 + i)
	}
	return unitytest.TTF(unitytest.TTFSpec{
		// Taller than the 1 em line of the fixture Font objects, like the
		// fonts that made labels disappear.
		Family: family, UnitsPerEm: 1000, Ascender: 935, Descender: -250, LineGap: 0,
		Chars: chars,
		Kern:  []unitytest.KernPair{{Left: 1, Right: 2, Value: -150}, {Left: 2, Right: 1, Value: -20}},
	})
}

func TestApplyFontData(t *testing.T) {
	data := cyrillicTTF("New")
	parsed, err := ttf.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[Metrics][3]float32{
		// The fixture's own ascent, descent and line spacing.
		MetricsOriginal: {12, -4, 16},
		// 935 * float32(16/1000) and 1185 * 16 / 1000, as Unity computes.
		MetricsFont: {float32(935) * (float32(16) / 1000), -4, float32(1185) * 16 / 1000},
	}
	for metrics, want := range cases {
		t.Run(string(metrics), func(t *testing.T) {
			font, err := serialized.ReadFont(unitytest.Font("F", 16, []byte("old"), [][3]float32{{1, 2, 3}}), le)
			if err != nil {
				t.Fatal(err)
			}
			ApplyFontData(font, data, parsed, metrics)
			if got := [3]float32{font.Ascent, font.Descent, font.LineSpacing}; got != want {
				t.Errorf("metrics = %v, want %v", got, want)
			}
			wantKern := serialized.KerningValue{Left: 'A', Right: 'V', Value: -2}
			if len(font.Kerning) != 1 || font.Kerning[0] != wantKern {
				t.Errorf("kerning = %+v", font.Kerning)
			}
			if !bytes.Equal(font.FontData, data) {
				t.Error("font data not replaced")
			}
		})
	}
}

func TestParseMetrics(t *testing.T) {
	for in, want := range map[string]Metrics{"": MetricsOriginal, "original": MetricsOriginal, "font": MetricsFont} {
		if got, err := ParseMetrics(in); err != nil || got != want {
			t.Errorf("ParseMetrics(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseMetrics("tall"); !errors.Is(err, ErrUnknownMetrics) {
		t.Errorf("err = %v", err)
	}
}

func fontBundle() []byte {
	latin := unitytest.TTF(unitytest.TTFSpec{Family: "Latin", UnitsPerEm: 1000, Ascender: 800, Descender: -200, Chars: map[rune]uint16{'A': 1, 'и': 2}})
	shared := unitytest.Serialized([]unitytest.Object{
		{PathID: 19, ClassID: serialized.ClassFont, Data: unitytest.Font("Helveticrap", 16, latin, nil)},
		{PathID: 20, ClassID: serialized.ClassFont, Data: unitytest.Font("Broken", 16, []byte("not a font"), nil)},
		{PathID: 21, ClassID: serialized.ClassFont, Data: []byte{1, 2, 3}},
	}, nil)
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 5, ClassID: serialized.ClassFont, Data: unitytest.Font("Helveticrap", 16, latin, nil)},
	}, nil)
	return unitytest.Bundle([]unitytest.Node{
		{Path: "sharedassets0.assets", Flags: nodeFlagSerialized, Data: shared},
		{Path: "level0", Flags: nodeFlagSerialized, Data: level},
	}, 256)
}

func TestListFonts(t *testing.T) {
	fonts, err := ListFonts(openBundle(t, fontBundle()))
	if err != nil {
		t.Fatal(err)
	}
	if len(fonts) != 4 {
		t.Fatalf("fonts = %+v", fonts)
	}
	byID := map[int64]FontInfo{}
	for _, f := range fonts {
		byID[f.PathID] = f
	}
	h := byID[19]
	if h.Name != "Helveticrap" || h.Family != "Latin" || h.File != "sharedassets0.assets" || len(h.Missing) != 65 || h.Err != nil {
		t.Errorf("Helveticrap = %+v", h)
	}
	if byID[20].Err == nil || byID[20].Name != "Broken" || byID[21].Err == nil {
		t.Errorf("broken fonts = %+v / %+v", byID[20], byID[21])
	}
	if !errors.Is(byID[21].Err, serialized.ErrFormat) {
		t.Errorf("unreadable object err = %v", byID[21].Err)
	}
}

func TestReplaceFont(t *testing.T) {
	b := openBundle(t, fontBundle())
	// The unreadable Font object makes replacement fail loudly rather than
	// skip it silently.
	_, _, err := Apply(b, nil, Options{Fonts: []FontReplacement{{Name: "Helveticrap", Data: cyrillicTTF("Cyr")}}})
	if !errors.Is(err, serialized.ErrFormat) {
		t.Fatalf("err = %v", err)
	}

	good := unitytest.Bundle([]unitytest.Node{
		{Path: "level0", Flags: nodeFlagSerialized, Data: unitytest.Serialized([]unitytest.Object{
			{PathID: 5, ClassID: serialized.ClassFont, Data: unitytest.Font("Helveticrap", 16, []byte("old"), nil)},
			{PathID: 6, ClassID: serialized.ClassFont, Data: unitytest.Font("Other", 16, []byte("keep"), nil)},
		}, nil)},
	}, 256)
	b = openBundle(t, good)
	rep, nodes, err := Apply(b, nil, Options{Fonts: []FontReplacement{{Name: "Helveticrap", Data: cyrillicTTF("Cyr")}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Fonts) != 1 || rep.Fonts[0].Family != "Cyr" || len(rep.Fonts[0].Missing) != 0 || len(rep.Fonts[0].Targets) != 1 {
		t.Errorf("report = %+v", rep.Fonts)
	}
	if r := rep.Fonts[0]; r.Metrics != MetricsOriginal || r.OriginalLineHeight != 1 || math.Abs(r.FontLineHeight-1.185) > 1e-9 {
		t.Errorf("line heights = %+v", r)
	}
	var out bytes.Buffer
	if err := Write(&out, b, nodes); err != nil {
		t.Fatal(err)
	}
	fonts, err := ListFonts(openBundle(t, out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fonts {
		switch f.Name {
		case "Helveticrap":
			// The default keeps the original 1 em line.
			if f.Family != "Cyr" || len(f.Missing) != 0 || f.LineHeight != 1 {
				t.Errorf("replaced font = %+v", f)
			}
		case "Other":
			if f.DataSize != 4 {
				t.Errorf("other font changed: %+v", f)
			}
		}
	}

	failures := map[string]FontReplacement{
		"unknown name": {Name: "Nope", Data: cyrillicTTF("Cyr")},
		"bad ttf":      {Name: "Helveticrap", Data: []byte("junk")},
		"bad metrics":  {Name: "Helveticrap", Data: cyrillicTTF("Cyr"), Metrics: "tall"},
	}
	for name, r := range failures {
		_, nodes, err := Apply(openBundle(t, good), nil, Options{Fonts: []FontReplacement{r}})
		if err == nil || nodes != nil {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := Apply(openBundle(t, good), nil, Options{Fonts: []FontReplacement{{Name: "Nope", Data: cyrillicTTF("C")}}}); !errors.Is(err, ErrUnknownFont) {
		t.Errorf("unknown font err = %v", err)
	}
}
