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
//
// PlayMaker holds PlayMakerFSM with the part of a PlayMaker FSM textkind
// reads; FSMFields encodes it.
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
	ints := list(Prim(ElemI4))
	fsmString := Class("HutongGames.PlayMaker.FsmString")
	playMaker := Assembly{Name: "PlayMaker", Types: []TypeDef{
		{Name: "PlayMakerFSM", Extends: extends(mono), Fields: []FieldDef{public("fsm", Class("HutongGames.PlayMaker.Fsm"))}},
		{Name: "HutongGames.PlayMaker.Fsm", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("name", str),
			public("states", ArrayOf(Class("HutongGames.PlayMaker.FsmState"))),
			public("variables", Class("HutongGames.PlayMaker.FsmVariables")),
		}},
		{Name: "HutongGames.PlayMaker.FsmState", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("name", str), public("actionData", Class("HutongGames.PlayMaker.ActionData")),
		}},
		{Name: "HutongGames.PlayMaker.ActionData", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("actionNames", list(str)),
			public("paramName", list(str)),
			public("actionStartIndex", ints),
			public("paramDataType", ints),
			public("paramDataPos", ints),
			public("arrayParamSizes", ints),
			public("customTypeSizes", ints),
			public("fsmStringParams", list(fsmString)),
			public("stringParams", list(str)),
		}},
		{Name: "HutongGames.PlayMaker.FsmString", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("useVariable", Prim(ElemBool)), public("name", str), public("value", str),
		}},
		{Name: "HutongGames.PlayMaker.FsmVariables", Flags: flagSerializable, Extends: extends(obj), Fields: []FieldDef{
			public("stringVariables", ArrayOf(fsmString)),
		}},
	}}
	return map[string][]byte{
		"PlayMaker.dll":              playMaker.Build(),
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

// FsmString is a string parameter or variable of a PlayMaker FSM.
type FsmString struct {
	UseVariable bool
	Name, Value string
}

// FsmState is one state of a PlayMaker FSM with its parameter tables.
type FsmState struct {
	Name                          string
	ActionNames, ParamNames       []string
	StartIndex, DataType, DataPos []int32
	FsmStrings                    []FsmString
}

// FSM is the fsm field of a PlayMakerFSM.
type FSM struct {
	Name            string
	States          []FsmState
	StringVariables []FsmString
}

// FSMFields encodes the script fields of a PlayMakerFSM from
// ScriptAssemblies, starting after m_Name.
func FSMFields(f FSM) []byte {
	strs := func(b []byte, list []string) []byte {
		b = le.AppendUint32(b, uint32(len(list)))
		for _, s := range list {
			b = append(b, String(s)...)
		}
		return b
	}
	ints := func(b []byte, list []int32) []byte {
		b = le.AppendUint32(b, uint32(len(list)))
		for _, n := range list {
			b = le.AppendUint32(b, uint32(n))
		}
		return b
	}
	fsmStrings := func(b []byte, list []FsmString) []byte {
		b = le.AppendUint32(b, uint32(len(list)))
		for _, s := range list {
			use := byte(0)
			if s.UseVariable {
				use = 1
			}
			b = append(b, use, 0, 0, 0)
			b = append(b, String(s.Name)...)
			b = append(b, String(s.Value)...)
		}
		return b
	}
	b := String(f.Name)
	b = le.AppendUint32(b, uint32(len(f.States)))
	for _, s := range f.States {
		b = append(b, String(s.Name)...)
		b = strs(b, s.ActionNames)
		b = strs(b, s.ParamNames)
		b = ints(b, s.StartIndex)
		b = ints(b, s.DataType)
		b = ints(b, s.DataPos)
		b = ints(b, nil)
		b = ints(b, nil)
		b = fsmStrings(b, s.FsmStrings)
		b = strs(b, nil)
	}
	return fsmStrings(b, f.StringVariables)
}

// Class IDs and the bundle node flag PlantBundle uses; serialized and
// gamepatch export the same values.
const (
	classGameObject    = 1
	classMonoBehaviour = 114
	classMonoScript    = 115
	nodeFlagSerialized = 4
)

// Labels of the plant in PlantBundle.
const (
	PlantGrowing = "Tobacco Plant Growing"
	PlantRipe    = "Tobacco Plant Ripe (Harvest - F)"
)

// PlantBundle builds a bundle with the tobacco plant of Apocalypter. The
// Plant FSM of the "Pot" GameObject writes PlantRipe into the ItemName
// variable of the ItemName FSM on the "Plant" GameObject, which shows
// the variable with UiTextSetText; PlantGrowing is its initial value.
// In level0, PlayMakerFSM components 2 (Plant) and 4 (ItemName) hold the
// string parameters fsm.states[0].actionData.fsmStringParams[0..2].
func PlantBundle() []byte {
	const script = 7
	scripts := Serialized([]Object{
		{PathID: script, ClassID: classMonoScript, Data: MonoScript("PlayMakerFSM", "", "PlayMaker.dll")},
	}, nil)
	pot := FSM{Name: "Plant", States: []FsmState{{
		Name:        "ripe",
		ActionNames: []string{"HutongGames.PlayMaker.Actions.SetFsmString"},
		ParamNames:  []string{"gameObject", "fsmName", "variableName", "setValue", "everyFrame"},
		StartIndex:  []int32{0},
		DataType:    []int32{20, 18, 18, 18, 17},
		DataPos:     []int32{0, 0, 1, 2, 0},
		FsmStrings:  []FsmString{{Value: "ItemName"}, {Value: "ItemName"}, {Value: PlantRipe}},
	}}}
	label := FSM{
		Name: "ItemName",
		States: []FsmState{{
			Name:        "over",
			ActionNames: []string{"HutongGames.PlayMaker.Actions.UiTextSetText"},
			ParamNames:  []string{"gameObject", "text"},
			StartIndex:  []int32{0},
			DataType:    []int32{20, 18},
			DataPos:     []int32{0, 0},
			FsmStrings:  []FsmString{{UseVariable: true, Name: "ItemName", Value: "Cassette Player"}},
		}},
		StringVariables: []FsmString{{Name: "ItemName", Value: PlantGrowing}},
	}
	level := Serialized([]Object{
		{PathID: 1, ClassID: classGameObject, Data: GameObject("Pot", 2)},
		{PathID: 2, ClassID: classMonoBehaviour, Data: append(MonoBehaviour(1, 1, script, ""), FSMFields(pot)...)},
		{PathID: 3, ClassID: classGameObject, Data: GameObject("Plant", 4)},
		{PathID: 4, ClassID: classMonoBehaviour, Data: append(MonoBehaviour(3, 1, script, ""), FSMFields(label)...)},
	}, []string{"library/globalgamemanagers.assets"})
	return Bundle([]Node{
		{Path: "globalgamemanagers.assets", Flags: nodeFlagSerialized, Data: scripts},
		{Path: "level0", Flags: nodeFlagSerialized, Data: level},
	}, 64)
}
