// Package scriptlayout reconstructs how Unity 2020.3 serializes the fields
// of a MonoBehaviour script from the script's assembly metadata, and
// decodes the strings of player-build objects that carry no type tree.
package scriptlayout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"apocalypter-l10n-tools/internal/editor/clr"
)

// ErrUnsupported reports a script whose layout the package cannot
// reproduce, such as one with [SerializeReference] fields.
var ErrUnsupported = errors.New("scriptlayout: unsupported script")

// Kind is the kind of a layout node.
type Kind uint8

// Node kinds.
const (
	// KindPrimitive is a fixed number of bytes: a number, a PPtr or a
	// fixed-size Unity struct member.
	KindPrimitive Kind = iota
	// KindString is an int32 byte length followed by UTF-8 bytes.
	KindString
	// KindArray is an int32 element count followed by the elements.
	KindArray
	// KindStruct is its fields in order.
	KindStruct
)

// Node describes one serialized value.
type Node struct {
	Name string
	Kind Kind
	// Size is the byte size of a KindPrimitive.
	Size int
	// Align pads the data to 4 bytes after the value.
	Align  bool
	Elem   *Node
	Fields []*Node
}

const (
	// maxDepth is Unity's serialization depth limit: class fields nested
	// deeper are not serialized.
	maxDepth = 10
	// maxNodes bounds the layout of one script; recursive types expand
	// until maxDepth and could otherwise grow exponentially.
	maxNodes = 1 << 18
	pptrSize = 12
)

// Resolver builds script layouts. It is not safe for concurrent use.
type Resolver struct {
	mods     *modules
	scripts  map[string]scriptResult
	isObject map[typeDef]bool
}

type scriptResult struct {
	node *Node
	err  error
}

// New returns a resolver over the given assemblies.
func New(mods []*clr.Module) *Resolver {
	return &Resolver{mods: newModules(mods), scripts: map[string]scriptResult{}, isObject: map[typeDef]bool{}}
}

