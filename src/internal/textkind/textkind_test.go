package textkind

import (
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestKindText(t *testing.T) {
	for _, k := range []Kind{Screen, Maybe, Service} {
		b, err := json.Marshal(k)
		if err != nil {
			t.Fatal(err)
		}
		var back Kind
		if err := json.Unmarshal(b, &back); err != nil || back != k {
			t.Errorf("%v round trip = %v, %v", k, back, err)
		}
	}
	var zero Kind
	if zero != Maybe || zero.String() != "maybe" {
		t.Errorf("zero kind = %v", zero)
	}
	if err := json.Unmarshal([]byte(`"visible"`), &zero); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestParseNumbers(t *testing.T) {
	yaml := map[string][]int32{
		"0000000002000000":  {0, 2},
		"14000000 00000000": {20, 0},
		"ffffffff":          {-1},
		"":                  {},
		"000000":            nil,
		"zz000000":          nil,
	}
	for in, want := range yaml {
		if got := ParseYAMLNumber(in, true); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0 && (got == nil) == (want == nil)) {
			t.Errorf("ParseYAMLNumber(%q) = %v, want %v", in, got, want)
		}
	}
	if got := ParseYAMLNumber(" 1 ", false); !reflect.DeepEqual(got, []int32{1}) {
		t.Errorf("scalar = %v", got)
	}
	if got := ParseYAMLNumber("yes", false); got != nil {
		t.Errorf("bad scalar = %v", got)
	}

	le := binary.LittleEndian
	if got := ParseRawNumber([]byte{1, 0, 0, 0, 0xfe, 0xff, 0xff, 0xff}, true, le); !reflect.DeepEqual(got, []int32{1, -2}) {
		t.Errorf("raw array = %v", got)
	}
	if got := ParseRawNumber([]byte{1}, false, le); !reflect.DeepEqual(got, []int32{1}) {
		t.Errorf("raw bool = %v", got)
	}
	if got := ParseRawNumber([]byte{7, 0, 0, 0}, false, le); !reflect.DeepEqual(got, []int32{7}) {
		t.Errorf("raw int = %v", got)
	}
	if got := ParseRawNumber([]byte{1, 2}, false, le); got != nil {
		t.Errorf("raw short = %v", got)
	}
}

func TestNumberField(t *testing.T) {
	cases := map[string][2]bool{
		"fsm.states[1].actionData.paramDataType":                  {true, true},
		"fsm.states[1].actionData.actionStartIndex":               {true, true},
		"fsm.states[1].actionData.fsmStringParams[4].useVariable": {true, false},
		"fsm.states[1].actionData.fsmStringParams[4].value":       {false, false},
		"fsm.states[1].actionData.paramName[0]":                   {false, false},
		"m_Enabled":                                               {false, false},
	}
	for path, want := range cases {
		needed, array := NumberField(path)
		if [2]bool{needed, array} != want {
			t.Errorf("NumberField(%s) = %v %v, want %v", path, needed, array, want)
		}
	}
}

func kinds(c Component) map[string]Kind {
	out := map[string]Kind{}
	for i, k := range Classify(c) {
		out[c.Strings[i].Path] = k
	}
	return out
}

func strs(paths ...string) []Field {
	out := make([]Field, len(paths))
	for i, p := range paths {
		out[i] = Field{Path: p, Value: "v"}
	}
	return out
}

func TestClassifyScripts(t *testing.T) {
	cases := []struct {
		name   string
		script Script
		want   map[string]Kind
	}{
		{"ui text", Script{Assembly: "UnityEngine.UI.dll", Namespace: "UnityEngine.UI", Class: "Text"},
			map[string]Kind{"m_Text": Screen, "m_Name": Service, "m_FontData.m_Font": Service, "str[0]": Maybe}},
		{"tmp", Script{Namespace: "TMPro", Class: "TextMeshProUGUI"},
			map[string]Kind{"m_text": Screen, "m_fontAsset": Service}},
		{"input field", Script{Namespace: "UnityEngine.UI", Class: "InputField"},
			map[string]Kind{"m_Text": Maybe, "m_CharacterValidation": Service}},
		{"dropdown", Script{Namespace: "TMPro", Class: "TMP_Dropdown"},
			map[string]Kind{"m_Options.m_Options[3].m_Text": Screen, "m_CaptionText": Service}},
		{"game code", Script{Assembly: "Assembly-CSharp.dll", Class: "Shop"},
			map[string]Kind{"title": Maybe, "m_Name": Service, "m_EditorClassIdentifier": Service}},
		{"exported .cs", Script{Class: "Shop"}, map[string]Kind{"title": Maybe}},
		{"library", Script{Assembly: "NWH.VehiclePhysics2.dll", Class: "VehicleController"},
			map[string]Kind{"tag": Service}},
		{"easy save", Script{Assembly: "Assembly-CSharp.dll", Class: "ES3ReferenceMgr"},
			map[string]Kind{"idRef._Keys[0]": Service}},
		{"arraymaker", Script{Assembly: "Assembly-CSharp.dll", Class: "PlayMakerArrayListProxy"},
			map[string]Kind{"referenceName": Service, "preFillStringList[0]": Maybe}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			for p := range tc.want {
				paths = append(paths, p)
			}
			got := kinds(Component{Script: tc.script, Strings: strs(paths...)})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// fsmState describes one FSM state for the tests: its actions, parameter
// tables and string values.
type fsmState struct {
	actions  []string
	names    []string
	types    []int32
	pos      []int32
	starts   []int32
	arrays   []int32
	useVar   map[int]bool
	varNames map[int]string // variable names of fsmStringParams
	fsmStrs  []string
	plainStr []string
}

func (s fsmState) component(class string) Component {
	const p = "fsm.states[1].actionData"
	c := Component{Script: Script{Assembly: "PlayMaker.dll", Class: class}, Numbers: map[string][]int32{
		p + ".actionStartIndex": s.starts, p + ".paramDataType": s.types, p + ".paramDataPos": s.pos,
		p + ".arrayParamSizes": s.arrays, p + ".customTypeSizes": nil,
	}}
	add := func(path, v string) { c.Strings = append(c.Strings, Field{Path: path, Value: v}) }
	for i, a := range s.actions {
		add(p+".actionNames["+itoa(i)+"]", a)
	}
	for i, n := range s.names {
		add(p+".paramName["+itoa(i)+"]", n)
	}
	for i, v := range s.fsmStrs {
		add(p+".fsmStringParams["+itoa(i)+"].value", v)
		use := int32(0)
		if s.useVar[i] {
			use = 1
		}
		c.Numbers[p+".fsmStringParams["+itoa(i)+"].useVariable"] = []int32{use}
	}
	for i, n := range s.varNames {
		add(p+".fsmStringParams["+itoa(i)+"].name", n)
	}
	for i, v := range s.plainStr {
		add(p+".stringParams["+itoa(i)+"]", v)
	}
	add("fsm.name", "FSM")
	add("fsm.variables.stringVariables[0].value", "Ammo Box")
	add("fsm.variables.stringVariables[0].name", "itemName")
	add("fsm.states[1].name", "idle")
	return c
}

func itoa(i int) string { return strconv.Itoa(i) }

// A state from Apocalypter: GetFsmFloat, ConvertFloatToString and three
// UiTextSetText actions. paramName is parallel to paramDataType.
var gauge = fsmState{
	actions: []string{
		"HutongGames.PlayMaker.Actions.GetFsmFloat", "HutongGames.PlayMaker.Actions.ConvertFloatToString",
		"HutongGames.PlayMaker.Actions.UiTextSetText", "HutongGames.PlayMaker.Actions.UiTextSetText",
	},
	names:   []string{"gameObject", "fsmName", "variableName", "storeValue", "floatVariable", "stringVariable", "format", "gameObject", "text", "gameObject", "text"},
	types:   []int32{20, 18, 18, 15, 15, 18, 18, 20, 18, 20, 18},
	pos:     []int32{0, 0, 1, 0, 1, 2, 3, 1, 4, 2, 5},
	starts:  []int32{0, 4, 7, 9},
	useVar:  map[int]bool{2: true, 5: true},
	fsmStrs: []string{"Condition", "Condition", "", "0", "Grip: 1", "unused"},
}

func TestClassifyFSM(t *testing.T) {
	const p = "fsm.states[1].actionData."
	got := kinds(gauge.component("PlayMakerFSM"))
	want := map[string]Kind{
		p + "fsmStringParams[0].value":           Service, // GetFsmFloat.fsmName
		p + "fsmStringParams[1].value":           Service, // GetFsmFloat.variableName
		p + "fsmStringParams[2].value":           Service, // bound to a variable
		p + "fsmStringParams[3].value":           Service, // number format
		p + "fsmStringParams[4].value":           Screen,  // UiTextSetText.text
		p + "fsmStringParams[5].value":           Service, // UiTextSetText.text bound to a variable
		p + "actionNames[2]":                     Service,
		p + "paramName[8]":                       Service,
		"fsm.name":                               Service,
		"fsm.states[1].name":                     Service,
		"fsm.variables.stringVariables[0].value": Maybe,
		"fsm.variables.stringVariables[0].name":  Service,
	}
	for path, k := range want {
		if got[path] != k {
			t.Errorf("%s = %v, want %v", path, got[path], k)
		}
	}
	// FSM templates hold the same data.
	if k := kinds(gauge.component("FsmTemplate"))[p+"fsmStringParams[4].value"]; k != Screen {
		t.Errorf("template = %v", k)
	}
}

// The ItemName FSM of the Water Can in Apocalypter: UiTextSetText shows
// the itemName variable, so the variable's value is on screen, not the
// literal the parameter keeps.
func TestClassifyFSMBoundVariable(t *testing.T) {
	const (
		p     = "fsm.states[1].actionData."
		value = "fsm.variables.stringVariables[0].value"
	)
	shown := fsmState{
		actions:  []string{"HutongGames.PlayMaker.Actions.UiTextSetText", "HutongGames.PlayMaker.Actions.UiTextSetText"},
		names:    []string{"gameObject", "text", "gameObject", "text"},
		types:    []int32{20, 18, 20, 18},
		pos:      []int32{0, 0, 1, 1},
		starts:   []int32{0, 2},
		useVar:   map[int]bool{0: true},
		varNames: map[int]string{0: "itemName"},
		fsmStrs:  []string{"Ammo Box", "Drink: Hold F"},
	}
	got := kinds(shown.component("PlayMakerFSM"))
	for path, want := range map[string]Kind{
		value:                                   Screen,
		"fsm.variables.stringVariables[0].name": Service,
		p + "fsmStringParams[0].value":          Service,
		p + "fsmStringParams[1].value":          Screen,
	} {
		if got[path] != want {
			t.Errorf("%s = %v, want %v", path, got[path], want)
		}
	}

	// Only text parameters make a variable screen text.
	set := shown
	set.actions = []string{"HutongGames.PlayMaker.Actions.SetFsmString", "HutongGames.PlayMaker.Actions.UiTextSetText"}
	set.names = []string{"gameObject", "setValue", "gameObject", "text"}
	if k := kinds(set.component("PlayMakerFSM"))[value]; k != Maybe {
		t.Errorf("bound SetFsmString.setValue: variable = %v, want maybe", k)
	}
	// A text parameter bound to another variable leaves this one alone.
	other := shown
	other.varNames = map[int]string{0: "liquid"}
	if k := kinds(other.component("PlayMakerFSM"))[value]; k != Maybe {
		t.Errorf("other variable = %v, want maybe", k)
	}
}

func TestClassifyFSMArraysAndPlainStrings(t *testing.T) {
	const p = "fsm.states[1].actionData."
	// BuildString: stringParts is an array of two FsmStrings, separator a
	// FsmString; SendMessage takes a plain string (type 3).
	aligned := fsmState{
		actions:  []string{"HutongGames.PlayMaker.Actions.BuildString", "HutongGames.PlayMaker.Actions.SendMessage"},
		names:    []string{"stringParts", "", "", "separator", "functionName"},
		types:    []int32{12, 18, 18, 18, 3},
		pos:      []int32{0, 0, 1, 2, 0},
		starts:   []int32{0, 4},
		arrays:   []int32{2},
		fsmStrs:  []string{"Hello, ", "world", " "},
		plainStr: []string{"OnHit"},
	}
	// The same state with names for top-level fields only.
	topLevel := aligned
	topLevel.names = []string{"stringParts", "separator", "functionName"}
	for name, s := range map[string]fsmState{"aligned": aligned, "top-level names": topLevel} {
		t.Run(name, func(t *testing.T) {
			got := kinds(s.component("PlayMakerFSM"))
			for path, want := range map[string]Kind{
				p + "fsmStringParams[0].value": Maybe,
				p + "fsmStringParams[1].value": Maybe,
				p + "fsmStringParams[2].value": Maybe,
				p + "stringParams[0]":          Service,
			} {
				if got[path] != want {
					t.Errorf("%s = %v, want %v", path, got[path], want)
				}
			}
		})
	}
}

// The fuel tank label of Apocalypter vehicles: two StringAppend2 actions
// add "/ " and "L" to the capacity variable that UiTextSetText shows.
func TestClassifyFSMBuiltText(t *testing.T) {
	const p = "fsm.states[1].actionData."
	label := fsmState{
		actions: []string{
			"HutongGames.PlayMaker.Actions.StringAppend2", "HutongGames.PlayMaker.Actions.StringAppend2",
			"HutongGames.PlayMaker.Actions.UiTextSetText",
		},
		names: []string{
			"_string", "stringToAdd", "addToEnd", "everyFrame",
			"_string", "stringToAdd", "addToEnd", "everyFrame",
			"gameObject", "text",
		},
		types:    []int32{18, 18, 17, 17, 18, 18, 17, 17, 20, 18},
		pos:      []int32{0, 1, 0, 1, 2, 3, 2, 3, 0, 4},
		starts:   []int32{0, 4, 8},
		useVar:   map[int]bool{0: true, 2: true, 4: true},
		varNames: map[int]string{0: "capacity", 2: "capacity", 4: "capacity"},
		fsmStrs:  []string{"", "/ ", "", "L", ""},
	}
	got := kinds(label.component("PlayMakerFSM"))
	for path, want := range map[string]Kind{
		p + "fsmStringParams[1].value": Screen,
		p + "fsmStringParams[3].value": Screen,
		p + "fsmStringParams[4].value": Service,
	} {
		if got[path] != want {
			t.Errorf("%s = %v, want %v", path, got[path], want)
		}
	}

	// A variable that is not shown leaves its sources Maybe.
	hidden := label
	hidden.varNames = map[int]string{0: "capacity", 2: "capacity", 4: "liquid"}
	if k := kinds(hidden.component("PlayMakerFSM"))[p+"fsmStringParams[1].value"]; k != Maybe {
		t.Errorf("hidden variable: source = %v, want maybe", k)
	}

	// The second action appends the "unit" variable, which SetStringValue
	// fills with "L" first; the literal of the second action is unused.
	chained := label
	chained.actions = []string{
		"HutongGames.PlayMaker.Actions.SetStringValue", "HutongGames.PlayMaker.Actions.StringAppend2",
		"HutongGames.PlayMaker.Actions.UiTextSetText",
	}
	chained.names = []string{
		"stringVariable", "stringValue", "everyFrame", "",
		"_string", "stringToAdd", "addToEnd", "everyFrame",
		"gameObject", "text",
	}
	chained.useVar = map[int]bool{0: true, 2: true, 3: true, 4: true}
	chained.varNames = map[int]string{0: "unit", 2: "capacity", 3: "unit", 4: "capacity"}
	chained.fsmStrs = []string{"", "L", "", "unused", ""}
	got = kinds(chained.component("PlayMakerFSM"))
	for path, want := range map[string]Kind{
		p + "fsmStringParams[1].value": Screen,
		p + "fsmStringParams[3].value": Service,
	} {
		if got[path] != want {
			t.Errorf("chained: %s = %v, want %v", path, got[path], want)
		}
	}
}

func TestClassifyFSMBrokenTables(t *testing.T) {
	const p = "fsm.states[1].actionData."
	broken := map[string]func(*fsmState){
		"too few names": func(s *fsmState) { s.names = s.names[:2] },
		"too many names": func(s *fsmState) {
			s.names = append(s.names[:len(s.names):len(s.names)], "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l")
		},
		"array size missing":  func(s *fsmState) { s.types = append([]int32(nil), s.types...); s.types[3] = 12 },
		"custom size missing": func(s *fsmState) { s.types = append([]int32(nil), s.types...); s.types[3] = 40 },
		"positions short":     func(s *fsmState) { s.pos = s.pos[:3] },
	}
	for name, mutate := range broken {
		t.Run(name, func(t *testing.T) {
			s := gauge
			mutate(&s)
			got := kinds(s.component("PlayMakerFSM"))
			// Unreadable tables: every string parameter is Maybe, the
			// structural names stay Service.
			if got[p+"fsmStringParams[0].value"] != Maybe || got[p+"fsmStringParams[4].value"] != Maybe || got[p+"actionNames[0]"] != Service {
				t.Errorf("kinds = %v", got)
			}
		})
	}
	// Without layouts FSM strings are named str[N].
	c := Component{Script: Script{Assembly: "PlayMaker.dll", Class: "PlayMakerFSM"}, Strings: strs("m_Name", "str[0]")}
	if got := kinds(c); got["str[0]"] != Maybe || got["m_Name"] != Service {
		t.Errorf("heuristic = %v", got)
	}
}
