package scriptlayout

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/clr"
	"apocalypter-l10n-tools/internal/unitytest"
)

var le = binary.LittleEndian

func resolver(t *testing.T) *Resolver {
	t.Helper()
	var mods []*clr.Module
	for name, data := range unitytest.ScriptAssemblies() {
		m, err := clr.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mods = append(mods, m)
	}
	return New(mods)
}

// describe renders a layout as "name:kind" lines for comparison.
func describe(n *Node, prefix string, out *[]string) {
	for _, f := range n.Fields {
		path := prefix + f.Name
		switch f.Kind {
		case KindPrimitive:
			s := path + ":" + strconv.Itoa(f.Size)
			if f.Align {
				s += "a"
			}
			*out = append(*out, s)
		case KindString:
			*out = append(*out, path+":str")
		case KindArray:
			*out = append(*out, path+":array")
			if f.Elem.Kind == KindStruct {
				describe(f.Elem, path+"[].", out)
			}
		case KindStruct:
			describe(f, path+".", out)
		}
	}
}

func TestDialogLayout(t *testing.T) {
	r := resolver(t)
	layout, err := r.Script("Assembly-CSharp.dll", "Game", "Dialog")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	describe(layout, "", &got)
	// node nests Game.Node down to Unity's depth limit.
	nodeDepth := 0
	for _, g := range got {
		if strings.HasSuffix(g, "name:str") && strings.HasPrefix(g, "node.") {
			nodeDepth++
		}
	}
	if nodeDepth != maxDepth {
		t.Errorf("node nests %d levels, want %d", nodeDepth, maxDepth)
	}
	got = slices.DeleteFunc(got, func(s string) bool { return strings.HasPrefix(s, "node.children") })
	want := []string{
		"baseName:str", "text:str",
		"item.title:str", "item.count:4", "item.flag:1a",
		"items:array", "items[].title:str", "items[].count:4", "items[].flag:1a",
		"list:array", "list[].title:str", "list[].count:4", "list[].flag:1a",
		"mode:1a", "target:12", "pos.x:4", "pos.y:4", "pos.z:4",
		"curve.m_Curve:array", "curve.m_Curve[].time:4", "curve.m_Curve[].value:4", "curve.m_Curve[].inSlope:4",
		"curve.m_Curve[].outSlope:4", "curve.m_Curve[].weightedMode:4", "curve.m_Curve[].inWeight:4",
		"curve.m_Curve[].outWeight:4", "curve.m_PreInfinity:4", "curve.m_PostInfinity:4", "curve.m_RotationOrder:4",
		"pair.a:str", "pair.b:str", "flags:array", "letter:2a", "node.name:str",
	}
	if !slices.Equal(got, want) {
		t.Errorf("layout:\n got %q\nwant %q", got, want)
	}

	if again, _ := r.Script("Assembly-CSharp", "Game", "Dialog"); again != layout {
		t.Error("layout is not cached")
	}
}

