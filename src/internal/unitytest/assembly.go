package unitytest

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"strings"
)

// Assembly describes a synthetic .NET assembly with type metadata only.
//
// Type names are "Namespace.Name" for types of this assembly and
// "[Assembly]Namespace.Name" for types of other assemblies; nested types
// are written "Namespace.Outer/Inner".
type Assembly struct {
	Name     string
	Types    []TypeDef
	Forwards []Forward
}

// TypeDef is a type definition.
type TypeDef struct {
	// Name is the full name, "Namespace.Name" or "Namespace.Outer/Inner".
	Name       string
	Flags      uint32
	Extends    *Sig
	Attributes []string
	Fields     []FieldDef
}

// FieldDef is a field definition.
type FieldDef struct {
	Name       string
	Flags      uint16
	Type       Sig
	Attributes []string
}

// Forward forwards a type to another assembly.
type Forward struct {
	// Name is "Namespace.Name".
	Name     string
	Assembly string
}

// Sig is a type in a signature: a primitive (Elem only), a class or value
// type (Name), an array (Of), a generic instance (Name and Args) or a
// generic parameter (Var).
type Sig struct {
	Elem byte
	Name string
	Of   *Sig
	Args []Sig
	Var  int
}

// Element types for Sig.
const (
	ElemBool        = 0x02
	ElemChar        = 0x03
	ElemI1          = 0x04
	ElemU1          = 0x05
	ElemI2          = 0x06
	ElemI4          = 0x08
	ElemI8          = 0x0a
	ElemR4          = 0x0c
	ElemR8          = 0x0d
	ElemString      = 0x0e
	ElemPtr         = 0x0f
	ElemValueType   = 0x11
	ElemClass       = 0x12
	ElemVar         = 0x13
	ElemGenericInst = 0x15
	ElemI           = 0x18
	ElemObject      = 0x1c
	ElemSzArray     = 0x1d
)

// Sig helpers.
func Prim(elem byte) Sig     { return Sig{Elem: elem} }
func Class(name string) Sig  { return Sig{Elem: ElemClass, Name: name} }
func Struct(name string) Sig { return Sig{Elem: ElemValueType, Name: name} }
func ArrayOf(of Sig) Sig     { return Sig{Elem: ElemSzArray, Of: &of} }
func Var(n int) Sig          { return Sig{Elem: ElemVar, Var: n} }
func Generic(name string, args ...Sig) Sig {
	return Sig{Elem: ElemGenericInst, Name: name, Args: args}
}

// Table numbers and coded index tags written by Build.
const (
	tModule       = 0x00
	tTypeRef      = 0x01
	tTypeDef      = 0x02
	tField        = 0x04
	tMemberRef    = 0x0a
	tCustomAttr   = 0x0c
	tTypeSpec     = 0x1b
	tAssembly     = 0x20
	tAssemblyRef  = 0x23
	tExportedType = 0x27
	tNestedClass  = 0x29
)

type asmWriter struct {
	a        Assembly
	strings  bytes.Buffer
	strIndex map[string]uint16
	blobs    bytes.Buffer
	typeDefs map[string]int
	refs     []string
	refIndex map[string]int
	typeRefs [][3]uint16 // scope coded, name, namespace
	refByKey map[string]int
	specs    []uint16
	members  [][3]uint16 // parent coded, name, sig
	memberBy map[string]int
	attrs    [][2]uint16 // parent coded, type coded
}

// Build returns the assembly as a PE file. It panics when the metadata
// outgrows the 2-byte indexes it uses, which never happens in tests.
func (a Assembly) Build() []byte {
	w := &asmWriter{
		a: a, strIndex: map[string]uint16{}, typeDefs: map[string]int{}, refIndex: map[string]int{},
		refByKey: map[string]int{}, memberBy: map[string]int{},
	}
	w.strings.WriteByte(0)
	w.blobs.WriteByte(0)
	for i, t := range a.Types {
		w.typeDefs[t.Name] = i + 1
	}
	return w.pe(w.metadata())
}

