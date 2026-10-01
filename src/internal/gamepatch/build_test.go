package gamepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unitytest"
)

func TestBuild(t *testing.T) {
	build, err := Load(openBundle(t, gameBundle()))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range build.Components() {
		got = append(got, c.File+":"+c.Owner)
	}
	want := []string{"level0:Title", "level0:Button", "level0:Settings", "level0:Orphan", "sharedassets1.assets:Title"}
	if !slices.Equal(got, want) {
		t.Fatalf("components = %q, want %q", got, want)
	}
	if build.Scripts() != 2 {
		t.Errorf("scripts = %d", build.Scripts())
	}
	title := build.Components()[0]
	if title.Script.ClassName != "TextMeshProUGUI" || title.FileID != tmp.FileID {
		t.Errorf("title script = %+v", title)
	}

	res := build.Apply(patch.Patch{Owner: "Title", Script: tmp, Occurrence: 1, Old: "OK", New: "ОК"}, Options{})
	if res.Err != nil || len(res.Targets) != 1 {
		t.Fatalf("apply = %+v", res)
	}
	if data := string(build.Data(title)); !strings.Contains(data, "ОК") || !strings.Contains(data, "OK") {
		t.Errorf("data after apply = %q", data)
	}
	if res := build.Apply(patch.Patch{Owner: "Title", Old: "missing", New: "x"}, Options{}); res.Err == nil {
		t.Error("apply of a missing string succeeded")
	}
	if build.ByteOrder("level0") != binary.LittleEndian {
		t.Error("unexpected byte order")
	}
}

func TestOccurrence(t *testing.T) {
	data := unitytest.MonoBehaviour(1, 0, 7, "", "OK", "Hello", "OK")
	strs := serialized.ScanStrings(data, 32, binary.LittleEndian)
	if len(strs) != 3 {
		t.Fatalf("strings = %+v", strs)
	}
	for i, want := range []int{0, -1, 1} {
		n, ok := Occurrence(data, strs[i].Offset, "OK", binary.LittleEndian)
		if want < 0 {
			if ok {
				t.Errorf("string %d: found OK at a Hello offset", i)
			}
			continue
		}
		if !ok || n != want {
			t.Errorf("string %d: occurrence = %d, %v; want %d", i, n, ok, want)
		}
		if pos := findOccurrence(data, serialized.EncodeString("OK", binary.LittleEndian), n); pos != strs[i].Offset {
			t.Errorf("string %d: patcher finds occurrence %d at %d", i, n, pos)
		}
	}
	for _, offset := range []int{-4, len(data), len(data) + 8, 4} {
		if _, ok := Occurrence(data, offset, "OK", binary.LittleEndian); ok {
			t.Errorf("offset %d: unexpected match", offset)
		}
	}
}

func TestBundlePath(t *testing.T) {
	dir := t.TempDir()
	if _, err := BundlePath(dir); err == nil {
		t.Error("directory without a bundle accepted")
	}
	if _, err := BundlePath(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing path accepted")
	}
	bundle := filepath.Join(dir, BundleName)
	if err := os.WriteFile(bundle, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, bundle} {
		if got, err := BundlePath(p); err != nil || got != bundle {
			t.Errorf("BundlePath(%s) = %s, %v", p, got, err)
		}
	}

}

func TestPristinePath(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, BundleName)
	backup := bundle + BackupSuffix
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	original := gameBundle()
	write(bundle, original)
	if got, err := PristinePath(bundle); err != nil || got != bundle {
		t.Errorf("without backup = %s, %v", got, err)
	}

	// The state after "patcher -in-place": a patched bundle made from
	// the backup.
	write(backup, original)
	var patched bytes.Buffer
	if _, err := openBundle(t, original).WriteTo(&patched, map[string][]byte{"level0.resS": []byte("new")}); err != nil {
		t.Fatal(err)
	}
	write(bundle, patched.Bytes())
	if got, err := PristinePath(bundle); err != nil || got != backup {
		t.Errorf("patched bundle = %s, %v", got, err)
	}
	// Restoring the backup by hand keeps it usable.
	write(bundle, original)
	if got, err := PristinePath(bundle); err != nil || got != backup {
		t.Errorf("restored bundle = %s, %v", got, err)
	}

	// A game update replaces the bundle with one Unity wrote anew.
	write(bundle, fontBundle())
	if _, err := PristinePath(bundle); !errors.Is(err, ErrStaleBackup) {
		t.Errorf("updated game err = %v", err)
	}

	write(backup, []byte("garbage"))
	if _, err := PristinePath(bundle); err == nil || errors.Is(err, ErrStaleBackup) {
		t.Errorf("broken backup err = %v", err)
	}
	write(backup, original)
	write(bundle, []byte("garbage"))
	if _, err := PristinePath(bundle); err == nil || errors.Is(err, ErrStaleBackup) {
		t.Errorf("broken bundle err = %v", err)
	}
}
