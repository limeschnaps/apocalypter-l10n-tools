package scriptlayout

import (
	"errors"
	"fmt"
	"strings"

	"apocalypter-l10n-tools/internal/editor/clr"
)

// maxForwardDepth bounds chains of type forwarders and nested references.
const maxForwardDepth = 16

// errNotFound reports a type that no loaded assembly defines.
var errNotFound = errors.New("type not found")

// typeDef is a type definition of a loaded assembly.
type typeDef struct {
	mod *clr.Module
	idx int
}

func (d typeDef) def() *clr.TypeDef { return &d.mod.TypeDefs[d.idx] }

// fullName returns "Namespace.Name", with nested types as
// "Namespace.Outer/Inner".
func (d typeDef) fullName() string {
	t := d.def()
	if t.Enclosing >= 0 {
		return typeDef{d.mod, t.Enclosing}.fullName() + "/" + t.Name
	}
	if t.Namespace == "" {
		return t.Name
	}
	return t.Namespace + "." + t.Name
}

// ctype is a type signature resolved in the context of the generic
// arguments of its declaring type.
type ctype struct {
	kind clr.ElementType
	// def is set for Class, ValueType and GenericInst.
	def  typeDef
	elem *ctype
	args []*ctype
}

type modules struct {
	byName map[string]*clr.Module
	// types maps each module to its top-level types by full name.
	types map[*clr.Module]map[string]int
	// nested maps an enclosing type to its nested types by name.
	nested map[typeDef]map[string]int
}

func newModules(mods []*clr.Module) *modules {
	m := &modules{byName: map[string]*clr.Module{}, types: map[*clr.Module]map[string]int{}, nested: map[typeDef]map[string]int{}}
	for _, mod := range mods {
		m.byName[strings.ToLower(mod.Name)] = mod
		top := map[string]int{}
		for i, t := range mod.TypeDefs {
			if t.Enclosing >= 0 {
				outer := typeDef{mod, t.Enclosing}
				if m.nested[outer] == nil {
					m.nested[outer] = map[string]int{}
				}
				m.nested[outer][t.Name] = i
				continue
			}
			top[qualify(t.Namespace, t.Name)] = i
		}
		m.types[mod] = top
	}
	return m
}

func qualify(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "." + name
}

// find looks a top-level type up by assembly and full name, following
// type forwarders.
func (m *modules) find(assembly, namespace, name string, depth int) (typeDef, error) {
	if depth > maxForwardDepth {
		return typeDef{}, fmt.Errorf("forwarding loop for %s", qualify(namespace, name))
	}
	mod, ok := m.byName[strings.ToLower(assembly)]
	if !ok {
		return typeDef{}, fmt.Errorf("%w: %s: assembly %s is not loaded", errNotFound, qualify(namespace, name), assembly)
	}
	if i, ok := m.types[mod][qualify(namespace, name)]; ok {
		return typeDef{mod, i}, nil
	}
	for _, e := range mod.ExportedTypes {
		if e.Namespace != namespace || e.Name != name || e.Implementation.Table() != clr.TableAssemblyRef {
			continue
		}
		if row := e.Implementation.Row(); row >= 1 && row <= len(mod.AssemblyRefs) {
			return m.find(mod.AssemblyRefs[row-1], namespace, name, depth+1)
		}
	}
	return typeDef{}, fmt.Errorf("%w: %s in %s", errNotFound, qualify(namespace, name), mod.Name)
}

// token resolves a TypeDef or TypeRef token of mod.
func (m *modules) token(mod *clr.Module, t clr.Token, depth int) (typeDef, error) {
	switch t.Table() {
	case clr.TableTypeDef:
		if t.Row() < 1 || t.Row() > len(mod.TypeDefs) {
			return typeDef{}, fmt.Errorf("%s: TypeDef %d out of range", mod.Name, t.Row())
		}
		return typeDef{mod, t.Row() - 1}, nil
	case clr.TableTypeRef:
		return m.typeRef(mod, t.Row(), depth)
	}
	return typeDef{}, fmt.Errorf("%s: token 0x%x is not a type definition or reference", mod.Name, uint32(t))
}