// LoadDir parses every *.dll in dir, typically the Managed directory of a
// player build. Files that are not .NET assemblies are skipped and
// reported in the returned slice.
func LoadDir(dir string) (*Resolver, []error, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.dll"))
	if err != nil {
		return nil, nil, err
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("%s: no assemblies", dir)
	}
	var mods []*clr.Module
	var skipped []error
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		m, err := clr.Parse(data)
		if err != nil {
			skipped = append(skipped, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		mods = append(mods, m)
	}
	return New(mods), skipped, nil
}

// Script returns the layout of the fields a MonoBehaviour or
// ScriptableObject of the given class serializes after m_Name. assembly
// is the MonoScript assembly name, with or without ".dll".
func (r *Resolver) Script(assembly, namespace, class string) (*Node, error) {
	assembly = strings.TrimSuffix(assembly, ".dll")
	key := assembly + "|" + namespace + "|" + class
	if res, ok := r.scripts[key]; ok {
		return res.node, res.err
	}
	node, err := r.script(assembly, namespace, class)
	if err != nil {
		err = fmt.Errorf("%s %s: %w", assembly, qualify(namespace, class), err)
	}
	r.scripts[key] = scriptResult{node, err}
	return node, err
}

func (r *Resolver) script(assembly, namespace, class string) (*Node, error) {
	outer, rest, nested := strings.Cut(strings.ReplaceAll(class, "+", "/"), "/")
	def, err := r.mods.find(assembly, namespace, outer, 0)
	if err != nil {
		return nil, err
	}
	for nested {
		var name string
		name, rest, nested = strings.Cut(rest, "/")
		i, ok := r.mods.nested[def][name]
		if !ok {
			return nil, fmt.Errorf("%w: nested type %s", errNotFound, name)
		}
		def = typeDef{def.mod, i}
	}
	b := &builder{r: r}
	fields, err := b.fields(def, nil, 0)
	if err != nil {
		return nil, err
	}
	return &Node{Kind: KindStruct, Fields: fields}, nil
}

// Unity base classes whose own fields are native and live in the
// MonoBehaviour header, and the roots of every managed type.
var stopBases = map[string]bool{
	"UnityEngine.MonoBehaviour":    true,
	"UnityEngine.ScriptableObject": true,
	"UnityEngine.Behaviour":        true,
	"UnityEngine.Component":        true,
	"UnityEngine.Object":           true,
	"System.Object":                true,
	"System.ValueType":             true,
}

type builder struct {
	r     *Resolver
	nodes int
}

func (b *builder) add(n *Node) (*Node, error) {
	b.nodes++
	if b.nodes > maxNodes {
		return nil, fmt.Errorf("%w: layout exceeds %d nodes", ErrUnsupported, maxNodes)
	}
	return n, nil
}

// fields returns the serialized fields of d and its base classes, base
// classes first.
func (b *builder) fields(d typeDef, args []*ctype, depth int) ([]*Node, error) {
	var out []*Node
	base, err := b.r.mods.base(d, args)
	if err != nil {
		return nil, err
	}
	if base != nil && base.def.mod != nil && !stopBases[base.def.fullName()] {
		if out, err = b.fields(base.def, base.args, depth); err != nil {
			return nil, err
		}
	}
	for _, f := range d.def().Fields {
		if !serializedField(f) {
			continue
		}
		if slices.Contains(f.Attributes, "UnityEngine.SerializeReference") {
			return nil, fmt.Errorf("%w: [SerializeReference] field %s", ErrUnsupported, f.Name)
		}
		sig, err := clr.ParseFieldSig(f.Signature)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		t, err := b.r.mods.resolve(d.mod, sig, args)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		n, err := b.node(t, f.Name, depth, false)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		if n != nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func serializedField(f clr.Field) bool {
	if f.Flags&(clr.FieldStatic|clr.FieldLiteral|clr.FieldInitOnly|clr.FieldNotSerialized) != 0 {
		return false
	}
	return f.Flags&clr.FieldAccessMask == clr.FieldPublic ||
		slices.Contains(f.Attributes, "UnityEngine.SerializeField") ||
		slices.Contains(f.Attributes, "UnityEngine.SerializeReference")
}

// primitiveSizes gives the serialized size of primitive element types.
// char is stored as a UTF-16 code unit.
var primitiveSizes = map[clr.ElementType]int{
	clr.ElementBoolean: 1, clr.ElementI1: 1, clr.ElementU1: 1,
	clr.ElementChar: 2, clr.ElementI2: 2, clr.ElementU2: 2,
	clr.ElementI4: 4, clr.ElementU4: 4, clr.ElementR4: 4,
	clr.ElementI8: 8, clr.ElementU8: 8, clr.ElementR8: 8,
}

// node returns the layout of a value of type t, or nil when Unity does not
// serialize it. inArray marks array elements: Unity aligns small
// primitive fields but packs small primitive array elements.
func (b *builder) node(t *ctype, name string, depth int, inArray bool) (*Node, error) {
	if size, ok := primitiveSizes[t.kind]; ok {
		return b.add(&Node{Name: name, Kind: KindPrimitive, Size: size, Align: !inArray && size < 4})
	}
	switch t.kind {
	case clr.ElementString:
		return b.add(&Node{Name: name, Kind: KindString, Align: true})
	case clr.ElementSzArray:
		return b.array(t.elem, name, depth)
	case clr.ElementClass, clr.ElementValueType, clr.ElementGenericInst:
		return b.named(t, name, depth, inArray)
	}
	return nil, nil
}

func (b *builder) array(elem *ctype, name string, depth int) (*Node, error) {
	if elem.kind == clr.ElementSzArray || isList(elem) {
		// Unity does not serialize nested collections.
		return nil, nil
	}
	e, err := b.node(elem, "data", depth, true)
	if err != nil || e == nil {
		return nil, err
	}
	return b.add(&Node{Name: name, Kind: KindArray, Elem: e, Align: true})
}

func isList(t *ctype) bool {
	return t.kind == clr.ElementGenericInst && t.def.fullName() == "System.Collections.Generic.List`1"
}

// named handles class and struct types.
func (b *builder) named(t *ctype, name string, depth int, inArray bool) (*Node, error) {
	full := t.def.fullName()
	if isList(t) {
		if len(t.args) != 1 {
			return nil, fmt.Errorf("List with %d type arguments", len(t.args))
		}
		return b.array(t.args[0], name, depth)
	}
	if builtin, ok := builtins[full]; ok {
		n := builtin()
		n.Name = name
		return b.add(n)
	}
	obj, err := b.r.unityObject(t.def)
	if err != nil {
		return nil, err
	}
	if obj {
		return b.add(&Node{Name: name, Kind: KindPrimitive, Size: pptrSize})
	}
	if systemAssembly(t.def.mod.Name) {
		return nil, nil
	}
	def := t.def.def()
	base, ok, err := b.r.mods.baseDef(t.def)
	if err != nil {
		return nil, err
	}
	baseName := ""
	if ok {
		baseName = base.fullName()
	}
	if baseName == "System.Enum" {
		return b.enum(t.def, name, inArray)
	}
	if def.Flags&(clr.TypeInterface|clr.TypeAbstract) != 0 || def.Flags&clr.TypeSerializable == 0 ||
		baseName == "System.MulticastDelegate" || baseName == "System.Delegate" {
		return nil, nil
	}
	if depth >= maxDepth {
		return nil, nil
	}
	fields, err := b.fields(t.def, t.args, depth+1)
	if err != nil {
		return nil, err
	}
	return b.add(&Node{Name: name, Kind: KindStruct, Fields: fields})
}

// enum returns the layout of an enum: its underlying integer type.
func (b *builder) enum(d typeDef, name string, inArray bool) (*Node, error) {
	for _, f := range d.def().Fields {
		if f.Flags&clr.FieldStatic != 0 {
			continue
		}
		sig, err := clr.ParseFieldSig(f.Signature)
		if err != nil {
			return nil, err
		}
		size, ok := primitiveSizes[sig.Kind]
		if !ok {
			return nil, fmt.Errorf("enum %s has underlying type 0x%x", d.fullName(), byte(sig.Kind))
		}
		return b.add(&Node{Name: name, Kind: KindPrimitive, Size: size, Align: !inArray && size < 4})
	}
	return nil, fmt.Errorf("enum %s has no value field", d.fullName())
}

// unityObject reports whether d derives from UnityEngine.Object; fields
// of such types serialize as a PPtr.
func (r *Resolver) unityObject(d typeDef) (bool, error) {
	if v, ok := r.isObject[d]; ok {
		return v, nil
	}
	var chain []typeDef
	result := false
	for cur := d; ; {
		if v, ok := r.isObject[cur]; ok {
			result = v
			break
		}
		chain = append(chain, cur)
		if cur.fullName() == "UnityEngine.Object" {
			result = true
			break
		}
		base, ok, err := r.mods.baseDef(cur)
		if err != nil {
			return false, err
		}
		if !ok || len(chain) > maxForwardDepth*4 {
			break
		}
		cur = base
	}
	for _, c := range chain {
		r.isObject[c] = result
	}
	return result, nil
}

// systemAssembly reports assemblies whose types Unity never serializes,
// apart from primitives, strings and List<T>.
func systemAssembly(name string) bool {
	return name == "mscorlib" || name == "netstandard" || name == "System" ||
		strings.HasPrefix(name, "System.") || strings.HasPrefix(name, "Mono.")
}
