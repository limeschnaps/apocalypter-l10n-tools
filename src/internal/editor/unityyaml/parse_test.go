package unityyaml

import (
	"errors"
	"strings"
	"testing"
)

const prefabSrc = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!1 &1001
GameObject:
  m_ObjectHideFlags: 0
  m_Component:
  - component: {fileID: 2001}
  m_Layer: 5
  m_Name: "\u041A\u043D\u043E\u043F\u043A\u0430"
  m_IsActive: 1
--- !u!224 &1500
RectTransform:
  m_GameObject: {fileID: 1001}
--- !u!114 &2001
MonoBehaviour:
  m_ObjectHideFlags: 0
  m_GameObject: {fileID: 1001}
  m_Enabled: 1
  m_Script: {fileID: 11500000, guid: F4688FDB7DF04437AEB418B961361DC5, type: 3}
  m_Name:
  m_EditorClassIdentifier:
  m_text: "\u041F\u0440\u0438\u0432\u0435\u0442 \u043C\u0438\u0440 \u043A\u0430\u043A
    \u0434\u0435\u043B\u0430\n\n\u0412\u0442\u043E\u0440\u0430\u044F"
  m_plain: Hello world
    continued here

    after blank
  m_single: 'it''s
    folded'
  m_color: {r: 1, g: 1,
    b: 1, a: 1}
  m_empty:
  m_items:
  - title: first
    tags:
    - red
    - green
  - title: second
  m_indented:
    - one
    - 'two'
  m_nested:
    inner: value
  m_list:
  -
    deep: x
  m_names:
  - a
  -
  - c
--- !u!114 &-2002
MonoBehaviour:
  m_GameObject: {fileID: 0}
  m_Script: {fileID: 11500000, guid: 0123456789abcdef0123456789abcdef, type: 3}
  m_last: tail
`

// prefab restores the trailing spaces Unity writes after empty values;
// editors tend to strip them from source files.
var prefab = strings.NewReplacer(
	"  m_Name:\n", "  m_Name: \n",
	"  m_EditorClassIdentifier:\n", "  m_EditorClassIdentifier: \n",
).Replace(prefabSrc)

func fieldsByPath(mb MonoBehaviour) map[string]Field {
	out := map[string]Field{}
	for _, f := range mb.Fields {
		out[f.Path] = f
	}
	return out
}

func TestParsePrefab(t *testing.T) {
	asset, err := Parse([]byte(prefab))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := asset.GameObjectNames[1001]; got != "Кнопка" {
		t.Errorf("GameObject name = %q", got)
	}
	if len(asset.MonoBehaviours) != 2 {
		t.Fatalf("got %d MonoBehaviours, want 2", len(asset.MonoBehaviours))
	}

	mb := asset.MonoBehaviours[0]
	if mb.ID != 2001 || mb.GameObjectID != 1001 || mb.ScriptFileID != 11500000 {
		t.Errorf("ids = %d/%d/%d", mb.ID, mb.GameObjectID, mb.ScriptFileID)
	}
	if mb.ScriptGUID != "f4688fdb7df04437aeb418b961361dc5" {
		t.Errorf("guid = %q", mb.ScriptGUID)
	}

	want := map[string]string{
		"m_ObjectHideFlags":       "0",
		"m_Enabled":               "1",
		"m_Name":                  "",
		"m_EditorClassIdentifier": "",
		"m_text":                  "Привет мир как дела\n\nВторая",
		"m_plain":                 "Hello world continued here\nafter blank",
		"m_single":                "it's folded",
		"m_empty":                 "",
		"m_items[0].title":        "first",
		"m_items[0].tags[0]":      "red",
		"m_items[0].tags[1]":      "green",
		"m_items[1].title":        "second",
		"m_indented[0]":           "one",
		"m_indented[1]":           "two",
		"m_nested.inner":          "value",
		"m_list[0].deep":          "x",
		"m_names[0]":              "a",
		"m_names[1]":              "",
		"m_names[2]":              "c",
	}
	got := fieldsByPath(mb)
	for path, value := range want {
		f, ok := got[path]
		if !ok {
			t.Errorf("missing field %s", path)
			continue
		}
		if f.Value != value {
			t.Errorf("%s = %q, want %q", path, f.Value, value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d fields, want %d: %v", len(got), len(want), got)
	}

	second := fieldsByPath(asset.MonoBehaviours[1])
	if asset.MonoBehaviours[1].ID != -2002 || second["m_last"].Value != "tail" {
		t.Errorf("second MonoBehaviour = %+v", asset.MonoBehaviours[1])
	}
}

func TestFieldRanges(t *testing.T) {
	asset, err := Parse([]byte(prefab))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := fieldsByPath(asset.MonoBehaviours[0])
	cases := map[string]string{
		"m_Name":             " ",
		"m_empty":            "",
		"m_plain":            " Hello world\n    continued here\n\n    after blank",
		"m_single":           " 'it''s\n    folded'",
		"m_items[0].tags[1]": " green",
		"m_indented[1]":      " 'two'",
	}
	for path, raw := range cases {
		f := got[path]
		if r := prefab[f.Start:f.End]; r != raw {
			t.Errorf("%s raw = %q, want %q", path, r, raw)
		}
	}
	if f := got["m_text"]; !strings.HasPrefix(prefab[f.Start:f.End], ` "\u041F`) || !strings.HasSuffix(prefab[f.Start:f.End], `"`) {
		t.Errorf("m_text raw = %q", prefab[f.Start:f.End])
	}
	if l := got["m_plain"].Line; l != 24 {
		t.Errorf("m_plain line = %d, want 24", l)
	}
}

func TestParseReplaceRoundTrip(t *testing.T) {
	values := []string{
		"", "plain", "Новый текст", "line1\nline2", "  padded  ", `quote " and \ slash`,
		"emoji 😀", "true", "- dash", "a: b", "tab\tand\rcr", "trailing:", "\x01ctrl",
	}
	for _, v := range values {
		asset, err := Parse([]byte(prefab))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		f := fieldsByPath(asset.MonoBehaviours[0])["m_text"]
		edited := prefab[:f.Start] + " " + Encode(v) + prefab[f.End:]

		again, err := Parse([]byte(edited))
		if err != nil {
			t.Fatalf("Parse edited %q: %v", v, err)
		}
		fields := fieldsByPath(again.MonoBehaviours[0])
		if got := fields["m_text"].Value; got != v {
			t.Errorf("round trip %q -> %q", v, got)
		}
		if got := fields["m_plain"].Value; got != "Hello world continued here\nafter blank" {
			t.Errorf("neighbour field changed after editing %q: %q", v, got)
		}
	}
}