func (m *modules) typeRef(mod *clr.Module, row, depth int) (typeDef, error) {
	if row < 1 || row > len(mod.TypeRefs) {
		return typeDef{}, fmt.Errorf("%s: TypeRef %d out of range", mod.Name, row)
	}
	if depth > maxForwardDepth {
		return typeDef{}, fmt.Errorf("%s: TypeRef %d nested too deeply", mod.Name, row)
	}
	ref := mod.TypeRefs[row-1]
	switch ref.Scope.Table() {
	case clr.TableAssemblyRef:
		r := ref.Scope.Row()
		if r < 1 || r > len(mod.AssemblyRefs) {
			return typeDef{}, fmt.Errorf("%s: AssemblyRef %d out of range", mod.Name, r)
		}
		return m.find(mod.AssemblyRefs[r-1], ref.Namespace, ref.Name, depth+1)
	case clr.TableTypeRef:
		outer, err := m.typeRef(mod, ref.Scope.Row(), depth+1)
		if err != nil {
			return typeDef{}, err
		}
		i, ok := m.nested[outer][ref.Name]
		if !ok {
			return typeDef{}, fmt.Errorf("%w: %s/%s", errNotFound, outer.fullName(), ref.Name)
		}
		return typeDef{outer.mod, i}, nil
	default:
		// Module, ModuleRef or no scope: the type is in this assembly.
		return m.find(mod.Name, ref.Namespace, ref.Name, depth+1)
	}
}

// resolve turns a signature of mod into a ctype. args are the generic
// arguments of the type that declares the signature.
func (m *modules) resolve(mod *clr.Module, t clr.Type, args []*ctype) (*ctype, error) {
	switch t.Kind {
	case clr.ElementVar:
		if t.Index >= len(args) {
			return nil, fmt.Errorf("%s: generic parameter %d out of range", mod.Name, t.Index)
		}
		return args[t.Index], nil
	case clr.ElementSzArray:
		elem, err := m.resolve(mod, *t.Elem, args)
		if err != nil {
			return nil, err
		}
		return &ctype{kind: t.Kind, elem: elem}, nil
	case clr.ElementClass, clr.ElementValueType:
		if t.Token.Table() == clr.TableTypeSpec {
			return m.typeSpec(mod, t.Token.Row(), args)
		}
		def, err := m.token(mod, t.Token, 0)
		if err != nil {
			return nil, err
		}
		return &ctype{kind: t.Kind, def: def}, nil
	case clr.ElementGenericInst:
		def, err := m.token(mod, t.Token, 0)
		if err != nil {
			return nil, err
		}
		c := &ctype{kind: t.Kind, def: def}
		for _, a := range t.Args {
			ra, err := m.resolve(mod, a, args)
			if err != nil {
				return nil, err
			}
			c.args = append(c.args, ra)
		}
		return c, nil
	}
	// Primitives, strings and kinds Unity never serializes need no
	// further resolution.
	return &ctype{kind: t.Kind}, nil
}

func (m *modules) typeSpec(mod *clr.Module, row int, args []*ctype) (*ctype, error) {
	if row < 1 || row > len(mod.TypeSpecs) {
		return nil, fmt.Errorf("%s: TypeSpec %d out of range", mod.Name, row)
	}
	t, err := clr.ParseTypeSpec(mod.TypeSpecs[row-1])
	if err != nil {
		return nil, err
	}
	return m.resolve(mod, t, args)
}

// base returns the resolved base type of d, or nil for types without one.
func (m *modules) base(d typeDef, args []*ctype) (*ctype, error) {
	ext := d.def().Extends
	if ext == 0 {
		return nil, nil
	}
	if ext.Table() == clr.TableTypeSpec {
		return m.typeSpec(d.mod, ext.Row(), args)
	}
	def, err := m.token(d.mod, ext, 0)
	if err != nil {
		return nil, err
	}
	return &ctype{kind: clr.ElementClass, def: def}, nil
}

// baseDef returns the definition of the base type of d without resolving
// generic arguments; ok is false for types without a base.
func (m *modules) baseDef(d typeDef) (typeDef, bool, error) {
	ext := d.def().Extends
	switch {
	case ext == 0:
		return typeDef{}, false, nil
	case ext.Table() != clr.TableTypeSpec:
		def, err := m.token(d.mod, ext, 0)
		return def, err == nil, err
	}
	if ext.Row() < 1 || ext.Row() > len(d.mod.TypeSpecs) {
		return typeDef{}, false, fmt.Errorf("%s: TypeSpec %d out of range", d.mod.Name, ext.Row())
	}
	t, err := clr.ParseTypeSpec(d.mod.TypeSpecs[ext.Row()-1])
	if err != nil {
		return typeDef{}, false, err
	}
	if t.Kind != clr.ElementGenericInst {
		return typeDef{}, false, fmt.Errorf("%s: base of %s is not a class", d.mod.Name, d.fullName())
	}
	def, err := m.token(d.mod, t.Token, 0)
	return def, err == nil, err
}
