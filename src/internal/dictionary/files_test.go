package dictionary

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/po"
)

func sampleEntries() []Entry {
	return []Entry{
		{Old: "Duke Ironjaw", FoundIn: []Location{
			{File: "level0", Path: "m_Text", Owner: "Text", Script: uiText},
			{File: "level0", Path: "m_Text", Owner: "noSaveFile", Script: uiText},
		}},
		{Old: "Line one\n<b>Line two</b>", FoundIn: []Location{{File: "level1", Path: "m_Text", Owner: "Hint", Script: uiText}}},
	}
}

// translate sets msgstr of the message with msgid id.
func translate(t *testing.T, poData []byte, id, str string, flags ...string) []byte {
	t.Helper()
	f, err := po.Parse(poData)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Messages {
		if f.Messages[i].ID == id {
			f.Messages[i].Str, f.Messages[i].Flags = str, flags
			return po.Marshal(f)
		}
	}
	t.Fatalf("no msgid %q", id)
	return nil
}

func TestEncodeDecode(t *testing.T) {
	mapData, poData, obsoleted, err := Encode(sampleEntries(), nil, Header{Project: "Apocalypter", Language: "ru"})
	if err != nil || len(obsoleted) != 0 {
		t.Fatalf("encode: %v, %+v", err, obsoleted)
	}
	// The map holds locations keyed by ID, and no text.
	for _, s := range []string{`"id": "` + ID("Duke Ironjaw") + `"`, `"found_in"`, `"owner": "noSaveFile"`} {
		if !strings.Contains(string(mapData), s) {
			t.Errorf("map lacks %s:\n%s", s, mapData)
		}
	}
	if strings.Contains(string(mapData), "Duke Ironjaw") || strings.Contains(string(mapData), `"old"`) {
		t.Errorf("map holds text:\n%s", mapData)
	}
	for _, s := range []string{"Project-Id-Version: Apocalypter\\n", "Language: ru\\n", "#. level0 | Text | m_Text\n", "msgid \"Duke Ironjaw\"\nmsgstr \"\"\n", "\"<b>Line two</b>\""} {
		if !strings.Contains(string(poData), s) {
			t.Errorf("po lacks %q:\n%s", s, poData)
		}
	}

	entries, fuzzy, err := Decode(mapData, translate(t, poData, "Duke Ironjaw", "Дюк Железнозуб"))
	if err != nil || fuzzy != 0 {
		t.Fatalf("decode: %v, fuzzy %d", err, fuzzy)
	}
	want := sampleEntries()
	want[0].New = "Дюк Железнозуб"
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v\nwant %+v", entries, want)
	}

	// Fuzzy translations are not applied.
	entries, fuzzy, err = Decode(mapData, translate(t, poData, "Duke Ironjaw", "Дюк?", "fuzzy"))
	if err != nil || fuzzy != 1 || entries[0].New != "" {
		t.Errorf("fuzzy: %+v, %d, %v", entries, fuzzy, err)
	}
	// A map record without a message stays untranslated.
	entries, _, err = Decode(mapData, []byte(""))
	if err != nil || len(entries) != 0 {
		t.Errorf("empty po: %+v, %v", entries, err)
	}
}

func TestEncodeKeepsTranslations(t *testing.T) {
	_, poData, _, err := Encode(sampleEntries(), nil, Header{Language: "not a language"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(poData), "Language:") {
		t.Errorf("header with a bad language:\n%s", poData)
	}
	poData = translate(t, poData, "Duke Ironjaw", "Дюк", "fuzzy")
	previous, err := po.Parse(poData)
	if err != nil {
		t.Fatal(err)
	}
	previous.Header.Str += "Last-Translator: someone\n"
	previous.Messages[0].Comments = []string{"keep short"}

	// The string is gone: its translation turns obsolete.
	gone := sampleEntries()[1:]
	_, poData, obsoleted, err := Encode(gone, previous, Header{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if len(obsoleted) != 1 || obsoleted[0].ID != "Duke Ironjaw" {
		t.Errorf("obsoleted = %+v", obsoleted)
	}
	for _, s := range []string{"Last-Translator: someone", "#~ msgid \"Duke Ironjaw\"\n#~ msgstr \"Дюк\"\n"} {
		if !strings.Contains(string(poData), s) {
			t.Errorf("po lacks %q:\n%s", s, poData)
		}
	}

	// The string is back: the obsolete translation returns with its flags
	// and comments, and is not reported again.
	previous, err = po.Parse(poData)
	if err != nil {
		t.Fatal(err)
	}
	_, poData, obsoleted, err = Encode(sampleEntries(), previous, Header{Language: "ru"})
	if err != nil || len(obsoleted) != 0 {
		t.Fatalf("restore: %+v, %v", obsoleted, err)
	}
	f, err := po.Parse(poData)
	if err != nil {
		t.Fatal(err)
	}
	m := f.Messages[0]
	if m.ID != "Duke Ironjaw" || m.Str != "Дюк" || m.Obsolete || !m.Fuzzy() || !reflect.DeepEqual(m.Comments, []string{"keep short"}) {
		t.Errorf("restored = %+v", m)
	}
	if len(f.Messages) != 2 {
		t.Errorf("messages = %+v", f.Messages)
	}
}

func TestLocationComments(t *testing.T) {
	var locs []Location
	for range maxLocationComments + 3 {
		locs = append(locs, Location{File: "level0", Owner: "A", Path: "m_Text"})
	}
	got := locationComments(locs)
	if len(got) != maxLocationComments+1 || got[len(got)-1] != "… +3" {
		t.Errorf("comments = %q", got)
	}
}

func TestDecodeErrors(t *testing.T) {
	mapData, poData, _, err := Encode(sampleEntries(), nil, Header{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	loc := `"found_in": [{"file": "level0", "path": "m_Text", "owner": "A", "script": {}, "occurrence": 0}]`
	cases := map[string]struct{ mapData, poData string }{
		"bad map":        {`{`, string(poData)},
		"no id":          {`[{"id": "", ` + loc + `}]`, ""},
		"repeated id":    {`[{"id": "a", ` + loc + `}, {"id": "a", ` + loc + `}]`, ""},
		"no locations":   {`[{"id": "a", "found_in": []}]`, ""},
		"bad kind":       {`[{"id": "a", "found_in": [{"file": "level0", "path": "m_Text", "owner": "A", "script": {}, "kind": "label"}]}]`, ""},
		"bad po":         {string(mapData), `msgid "a"`},
		"unknown msgid":  {string(mapData), string(poData) + "\nmsgid \"Stranger\"\nmsgstr \"Чужак\"\n"},
		"context":        {string(mapData), "msgctxt \"menu\"\nmsgid \"Duke Ironjaw\"\nmsgstr \"\"\n"},
		"repeated msgid": {string(mapData), string(poData) + "\nmsgid \"Duke Ironjaw\"\nmsgstr \"Дюк\"\n"},
	}
	for name, tc := range cases {
		if _, _, err := Decode([]byte(tc.mapData), []byte(tc.poData)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, _, err := Decode([]byte(`[]`), []byte("msgid \"x\"\nmsgstr \"y\"\n")); !errors.Is(err, ErrFormat) {
		t.Errorf("unknown msgid: %v", err)
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, []byte("[]\n"), []byte("msgid \"\"\nmsgstr \"\"\n")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{MapName, POName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
	if err := Save(filepath.Join(dir, "missing"), nil, nil); err == nil {
		t.Error("save into a missing directory succeeded")
	}
}
