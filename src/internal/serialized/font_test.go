package serialized

import (
	"bytes"
	"errors"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

func TestReadFontRoundTrip(t *testing.T) {
	data := unitytest.Font("Helveticrap", 16, []byte("\x00\x01\x00\x00ttf!!"), [][3]float32{{65, 84, -1}, {76, 84, -2}})
	f, err := ReadFont(data, le)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "Helveticrap" || f.FontSize != 16 || f.Ascent != 12 || f.Descent != -4 || f.PixelScale != 0.1 {
		t.Errorf("font = %+v", f)
	}
	if len(f.CharacterRects) != 1 || len(f.Kerning) != 2 || f.Kerning[1] != (KerningValue{76, 84, -2}) {
		t.Errorf("tables = %d rects, %+v", len(f.CharacterRects), f.Kerning)
	}
	if string(f.FontData) != "\x00\x01\x00\x00ttf!!" || len(f.FontNames) != 1 || f.FallbackFonts[0] != (PPtr{1, 9}) || !f.LegacyBounds || f.RoundAdvanceValue {
		t.Errorf("tail = %+v", f)
	}
	if !bytes.Equal(f.Encode(le), data) {
		t.Error("Encode does not reproduce the object")
	}

	f.FontData = []byte("longer replacement data")
	f.Kerning = nil
	g, err := ReadFont(f.Encode(le), le)
	if err != nil || string(g.FontData) != "longer replacement data" || len(g.Kerning) != 0 || g.Name != "Helveticrap" {
		t.Errorf("re-read = %+v, %v", g, err)
	}
}

func TestReadFontErrors(t *testing.T) {
	data := unitytest.Font("F", 16, []byte("ttf"), nil)
	if _, err := ReadFont(append(data, 0), le); !errors.Is(err, ErrFormat) {
		t.Errorf("trailing byte err = %v", err)
	}
	if _, err := ReadFont(data[:len(data)-10], le); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated err = %v", err)
	}
	huge := bytes.Clone(data)
	// Name "F" (8) + spacing, material, size, texture, then five 4-byte fields.
	le.PutUint32(huge[8+4+12+4+12+4*5:], 1<<28)
	if _, err := ReadFont(huge, le); !errors.Is(err, ErrFormat) {
		t.Errorf("huge rect count err = %v", err)
	}
}
