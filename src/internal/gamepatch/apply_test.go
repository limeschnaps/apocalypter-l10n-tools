package gamepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"apocalypter-l10n-tools/internal/patch"
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
