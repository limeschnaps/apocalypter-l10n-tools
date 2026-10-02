package clr

import (
	"errors"
	"slices"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

func sampleAssembly() []byte {
	return unitytest.Assembly{
		Name: "Game",
		Types: []unitytest.TypeDef{
			{
				Name:       "Menu.Dialog",
				Flags:      TypeSerializable,
				Extends:    &unitytest.Sig{Elem: unitytest.ElemClass, Name: "[UnityEngine]UnityEngine.MonoBehaviour"},
				Attributes: []string{"[UnityEngine]UnityEngine.AddComponentMenu"},
				Fields: []unitytest.FieldDef{
					{Name: "title", Flags: FieldPublic, Type: unitytest.Prim(unitytest.ElemString)},
					{Name: "hidden", Flags: 1, Type: unitytest.Prim(unitytest.ElemI4), Attributes: []string{"[UnityEngine]UnityEngine.SerializeField", "Menu.Marker"}},
					{Name: "items", Flags: FieldPublic, Type: unitytest.Generic("[mscorlib]System.Collections.Generic.List`1", unitytest.Class("Menu.Dialog/Item"))},
					{Name: "grid", Flags: FieldPublic, Type: unitytest.ArrayOf(unitytest.Struct("[UnityEngine]UnityEngine.Vector3"))},
					{Name: "ref", Flags: FieldPublic, Type: unitytest.Class("[Other]Outer/Inner")},
				},
			},
			{Name: "Menu.Dialog/Item", Flags: TypeSerializable, Fields: []unitytest.FieldDef{{Name: "value", Flags: FieldPublic, Type: unitytest.Var(0)}}},
			{Name: "Menu.Marker", Extends: &unitytest.Sig{Elem: unitytest.ElemClass, Name: "[mscorlib]System.Attribute"}},
			{Name: "Menu.Generic", Extends: &unitytest.Sig{Elem: unitytest.ElemGenericInst, Name: "[mscorlib]System.Collections.Generic.List`1", Args: []unitytest.Sig{unitytest.Prim(unitytest.ElemI4)}}},
		},
		Forwards: []unitytest.Forward{{Name: "UnityEngine.Object", Assembly: "UnityEngine.CoreModule"}},
	}.Build()
}

func TestParse(t *testing.T) {
	m, err := Parse(sampleAssembly())
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Game" {
		t.Errorf("name = %q", m.Name)
	}
	if want := []string{"UnityEngine", "mscorlib", "Other", "UnityEngine.CoreModule"}; !slices.Equal(m.AssemblyRefs, want) {
		t.Errorf("assembly refs = %q, want %q", m.AssemblyRefs, want)
	}
	if len(m.TypeDefs) != 4 {
		t.Fatalf("types = %+v", m.TypeDefs)
	}
	dialog, item := m.TypeDefs[0], m.TypeDefs[1]
	if dialog.Namespace != "Menu" || dialog.Name != "Dialog" || dialog.Flags != TypeSerializable || dialog.Enclosing != -1 {
		t.Errorf("dialog = %+v", dialog)
	}
	if !slices.Equal(dialog.Attributes, []string{"UnityEngine.AddComponentMenu"}) {
		t.Errorf("type attributes = %q", dialog.Attributes)
	}
	if item.Name != "Item" || item.Namespace != "" || item.Enclosing != 0 || len(item.Fields) != 1 {
		t.Errorf("item = %+v", item)
	}
	if len(dialog.Fields) != 5 || dialog.Fields[1].Name != "hidden" || dialog.Fields[1].Flags != 1 {
		t.Fatalf("fields = %+v", dialog.Fields)
	}
	if got := dialog.Fields[1].Attributes; !slices.Equal(got, []string{"UnityEngine.SerializeField", "Menu.Marker"}) {
		t.Errorf("field attributes = %q", got)
	}

	ext := dialog.Extends
	if ext.Table() != TableTypeRef {
		t.Fatalf("extends = %x", uint32(ext))
	}
	base := m.TypeRefs[ext.Row()-1]
	if base.Namespace != "UnityEngine" || base.Name != "MonoBehaviour" || base.Scope != NewToken(TableAssemblyRef, 1) {
		t.Errorf("base = %+v", base)
	}
	if g := m.TypeDefs[3].Extends; g.Table() != TableTypeSpec || len(m.TypeSpecs) != 1 {
		t.Errorf("generic base = %x, specs %d", uint32(g), len(m.TypeSpecs))
	}
	if len(m.ExportedTypes) != 1 || m.ExportedTypes[0].Name != "Object" || m.ExportedTypes[0].Implementation.Table() != TableAssemblyRef {
		t.Errorf("exported = %+v", m.ExportedTypes)
	}

	// The nested reference [Other]Outer/Inner has a TypeRef scope.
	sig, err := ParseFieldSig(dialog.Fields[4].Signature)
	if err != nil {
		t.Fatal(err)
	}
	inner := m.TypeRefs[sig.Token.Row()-1]
	if inner.Name != "Inner" || inner.Scope.Table() != TableTypeRef || m.TypeRefs[inner.Scope.Row()-1].Name != "Outer" {
		t.Errorf("nested ref = %+v", inner)
	}
}

