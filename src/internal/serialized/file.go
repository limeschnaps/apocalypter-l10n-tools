// Package serialized reads and rewrites Unity SerializedFile data: the
// format of level*, *.assets and globalgamemanagers files in a player
// build.
//
// Supported format versions are 17 through 22 (Unity 5.5 to 2022).
package serialized

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Class IDs used by the patcher.
const (
	ClassGameObject    = 1
	ClassMonoBehaviour = 114
	ClassMonoScript    = 115
)

const (
	minVersion          = 17
	maxVersion          = 22
	versionLargeFiles   = 22
	versionRefTypes     = 20
	versionTypeDeps     = 21
	versionTypeTreeHash = 19
	legacyHeaderSize    = 20
	largeHeaderSize     = 48
	objectAlign         = 8
)

// Errors returned by the package.
var (
	ErrFormat      = errors.New("serialized: malformed file")
	ErrUnsupported = errors.New("serialized: unsupported file version")
)

// Type describes one entry of the type table.
type Type struct {
	ClassID         int32
	ScriptTypeIndex int16
}

// External is a reference to another serialized file.
type External struct {
	Path string
}

// Object is one entry of the object table.
type Object struct {
	PathID  int64
	ClassID int32
	// Offset and Size locate the object data within the file.
	Offset int64
	Size   uint32

	startPos int // position of byteStart in the metadata
}

// File is a parsed serialized file. Object data is read from the original
// bytes, which the File keeps.
type File struct {
	Version       uint32
	UnityVersion  string
	Platform      int32
	TypeTree      bool
	Types         []Type
	Objects       []Object
	Externals     []External
	LittleEndian  bool
	data          []byte
	dataOffset    int64
	objectsByPath map[int64]int
}

// Parse reads the header and metadata of data.
func Parse(data []byte) (*File, error) {
	if len(data) < legacyHeaderSize {
		return nil, fmt.Errorf("%w: too short", ErrFormat)
	}
	f := &File{data: data, Version: binary.BigEndian.Uint32(data[8:12])}
	if f.Version < minVersion || f.Version > maxVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnsupported, f.Version)
	}

	var metaStart int
	var metaSize, fileSize int64
	var endian byte
	if f.Version >= versionLargeFiles {
		if len(data) < largeHeaderSize {
			return nil, fmt.Errorf("%w: too short", ErrFormat)
		}
		endian = data[16]
		metaSize = int64(binary.BigEndian.Uint32(data[20:24]))
		fileSize = int64(binary.BigEndian.Uint64(data[24:32]))
		f.dataOffset = int64(binary.BigEndian.Uint64(data[32:40]))
		metaStart = largeHeaderSize
	} else {
		metaSize = int64(binary.BigEndian.Uint32(data[0:4]))
		fileSize = int64(binary.BigEndian.Uint32(data[4:8]))
		f.dataOffset = int64(binary.BigEndian.Uint32(data[12:16]))
		endian = data[16]
		metaStart = legacyHeaderSize
	}
	if fileSize != int64(len(data)) || f.dataOffset > fileSize || int64(metaStart)+metaSize > f.dataOffset {
		return nil, fmt.Errorf("%w: header sizes do not match data", ErrFormat)
	}
	f.LittleEndian = endian == 0

	r := &reader{data: data[:int64(metaStart)+metaSize], pos: metaStart, order: binary.ByteOrder(binary.BigEndian)}
	if f.LittleEndian {
		r.order = binary.LittleEndian
	}
	if err := f.parseMetadata(r); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *File) parseMetadata(r *reader) error {
	f.UnityVersion = r.cstring()
	f.Platform = r.i32()
	f.TypeTree = r.u8() != 0

	typeCount := r.count()
	for range typeCount {
		t, err := f.readType(r, false)
		if err != nil {
			return err
		}
		f.Types = append(f.Types, t)
	}

	objectCount := r.count()
	f.objectsByPath = make(map[int64]int, objectCount)
	for range objectCount {
		r.align(4)
		var o Object
		o.PathID = r.i64()
		o.startPos = r.pos
		if f.Version >= versionLargeFiles {
			o.Offset = r.i64()
		} else {
			o.Offset = int64(r.u32())
		}
		o.Size = r.u32()
		typeIndex := r.i32()
		if r.err != nil {
			break
		}
		if typeIndex < 0 || int(typeIndex) >= len(f.Types) {
			return fmt.Errorf("%w: object %d has type index %d", ErrFormat, o.PathID, typeIndex)
		}
		o.ClassID = f.Types[typeIndex].ClassID
		o.Offset += f.dataOffset
		if o.Offset+int64(o.Size) > int64(len(f.data)) {
			return fmt.Errorf("%w: object %d out of range", ErrFormat, o.PathID)
		}
		f.objectsByPath[o.PathID] = len(f.Objects)
		f.Objects = append(f.Objects, o)
	}

	scriptCount := r.count()
	for range scriptCount {
		r.i32()
		r.align(4)
		r.i64()
	}

	externalCount := r.count()
	for range externalCount {
		r.cstring()
		r.skip(16 + 4)
		f.Externals = append(f.Externals, External{Path: r.cstring()})
	}
	if r.err != nil {
		return fmt.Errorf("%w: metadata: %w", ErrFormat, r.err)
	}
	return nil
}