func (w *asmWriter) str(s string) uint16 {
	if s == "" {
		return 0
	}
	if i, ok := w.strIndex[s]; ok {
		return i
	}
	i := uint16(w.strings.Len())
	w.strings.WriteString(s + "\x00")
	w.strIndex[s] = i
	return i
}

func (w *asmWriter) blob(b []byte) uint16 {
	i := uint16(w.blobs.Len())
	w.blobs.Write(compress(nil, uint32(len(b))))
	w.blobs.Write(b)
	return i
}

func compress(b []byte, v uint32) []byte {
	switch {
	case v < 0x80:
		return append(b, byte(v))
	case v < 0x4000:
		return append(b, byte(v>>8|0x80), byte(v))
	}
	return append(b, byte(v>>24|0xc0), byte(v>>16), byte(v>>8), byte(v))
}

func splitName(full string) (namespace, name string) {
	if i := strings.LastIndexByte(full, '.'); i >= 0 {
		return full[:i], full[i+1:]
	}
	return "", full
}

func (w *asmWriter) assemblyRef(name string) int {
	if i, ok := w.refIndex[name]; ok {
		return i
	}
	w.refs = append(w.refs, name)
	w.refIndex[name] = len(w.refs)
	return len(w.refs)
}

// typeToken returns a TypeDefOrRef coded value (tag in the low 2 bits).
func (w *asmWriter) typeToken(name string) uint32 {
	if row, ok := w.typeDefs[name]; ok {
		return uint32(row) << 2
	}
	return uint32(w.typeRef(name))<<2 | 1
}

func (w *asmWriter) typeRef(full string) int {
	if i, ok := w.refByKey[full]; ok {
		return i
	}
	asm, rest, ok := strings.Cut(strings.TrimPrefix(full, "["), "]")
	if !ok || !strings.HasPrefix(full, "[") {
		panic(fmt.Sprintf("unitytest: unknown local type %q", full))
	}
	var scope uint16
	name := rest
	if outer, inner, nested := strings.Cut(rest, "/"); nested {
		scope = uint16(w.typeRef("["+asm+"]"+outer))<<2 | 3
		name = inner
		rest = ""
	}
	var ns string
	if rest != "" {
		ns, name = splitName(rest)
		scope = uint16(w.assemblyRef(asm))<<2 | 2
	}
	w.typeRefs = append(w.typeRefs, [3]uint16{scope, w.str(name), w.str(ns)})
	w.refByKey[full] = len(w.typeRefs)
	return len(w.typeRefs)
}

func (w *asmWriter) sig(b []byte, s Sig) []byte {
	b = append(b, s.Elem)
	switch s.Elem {
	case ElemClass, ElemValueType:
		b = compress(b, w.typeToken(s.Name))
	case ElemSzArray, ElemPtr:
		b = w.sig(b, *s.Of)
	case ElemVar:
		b = compress(b, uint32(s.Var))
	case ElemGenericInst:
		b = append(b, ElemClass)
		b = compress(b, w.typeToken(s.Name))
		b = compress(b, uint32(len(s.Args)))
		for _, a := range s.Args {
			b = w.sig(b, a)
		}
	}
	return b
}

// extends returns the TypeDefOrRef coded value of a base type.
func (w *asmWriter) extends(s *Sig) uint16 {
	switch {
	case s == nil:
		return 0
	case s.Elem == ElemGenericInst:
		w.specs = append(w.specs, w.blob(w.sig(nil, *s)))
		return uint16(len(w.specs))<<2 | 2
	}
	return uint16(w.typeToken(s.Name))
}

