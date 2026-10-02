package gamepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"apocalypter-l10n-tools/internal/clr"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/scriptlayout"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unityfs"
	"apocalypter-l10n-tools/internal/unitytest"
)

const (
	tmpScript    = 7
	dialogScript = 8
)

var tmp = patch.Script{Assembly: "Unity.TextMeshPro.dll", FileID: ScriptFileID("TMPro", "TextMeshProUGUI")}

func gameBundle() []byte {
	scripts := unitytest.Serialized([]unitytest.Object{
		{PathID: tmpScript, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("TextMeshProUGUI", "TMPro", "Unity.TextMeshPro.dll")},
		{PathID: dialogScript, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("Dialog", "", "Assembly-CSharp.dll")},
	}, nil)
	ext := []string{"library/globalgamemanagers.assets"}
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 1, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Title", 2)},
		{PathID: 2, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(1, 1, tmpScript, "", "Hello", "OK", "OK")},
		{PathID: 10, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Button", 11)},
		{PathID: 11, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(10, 1, dialogScript, "", "OK")},
		{PathID: 12, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(0, 1, dialogScript, "Settings", "Welcome")},
		{PathID: 13, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(0, 5, 99, "Orphan", "Lost")},
	}, ext)
	shared := unitytest.Serialized([]unitytest.Object{
		{PathID: 100, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Title", 101)},
		{PathID: 101, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(100, 1, tmpScript, "", "Hello")},
	}, ext)
	return unitytest.Bundle([]unitytest.Node{
		{Path: "globalgamemanagers.assets", Flags: nodeFlagSerialized, Data: scripts},
		{Path: "level0", Flags: nodeFlagSerialized, Data: level},
		{Path: "level0.resS", Data: []byte("texture bytes")},
		{Path: "sharedassets1.assets", Flags: nodeFlagSerialized, Data: shared},
	}, 64)
}