func TestParseCRLF(t *testing.T) {
	data := strings.ReplaceAll(prefab, "\n", "\r\n")
	asset, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := fieldsByPath(asset.MonoBehaviours[0])
	if v := got["m_text"].Value; v != "Привет мир как дела\n\nВторая" {
		t.Errorf("m_text = %q", v)
	}
	if v := got["m_plain"].Value; v != "Hello world continued here\nafter blank" {
		t.Errorf("m_plain = %q", v)
	}
	if v := got["m_single"].Value; v != "it's folded" {
		t.Errorf("m_single = %q", v)
	}
}

func TestParseErrors(t *testing.T) {
	head := "%YAML 1.1\n--- !u!114 &1\nMonoBehaviour:\n"
	cases := map[string]struct {
		data string
		want error
	}{
		"binary":           {"\x00\x01binary", ErrNotText},
		"bad header":       {"%YAML 1.1\n--- !u!x &1\n", ErrSyntax},
		"bad id":           {"%YAML 1.1\n--- !u!114 &abc\n", ErrSyntax},
		"short header":     {"%YAML 1.1\n--- !u!114\n", ErrSyntax},
		"orphan item":      {head + "- item\n", ErrSyntax},
		"not a key":        {head + "  just text\n", ErrSyntax},
		"unterminated":     {head + "  a: \"open\n", ErrSyntax},
		"unterminated one": {head + "  a: 'open\n--- !u!1 &2\nGameObject:\n  m_Name: x'\n", ErrSyntax},
		"bad escape":       {head + "  a: \"\\q\"\n", ErrSyntax},
		"bad hex":          {head + "  a: \"\\uZZZZ\"\n", ErrSyntax},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.data)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseSkipsUnsupportedValues(t *testing.T) {
	data := "%YAML 1.1\n--- !u!114 &1\nMonoBehaviour:\n" +
		"  # comment\n" +
		"  a: |\n    block\n" +
		"  b: &anchor x\n" +
		"  c: [1, 2]\n" +
		"  refs:\n" +
		"  - {fileID: 4703850765261885, guid: 7a3a6, type: 3}\n" +
		"  - {fileID: 1,\n" +
		"      guid: 2}\n" +
		"  - [1, 2]\n" +
		"  - &a x\n" +
		"  d: ok\n" +
		"--- !u!114 &2\n"
	asset, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fields := asset.MonoBehaviours[0].Fields
	if len(fields) != 1 || fields[0].Path != "d" || fields[0].Value != "ok" {
		t.Errorf("fields = %+v", fields)
	}
	if len(asset.MonoBehaviours) != 2 || len(asset.MonoBehaviours[1].Fields) != 0 {
		t.Errorf("empty trailing document not handled: %+v", asset.MonoBehaviours)
	}
}

func TestEmptySequenceItem(t *testing.T) {
	asset, err := Parse([]byte(prefab))
	if err != nil {
		t.Fatal(err)
	}
	f := fieldsByPath(asset.MonoBehaviours[0])["m_names[1]"]
	if raw := prefab[f.Start:f.End]; raw != "" {
		t.Fatalf("raw = %q", raw)
	}
	edited := prefab[:f.Start] + " " + Encode("b") + prefab[f.End:]
	again, err := Parse([]byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	got := fieldsByPath(again.MonoBehaviours[0])
	if got["m_names[0]"].Value != "a" || got["m_names[1]"].Value != "b" || got["m_names[2]"].Value != "c" {
		t.Errorf("after edit: %q %q %q", got["m_names[0]"].Value, got["m_names[1]"].Value, got["m_names[2]"].Value)
	}
}