func TestDecode(t *testing.T) {
	r := resolver(t)
	layout, err := r.Script("Assembly-CSharp.dll", "Game", "Dialog")
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 32)
	data := append(header, unitytest.DialogFields("Привет")...)
	strs, err := Decode(layout, data, len(header), le)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, s := range strs {
		paths = append(paths, s.Path+"="+s.Value)
		if string(data[s.Offset+4:s.Offset+4+len(s.Value)]) != s.Value || s.Size%4 != 0 {
			t.Errorf("%s: offset %d size %d do not locate the value", s.Path, s.Offset, s.Size)
		}
	}
	want := []string{
		"baseName=base", "text=Привет", "item.title=Sword", "items[0].title=Shield", "items[1].title=Bow",
		"list[0].title=Potion", "pair.a=left", "pair.b=right", "node.name=root", "node.children[0].name=leaf",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("strings:\n got %q\nwant %q", paths, want)
	}

	cases := map[string][]byte{
		"trailing bytes":  append(slices.Clone(data), 0, 0, 0, 0),
		"truncated":       data[:len(data)-4],
		"huge count":      append(slices.Clone(header), append(unitytest.String("base"), 0xff, 0xff, 0xff, 0x7f)...),
		"string too long": append(slices.Clone(header), 40, 0, 0, 0, 'a'),
		"invalid UTF-8":   append(slices.Clone(header), 2, 0, 0, 0, 0xff, 0xfe, 0, 0),
		"no length":       append(slices.Clone(header), 1, 0),
	}
	for name, d := range cases {
		if _, err := Decode(layout, d, len(header), le); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestScripts(t *testing.T) {
	r := resolver(t)
	fields := func(asm, ns, class string) []string {
		t.Helper()
		layout, err := r.Script(asm, ns, class)
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		var out []string
		describe(layout, "", &out)
		return out
	}
	if got := fields("Assembly-CSharp", "Game", "Settings"); !slices.Equal(got, []string{"title:str"}) {
		t.Errorf("Settings = %q", got)
	}
	for _, name := range []string{"Outer/Inner", "Outer+Inner"} {
		if got := fields("Assembly-CSharp", "Game", name); !slices.Equal(got, []string{"label:str"}) {
			t.Errorf("%s = %q", name, got)
		}
	}
	if got := fields("Assembly-CSharp", "Game", "Derived"); !slices.Equal(got, []string{"value:str"}) {
		t.Errorf("Derived = %q", got)
	}
	links, err := r.Script("Assembly-CSharp", "Game", "Links")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	describe(links, "", &got)
	if want := []string{"other:12", "entry.key:str", "modes:array"}; !slices.Equal(got, want) {
		t.Errorf("Links = %q, want %q", got, want)
	}
	if e := links.Fields[2].Elem; e.Size != 1 || e.Align {
		t.Errorf("enum array element = %+v", e)
	}

	failures := []struct {
		asm, ns, class string
		want           error
	}{
		{"Assembly-CSharp", "Game", "Refs", ErrUnsupported},
		{"Assembly-CSharp", "Game", "Broken", errNotFound},
		{"Assembly-CSharp", "Game", "Missing", errNotFound},
		{"Assembly-CSharp", "Game", "Outer/Missing", errNotFound},
		{"Nope", "Game", "Dialog", errNotFound},
	}
	for _, f := range failures {
		if _, err := r.Script(f.asm, f.ns, f.class); !errors.Is(err, f.want) {
			t.Errorf("%s %s: %v, want %v", f.asm, f.class, err, f.want)
		}
	}
}

func TestBuiltinSizes(t *testing.T) {
	var size func(n *Node) int
	size = func(n *Node) int {
		switch n.Kind {
		case KindPrimitive:
			return n.Size
		case KindStruct:
			s := 0
			for _, f := range n.Fields {
				s += size(f)
				if f.Align {
					s = (s + 3) &^ 3
				}
			}
			return s
		}
		t.Fatalf("%s is not fixed-size", n.Name)
		return 0
	}
	want := map[string]int{
		"UnityEngine.Vector2": 8, "UnityEngine.Vector3": 12, "UnityEngine.Quaternion": 16, "UnityEngine.Color": 16,
		"UnityEngine.Color32": 4, "UnityEngine.Rect": 16, "UnityEngine.Bounds": 24, "UnityEngine.BoundsInt": 24,
		"UnityEngine.Matrix4x4": 64, "UnityEngine.LayerMask": 4, "UnityEngine.Gradient": 168,
		"UnityEngine.RectOffset": 16, "UnityEngine.Hash128": 16, "UnityEngine.Rendering.SphericalHarmonicsL2": 108,
		"UnityEngine.Vector2Int": 8, "UnityEngine.Vector3Int": 12, "UnityEngine.RectInt": 16, "UnityEngine.Vector4": 16,
		"UnityEngine.LazyLoadReference`1": 12,
	}
	for name, n := range want {
		if got := size(builtins[name]()); got != n {
			t.Errorf("%s = %d bytes, want %d", name, got, n)
		}
	}

	// GUIStyle and ExposedReference hold strings and arrays; decode an
	// empty style instead.
	var style []byte
	style = append(style, unitytest.String("box")...)
	for range 8 {
		style = append(style, make([]byte, 12)...)
		style = le.AppendUint32(style, 0)
		style = append(style, make([]byte, 16)...)
	}
	style = append(style, make([]byte, 4*16+12+3*4+4+2*4+8+2*4+4)...)
	root := &Node{Kind: KindStruct, Fields: []*Node{named("style", guiStyle()), named("ref", builtins["UnityEngine.ExposedReference`1"]())}}
	style = append(style, unitytest.String("slot")...)
	style = append(style, make([]byte, 12)...)
	strs, err := Decode(root, style, 0, le)
	if err != nil {
		t.Fatal(err)
	}
	if len(strs) != 2 || strs[0].Path != "style.m_Name" || strs[1].Path != "ref.exposedName" {
		t.Errorf("strings = %+v", strs)
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := LoadDir(dir); err == nil {
		t.Error("empty directory accepted")
	}
	for name, data := range unitytest.ScriptAssemblies() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "native.dll"), []byte("MZ not managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, skipped, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), "native.dll") {
		t.Errorf("skipped = %v", skipped)
	}
	if _, err := r.Script("Assembly-CSharp", "Game", "Dialog"); err != nil {
		t.Error(err)
	}
}

func TestDecodeWithKeepsNumbers(t *testing.T) {
	r := resolver(t)
	layout, err := r.Script("Assembly-CSharp.dll", "Game", "Dialog")
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 32)
	data := append(header, unitytest.DialogFields("Hi")...)
	wanted := map[string]bool{"items[1].count": true, "items[0].flag": true, "flags": true}
	strs, raw, err := DecodeWith(layout, data, len(header), le, func(p string) bool { return wanted[p] })
	if err != nil {
		t.Fatal(err)
	}
	if len(strs) != 10 {
		t.Errorf("strings = %d", len(strs))
	}
	want := map[string][]byte{
		"items[1].count": {2, 0, 0, 0},
		"items[0].flag":  {0},
		"flags":          {1, 0, 1},
	}
	if !reflect.DeepEqual(raw, want) {
		t.Errorf("raw = %v, want %v", raw, want)
	}
	if _, raw, err := DecodeWith(layout, data, len(header), le, nil); err != nil || raw != nil {
		t.Errorf("without keep: %v, %v", raw, err)
	}
}