// attribute records a custom attribute of the given type on parent (a
// HasCustomAttribute coded value).
func (w *asmWriter) attribute(parent uint16, typeName string) {
	i, ok := w.memberBy[typeName]
	if !ok {
		class := w.typeToken(typeName)
		// MemberRefParent tags: TypeDef 0, TypeRef 1.
		parentCoded := uint16(class>>2)<<3 | uint16(class&3)
		w.members = append(w.members, [3]uint16{parentCoded, w.str(".ctor"), w.blob([]byte{0x20, 0, 0x01})})
		i = len(w.members)
		w.memberBy[typeName] = i
	}
	// CustomAttributeType tag 3 is MemberRef.
	w.attrs = append(w.attrs, [2]uint16{parent, uint16(i)<<3 | 3})
}

func (w *asmWriter) metadata() []byte {
	type typeRow struct {
		flags                           uint32
		name, ns, extends, fields, meth uint16
	}
	var types []typeRow
	var fields [][3]uint16
	var nested [][2]uint16
	for i, t := range w.a.Types {
		fullOuter, inner, isNested := strings.Cut(t.Name, "/")
		ns, name := splitName(fullOuter)
		if isNested {
			ns, name = "", inner
			nested = append(nested, [2]uint16{uint16(i + 1), uint16(w.typeDefs[fullOuter])})
		}
		row := typeRow{flags: t.Flags, name: w.str(name), ns: w.str(ns), extends: w.extends(t.Extends), fields: uint16(len(fields) + 1), meth: 1}
		for _, a := range t.Attributes {
			w.attribute(uint16(i+1)<<5|3, a)
		}
		for _, f := range t.Fields {
			fields = append(fields, [3]uint16{f.Flags, w.str(f.Name), w.blob(w.sig([]byte{0x06}, f.Type))})
			for _, a := range f.Attributes {
				w.attribute(uint16(len(fields))<<5|1, a)
			}
		}
		types = append(types, row)
	}
	var exported [][3]uint16
	for _, f := range w.a.Forwards {
		ns, name := splitName(f.Name)
		exported = append(exported, [3]uint16{w.str(name), w.str(ns), uint16(w.assemblyRef(f.Assembly))<<2 | 1})
	}
	asmName := w.str(w.a.Name)

	var t bytes.Buffer
	le := binary.LittleEndian
	put := func(vs ...any) {
		for _, v := range vs {
			_ = binary.Write(&t, le, v)
		}
	}
	rows := map[int]int{
		tModule: 1, tTypeRef: len(w.typeRefs), tTypeDef: len(types), tField: len(fields), tMemberRef: len(w.members),
		tCustomAttr: len(w.attrs), tTypeSpec: len(w.specs), tAssembly: 1, tAssemblyRef: len(w.refs),
		tExportedType: len(exported), tNestedClass: len(nested),
	}
	var valid uint64
	for table, n := range rows {
		if n > 0 {
			valid |= 1 << table
		}
	}
	put(uint32(0), uint8(2), uint8(0), uint8(0), uint8(1), valid, uint64(0))
	for table := range 64 {
		if valid&(1<<table) != 0 {
			put(uint32(rows[table]))
		}
	}
	put(uint16(0), asmName, uint16(1), uint16(0), uint16(0)) // Module
	for _, r := range w.typeRefs {
		put(r[0], r[1], r[2])
	}
	for _, r := range types {
		put(r.flags, r.name, r.ns, r.extends, r.fields, r.meth)
	}
	for _, r := range fields {
		put(r[0], r[1], r[2])
	}
	for _, r := range w.members {
		put(r[0], r[1], r[2])
	}
	for _, r := range w.attrs {
		put(r[0], r[1], uint16(w.blob([]byte{1, 0, 0, 0})))
	}
	for _, s := range w.specs {
		put(s)
	}
	put(uint32(0x8004), uint16(1), uint16(0), uint16(0), uint16(0), uint32(0), uint16(0), asmName, uint16(0)) // Assembly
	for _, r := range w.refs {
		put(uint16(0), uint16(0), uint16(0), uint16(0), uint32(0), uint16(0), w.str(r), uint16(0), uint16(0))
	}
	for _, r := range exported {
		put(uint32(0), uint32(0), r[0], r[1], r[2])
	}
	for _, r := range nested {
		put(r[0], r[1])
	}
	if w.strings.Len() >= 1<<16 || w.blobs.Len() >= 1<<16 {
		panic("unitytest: assembly heaps exceed 64 KiB")
	}

	streams := []struct {
		name string
		data []byte
	}{
		{"#~", t.Bytes()},
		{"#Strings", w.strings.Bytes()},
		{"#GUID", make([]byte, 16)},
		{"#Blob", w.blobs.Bytes()},
	}
	const version = "v4.0.30319\x00\x00"
	var root bytes.Buffer
	_ = binary.Write(&root, le, uint32(0x424a5342))
	_ = binary.Write(&root, le, [2]uint16{1, 1})
	_ = binary.Write(&root, le, uint32(0))
	_ = binary.Write(&root, le, uint32(len(version)))
	root.WriteString(version)
	_ = binary.Write(&root, le, [2]uint16{0, uint16(len(streams))})
	headerSize := root.Len()
	for _, s := range streams {
		headerSize += 8 + (len(s.name)+4)/4*4
	}
	offset := headerSize
	for _, s := range streams {
		_ = binary.Write(&root, le, [2]uint32{uint32(offset), uint32(len(s.data))})
		name := []byte(s.name + "\x00")
		for len(name)%4 != 0 {
			name = append(name, 0)
		}
		root.Write(name)
		offset += (len(s.data) + 3) / 4 * 4
	}
	for _, s := range streams {
		root.Write(s.data)
		for root.Len()%4 != 0 {
			root.WriteByte(0)
		}
	}
	return root.Bytes()
}