func openBundle(t *testing.T, data []byte) *unityfs.Bundle {
	t.Helper()
	b, err := unityfs.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fields returns the script-defined string fields of a MonoBehaviour.
func fields(t *testing.T, b *unityfs.Bundle, node string, pathID int64) []string {
	t.Helper()
	n, _ := b.Node(node)
	data, err := b.ReadNode(n)
	if err != nil {
		t.Fatal(err)
	}
	f, err := serialized.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := f.Object(pathID)
	obj := f.Data(o)
	h, err := serialized.ReadMonoBehaviourHeader(obj, f.ByteOrder())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for pos := h.FieldsOffset; pos < len(obj); {
		n := int(binary.LittleEndian.Uint32(obj[pos:]))
		out = append(out, string(obj[pos+4:pos+4+n]))
		pos += len(serialized.EncodeString(out[len(out)-1], f.ByteOrder()))
	}
	return out
}

func TestScriptFileID(t *testing.T) {
	// Values taken from a real Unity 2020.3 project.
	cases := map[[2]string]int64{
		{"TMPro", "TextMeshPro"}:   -806885394,
		{"UnityEngine.UI", "Text"}: 708705254,
		{"", "PlayMakerFSM"}:       1384774831,
	}
	for in, want := range cases {
		if got := ScriptFileID(in[0], in[1]); got != want {
			t.Errorf("ScriptFileID(%q, %q) = %d, want %d", in[0], in[1], got, want)
		}
	}
}

func TestApply(t *testing.T) {
	src := gameBundle()
	b := openBundle(t, src)
	patches := []patch.Patch{
		{Owner: "Title", Script: tmp, Old: "Hello", New: "Привет"},
		{Owner: "Title", Script: tmp, Occurrence: 1, Old: "OK", New: "Второй"},
		{Owner: "Button", Script: patch.Script{Class: "Dialog"}, Old: "OK", New: "Да"},
		{Owner: "Button", Old: "Да", New: "Ага"},
		{Owner: "Settings", Script: patch.Script{Class: "Dialog"}, Old: "Welcome", New: ""},
	}
	rep, nodes, err := Apply(b, patches, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	results := rep.Patches
	if len(results[0].Targets) != 2 || len(results[1].Targets) != 1 {
		t.Errorf("targets = %+v / %+v", results[0].Targets, results[1].Targets)
	}
	if len(nodes) != 2 {
		t.Fatalf("changed nodes = %d", len(nodes))
	}

	var out bytes.Buffer
	if err := Write(&out, b, nodes); err != nil {
		t.Fatal(err)
	}
	patched := openBundle(t, out.Bytes())
	checks := []struct {
		node   string
		pathID int64
		want   []string
	}{
		{"level0", 2, []string{"Привет", "OK", "Второй"}},
		{"level0", 11, []string{"Ага"}},
		{"level0", 12, []string{""}},
		{"level0", 13, []string{"Lost"}},
		{"sharedassets1.assets", 101, []string{"Привет"}},
	}
	for _, c := range checks {
		got := fields(t, patched, c.node, c.pathID)
		if len(got) != len(c.want) {
			t.Errorf("%s:%d = %q, want %q", c.node, c.pathID, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s:%d = %q, want %q", c.node, c.pathID, got, c.want)
			}
		}
	}
	res, _ := patched.Node("level0.resS")
	if data, _ := patched.ReadNode(res); string(data) != "texture bytes" {
		t.Errorf("resource node changed: %q", data)
	}
}

func TestApplyFailures(t *testing.T) {
	b := openBundle(t, gameBundle())
	cases := map[string]struct {
		patch patch.Patch
		opts  Options
		want  error
	}{
		"wrong owner":         {patch.Patch{Owner: "Nobody", Old: "Hello", New: "x"}, Options{}, ErrNoMatch},
		"wrong script":        {patch.Patch{Owner: "Title", Script: patch.Script{Class: "Dialog"}, Old: "Hello", New: "x"}, Options{}, ErrNoMatch},
		"wrong file id":       {patch.Patch{Owner: "Title", Script: patch.Script{Assembly: tmp.Assembly, FileID: 1}, Old: "Hello", New: "x"}, Options{}, ErrNoMatch},
		"no occurrence":       {patch.Patch{Owner: "Title", Script: tmp, Occurrence: 2, Old: "OK", New: "x"}, Options{}, ErrNoMatch},
		"negative occurrence": {patch.Patch{Owner: "Title", Occurrence: -1, Old: "OK", New: "x"}, Options{}, ErrNoMatch},
		"strict":              {patch.Patch{Owner: "Title", Script: tmp, Old: "Hello", New: "x"}, Options{Strict: true}, ErrAmbiguous},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ok := patch.Patch{Owner: "Button", Old: "OK", New: "fine"}
			rep, nodes, err := Apply(b, []patch.Patch{ok, tc.patch}, tc.opts)
			if !errors.Is(err, tc.want) || nodes != nil {
				t.Fatalf("err = %v, nodes = %v", err, nodes)
			}
			results := rep.Patches
			if results[0].Err != nil || !errors.Is(results[1].Err, tc.want) {
				t.Errorf("results = %+v", results)
			}
		})
	}
}

func TestApplyRejectsBrokenFiles(t *testing.T) {
	broken := unitytest.Bundle([]unitytest.Node{{Path: "level0", Flags: nodeFlagSerialized, Data: []byte("not serialized")}}, 64)
	if _, _, err := Apply(openBundle(t, broken), nil, Options{}); !errors.Is(err, serialized.ErrFormat) {
		t.Errorf("err = %v", err)
	}

	badMB := unitytest.Serialized([]unitytest.Object{{PathID: 1, ClassID: serialized.ClassMonoBehaviour, Data: []byte{1, 2}}}, nil)
	b := unitytest.Bundle([]unitytest.Node{{Path: "level0", Flags: nodeFlagSerialized, Data: badMB}}, 64)
	if _, _, err := Apply(openBundle(t, b), nil, Options{}); !errors.Is(err, serialized.ErrFormat) {
		t.Errorf("bad MonoBehaviour err = %v", err)
	}

	missingGO := unitytest.Serialized([]unitytest.Object{{PathID: 1, ClassID: serialized.ClassMonoBehaviour, Data: unitytest.MonoBehaviour(50, 0, 0, "")}}, nil)
	b = unitytest.Bundle([]unitytest.Node{{Path: "level0", Flags: nodeFlagSerialized, Data: missingGO}}, 64)
	if _, _, err := Apply(openBundle(t, b), nil, Options{}); err == nil {
		t.Error("expected missing GameObject error")
	}

	badScript := unitytest.Serialized([]unitytest.Object{{PathID: 1, ClassID: serialized.ClassMonoScript, Data: []byte{1}}}, nil)
	b = unitytest.Bundle([]unitytest.Node{{Path: "globalgamemanagers.assets", Flags: nodeFlagSerialized, Data: badScript}}, 64)
	if _, _, err := Apply(openBundle(t, b), nil, Options{}); !errors.Is(err, serialized.ErrFormat) {
		t.Errorf("bad MonoScript err = %v", err)
	}
}

