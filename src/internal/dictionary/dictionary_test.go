package dictionary

import (
	"errors"
	"reflect"
	"testing"

	"apocalypter-l10n-tools/internal/patch"
)

var uiText = patch.Script{Assembly: "UnityEngine.UI.dll", FileID: 708705254}

func found(file, path, owner string, occurrence int, old string) patch.Patch {
	return patch.Patch{File: file, Path: path, Owner: owner, Script: uiText, Occurrence: occurrence, Old: old}
}

func TestBuild(t *testing.T) {
	strs := []patch.Patch{
		found("level1", "m_Text", "Label", 0, "Nuts"),
		found("level0", "m_Text", "Label", 0, "Nuts"),
		found("level0", "m_Text", "Label", 0, "Nuts"),
		found("level0", "m_Text", "Title", 0, "Duke"),
	}
	want := []Entry{
		{Old: "Duke", FoundIn: []Location{{File: "level0", Path: "m_Text", Owner: "Title", Script: uiText}}},
		{Old: "Nuts", FoundIn: []Location{
			{File: "level0", Path: "m_Text", Owner: "Label", Script: uiText},
			{File: "level1", Path: "m_Text", Owner: "Label", Script: uiText},
		}},
	}
	if got := Build(strs); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %+v\nwant %+v", got, want)
	}
}

func TestPatches(t *testing.T) {
	entries := []Entry{
		{Old: "Nuts", New: "Гайки", FoundIn: []Location{
			{File: "level0", Path: "m_Text", Owner: "Label", Script: uiText},
			// The patcher ignores file and path: this one repeats the first.
			{File: "level1", Path: "m_Text", Owner: "Label", Script: uiText},
			{File: "level0", Path: "str[3]", Owner: "Label", Script: uiText, Occurrence: 1},
			{File: "level0", Path: "m_Text", Owner: "Title", Script: uiText},
		}},
		{Old: "Duke", FoundIn: []Location{{File: "level0", Path: "m_Text", Owner: "Title", Script: uiText}}},
		{Old: "Same", New: "Same", FoundIn: []Location{{File: "level0", Path: "m_Text", Owner: "Title", Script: uiText}}},
	}
	got, err := Patches(entries)
	if err != nil {
		t.Fatal(err)
	}
	want := []patch.Patch{
		{File: "level0", Path: "str[3]", Owner: "Label", Script: uiText, Occurrence: 1, Old: "Nuts", New: "Гайки"},
		{File: "level0", Path: "m_Text", Owner: "Label", Script: uiText, Old: "Nuts", New: "Гайки"},
		{File: "level0", Path: "m_Text", Owner: "Title", Script: uiText, Old: "Nuts", New: "Гайки"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("patches = %+v\nwant %+v", got, want)
	}
}

func TestPatchesRejectsChainedTranslation(t *testing.T) {
	loc := Location{File: "level0", Path: "m_Text", Owner: "Button", Script: uiText}
	entries := []Entry{
		{Old: "Ok", New: "OK", FoundIn: []Location{loc}},
		{Old: "OK", New: "Ок", FoundIn: []Location{{File: "level1", Path: "m_Text", Owner: "Button", Script: uiText, Occurrence: 2}}},
	}
	if _, err := Patches(entries); !errors.Is(err, ErrFormat) {
		t.Errorf("chained translation: %v", err)
	}
	// Different components cannot interfere.
	entries[1].FoundIn[0].Owner = "Other"
	if got, err := Patches(entries); err != nil || len(got) != 2 {
		t.Errorf("separate components = %+v, %v", got, err)
	}
}
