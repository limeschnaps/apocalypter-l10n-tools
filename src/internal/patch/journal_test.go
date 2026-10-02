package patch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAppendAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patches.json")
	got, err := Load(path)
	if err != nil || got != nil {
		t.Fatalf("missing journal = %v, %v", got, err)
	}
	want := []Patch{
		{File: "a.prefab", Path: "m_text", Owner: "Title", Script: Script{Assembly: "Unity.TextMeshPro.dll", FileID: -806885394}, Old: "Hi", New: "Привет"},
		{File: "b.unity", Path: "items[1]", Owner: "List", Script: Script{Class: "Menu"}, Occurrence: 2, Old: "x", New: ""},
	}
	for _, p := range want {
		if err := Append(path, p); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err = Load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Load = %+v, %v", got, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(leftovers) != 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"garbage": "{",
		"version": `{"version": 2, "patches": []}`,
		"kind":    `{"version": 1, "patches": [{"kind": "label"}]}`,
	}
	for name, content := range cases {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
		if err := Append(path, Patch{}); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: Append err = %v", name, err)
		}
	}
	if _, err := Load(dir); err == nil || errors.Is(err, ErrFormat) {
		t.Errorf("directory err = %v", err)
	}
	if err := Append(filepath.Join(dir, "missing", "p.json"), Patch{}); err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	patches := []Patch{
		{File: "level0", Path: "m_Text", Owner: "Title", Script: Script{Class: "Menu"}, Occurrence: 1, Old: "OK", New: "Ок"},
	}
	data, err := Marshal(patches)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil || len(got) != 1 || got[0] != patches[0] {
		t.Errorf("round trip = %+v, %v", got, err)
	}
	empty, err := Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Parse(empty); err != nil || len(got) != 0 {
		t.Errorf("empty journal = %+v, %v", got, err)
	}
}
