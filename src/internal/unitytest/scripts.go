package unitytest

// Type and field flags used by the fixture assemblies.
const (
	flagSerializable = 0x2000
	flagInterface    = 0xa0
	fieldPrivate     = 0x0001
	fieldPublic      = 0x0006
	fieldStatic      = 0x0010
	fieldInitOnly    = 0x0020
	fieldLiteral     = 0x0040
	fieldNotSerial   = 0x0080
	fieldEnumValue   = 0x0606
)

func extends(name string) *Sig {
	s := Class(name)
	return &s
}

func public(name string, t Sig) FieldDef { return FieldDef{Name: name, Flags: fieldPublic, Type: t} }

// ScriptAssemblies returns the assemblies of a minimal Mono player build,
// keyed by file name: mscorlib, UnityEngine.CoreModule, the UnityEngine
// facade that forwards to it, and Assembly-CSharp with these scripts:
//
//   - Game.Dialog: a MonoBehaviour (via Game.Base) with fields of every
//     kind the layout rules distinguish.
//   - Game.Settings: a ScriptableObject with one string.
//   - Game.Outer/Inner: a nested MonoBehaviour with one string.
//   - Game.Derived: a MonoBehaviour whose generic base declares the string
//     field "value".
//   - Game.Links: a MonoBehaviour that references Game.Derived, a nested
//     type of UnityEngine.CoreModule and an array of enums.
//   - Game.Refs: a MonoBehaviour with a [SerializeReference] field.
//   - Game.Broken: a MonoBehaviour whose field type cannot be resolved.
func ScriptAssemblies() map[string][]byte {
	const obj, valueType = "[mscorlib]System.Object", "[mscorlib]System.ValueType"
	mscorlib := Assembly{Name: "mscorlib", Types: []TypeDef{
		{Name: "System.Object"},
		{Name: "System.ValueType", Extends: extends("System.Object")},
		{Name: "System.Enum", Extends: extends("System.ValueType")},
		{Name: "System.Attribute", Extends: extends("System.Object")},
		{Name: "System.Delegate", Extends: extends("System.Object")},
		{Name: "System.MulticastDelegate", Extends: extends("System.Delegate")},
		{Name: "System.Action", Flags: flagSerializable, Extends: extends("System.MulticastDelegate")},
		{Name: "System.DateTime", Flags: flagSerializable, Extends: extends("System.ValueType")},
		{Name: "System.Collections.Generic.List`1", Flags: flagSerializable, Extends: extends("System.Object")},
		{Name: "System.Collections.Generic.Dictionary`2", Flags: flagSerializable, Extends: extends("System.Object")},
	}}
	core := Assembly{Name: "UnityEngine.CoreModule", Types: []TypeDef{
		{Name: "UnityEngine.Object", Extends: extends(obj)},
		{Name: "UnityEngine.Component", Extends: extends("UnityEngine.Object")},
		{Name: "UnityEngine.Behaviour", Extends: extends("UnityEngine.Component")},
		{Name: "UnityEngine.MonoBehaviour", Extends: extends("UnityEngine.Behaviour")},
		{Name: "UnityEngine.ScriptableObject", Extends: extends("UnityEngine.Object")},
		{Name: "UnityEngine.GameObject", Extends: extends("UnityEngine.Object")},
		{Name: "UnityEngine.SerializeField", Extends: extends("[mscorlib]System.Attribute")},
		{Name: "UnityEngine.SerializeReference", Extends: extends("[mscorlib]System.Attribute")},
		{Name: "UnityEngine.Vector3", Flags: flagSerializable, Extends: extends(valueType)},
		{Name: "UnityEngine.AnimationCurve", Flags: flagSerializable, Extends: extends(obj)},
		{Name: "UnityEngine.Gradient", Extends: extends(obj)},
		{Name: "UnityEngine.Events.Holder", Extends: extends(obj)},
		{Name: "UnityEngine.Events.Holder/Entry", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{public("key", Prim(ElemString))}},
	}}
	facade := Assembly{Name: "UnityEngine", Forwards: []Forward{
		{Name: "UnityEngine.MonoBehaviour", Assembly: "UnityEngine.CoreModule"},
		{Name: "UnityEngine.ScriptableObject", Assembly: "UnityEngine.CoreModule"},
		{Name: "UnityEngine.GameObject", Assembly: "UnityEngine.CoreModule"},
		{Name: "UnityEngine.SerializeField", Assembly: "UnityEngine.CoreModule"},
		{Name: "UnityEngine.SerializeReference", Assembly: "UnityEngine.CoreModule"},
		{Name: "UnityEngine.Vector3", Assembly: "UnityEngine.CoreModule"},
		{Name: "UnityEngine.AnimationCurve", Assembly: "UnityEngine.CoreModule"},
	}}

	const mono = "[UnityEngine]UnityEngine.MonoBehaviour"
	str := Prim(ElemString)
	list := func(of Sig) Sig { return Generic("[mscorlib]System.Collections.Generic.List`1", of) }
	game := Assembly{Name: "Assembly-CSharp", Types: []TypeDef{
		{Name: "Game.Mode", Extends: extends("[mscorlib]System.Enum"), Fields: []FieldDef{
			{Name: "value__", Flags: fieldEnumValue, Type: Prim(ElemU1)},
			{Name: "Open", Flags: fieldPublic | fieldStatic | fieldLiteral, Type: Struct("Game.Mode")},
		}},
		{Name: "Game.Item", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("title", str),
			public("count", Prim(ElemI4)),
			{Name: "flag", Flags: fieldPrivate, Type: Prim(ElemBool), Attributes: []string{"[UnityEngine]UnityEngine.SerializeField"}},
			{Name: "hidden", Flags: fieldPrivate, Type: str},
			{Name: "shared", Flags: fieldPublic | fieldStatic, Type: str},
		}},
		{Name: "Game.Pair`1", Flags: flagSerializable, Extends: extends(valueType), Fields: []FieldDef{
			public("a", Var(0)), public("b", Var(0)),
		}},
		{Name: "Game.Node", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("name", str), public("children", list(Class("Game.Node"))),
		}},
		{Name: "Game.Plain", Extends: extends(obj), Fields: []FieldDef{public("x", str)}},
		{Name: "Game.IFoo", Flags: flagInterface},
		{Name: "Game.Base", Extends: extends(mono), Fields: []FieldDef{public("baseName", str)}},
		{Name: "Game.Dialog", Extends: extends("Game.Base"), Fields: []FieldDef{
			public("text", str),
			public("item", Class("Game.Item")),
			public("items", ArrayOf(Class("Game.Item"))),
			public("list", list(Class("Game.Item"))),
			public("nested", list(list(Prim(ElemI4)))),
			public("jagged", ArrayOf(ArrayOf(Prim(ElemI4)))),
			public("mode", Struct("Game.Mode")),
			public("target", Class("[UnityEngine]UnityEngine.GameObject")),
			public("pos", Struct("[UnityEngine]UnityEngine.Vector3")),
			public("curve", Class("[UnityEngine]UnityEngine.AnimationCurve")),
			public("dict", Generic("[mscorlib]System.Collections.Generic.Dictionary`2", str, Prim(ElemI4))),
			public("date", Struct("[mscorlib]System.DateTime")),
			public("callback", Class("[mscorlib]System.Action")),
			public("pair", Sig{Elem: ElemGenericInst, Name: "Game.Pair`1", Args: []Sig{str}}),
			public("flags", ArrayOf(Prim(ElemBool))),
			public("letter", Prim(ElemChar)),
			{Name: "skipped", Flags: fieldPublic | fieldNotSerial, Type: str},
			{Name: "constant", Flags: fieldPublic | fieldInitOnly, Type: str},
			public("iface", Class("Game.IFoo")),
			public("plain", Class("Game.Plain")),
			public("node", Class("Game.Node")),
			public("handle", Prim(ElemI)),
			public("any", Prim(ElemObject)),
		}},
		{Name: "Game.Settings", Extends: extends("[UnityEngine]UnityEngine.ScriptableObject"), Fields: []FieldDef{public("title", str)}},
		{Name: "Game.Outer", Extends: extends(obj)},
		{Name: "Game.Outer/Inner", Extends: extends(mono), Fields: []FieldDef{public("label", str)}},
		{Name: "Game.GenericBase`1", Extends: extends(mono), Fields: []FieldDef{public("value", Var(0))}},
		{Name: "Game.Derived", Extends: &Sig{Elem: ElemGenericInst, Name: "Game.GenericBase`1", Args: []Sig{str}}},
		{Name: "Game.Links", Extends: extends(mono), Fields: []FieldDef{
			public("other", Class("Game.Derived")),
			public("entry", Class("[UnityEngine.CoreModule]UnityEngine.Events.Holder/Entry")),
			public("modes", ArrayOf(Struct("Game.Mode"))),
		}},
		{Name: "Game.Refs", Extends: extends(mono), Fields: []FieldDef{
			{Name: "r", Flags: fieldPublic, Type: Prim(ElemObject), Attributes: []string{"[UnityEngine]UnityEngine.SerializeReference"}},
		}},
		{Name: "Game.Broken", Extends: extends(mono), Fields: []FieldDef{public("missing", Class("[Missing]Nowhere.Type"))}},
	}}
	return map[string][]byte{
		"mscorlib.dll":               mscorlib.Build(),
		"UnityEngine.CoreModule.dll": core.Build(),
		"UnityEngine.dll":            facade.Build(),
		"Assembly-CSharp.dll":        game.Build(),
	}
}