// fsmBundle holds two Game.Dialog components of one GameObject, like two
// FSMs of an item: the first shows "left" as text and also names a pair
// field "left", the second uses "left" only as a pair field.
func fsmBundle() []byte {
	const script = 7
	scripts := unitytest.Serialized([]unitytest.Object{
		{PathID: script, ClassID: serialized.ClassMonoScript, Data: unitytest.MonoScript("Dialog", "Game", "Assembly-CSharp.dll")},
	}, nil)
	dialog := func(text string) []byte {
		return append(unitytest.MonoBehaviour(1, 1, script, ""), unitytest.DialogFields(text)...)
	}
	level := unitytest.Serialized([]unitytest.Object{
		{PathID: 1, ClassID: serialized.ClassGameObject, Data: unitytest.GameObject("Lamp", 2)},
		{PathID: 2, ClassID: serialized.ClassMonoBehaviour, Data: dialog("left")},
		{PathID: 3, ClassID: serialized.ClassMonoBehaviour, Data: dialog("other")},
	}, []string{"library/globalgamemanagers.assets"})
	return unitytest.Bundle([]unitytest.Node{
		{Path: "globalgamemanagers.assets", Flags: nodeFlagSerialized, Data: scripts},
		{Path: "level0", Flags: nodeFlagSerialized, Data: level},
	}, 64)
}

func scriptLayouts(t *testing.T) *scriptlayout.Resolver {
	t.Helper()
	var mods []*clr.Module
	for _, data := range unitytest.ScriptAssemblies() {
		m, err := clr.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, m)
	}
	return scriptlayout.New(mods)
}

// dialogStrings decodes the string fields of a Game.Dialog by path.
func dialogStrings(t *testing.T, layouts *scriptlayout.Resolver, b *unityfs.Bundle, pathID int64) map[string]string {
	t.Helper()
	n, _ := b.Node("level0")
	data, err := b.ReadNode(n)
	if err != nil {
		t.Fatal(err)
	}
	f, err := serialized.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := f.Object(pathID)
	obj := f.Data(o)
	h, err := serialized.ReadMonoBehaviourHeader(obj, f.ByteOrder())
	if err != nil {
		t.Fatal(err)
	}
	layout, err := layouts.Script("Assembly-CSharp.dll", "Game", "Dialog")
	if err != nil {
		t.Fatal(err)
	}
	strs, err := scriptlayout.Decode(layout, obj, h.FieldsOffset, f.ByteOrder())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range strs {
		out[s.Path] = s.Value
	}
	return out
}

func TestApplyMatchesFieldPath(t *testing.T) {
	layouts := scriptLayouts(t)
	dialog := patch.Script{Assembly: "Assembly-CSharp.dll", FileID: ScriptFileID("Game", "Dialog")}
	b := openBundle(t, fsmBundle())
	patches := []patch.Patch{
		{Owner: "Lamp", Script: dialog, Path: "text", Old: "left", New: "Свет"},
		// Occurrence is ignored: the field at the path decides.
		{Owner: "Lamp", Script: dialog, Path: "item.title", Occurrence: 5, Old: "Sword", New: "Меч"},
	}
	rep, nodes, err := Apply(b, patches, Options{Layouts: layouts})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := rep.Patches[0].Targets; len(got) != 1 || got[0].PathID != 2 {
		t.Errorf("text targets = %+v, want only the first component", got)
	}
	if got := rep.Patches[1].Targets; len(got) != 2 {
		t.Errorf("item.title targets = %+v, want both components", got)
	}
	var out bytes.Buffer
	if err := Write(&out, b, nodes); err != nil {
		t.Fatal(err)
	}
	patched := openBundle(t, out.Bytes())
	first, second := dialogStrings(t, layouts, patched, 2), dialogStrings(t, layouts, patched, 3)
	if first["text"] != "Свет" || first["pair.a"] != "left" || first["item.title"] != "Меч" {
		t.Errorf("first component = %q", first)
	}
	if second["text"] != "other" || second["pair.a"] != "left" || second["item.title"] != "Меч" {
		t.Errorf("second component = %q", second)
	}

	missing := patch.Patch{Owner: "Lamp", Script: dialog, Path: "pair.b", Old: "left", New: "x"}
	if _, _, err := Apply(openBundle(t, fsmBundle()), []patch.Patch{missing}, Options{Layouts: layouts}); !errors.Is(err, ErrNoMatch) {
		t.Errorf("value at another path: err = %v", err)
	}
}