func (f *File) readType(r *reader, refType bool) (Type, error) {
	var t Type
	t.ClassID = r.i32()
	r.u8() // isStrippedType
	t.ScriptTypeIndex = r.i16()
	if (refType && t.ScriptTypeIndex >= 0) || t.ClassID == ClassMonoBehaviour {
		r.skip(16) // script ID
	}
	r.skip(16) // old type hash
	if f.TypeTree {
		nodes := r.count()
		strBuf := r.count()
		nodeSize := 24
		if f.Version >= versionTypeTreeHash {
			nodeSize = 32
		}
		r.skip(nodes*nodeSize + strBuf)
		if f.Version >= versionTypeDeps {
			if refType {
				r.cstring()
				r.cstring()
				r.cstring()
			} else {
				r.skip(r.count() * 4)
			}
		}
	}
	if r.err != nil {
		return t, fmt.Errorf("%w: type table: %w", ErrFormat, r.err)
	}
	return t, nil
}

// Object returns the object with the given path ID.
func (f *File) Object(pathID int64) (Object, bool) {
	i, ok := f.objectsByPath[pathID]
	if !ok {
		return Object{}, false
	}
	return f.Objects[i], true
}

// Data returns the raw bytes of o. The slice aliases the file data.
func (f *File) Data(o Object) []byte {
	return f.data[o.Offset : o.Offset+int64(o.Size)]
}

// ByteOrder can both decode and append multi-byte values.
type ByteOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// ByteOrder returns the byte order of object data.
func (f *File) ByteOrder() ByteOrder {
	if f.LittleEndian {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

// Rewrite returns a copy of the file in which the objects in replace (by
// path ID) have new data.
//
// Bytes before the first replaced object stay identical. Every object
// after a replaced one moves by a multiple of 8, so the original
// alignment of all objects is preserved.
func (f *File) Rewrite(replace map[int64][]byte) ([]byte, error) {
	for id := range replace {
		if _, ok := f.objectsByPath[id]; !ok {
			return nil, fmt.Errorf("%w: no object %d", ErrFormat, id)
		}
	}
	order := make([]int, len(f.Objects))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		return int(f.Objects[a].Offset - f.Objects[b].Offset)
	})

	out := bytes.NewBuffer(make([]byte, 0, len(f.data)))
	newOffset := make([]int64, len(f.Objects))
	newSize := make([]uint32, len(f.Objects))
	copied := f.dataOffset
	out.Write(f.data[:copied])
	for k, i := range order {
		o := f.Objects[i]
		if o.Offset < copied {
			return nil, fmt.Errorf("%w: objects overlap at %d", ErrFormat, o.PathID)
		}
		out.Write(f.data[copied:o.Offset])
		newOffset[i] = int64(out.Len())
		data, ok := replace[o.PathID]
		if !ok {
			out.Write(f.Data(o))
			newSize[i] = o.Size
			copied = o.Offset + int64(o.Size)
			continue
		}
		next := int64(len(f.data))
		if k+1 < len(order) {
			next = f.Objects[order[k+1]].Offset
		}
		// The replaced object and its trailing gap take a slot whose
		// length differs from the original by a multiple of 8.
		slot := next - o.Offset
		length := int64(len(data))
		padding := (slot - length) % objectAlign
		if padding < 0 {
			padding += objectAlign
		}
		out.Write(data)
		out.Write(make([]byte, padding))
		newSize[i] = uint32(len(data))
		copied = next
	}
	out.Write(f.data[copied:])

	res := out.Bytes()
	order2 := f.ByteOrder()
	for i, o := range f.Objects {
		rel := newOffset[i] - f.dataOffset
		pos := o.startPos
		if f.Version >= versionLargeFiles {
			order2.PutUint64(res[pos:], uint64(rel))
			pos += 8
		} else {
			if rel > int64(^uint32(0)) {
				return nil, fmt.Errorf("%w: file exceeds 4 GiB", ErrUnsupported)
			}
			order2.PutUint32(res[pos:], uint32(rel))
			pos += 4
		}
		order2.PutUint32(res[pos:], newSize[i])
	}
	if f.Version >= versionLargeFiles {
		binary.BigEndian.PutUint64(res[24:32], uint64(len(res)))
	} else {
		binary.BigEndian.PutUint32(res[4:8], uint32(len(res)))
	}
	return res, nil
}

type reader struct {
	data  []byte
	pos   int
	order binary.ByteOrder
	err   error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.pos+n > len(r.data) {
		r.err = io.ErrUnexpectedEOF
		return nil
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b
}

func (r *reader) skip(n int) { r.take(n) }

func (r *reader) u8() byte {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) i16() int16 {
	if b := r.take(2); b != nil {
		return int16(r.order.Uint16(b))
	}
	return 0
}

func (r *reader) u32() uint32 {
	if b := r.take(4); b != nil {
		return r.order.Uint32(b)
	}
	return 0
}

func (r *reader) i32() int32 { return int32(r.u32()) }

func (r *reader) i64() int64 {
	if b := r.take(8); b != nil {
		return int64(r.order.Uint64(b))
	}
	return 0
}

// count reads a non-negative int32 element count bounded by the remaining
// data, so corrupt input cannot trigger huge allocations.
func (r *reader) count() int {
	n := r.i32()
	if r.err == nil && (n < 0 || int(n) > len(r.data)-r.pos) {
		r.err = fmt.Errorf("implausible count %d", n)
	}
	if r.err != nil {
		return 0
	}
	return int(n)
}

func (r *reader) cstring() string {
	if r.err != nil {
		return ""
	}
	end := bytes.IndexByte(r.data[r.pos:], 0)
	if end < 0 {
		r.err = io.ErrUnexpectedEOF
		return ""
	}
	s := string(r.data[r.pos : r.pos+end])
	r.pos += end + 1
	return s
}

func (r *reader) align(n int) {
	r.pos = (r.pos + n - 1) / n * n
	if r.pos > len(r.data) {
		r.err = io.ErrUnexpectedEOF
	}
}
