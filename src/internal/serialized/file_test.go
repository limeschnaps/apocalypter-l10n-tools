package serialized

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

var le = binary.LittleEndian

func sample() []unitytest.Object {
	return []unitytest.Object{
		{PathID: 1, ClassID: ClassGameObject, Data: unitytest.GameObject("Title", 2)},
		{PathID: 2, ClassID: ClassMonoBehaviour, Data: unitytest.MonoBehaviour(1, 1, 7, "", "Hello", "World")},
		{PathID: 3, ClassID: ClassMonoBehaviour, Data: unitytest.MonoBehaviour(0, 1, 7, "Settings", "x")},
		{PathID: -5, ClassID: ClassMonoScript, Data: unitytest.MonoScript("TextMeshProUGUI", "TMPro", "Unity.TextMeshPro.dll")},
	}
}

func parse(t *testing.T, data []byte) *File {
	t.Helper()
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestParseAndReadObjects(t *testing.T) {
	f := parse(t, unitytest.Serialized(sample(), []string{"archive:/globalgamemanagers.assets"}))
	if f.Version != 22 || f.UnityVersion != "2020.3.49f1" || f.Platform != 19 || f.TypeTree || !f.LittleEndian {
		t.Errorf("file = %+v", f)
	}
	if len(f.Objects) != 4 || len(f.Types) != 3 || len(f.Externals) != 1 || f.Externals[0].Path != "archive:/globalgamemanagers.assets" {
		t.Fatalf("tables: %d objects, %d types, %+v", len(f.Objects), len(f.Types), f.Externals)
	}

	goObj, _ := f.Object(1)
	name, err := ReadGameObjectName(f.Data(goObj), f.ByteOrder())
	if err != nil || name != "Title" {
		t.Errorf("GameObject name = %q, %v", name, err)
	}

	mbObj, ok := f.Object(2)
	if !ok || mbObj.ClassID != ClassMonoBehaviour {
		t.Fatalf("object 2 = %+v", mbObj)
	}
	h, err := ReadMonoBehaviourHeader(f.Data(mbObj), f.ByteOrder())
	if err != nil {
		t.Fatal(err)
	}
	if h.GameObject != (PPtr{0, 1}) || !h.Enabled || h.Script != (PPtr{1, 7}) || h.Name != "" || h.FieldsOffset != 32 {
		t.Errorf("header = %+v", h)
	}

	msObj, _ := f.Object(-5)
	ms, err := ReadMonoScript(f.Data(msObj), f.ByteOrder())
	want := MonoScript{Name: "TextMeshProUGUI", ClassName: "TextMeshProUGUI", Namespace: "TMPro", AssemblyName: "Unity.TextMeshPro.dll"}
	if err != nil || ms != want {
		t.Errorf("MonoScript = %+v, %v", ms, err)
	}
	if _, ok := f.Object(99); ok {
		t.Error("found missing object")
	}
}

func TestReadObjectErrors(t *testing.T) {
	if _, err := ReadMonoBehaviourHeader([]byte{1, 2, 3}, le); !errors.Is(err, ErrFormat) {
		t.Errorf("MonoBehaviour err = %v", err)
	}
	if _, err := ReadGameObjectName([]byte{0, 0, 0, 0}, le); !errors.Is(err, ErrFormat) {
		t.Errorf("GameObject err = %v", err)
	}
	if _, err := ReadMonoScript(unitytest.String("x"), le); !errors.Is(err, ErrFormat) {
		t.Errorf("MonoScript err = %v", err)
	}
	bad := unitytest.MonoBehaviour(0, 0, 0, "\xff\xfe")
	if _, err := ReadMonoBehaviourHeader(bad, le); !errors.Is(err, ErrFormat) {
		t.Errorf("invalid UTF-8 err = %v", err)
	}
	huge := append(make([]byte, 28), 0xff, 0xff, 0xff, 0x7f)
	if _, err := ReadMonoBehaviourHeader(huge, le); !errors.Is(err, ErrFormat) {
		t.Errorf("huge length err = %v", err)
	}
}

func TestEncodeString(t *testing.T) {
	cases := map[string]int{"": 4, "a": 8, "abcd": 8, "Привет": 16}
	for s, size := range cases {
		enc := EncodeString(s, le)
		if len(enc) != size || int(le.Uint32(enc)) != len(s) || !bytes.Equal(enc, unitytest.String(s)) {
			t.Errorf("EncodeString(%q) = %v", s, enc)
		}
	}
}

func TestRewrite(t *testing.T) {
	data := unitytest.Serialized(sample(), nil)
	f := parse(t, data)

	same, err := f.Rewrite(nil)
	if err != nil || !bytes.Equal(same, data) {
		t.Fatalf("identity rewrite differs: %v", err)
	}

	cases := map[string]map[int64][]byte{
		"grow":     {2: unitytest.MonoBehaviour(1, 1, 7, "", "Hello, this is much longer", "World")},
		"shrink":   {2: unitytest.MonoBehaviour(1, 1, 7, "", "", "")},
		"last":     {-5: unitytest.MonoScript("A", "", "B.dll")},
		"multiple": {1: unitytest.GameObject("Renamed title", 2), 3: unitytest.MonoBehaviour(0, 1, 7, "S", "yyyyyyyyy")},
	}
	for name, replace := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := f.Rewrite(replace)
			if err != nil {
				t.Fatal(err)
			}
			g := parse(t, out)
			for _, o := range f.Objects {
				n, ok := g.Object(o.PathID)
				if !ok || n.ClassID != o.ClassID {
					t.Fatalf("object %d lost", o.PathID)
				}
				if (n.Offset-o.Offset)%8 != 0 {
					t.Errorf("object %d moved by %d bytes", o.PathID, n.Offset-o.Offset)
				}
				want := f.Data(o)
				if r, ok := replace[o.PathID]; ok {
					want = r
				}
				if !bytes.Equal(g.Data(n), want) {
					t.Errorf("object %d data differs", o.PathID)
				}
			}
			// Metadata before the object table never changes.
			tableStart := f.Objects[0].startPos
			if !bytes.Equal(out[48:tableStart], data[48:tableStart]) {
				t.Error("metadata before the object table changed")
			}
		})
	}

	if _, err := f.Rewrite(map[int64][]byte{42: nil}); !errors.Is(err, ErrFormat) {
		t.Errorf("unknown object err = %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	good := unitytest.Serialized(sample(), nil)
	mutate := func(f func([]byte) []byte) []byte { return f(bytes.Clone(good)) }
	cases := map[string]struct {
		data []byte
		want error
	}{
		"short":        {good[:10], ErrFormat},
		"short large":  {good[:30], ErrFormat},
		"old version":  {mutate(func(d []byte) []byte { binary.BigEndian.PutUint32(d[8:], 9); return d }), ErrUnsupported},
		"size":         {good[:len(good)-1], ErrFormat},
		"meta too big": {mutate(func(d []byte) []byte { binary.BigEndian.PutUint32(d[20:], 1<<20); return d }), ErrFormat},
		"type index": {mutate(func(d []byte) []byte {
			f, _ := Parse(d)
			le.PutUint32(d[f.Objects[0].startPos+12:], 99)
			return d
		}), ErrFormat},
		"object range": {mutate(func(d []byte) []byte {
			f, _ := Parse(d)
			le.PutUint32(d[f.Objects[0].startPos+8:], 1<<20)
			return d
		}), ErrFormat},
		"truncated metadata": {mutate(func(d []byte) []byte {
			binary.BigEndian.PutUint32(d[20:], 20)
			return d
		}), ErrFormat},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(tc.data); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLegacyHeader(t *testing.T) {
	// Convert a version 22 file into the version 21 layout: a 20-byte
	// header with 32-bit fields and 32-bit object offsets.
	v22 := unitytest.Serialized(sample(), nil)
	f := parse(t, v22)
	meta := v22[48:f.dataOffset]
	var m bytes.Buffer
	prev := 0
	for _, o := range f.Objects {
		start := o.startPos - 48
		m.Write(meta[prev:start])
		m.Write(le.AppendUint32(nil, uint32(o.Offset-f.dataOffset)))
		prev = start + 8
	}
	m.Write(meta[prev:])
	dataOffset := (20 + m.Len() + 15) / 16 * 16
	out := make([]byte, dataOffset)
	binary.BigEndian.PutUint32(out[0:], uint32(m.Len()))
	binary.BigEndian.PutUint32(out[8:], 21)
	binary.BigEndian.PutUint32(out[12:], uint32(dataOffset))
	copy(out[20:], m.Bytes())
	out = append(out, v22[f.dataOffset:]...)
	binary.BigEndian.PutUint32(out[4:], uint32(len(out)))

	g := parse(t, out)
	if g.Version != 21 || len(g.Objects) != len(f.Objects) {
		t.Fatalf("v21 file = %+v", g)
	}
	replacement := unitytest.MonoBehaviour(1, 1, 7, "", "longer text here", "World")
	rewritten, err := g.Rewrite(map[int64][]byte{2: replacement})
	if err != nil {
		t.Fatal(err)
	}
	h := parse(t, rewritten)
	for _, o := range g.Objects {
		n, _ := h.Object(o.PathID)
		want := g.Data(o)
		if o.PathID == 2 {
			want = replacement
		}
		if !bytes.Equal(h.Data(n), want) {
			t.Errorf("v21 object %d differs after rewrite", o.PathID)
		}
	}
}