// pe wraps metadata into a PE32 image with one section that holds the CLI
// header followed by the metadata.
func (w *asmWriter) pe(meta []byte) []byte {
	const (
		lfanew       = 0x80
		fileAlign    = 0x200
		sectionRVA   = 0x2000
		cliSize      = 72
		headersSize  = fileAlign
		optionalSize = 224
	)
	le := binary.LittleEndian
	body := make([]byte, cliSize, cliSize+len(meta))
	le.PutUint32(body[0:], cliSize)
	le.PutUint16(body[4:], 2)
	le.PutUint16(body[6:], 5)
	le.PutUint32(body[8:], sectionRVA+cliSize)
	le.PutUint32(body[12:], uint32(len(meta)))
	le.PutUint32(body[16:], 1)
	body = append(body, meta...)
	rawSize := (len(body) + fileAlign - 1) / fileAlign * fileAlign

	var out bytes.Buffer
	dos := make([]byte, lfanew)
	copy(dos, "MZ")
	le.PutUint32(dos[0x3c:], lfanew)
	out.Write(dos)
	out.WriteString("PE\x00\x00")
	_ = binary.Write(&out, le, pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_I386, NumberOfSections: 1, SizeOfOptionalHeader: optionalSize, Characteristics: 0x2102})
	opt := pe.OptionalHeader32{
		Magic: 0x10b, SizeOfCode: uint32(rawSize), BaseOfCode: sectionRVA, ImageBase: 0x400000,
		SectionAlignment: sectionRVA, FileAlignment: fileAlign, MajorSubsystemVersion: 4,
		SizeOfImage: sectionRVA * 2, SizeOfHeaders: headersSize, Subsystem: 3, NumberOfRvaAndSizes: 16,
	}
	opt.DataDirectory[14] = pe.DataDirectory{VirtualAddress: sectionRVA, Size: cliSize}
	_ = binary.Write(&out, le, opt)
	_ = binary.Write(&out, le, pe.SectionHeader32{
		Name: [8]uint8{'.', 't', 'e', 'x', 't'}, VirtualSize: uint32(len(body)), VirtualAddress: sectionRVA,
		SizeOfRawData: uint32(rawSize), PointerToRawData: headersSize, Characteristics: 0x60000020,
	})
	out.Write(make([]byte, headersSize-out.Len()))
	out.Write(body)
	out.Write(make([]byte, rawSize-len(body)))
	return out.Bytes()
}