func TestFieldSignatures(t *testing.T) {
	m, err := Parse(sampleAssembly())
	if err != nil {
		t.Fatal(err)
	}
	f := m.TypeDefs[0].Fields
	sig := func(i int) Type {
		t.Helper()
		s, err := ParseFieldSig(f[i].Signature)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := sig(0); s.Kind != ElementString {
		t.Errorf("title = %+v", s)
	}
	list := sig(2)
	if list.Kind != ElementGenericInst || list.Token.Table() != TableTypeRef || len(list.Args) != 1 ||
		list.Args[0].Kind != ElementClass || list.Args[0].Token != NewToken(TableTypeDef, 2) {
		t.Errorf("items = %+v", list)
	}
	grid := sig(3)
	if grid.Kind != ElementSzArray || grid.Elem.Kind != ElementValueType {
		t.Errorf("grid = %+v", grid)
	}
	if s, err := ParseFieldSig(m.TypeDefs[1].Fields[0].Signature); err != nil || s.Kind != ElementVar || s.Index != 0 {
		t.Errorf("var = %+v, %v", s, err)
	}
	spec, err := ParseTypeSpec(m.TypeSpecs[0])
	if err != nil || spec.Kind != ElementGenericInst || spec.Args[0].Kind != ElementI4 {
		t.Errorf("type spec = %+v, %v", spec, err)
	}
}

func TestSignatureEncodings(t *testing.T) {
	cases := []struct {
		name string
		blob []byte
		want ElementType
	}{
		{"modifiers", []byte{0x06, 0x20, 0x05, 0x1f, 0x05, 0x08}, ElementI4},
		{"pinned", []byte{0x06, 0x45, 0x0e}, ElementString},
		{"multi-dimensional array", []byte{0x06, 0x14, 0x08, 0x02, 0x01, 0x03, 0x01, 0x00}, ElementArray},
		{"function pointer", []byte{0x06, 0x1b, 0x00, 0x00, 0x01}, ElementFnPtr},
		{"pointer", []byte{0x06, 0x0f, 0x08}, ElementPtr},
		{"two-byte token", []byte{0x06, 0x12, 0x81, 0x01}, ElementClass},
		{"four-byte token", []byte{0x06, 0x12, 0xc0, 0x01, 0x00, 0x01}, ElementClass},
	}
	for _, tc := range cases {
		s, err := ParseFieldSig(tc.blob)
		if err != nil || s.Kind != tc.want {
			t.Errorf("%s: %+v, %v", tc.name, s, err)
		}
	}
	if s, _ := ParseFieldSig([]byte{0x06, 0x12, 0x81, 0x01}); s.Token != NewToken(TableTypeRef, 0x40) {
		t.Errorf("two-byte token = %x", uint32(s.Token))
	}

	bad := map[string][]byte{
		"empty":             nil,
		"not a field":       {0x07, 0x08},
		"truncated":         {0x06},
		"unknown element":   {0x06, 0x99},
		"bad token tag":     {0x06, 0x12, 0x03},
		"truncated token":   {0x06, 0x12, 0x81},
		"truncated generic": {0x06, 0x15},
		"huge arg count":    {0x06, 0x15, 0x12, 0x01, 0x7f},
		"missing elem":      {0x06, 0x1d},
	}
	for name, blob := range bad {
		if _, err := ParseFieldSig(blob); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
	deep := []byte{0x06}
	for range maxSigDepth + 2 {
		deep = append(deep, 0x1d)
	}
	if _, err := ParseFieldSig(append(deep, 0x08)); !errors.Is(err, ErrFormat) {
		t.Errorf("deep nesting: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	good := sampleAssembly()
	if _, err := Parse([]byte("garbage")); !errors.Is(err, ErrFormat) {
		t.Errorf("garbage: %v", err)
	}
	// Corrupting the metadata signature, which follows the 72-byte CLI
	// header at the start of the only section.
	broken := append([]byte(nil), good...)
	broken[0x200+72] ^= 0xff
	if _, err := Parse(broken); !errors.Is(err, ErrFormat) {
		t.Errorf("bad signature: %v", err)
	}
	// Truncating the file cuts the section the CLI header points into.
	if _, err := Parse(good[:0x200+80]); err == nil {
		t.Error("truncated assembly parsed")
	}
}

func TestTokens(t *testing.T) {
	tok := NewToken(TableTypeDef, 42)
	if tok.Table() != TableTypeDef || tok.Row() != 42 {
		t.Errorf("token = %x", uint32(tok))
	}
	if decodeCoded(colCustomAttributeType, 0) != 0 || decodeCoded(colCustomAttributeType, 1<<3|0) != 0 {
		t.Error("unused coded tags must decode to zero")
	}
	if got := decodeCoded(colHasCustomAttribute, 5<<5|3); got != NewToken(TableTypeDef, 5) {
		t.Errorf("coded = %x", uint32(got))
	}
}