func TestApplyMatchesKind(t *testing.T) {
	layouts := scriptLayouts(t)
	dialog := patch.Script{Assembly: "Assembly-CSharp.dll", FileID: ScriptFileID("Game", "Dialog")}
	// Fields of game scripts are "maybe" text.
	p := patch.Patch{Owner: "Lamp", Script: dialog, Path: "text", Kind: "maybe", Old: "left", New: "Свет"}
	if rep, _, err := Apply(openBundle(t, fsmBundle()), []patch.Patch{p}, Options{Layouts: layouts}); err != nil || len(rep.Patches[0].Targets) != 1 {
		t.Errorf("same kind: err = %v, results = %+v", err, rep.Patches)
	}
	p.Kind = "screen"
	if _, _, err := Apply(openBundle(t, fsmBundle()), []patch.Patch{p}, Options{Layouts: layouts}); !errors.Is(err, ErrNoMatch) {
		t.Errorf("other kind: err = %v", err)
	}
	// Without layouts the kind cannot be checked.
	if _, _, err := Apply(openBundle(t, fsmBundle()), []patch.Patch{p}, Options{}); err != nil {
		t.Errorf("no layouts: err = %v", err)
	}
}

func TestApplyFallsBackToOccurrence(t *testing.T) {
	layouts := scriptLayouts(t)
	cases := map[string]struct {
		patch patch.Patch
		opts  Options
	}{
		"no layouts":      {patch.Patch{Owner: "Title", Script: tmp, Path: "m_text", Old: "Hello", New: "Привет"}, Options{}},
		"unknown script":  {patch.Patch{Owner: "Title", Script: tmp, Path: "m_text", Old: "Hello", New: "Привет"}, Options{Layouts: layouts}},
		"heuristic path":  {patch.Patch{Owner: "Title", Path: "str[0]", Old: "Hello", New: "Привет"}, Options{Layouts: layouts}},
		"unresolved name": {patch.Patch{Owner: "Orphan", Path: "m_Name", Old: "Orphan", New: "Сирота"}, Options{Layouts: layouts}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rep, _, err := Apply(openBundle(t, gameBundle()), []patch.Patch{tc.patch}, tc.opts)
			if err != nil || len(rep.Patches[0].Targets) == 0 {
				t.Errorf("err = %v, results = %+v", err, rep.Patches)
			}
		})
	}
}

func TestApplySkipsRepeatedOccurrence(t *testing.T) {
	// Without layouts, patches that differ only by path select the same
	// string; the second must not replace the next "OK".
	b := openBundle(t, gameBundle())
	patches := []patch.Patch{
		{Owner: "Title", Script: tmp, Path: "a", Old: "OK", New: "Да"},
		{Owner: "Title", Script: tmp, Path: "b", Old: "OK", New: "Да"},
	}
	rep, nodes, err := Apply(b, patches, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(rep.Patches[1].Targets) != 0 {
		t.Errorf("second patch targets = %+v", rep.Patches[1].Targets)
	}
	var out bytes.Buffer
	if err := Write(&out, b, nodes); err != nil {
		t.Fatal(err)
	}
	if got := fields(t, openBundle(t, out.Bytes()), "level0", 2); len(got) != 3 || got[1] != "Да" || got[2] != "OK" {
		t.Errorf("fields = %q", got)
	}
}
