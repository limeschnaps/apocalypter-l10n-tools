// Package clr reads the type metadata of .NET assemblies (ECMA-335): type
// definitions and references, fields with their signatures, custom
// attributes and type forwarders. Method bodies and resources are not
// read.
package clr

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Errors returned by the package.
var (
	ErrFormat      = errors.New("clr: malformed assembly")
	ErrUnsupported = errors.New("clr: unsupported assembly")
)

// Token identifies a metadata row: the table number in the high byte and
// the 1-based row in the low 24 bits. Zero means no row.
type Token uint32

// Token tables used by callers.
const (
	TableModule       = tableModule
	TableTypeRef      = tableTypeRef
	TableTypeDef      = tableTypeDef
	TableModuleRef    = tableModuleRef
	TableTypeSpec     = tableTypeSpec
	TableAssemblyRef  = tableAssemblyRef
	TableFile         = tableFile
	TableExportedType = tableExportedType
)

// NewToken returns the token of row (1-based) in table.
func NewToken(table, row int) Token { return Token(uint32(table)<<24 | uint32(row)) }

// Table returns the table number.
func (t Token) Table() int { return int(t >> 24) }

// Row returns the 1-based row number.
func (t Token) Row() int { return int(t & 0xffffff) }

// Type attribute flags (II.23.1.15) used by callers.
const (
	TypeVisibilityMask = 0x00000007
	TypeInterface      = 0x00000020
	TypeAbstract       = 0x00000080
	TypeSerializable   = 0x00002000
)

// Field attribute flags (II.23.1.5) used by callers.
const (
	FieldAccessMask    = 0x0007
	FieldPublic        = 0x0006
	FieldStatic        = 0x0010
	FieldInitOnly      = 0x0020
	FieldLiteral       = 0x0040
	FieldNotSerialized = 0x0080
)

// Module is the metadata of one assembly.
type Module struct {
	// Name is the assembly name, such as "Assembly-CSharp".
	Name string
	// AssemblyRefs holds referenced assembly names by row - 1.
	AssemblyRefs []string
	TypeRefs     []TypeRef
	TypeDefs     []TypeDef
	// TypeSpecs holds the signature blob of every TypeSpec row.
	TypeSpecs     [][]byte
	ExportedTypes []ExportedType
}

// TypeRef is a reference to a type of this or another assembly.
type TypeRef struct {
	// Scope is a Module, ModuleRef, AssemblyRef or (for nested types)
	// TypeRef token.
	Scope     Token
	Namespace string
	Name      string
}

// TypeDef is a type defined in the assembly.
type TypeDef struct {
	Flags     uint32
	Namespace string
	Name      string
	// Extends is a TypeDef, TypeRef or TypeSpec token, or zero.
	Extends Token
	// Enclosing is the index of the enclosing type in TypeDefs, or -1.
	Enclosing  int
	Fields     []Field
	Attributes []string
}

// Field is a field of a type.
type Field struct {
	Flags     uint16
	Name      string
	Signature []byte
	// Attributes holds the full names of the custom attribute types.
	Attributes []string
}

// ExportedType forwards a type name to another assembly.
type ExportedType struct {
	Namespace string
	Name      string
	// Implementation is an AssemblyRef, File or (for nested types)
	// ExportedType token.
	Implementation Token
}

// Parse reads the metadata of a PE assembly.
func Parse(data []byte) (*Module, error) {
	meta, err := metadata(data)
	if err != nil {
		return nil, err
	}
	streams, err := parseStreams(meta)
	if err != nil {
		return nil, err
	}
	tableData, ok := streams["#~"]
	if !ok {
		if tableData, ok = streams["#-"]; !ok {
			return nil, fmt.Errorf("%w: no table stream", ErrFormat)
		}
	}
	ts, err := parseTables(tableData)
	if err != nil {
		return nil, err
	}
	r := &moduleReader{ts: ts, strings: streams["#Strings"], blobs: streams["#Blob"]}
	m := r.read()
	if r.err != nil {
		return nil, r.err
	}
	return m, nil
}

// metadata returns the metadata root that the CLI header points to.
func metadata(data []byte) ([]byte, error) {
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	const cliHeaderDir = 14
	var dirs []pe.DataDirectory
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		dirs = h.DataDirectory[:h.NumberOfRvaAndSizes]
	case *pe.OptionalHeader64:
		dirs = h.DataDirectory[:h.NumberOfRvaAndSizes]
	}
	if len(dirs) <= cliHeaderDir || dirs[cliHeaderDir].VirtualAddress == 0 {
		return nil, fmt.Errorf("%w: not a .NET assembly", ErrFormat)
	}
	rva := func(addr, size uint32) ([]byte, error) {
		for _, s := range f.Sections {
			if addr >= s.VirtualAddress && addr-s.VirtualAddress+size <= s.Size {
				off := int64(s.Offset) + int64(addr-s.VirtualAddress)
				if off+int64(size) <= int64(len(data)) {
					return data[off : off+int64(size)], nil
				}
			}
		}
		return nil, fmt.Errorf("%w: RVA 0x%x outside sections", ErrFormat, addr)
	}
	cli, err := rva(dirs[cliHeaderDir].VirtualAddress, 16)
	if err != nil {
		return nil, err
	}
	return rva(binary.LittleEndian.Uint32(cli[8:]), binary.LittleEndian.Uint32(cli[12:]))
}

