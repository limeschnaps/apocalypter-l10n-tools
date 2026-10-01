// Package textkind tells player-visible text apart from strings a game
// uses internally, such as object, variable and event names.
//
// Classify sees one component at a time: its script and every string it
// holds, keyed by the field path Unity YAML uses (for example
// "fsm.states[1].actionData.fsmStringParams[4].value"). PlayMaker FSMs
// need a few integer arrays as well; NumberField says which ones.
package textkind

import (
	"encoding/binary"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Kind classifies a string.
type Kind uint8

// The zero value is Maybe, so an unclassified string is neither hidden
// nor presented as certain screen text.
const (
	// Maybe is text that may reach the screen through game logic, such as
	// fields of the game's own scripts or strings FSMs build text from.
	Maybe Kind = iota
	// Screen is text a component displays as is.
	Screen
	// Service is a string the game uses internally.
	Service
)

// String returns the name used in the API: "screen", "maybe" or
// "service".
func (k Kind) String() string {
	switch k {
	case Screen:
		return "screen"
	case Service:
		return "service"
	default:
		return "maybe"
	}
}

// MarshalText implements encoding.TextMarshaler.
func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (k *Kind) UnmarshalText(b []byte) error {
	switch string(b) {
	case "screen":
		*k = Screen
	case "maybe":
		*k = Maybe
	case "service":
		*k = Service
	default:
		return fmt.Errorf("textkind: unknown kind %q", b)
	}
	return nil
}

// Script identifies the MonoScript of a component. Any field may be empty
// when it is unknown.
type Script struct {
	Assembly  string
	Namespace string
	Class     string
}

// FullName returns Namespace.Class, or Class without a namespace.
func (s Script) FullName() string {
	if s.Namespace == "" {
		return s.Class
	}
	return s.Namespace + "." + s.Class
}

// Field is one string of a component.
type Field struct {
	Path  string
	Value string
}

// Component is the input of Classify.
type Component struct {
	Script  Script
	Strings []Field
	// Numbers holds the fields NumberField selects, keyed by path. Arrays
	// hold one value per element; scalars one value.
	Numbers map[string][]int32
}

// textComponent describes a UI component whose own fields hold its text.
type textComponent struct {
	screen []string // exact paths shown as is
	maybe  []string // exact paths that are editable text, e.g. input fields
	// screenItems are path prefix/suffix pairs for text inside arrays.
	screenItems [][2]string
}

// TextComponents are the text components of Unity UI and TextMeshPro, by
// full class name.
var TextComponents = map[string]textComponent{
	"UnityEngine.UI.Text":       {screen: []string{"m_Text"}},
	"TMPro.TextMeshProUGUI":     {screen: []string{"m_text"}},
	"TMPro.TextMeshPro":         {screen: []string{"m_text"}},
	"UnityEngine.UI.InputField": {maybe: []string{"m_Text"}},
	"TMPro.TMP_InputField":      {maybe: []string{"m_Text"}},
	"UnityEngine.UI.Dropdown":   {screenItems: [][2]string{{"m_Options.m_Options[", "].m_Text"}}},
	"TMPro.TMP_Dropdown":        {screenItems: [][2]string{{"m_Options.m_Options[", "].m_Text"}}},
}

// Third-party libraries often ship as source and compile into
// Assembly-CSharp, where they look like game code. serviceFields lists
// the internal fields of such scripts by class; an empty list marks the
// whole script as internal.
var serviceFields = map[string][]string{
	// PlayMaker ArrayMaker: the proxy name is a key; prefilled lists may
	// hold text.
	"PlayMakerArrayListProxy": {"referenceName"},
	"PlayMakerHashTableProxy": {"referenceName"},
}

// isEasySave reports scripts of Easy Save 3, whose strings are object
// references and save keys.
func isEasySave(class string) bool { return strings.HasPrefix(class, "ES3") }

// playMakerScripts hold an FSM in their "fsm" field.
var playMakerScripts = map[string]bool{"PlayMakerFSM": true, "FsmTemplate": true}

// Action parameters that carry text, by action class name (without
// namespace) and parameter name. Parameters of other actions are names of
// FSMs, variables, events, tags, input axes and the like.
var (
	screenParams = map[string][]string{
		"UiTextSetText":      {"text"},
		"setTextmeshProText": {"textString"},
		"SetGUIText":         {"text"},
		"GUIBox":             {"text"},
		"GUIButton":          {"text"},
		"GUILabel":           {"text"},
		"GUILayoutBox":       {"text"},
		"GUILayoutButton":    {"text"},
		"GUILayoutLabel":     {"text"},
		"GUILayoutToggle":    {"text"},
	}
	maybeParams = map[string][]string{
		"SetFsmString":        {"setValue"},
		"SetStringValue":      {"stringValue"},
		"StringAppend":        {"appendString"},
		"StringAppend2":       {"stringToAdd"},
		"BuildString":         {"stringParts", "separator"},
		"UiInputFieldSetText": {"text"},
	}
	// Actions that build a string in a variable of the same FSM: the
	// parameter holding that variable and the parameters it is built from.
	builders = map[string]builder{
		"StringAppend":   {"stringVariable", []string{"appendString"}},
		"StringAppend2":  {"_string", []string{"stringToAdd"}},
		"BuildString":    {"storeResult", []string{"stringParts", "separator"}},
		"SetStringValue": {"stringVariable", []string{"stringValue"}},
	}
)

type builder struct {
	target  string
	sources []string
}

// flow is one builder action of an FSM: the variable it writes, its
// literal source parameters by path, and the variables its sources are
// bound to.
type flow struct {
	target   string
	literals []string
	vars     []string
}

// PlayMaker ParamDataType values used by the parameter walk.
const (
	paramString     = 3
	paramArray      = 12
	paramFsmString  = 18
	paramCustomType = 40
)

const actionDataSep = ".actionData."

// fsmStringVars prefixes the paths of FSM string variables.
const fsmStringVars = "fsm.variables.stringVariables["

// Numeric fields of ActionData the parameter walk reads.
var actionDataArrays = []string{"actionStartIndex", "paramDataType", "paramDataPos", "arrayParamSizes", "customTypeSizes"}

// NumberField reports whether Classify needs the numeric field at path,
// and whether it is an int32 array (otherwise a bool or int scalar).
func NumberField(path string) (needed, array bool) {
	i := strings.LastIndex(path, actionDataSep)
	if i < 0 {
		return false, false
	}
	rest := path[i+len(actionDataSep):]
	for _, name := range actionDataArrays {
		if rest == name {
			return true, true
		}
	}
	if strings.HasPrefix(rest, "fsmStringParams[") && strings.HasSuffix(rest, "].useVariable") {
		return true, false
	}
	return false, false
}

// Classify returns the kind of every string of c, in order.
func Classify(c Component) []Kind {
	out := make([]Kind, len(c.Strings))
	full := c.Script.FullName()
	tc, isText := TextComponents[full]
	var fsm map[string]Kind
	if playMakerScripts[c.Script.Class] || strings.EqualFold(c.Script.Assembly, "PlayMaker.dll") {
		fsm = fsmParamKinds(c)
	}
	for i, f := range c.Strings {
		switch {
		case f.Path == "m_Name" || f.Path == "m_EditorClassIdentifier":
			out[i] = Service
		case isText:
			out[i] = tc.kind(f.Path)
		case fsm != nil && (strings.HasPrefix(f.Path, "fsm.") || strings.HasPrefix(f.Path, "str[")):
			out[i] = fsmKind(fsm, f.Path)
		case isEasySave(c.Script.Class), serviceField(c.Script.Class, f.Path):
			out[i] = Service
		case gameCode(c.Script.Assembly):
			out[i] = Maybe
		default:
			out[i] = Service
		}
	}
	return out
}

func serviceField(class, path string) bool {
	fields, ok := serviceFields[class]
	if !ok {
		return false
	}
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		if path == f {
			return true
		}
	}
	return false
}