// DialogFields encodes the script fields of a Game.Dialog from
// ScriptAssemblies, starting after m_Name. The strings it holds, by
// path: baseName, text, item.title, items[0].title, items[1].title,
// list[0].title, pair.a, pair.b, node.name and node.children[0].name.
func DialogFields(text string) []byte {
	item := func(title string, count int32, flag bool) []byte {
		b := append(String(title), le.AppendUint32(nil, uint32(count))...)
		if flag {
			return append(b, 1, 0, 0, 0)
		}
		return append(b, 0, 0, 0, 0)
	}
	b := String("base")
	b = append(b, String(text)...)
	b = append(b, item("Sword", 3, true)...)
	b = le.AppendUint32(b, 2)
	b = append(b, item("Shield", 1, false)...)
	b = append(b, item("Bow", 2, true)...)
	b = le.AppendUint32(b, 1)
	b = append(b, item("Potion", 5, false)...)
	b = append(b, 2, 0, 0, 0)                     // mode
	b = append(b, pptr(0, 77)...)                 // target
	b = append(b, make([]byte, 12)...)            // pos
	b = le.AppendUint32(b, 1)                     // curve keys
	b = append(b, make([]byte, 28+12)...)         // one key, infinity and rotation order
	b = append(b, String("left")...)              // pair.a
	b = append(b, String("right")...)             // pair.b
	b = append(le.AppendUint32(b, 3), 1, 0, 1, 0) // flags and padding
	b = append(le.AppendUint16(b, 'Z'), 0, 0)     // letter
	b = append(b, String("root")...)              // node.name
	b = le.AppendUint32(b, 1)                     // node.children
	b = append(b, String("leaf")...)              // node.children[0].name
	return le.AppendUint32(b, 0)                  // node.children[0].children
}
