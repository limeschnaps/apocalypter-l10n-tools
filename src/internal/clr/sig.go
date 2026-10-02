package clr

import "fmt"

// ElementType is a signature element type (II.23.1.16).
type ElementType byte

// Element types.
const (
	ElementVoid        ElementType = 0x01
	ElementBoolean     ElementType = 0x02
	ElementChar        ElementType = 0x03
	ElementI1          ElementType = 0x04
	ElementU1          ElementType = 0x05
	ElementI2          ElementType = 0x06
	ElementU2          ElementType = 0x07
	ElementI4          ElementType = 0x08
	ElementU4          ElementType = 0x09
	ElementI8          ElementType = 0x0a
	ElementU8          ElementType = 0x0b
	ElementR4          ElementType = 0x0c
	ElementR8          ElementType = 0x0d
	ElementString      ElementType = 0x0e
	ElementPtr         ElementType = 0x0f
	ElementByRef       ElementType = 0x10
	ElementValueType   ElementType = 0x11
	ElementClass       ElementType = 0x12
	ElementVar         ElementType = 0x13
	ElementArray       ElementType = 0x14
	ElementGenericInst ElementType = 0x15
	ElementTypedByRef  ElementType = 0x16
	ElementI           ElementType = 0x18
	ElementU           ElementType = 0x19
	ElementFnPtr       ElementType = 0x1b
	ElementObject      ElementType = 0x1c
	ElementSzArray     ElementType = 0x1d
	ElementMVar        ElementType = 0x1e
	elementCModReqd    ElementType = 0x1f
	elementCModOpt     ElementType = 0x20
	elementPinned      ElementType = 0x45
)

// fieldSig starts every field signature.
const fieldSig = 0x06

// Type is a decoded type signature.
type Type struct {
	Kind ElementType
	// Token is the TypeDef, TypeRef or TypeSpec of Class and ValueType,
	// and the generic type of GenericInst.
	Token Token
	// Elem is the element of SzArray, Array, Ptr and ByRef.
	Elem *Type
	// Args are the type arguments of GenericInst.
	Args []Type
	// Index is the parameter number of Var and MVar.
	Index int
}

// ParseFieldSig decodes a field signature blob.
func ParseFieldSig(b []byte) (Type, error) {
	if len(b) == 0 || b[0] != fieldSig {
		return Type{}, fmt.Errorf("%w: not a field signature", ErrFormat)
	}
	p := &sigParser{b: b, pos: 1}
	t := p.typ(0)
	return t, p.err
}

// ParseTypeSpec decodes a TypeSpec signature blob.
func ParseTypeSpec(b []byte) (Type, error) {
	p := &sigParser{b: b}
	t := p.typ(0)
	return t, p.err
}

// maxSigDepth bounds nesting so corrupt blobs cannot recurse forever.
const maxSigDepth = 64

type sigParser struct {
	b   []byte
	pos int
	err error
}

func (p *sigParser) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("%w: signature: "+format, append([]any{ErrFormat}, args...)...)
	}
}

func (p *sigParser) u() uint32 {
	if p.err != nil {
		return 0
	}
	v, n, ok := uncompress(p.b[p.pos:])
	if !ok {
		p.fail("truncated at %d", p.pos)
		return 0
	}
	p.pos += n
	return v
}

func (p *sigParser) typeDefOrRef() Token {
	v := p.u()
	tables := [...]int{tableTypeDef, tableTypeRef, tableTypeSpec}
	if v&3 == 3 {
		p.fail("bad TypeDefOrRef tag")
		return 0
	}
	return NewToken(tables[v&3], int(v>>2))
}

func (p *sigParser) typ(depth int) Type {
	if depth > maxSigDepth {
		p.fail("nested too deeply")
	}
	if p.err != nil {
		return Type{}
	}
	if p.pos >= len(p.b) {
		p.fail("truncated")
		return Type{}
	}
	kind := ElementType(p.b[p.pos])
	p.pos++
	switch kind {
	case elementCModReqd, elementCModOpt:
		p.typeDefOrRef()
		return p.typ(depth + 1)
	case elementPinned:
		return p.typ(depth + 1)
	case ElementVoid, ElementBoolean, ElementChar, ElementI1, ElementU1, ElementI2, ElementU2,
		ElementI4, ElementU4, ElementI8, ElementU8, ElementR4, ElementR8, ElementString,
		ElementTypedByRef, ElementI, ElementU, ElementObject:
		return Type{Kind: kind}
	case ElementClass, ElementValueType:
		return Type{Kind: kind, Token: p.typeDefOrRef()}
	case ElementVar, ElementMVar:
		return Type{Kind: kind, Index: int(p.u())}
	case ElementPtr, ElementByRef, ElementSzArray:
		elem := p.typ(depth + 1)
		return Type{Kind: kind, Elem: &elem}
	case ElementArray:
		elem := p.typ(depth + 1)
		p.u() // rank
		for range p.u() {
			p.u() // sizes
		}
		for range p.u() {
			p.u() // lower bounds, signed but equally long
		}
		return Type{Kind: kind, Elem: &elem}
	case ElementGenericInst:
		if p.pos >= len(p.b) {
			p.fail("truncated generic instance")
			return Type{}
		}
		p.pos++ // CLASS or VALUETYPE
		t := Type{Kind: kind, Token: p.typeDefOrRef()}
		n := int(p.u())
		if n > len(p.b) {
			p.fail("implausible argument count %d", n)
			return Type{}
		}
		for range n {
			t.Args = append(t.Args, p.typ(depth+1))
		}
		return t
	case ElementFnPtr:
		// Function pointers are never serialized and end the field
		// signature, so their method signature is not decoded.
		return Type{Kind: kind}
	}
	p.fail("unknown element type 0x%x", byte(kind))
	return Type{}
}