func (tc textComponent) kind(path string) Kind {
	for _, p := range tc.screen {
		if path == p {
			return Screen
		}
	}
	for _, p := range tc.maybe {
		if path == p {
			return Maybe
		}
	}
	for _, it := range tc.screenItems {
		if strings.HasPrefix(path, it[0]) && strings.HasSuffix(path, it[1]) {
			return Screen
		}
	}
	// Without a layout (heuristic str[N] paths) the text field cannot be
	// told apart from the rest.
	if strings.HasPrefix(path, "str[") {
		return Maybe
	}
	return Service
}

// gameCode reports whether a script belongs to the game itself rather
// than to Unity or a third-party library. An unknown assembly, such as a
// .cs script of an exported project, counts as game code.
func gameCode(assembly string) bool {
	return assembly == "" || strings.HasPrefix(assembly, "Assembly-CSharp")
}

func fsmKind(params map[string]Kind, path string) Kind {
	if k, ok := params[path]; ok {
		return k
	}
	switch {
	case strings.HasPrefix(path, fsmStringVars) && strings.HasSuffix(path, "].value"):
		return Maybe
	case strings.HasPrefix(path, "str["):
		return Maybe
	default:
		return Service
	}
}

// fsmParamKinds classifies the string parameters of every FSM action of
// c by path: its fsmStringParams[N].value and stringParams[N] entries.
// A text parameter bound to a variable displays that variable, so the
// initial value of the FSM string variable with that name is Screen too.
// So are the sources of a builder action writing a displayed variable,
// such as the "L" StringAppend2 adds to a capacity label, and, through
// their variables, the sources of the builders before it.
func fsmParamKinds(c Component) map[string]Kind {
	byPath := make(map[string]string, len(c.Strings))
	prefixes := map[string]bool{}
	for _, f := range c.Strings {
		byPath[f.Path] = f.Value
		if i := strings.LastIndex(f.Path, actionDataSep); i >= 0 {
			prefixes[f.Path[:i+len(actionDataSep)-1]] = true
		}
	}
	for p := range c.Numbers {
		if i := strings.LastIndex(p, actionDataSep); i >= 0 {
			prefixes[p[:i+len(actionDataSep)-1]] = true
		}
	}
	out := map[string]Kind{}
	screenVars := map[string]bool{}
	var flows []flow
	for prefix := range prefixes {
		flows = append(flows, classifyActionData(prefix, byPath, c.Numbers, out, screenVars)...)
	}
	for shown := true; shown; {
		shown = false
		for i := range flows {
			f := &flows[i]
			if f.target == "" || !screenVars[f.target] {
				continue
			}
			for _, p := range f.literals {
				out[p] = Screen
			}
			for _, v := range f.vars {
				screenVars[v] = true
			}
			f.target = ""
			shown = true
		}
	}
	for p, name := range byPath {
		if screenVars[name] && strings.HasPrefix(p, fsmStringVars) && strings.HasSuffix(p, "].name") {
			out[strings.TrimSuffix(p, ".name")+".value"] = Screen
		}
	}
	return out
}

