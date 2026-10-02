package locpack

import (
	"archive/zip"
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

func oggFile(rate int) []byte {
	return unitytest.OggVorbis(unitytest.OggSpec{Channels: 2, Rate: rate, Samples: 4410, Setup: []byte("s"), Packets: [][]byte{{0, 1}}})
}

// soundSources creates a localization directory with sounds and the given
// sounds.json and returns its path.
func soundSources(t *testing.T, soundsJSON string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sounds", "shot.ogg"), oggFile(44100))
	write(t, filepath.Join(dir, "other", "shot.ogg"), oggFile(48000))
	write(t, filepath.Join(dir, "other", "bad.ogg"), oggFile(44000))
	return write(t, filepath.Join(dir, "sounds.json"), []byte(soundsJSON))
}

func TestPackSounds(t *testing.T) {
	sounds := soundSources(t, `{"version": 1, "sounds": [
		{"name": "enemy_1", "file": "sounds/shot.ogg"},
		{"name": "enemy_2", "file": "./sounds/shot.ogg"}
	]}`)
	var buf bytes.Buffer
	res, err := Pack(&buf, Sources{Sounds: sounds})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if len(res.Sounds) != 2 || res.Sounds[0] != (PackedSound{Name: "enemy_1", Entry: "sounds/shot.ogg", Channels: 2, Rate: 44100, Samples: 4410}) {
		t.Errorf("packed = %+v", res.Sounds)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	// The shared file is stored once.
	if got := strings.Join(names, ","); got != "sounds/shot.ogg,sounds.json" {
		t.Errorf("entries = %s", got)
	}

	pkg, err := Read(write(t, filepath.Join(t.TempDir(), "s.lang"), buf.Bytes()))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(pkg.Sounds) != 2 || pkg.Sounds[1].Name != "enemy_2" || !bytes.Equal(pkg.Sounds[1].Data, oggFile(44100)) {
		t.Errorf("sounds = %+v", pkg.Sounds)
	}
	if len(pkg.Patches) != 0 || len(pkg.Fonts) != 0 {
		t.Errorf("package = %+v", pkg)
	}
}

func TestPackSoundsErrors(t *testing.T) {
	cases := map[string]string{
		"unknown field":  `{"version":1,"sounds":[],"extra":1}`,
		"version":        `{"version":2,"sounds":[]}`,
		"no sounds":      `{"version":1,"sounds":[]}`,
		"empty file":     `{"version":1,"sounds":[{"name":"A","file":""}]}`,
		"duplicate name": `{"version":1,"sounds":[{"name":"A","file":"sounds/shot.ogg"},{"name":"A","file":"sounds/shot.ogg"}]}`,
		"missing file":   `{"version":1,"sounds":[{"name":"A","file":"sounds/none.ogg"}]}`,
		"not ogg":        `{"version":1,"sounds":[{"name":"A","file":"sounds.json"}]}`,
		"bad rate":       `{"version":1,"sounds":[{"name":"A","file":"other/bad.ogg"}]}`,
		"name collision": `{"version":1,"sounds":[{"name":"A","file":"sounds/shot.ogg"},{"name":"B","file":"other/shot.ogg"}]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Pack(&bytes.Buffer{}, Sources{Sounds: soundSources(t, content)}); err == nil {
				t.Error("expected error")
			}
		})
	}
	if _, err := Pack(&bytes.Buffer{}, Sources{Sounds: "/nonexistent/sounds.json"}); err == nil {
		t.Error("missing sounds.json: expected error")
	}
}

func TestReadSoundsErrors(t *testing.T) {
	cases := map[string]string{
		"bad sounds.json": rawZip(t, entry{name: "sounds.json", data: "{"}),
		"missing sound":   rawZip(t, entry{name: "sounds.json", data: `{"version":1,"sounds":[{"name":"A","file":"sounds/a.ogg"}]}`}),
		"unsafe ref":      rawZip(t, entry{name: "sounds.json", data: `{"version":1,"sounds":[{"name":"A","file":"/a.ogg"}]}`}),
		"empty list":      rawZip(t, entry{name: "sounds.json", data: `{"version":1,"sounds":[]}`}),
	}
	for name, path := range cases {
		if _, err := Read(path); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