// parseStreams splits the metadata root into named streams (II.24.2.1).
func parseStreams(meta []byte) (map[string][]byte, error) {
	const signature = 0x424a5342
	if len(meta) < 16 || binary.LittleEndian.Uint32(meta) != signature {
		return nil, fmt.Errorf("%w: bad metadata signature", ErrFormat)
	}
	pos := 16 + int(binary.LittleEndian.Uint32(meta[12:]))
	if pos+4 > len(meta) {
		return nil, fmt.Errorf("%w: truncated metadata root", ErrFormat)
	}
	count := int(binary.LittleEndian.Uint16(meta[pos+2:]))
	pos += 4
	streams := map[string][]byte{}
	for range count {
		if pos+8 > len(meta) {
			return nil, fmt.Errorf("%w: truncated stream header", ErrFormat)
		}
		off := int(binary.LittleEndian.Uint32(meta[pos:]))
		size := int(binary.LittleEndian.Uint32(meta[pos+4:]))
		end := bytes.IndexByte(meta[pos+8:], 0)
		if end < 0 {
			return nil, fmt.Errorf("%w: unterminated stream name", ErrFormat)
		}
		name := string(meta[pos+8 : pos+8+end])
		pos += 8 + (end+4)/4*4
		if off < 0 || size < 0 || off+size > len(meta) {
			return nil, fmt.Errorf("%w: stream %s out of bounds", ErrFormat, name)
		}
		streams[name] = meta[off : off+size]
	}
	return streams, nil
}

type moduleReader struct {
	ts      *tables
	strings []byte
	blobs   []byte
	err     error
}

func (r *moduleReader) str(v uint32) string {
	if int(v) >= len(r.strings) {
		if v != 0 && r.err == nil {
			r.err = fmt.Errorf("%w: string index %d out of range", ErrFormat, v)
		}
		return ""
	}
	end := bytes.IndexByte(r.strings[v:], 0)
	if end < 0 {
		end = len(r.strings) - int(v)
	}
	return string(r.strings[v : int(v)+end])
}

func (r *moduleReader) blob(v uint32) []byte {
	if int(v) >= len(r.blobs) {
		if v != 0 && r.err == nil {
			r.err = fmt.Errorf("%w: blob index %d out of range", ErrFormat, v)
		}
		return nil
	}
	n, size, ok := uncompress(r.blobs[v:])
	if !ok || int(v)+size+int(n) > len(r.blobs) {
		if r.err == nil {
			r.err = fmt.Errorf("%w: bad blob at %d", ErrFormat, v)
		}
		return nil
	}
	start := int(v) + size
	return r.blobs[start : start+int(n)]
}

func (r *moduleReader) read() *Module {
	ts := r.ts
	m := &Module{}
	if t := &ts[tableAssembly]; t.rows > 0 {
		m.Name = r.str(t.value(1, 7))
	} else if t := &ts[tableModule]; t.rows > 0 {
		m.Name = r.str(t.value(1, 1))
	}
	for row := 1; row <= ts[tableAssemblyRef].rows; row++ {
		m.AssemblyRefs = append(m.AssemblyRefs, r.str(ts[tableAssemblyRef].value(row, 6)))
	}
	for row := 1; row <= ts[tableTypeRef].rows; row++ {
		t := &ts[tableTypeRef]
		m.TypeRefs = append(m.TypeRefs, TypeRef{
			Scope:     decodeCoded(colResolutionScope, t.value(row, 0)),
			Name:      r.str(t.value(row, 1)),
			Namespace: r.str(t.value(row, 2)),
		})
	}
	for row := 1; row <= ts[tableTypeSpec].rows; row++ {
		m.TypeSpecs = append(m.TypeSpecs, r.blob(ts[tableTypeSpec].value(row, 0)))
	}
	for row := 1; row <= ts[tableExportedType].rows; row++ {
		t := &ts[tableExportedType]
		m.ExportedTypes = append(m.ExportedTypes, ExportedType{
			Name:           r.str(t.value(row, 2)),
			Namespace:      r.str(t.value(row, 3)),
			Implementation: decodeCoded(colImplementation, t.value(row, 4)),
		})
	}

	td := &ts[tableTypeDef]
	fieldCount := ts[tableField].rows
	methodStarts := make([]int, td.rows)
	for row := 1; row <= td.rows; row++ {
		first := int(td.value(row, 4))
		last := fieldCount + 1
		if row < td.rows {
			last = int(td.value(row+1, 4))
		}
		if first < 1 || last < first || last > fieldCount+1 {
			if first != last {
				r.fail("field list of type %d out of range", row)
			}
			first, last = 1, 1
		}
		def := TypeDef{
			Flags:     td.value(row, 0),
			Name:      r.str(td.value(row, 1)),
			Namespace: r.str(td.value(row, 2)),
			Extends:   decodeCoded(colTypeDefOrRef, td.value(row, 3)),
			Enclosing: -1,
		}
		for f := first; f < last; f++ {
			ft := &ts[tableField]
			def.Fields = append(def.Fields, Field{
				Flags:     uint16(ft.value(f, 0)),
				Name:      r.str(ft.value(f, 1)),
				Signature: r.blob(ft.value(f, 2)),
			})
		}
		m.TypeDefs = append(m.TypeDefs, def)
		methodStarts[row-1] = int(td.value(row, 5))
	}
	for row := 1; row <= ts[tableNestedClass].rows; row++ {
		nested := int(ts[tableNestedClass].value(row, 0))
		enclosing := int(ts[tableNestedClass].value(row, 1))
		if nested < 1 || nested > len(m.TypeDefs) || enclosing < 1 || enclosing > len(m.TypeDefs) {
			r.fail("nested class row %d out of range", row)
			continue
		}
		m.TypeDefs[nested-1].Enclosing = enclosing - 1
	}
	r.readAttributes(m, methodStarts)
	return m
}