// classifyActionData walks the parameters of one FSM state. PlayMaker
// keeps the parameters of all actions of a state in shared arrays:
// actionStartIndex gives each action's first entry in paramDataType and
// paramDataPos, paramDataPos indexes the typed value arrays, and
// paramName names the top-level fields only. Array (12) and custom type
// (40) entries are followed by their elements, counted in arrayParamSizes
// and customTypeSizes. When the tables are inconsistent every string
// parameter of the state is Maybe. The names of the variables that Screen
// parameters are bound to go to screenVars. The result lists the builder
// actions of the state.
func classifyActionData(prefix string, byPath map[string]string, numbers map[string][]int32, out map[string]Kind, screenVars map[string]bool) []flow {
	list := func(name string) []string {
		var vals []string
		for i := 0; ; i++ {
			v, ok := byPath[prefix+"."+name+"["+strconv.Itoa(i)+"]"]
			if !ok {
				return vals
			}
			vals = append(vals, v)
		}
	}
	num := func(name string) []int32 { return numbers[prefix+"."+name] }
	actions, names := list("actionNames"), list("paramName")
	starts, types, pos := num("actionStartIndex"), num("paramDataType"), num("paramDataPos")
	arrays, customs := num("arrayParamSizes"), num("customTypeSizes")

	fsmValue := func(n int32) string {
		return prefix + ".fsmStringParams[" + strconv.Itoa(int(n)) + "].value"
	}
	plain := func(n int32) string {
		return prefix + ".stringParams[" + strconv.Itoa(int(n)) + "]"
	}
	markAll := func(k Kind) {
		for p := range byPath {
			if strings.HasPrefix(p, prefix+".fsmStringParams[") && strings.HasSuffix(p, "].value") ||
				strings.HasPrefix(p, prefix+".stringParams[") {
				out[p] = k
			}
		}
	}

	actionAt := func(i int) int {
		// The last action whose first entry is at or before i.
		return sort.Search(len(starts), func(a int) bool { return int(starts[a]) > i }) - 1
	}
	type leaf struct {
		action int
		name   string
		index  int
	}
	var leaves []leaf
	var consume func(i int, name string) (int, bool)
	consume = func(i int, name string) (int, bool) {
		if i >= len(types) || i >= len(pos) {
			return 0, false
		}
		leaves = append(leaves, leaf{actionAt(i), name, i})
		var n int32
		switch types[i] {
		case paramArray:
			if int(pos[i]) >= len(arrays) || pos[i] < 0 {
				return 0, false
			}
			n = arrays[pos[i]]
		case paramCustomType:
			if int(pos[i]) >= len(customs) || pos[i] < 0 {
				return 0, false
			}
			n = customs[pos[i]]
		default:
			return i + 1, true
		}
		j := i + 1
		for range n {
			var ok bool
			if j, ok = consume(j, name); !ok {
				return 0, false
			}
		}
		return j, true
	}
	// paramName is usually parallel to paramDataType, with empty names
	// for array elements; some states name only the top-level fields.
	aligned := len(names) == len(types)
	i, ni := 0, 0
	for i < len(types) {
		var name string
		switch {
		case aligned:
			name = names[i]
		case ni < len(names):
			name = names[ni]
			ni++
		default:
			markAll(Maybe)
			return nil
		}
		var ok bool
		if i, ok = consume(i, name); !ok {
			markAll(Maybe)
			return nil
		}
	}
	if !aligned && ni != len(names) {
		markAll(Maybe)
		return nil
	}

	flows := map[int]*flow{}
	for _, l := range leaves {
		var path string
		switch types[l.index] {
		case paramFsmString:
			path = fsmValue(pos[l.index])
		case paramString:
			path = plain(pos[l.index])
		default:
			continue
		}
		action := ""
		if l.action >= 0 && l.action < len(actions) {
			action = actions[l.action]
			action = action[strings.LastIndex(action, ".")+1:]
		}
		// A parameter bound to a variable ignores its literal value.
		useVar, bound := false, ""
		if types[l.index] == paramFsmString {
			param := prefix + ".fsmStringParams[" + strconv.Itoa(int(pos[l.index])) + "]"
			if use := numbers[param+".useVariable"]; len(use) == 1 && use[0] != 0 {
				useVar, bound = true, byPath[param+".name"]
			}
		}
		_, indexed := byPath[path]
		if b, ok := builders[action]; ok {
			f := flows[l.action]
			if f == nil {
				f = &flow{}
				flows[l.action] = f
			}
			switch {
			case l.name == b.target:
				f.target = bound
			case !slices.Contains(b.sources, l.name):
			case useVar:
				if bound != "" {
					f.vars = append(f.vars, bound)
				}
			case indexed:
				f.literals = append(f.literals, path)
			}
		}
		if !indexed {
			continue
		}
		kind := paramKind(action, l.name)
		if useVar {
			if kind == Screen && bound != "" {
				screenVars[bound] = true
			}
			kind = Service
		}
		out[path] = kind
	}
	result := make([]flow, 0, len(flows))
	for _, f := range flows {
		result = append(result, *f)
	}
	return result
}

