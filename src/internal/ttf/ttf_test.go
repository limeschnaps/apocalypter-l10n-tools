package ttf

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

func spec() unitytest.TTFSpec {
	return unitytest.TTFSpec{
		Family:     "Тест Sans",
		UnitsPerEm: 2048,
		Ascender:   1854,
		Descender:  -434,
		LineGap:    67,
		Chars:      map[rune]uint16{' ': 3, 'A': 36, 'V': 57, 'Α': 36, 'и': 600, '😀': 900},
		Kern: []unitytest.KernPair{
			{Left: 36, Right: 57, Value: -150},
			{Left: 3, Right: 36, Value: -113},
		},
	}
}

func TestParse(t *testing.T) {
	for _, format12 := range []bool{false, true} {
		s := spec()
		s.Format12 = format12
		if !format12 {
			delete(s.Chars, '😀')
		}
		f, err := Parse(unitytest.TTF(s))
		if err != nil {
			t.Fatalf("format12=%v: %v", format12, err)
		}
		if f.Family != "Тест Sans" || f.UnitsPerEm != 2048 || f.Ascender != 1854 || f.Descender != -434 || f.LineGap != 67 {
			t.Errorf("metrics = %+v", f)
		}
		if lh := f.LineHeight(); lh != float64(1854+434+67)/2048 {
			t.Errorf("LineHeight = %v", lh)
		}
		if g, ok := f.Glyph('и'); !ok || g != 600 {
			t.Errorf("Glyph(и) = %d, %v", g, ok)
		}
		if f.Has('Ж') || !f.Has('A') || f.Has('😀') != format12 {
			t.Error("Has reports wrong coverage")
		}
		if got := string(f.Missing("иЖAЖй")); got != "Жй" {
			t.Errorf("Missing = %q", got)
		}
		want := []KerningPair{
			{Left: 'A', Right: 'V', Value: -150},
			{Left: 'Α', Right: 'V', Value: -150},
			{Left: ' ', Right: 'A', Value: -113},
			{Left: ' ', Right: 'Α', Value: -113},
		}
		if got := f.KerningPairs(); !reflect.DeepEqual(got, want) {
			t.Errorf("KerningPairs = %+v", got)
		}
	}
}

func TestKernDeclaredLength(t *testing.T) {
	// A declared length that covers only the first pair, as happens when
	// a large subtable overflows the 16-bit field.
	s := spec()
	s.KernLength = 14 + 6
	f, err := Parse(unitytest.TTF(s))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.KerningPairs(); len(got) != 2 || got[0].Value != -150 {
		t.Errorf("KerningPairs = %+v", got)
	}

	s.KernLength = 3
	if _, err := Parse(unitytest.TTF(s)); !errors.Is(err, ErrFormat) {
		t.Errorf("tiny subtable err = %v", err)
	}

	s.Kern = nil
	f, err = Parse(unitytest.TTF(s))
	if err != nil || len(f.KerningPairs()) != 0 {
		t.Errorf("no kern table: %v, %v", f.KerningPairs(), err)
	}
}

func TestParseErrors(t *testing.T) {
	good := unitytest.TTF(spec())
	be := binary.BigEndian
	mutate := func(f func([]byte)) []byte {
		d := append([]byte(nil), good...)
		f(d)
		return d
	}
	tableOffset := func(d []byte, tag string) int {
		for i := range int(be.Uint16(d[4:])) {
			rec := d[12+16*i:]
			if string(rec[:4]) == tag {
				return int(be.Uint32(rec[8:]))
			}
		}
		t.Fatalf("no %s table", tag)
		return 0
	}
	cases := map[string][]byte{
		"short":       good[:8],
		"signature":   mutate(func(d []byte) { copy(d, "wOFF") }),
		"directory":   good[:20],
		"table range": mutate(func(d []byte) { be.PutUint32(d[12+12:], 1<<30) }),
		"zero upem":   mutate(func(d []byte) { be.PutUint16(d[tableOffset(d, "head")+18:], 0) }),
		"no unicode cmap": mutate(func(d []byte) {
			be.PutUint16(d[tableOffset(d, "cmap")+4:], 1)
			be.PutUint16(d[tableOffset(d, "cmap")+6:], 0)
		}),
		"cmap offset": mutate(func(d []byte) { be.PutUint32(d[tableOffset(d, "cmap")+8:], 1<<20) }),
	}
	for name, data := range cases {
		if _, err := Parse(data); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	missingHead := mutate(func(d []byte) {
		for i := range int(be.Uint16(d[4:])) {
			rec := d[12+16*i:]
			if string(rec[:4]) == "head" {
				copy(rec, "xxxx")
			}
		}
	})
	if _, err := Parse(missingHead); !errors.Is(err, ErrFormat) {
		t.Errorf("missing head err = %v", err)
	}
}

func TestCmapErrors(t *testing.T) {
	if _, err := parseCmap(nil); !errors.Is(err, ErrFormat) {
		t.Errorf("empty cmap err = %v", err)
	}
	if _, err := parseCmap([]byte{0, 0, 0, 2, 0, 3}); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated records err = %v", err)
	}
	if _, err := parseCmap4(make([]byte, 10)); !errors.Is(err, ErrFormat) {
		t.Errorf("short format 4 err = %v", err)
	}
	f4 := make([]byte, 14)
	binary.BigEndian.PutUint16(f4[6:], 20)
	if _, err := parseCmap4(f4); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated segments err = %v", err)
	}
	if _, err := parseCmap12(make([]byte, 10)); !errors.Is(err, ErrFormat) {
		t.Errorf("short format 12 err = %v", err)
	}
	f12 := make([]byte, 28)
	binary.BigEndian.PutUint32(f12[12:], 1)
	binary.BigEndian.PutUint32(f12[16:], 10)
	binary.BigEndian.PutUint32(f12[20:], 5)
	if _, err := parseCmap12(f12); !errors.Is(err, ErrFormat) {
		t.Errorf("bad group err = %v", err)
	}
	binary.BigEndian.PutUint32(f12[12:], 5)
	if _, err := parseCmap12(f12); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated groups err = %v", err)
	}
}

func TestParseFamilyFallbacks(t *testing.T) {
	if parseFamily(nil) != "" {
		t.Error("empty name table")
	}
	be := binary.BigEndian
	mac := be.AppendUint16(nil, 0)
	mac = be.AppendUint16(mac, 1)
	mac = be.AppendUint16(mac, 18)
	for _, v := range []uint16{1, 0, 0, 1, 4, 0} {
		mac = be.AppendUint16(mac, v)
	}
	mac = append(mac, "Mono"...)
	if got := parseFamily(mac); got != "Mono" {
		t.Errorf("mac family = %q", got)
	}
}