func (r *moduleReader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: "+format, append([]any{ErrFormat}, args...)...)
	}
}

// readAttributes attaches custom attribute type names to types and
// fields. Attributes on other rows are ignored.
func (r *moduleReader) readAttributes(m *Module, methodStarts []int) {
	ts := r.ts
	// fieldOwner maps a field row to its type and index within the type.
	type fieldPos struct{ def, index int }
	fieldOwner := make([]fieldPos, ts[tableField].rows+1)
	for i := range m.TypeDefs {
		first := int(ts[tableTypeDef].value(i+1, 4))
		for j := range m.TypeDefs[i].Fields {
			fieldOwner[first+j] = fieldPos{i, j}
		}
	}
	// ownerOfMethod returns the type whose method list contains method.
	ownerOfMethod := func(method int) int {
		i := sort.Search(len(methodStarts), func(i int) bool { return methodStarts[i] > method })
		return i - 1
	}
	typeName := func(t Token) string {
		switch t.Table() {
		case tableTypeRef:
			if t.Row() <= len(m.TypeRefs) {
				ref := m.TypeRefs[t.Row()-1]
				return fullName(ref.Namespace, ref.Name)
			}
		case tableTypeDef:
			if t.Row() <= len(m.TypeDefs) {
				def := m.TypeDefs[t.Row()-1]
				return fullName(def.Namespace, def.Name)
			}
		}
		return ""
	}

	ca := &ts[tableCustomAttribute]
	mr := &ts[tableMemberRef]
	for row := 1; row <= ca.rows; row++ {
		parent := decodeCoded(colHasCustomAttribute, ca.value(row, 0))
		ctor := decodeCoded(colCustomAttributeType, ca.value(row, 1))
		var name string
		switch ctor.Table() {
		case tableMemberRef:
			if ctor.Row() <= mr.rows {
				name = typeName(decodeCoded(colMemberRefParent, mr.value(ctor.Row(), 0)))
			}
		case tableMethodDef:
			if owner := ownerOfMethod(ctor.Row()); owner >= 0 {
				name = typeName(NewToken(tableTypeDef, owner+1))
			}
		}
		if name == "" {
			continue
		}
		switch parent.Table() {
		case tableTypeDef:
			if parent.Row() <= len(m.TypeDefs) {
				def := &m.TypeDefs[parent.Row()-1]
				def.Attributes = append(def.Attributes, name)
			}
		case tableField:
			if parent.Row() < len(fieldOwner) {
				p := fieldOwner[parent.Row()]
				if p.def < len(m.TypeDefs) && p.index < len(m.TypeDefs[p.def].Fields) {
					f := &m.TypeDefs[p.def].Fields[p.index]
					f.Attributes = append(f.Attributes, name)
				}
			}
		}
	}
}

func fullName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "." + name
}

// uncompress decodes a compressed unsigned integer (II.23.2) and returns
// the value and its encoded size.
func uncompress(b []byte) (uint32, int, bool) {
	switch {
	case len(b) >= 1 && b[0]&0x80 == 0:
		return uint32(b[0]), 1, true
	case len(b) >= 2 && b[0]&0xc0 == 0x80:
		return uint32(b[0]&0x3f)<<8 | uint32(b[1]), 2, true
	case len(b) >= 4 && b[0]&0xe0 == 0xc0:
		return uint32(b[0]&0x1f)<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), 4, true
	}
	return 0, 0, false
}