func paramKind(action, param string) Kind {
	for _, p := range screenParams[action] {
		if p == param {
			return Screen
		}
	}
	for _, p := range maybeParams[action] {
		if p == param {
			return Maybe
		}
	}
	return Service
}

// ParseYAMLNumber decodes a numeric field from Unity YAML. Unity writes
// arrays of primitives as a hex string of their little-endian bytes,
// which long values wrap across lines, and scalars as decimals. It returns
// nil for malformed input.
func ParseYAMLNumber(value string, array bool) []int32 {
	if !array {
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if err != nil {
			return nil
		}
		return []int32{int32(n)}
	}
	hex := strings.Join(strings.Fields(value), "")
	if len(hex)%8 != 0 {
		return nil
	}
	out := make([]int32, 0, len(hex)/8)
	for i := 0; i < len(hex); i += 8 {
		var v uint32
		for b := 0; b < 4; b++ {
			n, err := strconv.ParseUint(hex[i+2*b:i+2*b+2], 16, 8)
			if err != nil {
				return nil
			}
			v |= uint32(n) << (8 * b)
		}
		out = append(out, int32(v))
	}
	return out
}

// ParseRawNumber decodes a numeric field from serialized data: an int32
// array, or a scalar of 1 (bool) or 4 bytes.
func ParseRawNumber(raw []byte, array bool, order binary.ByteOrder) []int32 {
	switch {
	case array:
		out := make([]int32, len(raw)/4)
		for i := range out {
			out[i] = int32(order.Uint32(raw[4*i:]))
		}
		return out
	case len(raw) == 1:
		return []int32{int32(raw[0])}
	case len(raw) == 4:
		return []int32{int32(order.Uint32(raw))}
	default:
		return nil
	}
}
