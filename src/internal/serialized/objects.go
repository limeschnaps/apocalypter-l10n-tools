package serialized

import (
	"encoding/binary"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// PPtr is a reference to an object: FileID 0 means the same file, N means
// Externals[N-1].
type PPtr struct {
	FileID int32
	PathID int64
}

// MonoBehaviourHeader holds the fields every MonoBehaviour starts with.
// Script-defined fields follow at FieldsOffset.
type MonoBehaviourHeader struct {
	GameObject   PPtr
	Enabled      bool
	Script       PPtr
	Name         string
	FieldsOffset int
}

// MonoScript identifies a script class.
type MonoScript struct {
	Name         string
	ClassName    string
	Namespace    string
	AssemblyName string
}

// The layouts below match Unity 2019.x-2022.x player builds without type
// trees.

// ReadMonoBehaviourHeader decodes the common MonoBehaviour prefix.
func ReadMonoBehaviourHeader(data []byte, order binary.ByteOrder) (MonoBehaviourHeader, error) {
	r := &reader{data: data, order: order}
	var h MonoBehaviourHeader
	h.GameObject = readPPtr(r)
	h.Enabled = r.u8() != 0
	r.align(4)
	h.Script = readPPtr(r)
	h.Name = readString(r)
	h.FieldsOffset = r.pos
	if r.err != nil {
		return h, fmt.Errorf("%w: MonoBehaviour header: %w", ErrFormat, r.err)
	}
	return h, nil
}

// ReadGameObjectName returns m_Name of a GameObject.
func ReadGameObjectName(data []byte, order binary.ByteOrder) (string, error) {
	r := &reader{data: data, order: order}
	components := r.count()
	r.skip(components * 12)
	r.u32() // m_Layer
	name := readString(r)
	if r.err != nil {
		return "", fmt.Errorf("%w: GameObject: %w", ErrFormat, r.err)
	}
	return name, nil
}

// ReadMonoScript decodes a MonoScript.
func ReadMonoScript(data []byte, order binary.ByteOrder) (MonoScript, error) {
	r := &reader{data: data, order: order}
	var s MonoScript
	s.Name = readString(r)
	r.i32()    // m_ExecutionOrder
	r.skip(16) // m_PropertiesHash
	s.ClassName = readString(r)
	s.Namespace = readString(r)
	s.AssemblyName = readString(r)
	if r.err != nil {
		return s, fmt.Errorf("%w: MonoScript: %w", ErrFormat, r.err)
	}
	return s, nil
}

func readPPtr(r *reader) PPtr {
	return PPtr{FileID: r.i32(), PathID: r.i64()}
}

// readString reads a length-prefixed UTF-8 string padded to 4 bytes.
func readString(r *reader) string {
	n := r.count()
	s := string(r.take(n))
	r.align(4)
	if r.err == nil && !utf8.ValidString(s) {
		r.err = fmt.Errorf("invalid UTF-8 string")
	}
	return s
}

// EncodeString returns the serialized form of s: int32 length, UTF-8
// bytes and zero padding to a multiple of 4.
func EncodeString(s string, order binary.ByteOrder) []byte {
	n := len(s)
	buf := make([]byte, 4+n+(4-n%4)%4)
	order.PutUint32(buf, uint32(n))
	copy(buf[4:], s)
	return buf
}

// StringValue is a string that ScanStrings found in object data.
type StringValue struct {
	// Offset and Size locate the encoded string, including its length
	// prefix and padding.
	Offset int
	Size   int
	Value  string
}

// minScanLength keeps ScanStrings from reading an int32 1 followed by a
// small number as a one-letter string, a common pattern in arrays.
const minScanLength = 2

// ScanStrings finds text strings in object data that has no type tree.
//
// Unity writes a string as an int32 length, UTF-8 bytes and zero padding
// to 4 bytes, and every field of a script starts 4-aligned. ScanStrings
// checks every 4-aligned offset from "from" and accepts a string when its
// bytes are valid UTF-8 made of printable characters with at least one
// letter, and its padding is zero. Other data rarely looks like this, but
// the result is a heuristic: numbers can occasionally pass as text.
func ScanStrings(data []byte, from int, order binary.ByteOrder) []StringValue {
	var out []StringValue
	for pos := from; pos >= 0 && pos+4 <= len(data); {
		n := int(order.Uint32(data[pos:]))
		size := 4 + n + (4-n%4)%4
		if n < minScanLength || size > len(data)-pos || !isText(data[pos+4:pos+4+n]) || !isZero(data[pos+4+n:pos+size]) {
			pos += 4
			continue
		}
		out = append(out, StringValue{Offset: pos, Size: size, Value: string(data[pos+4 : pos+4+n])})
		pos += size
	}
	return out
}

func isText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	letter := false
	for _, r := range string(b) {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsPrint(r), r == '\n', r == '\r', r == '\t':
		default:
			return false
		}
	}
	return letter
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
