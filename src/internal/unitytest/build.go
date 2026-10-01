// Package unitytest builds synthetic Unity serialized files and UnityFS
// bundles for tests.
package unitytest

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"unicode/utf16"
)

var le = binary.LittleEndian

// Object is an object to place into a serialized file.
type Object struct {
	PathID  int64
	ClassID int32
	Data    []byte
}

// Serialized builds a version 22 little-endian serialized file without
// type trees.
func Serialized(objects []Object, externals []string) []byte {
	var classes []int32
	typeIndex := map[int32]int32{}
	for _, o := range objects {
		if _, ok := typeIndex[o.ClassID]; !ok {
			typeIndex[o.ClassID] = int32(len(classes))
			classes = append(classes, o.ClassID)
		}
	}

	const headerSize = 48
	meta := &bytes.Buffer{}
	meta.WriteString("2020.3.49f1\x00")
	_ = binary.Write(meta, le, int32(19))
	meta.WriteByte(0)
	_ = binary.Write(meta, le, int32(len(classes)))
	for _, c := range classes {
		_ = binary.Write(meta, le, c)
		meta.WriteByte(0)
		_ = binary.Write(meta, le, int16(-1))
		if c == 114 {
			meta.Write(make([]byte, 16))
		}
		meta.Write(make([]byte, 16))
	}
	_ = binary.Write(meta, le, int32(len(objects)))
	entryPos := make([]int, len(objects))
	for i, o := range objects {
		for (headerSize+meta.Len())%4 != 0 {
			meta.WriteByte(0)
		}
		_ = binary.Write(meta, le, o.PathID)
		entryPos[i] = meta.Len()
		_ = binary.Write(meta, le, int64(0))
		_ = binary.Write(meta, le, uint32(len(o.Data)))
		_ = binary.Write(meta, le, typeIndex[o.ClassID])
	}
	_ = binary.Write(meta, le, int32(0))
	_ = binary.Write(meta, le, int32(len(externals)))
	for _, e := range externals {
		meta.WriteByte(0)
		meta.Write(make([]byte, 16))
		_ = binary.Write(meta, le, int32(0))
		meta.WriteString(e + "\x00")
	}
	_ = binary.Write(meta, le, int32(0))
	meta.WriteByte(0)

	dataOffset := (headerSize + meta.Len() + 15) / 16 * 16
	metaBytes := meta.Bytes()
	var body bytes.Buffer
	for i, o := range objects {
		for body.Len()%8 != 0 {
			body.WriteByte(0)
		}
		le.PutUint64(metaBytes[entryPos[i]:], uint64(body.Len()))
		body.Write(o.Data)
	}

	out := make([]byte, dataOffset, dataOffset+body.Len())
	binary.BigEndian.PutUint32(out[8:], 22)
	binary.BigEndian.PutUint32(out[20:], uint32(len(metaBytes)))
	binary.BigEndian.PutUint64(out[32:], uint64(dataOffset))
	copy(out[headerSize:], metaBytes)
	out = append(out, body.Bytes()...)
	binary.BigEndian.PutUint64(out[24:], uint64(len(out)))
	return out
}

// String encodes a serialized string.
func String(s string) []byte {
	buf := le.AppendUint32(nil, uint32(len(s)))
	buf = append(buf, s...)
	for len(buf)%4 != 0 {
		buf = append(buf, 0)
	}
	return buf
}

func pptr(fileID int32, pathID int64) []byte {
	return le.AppendUint64(le.AppendUint32(nil, uint32(fileID)), uint64(pathID))
}

// MonoBehaviour encodes a MonoBehaviour whose script fields are strings.
func MonoBehaviour(gameObject int64, scriptFile int32, script int64, name string, fields ...string) []byte {
	buf := pptr(0, gameObject)
	buf = append(buf, 1, 0, 0, 0)
	buf = append(buf, pptr(scriptFile, script)...)
	buf = append(buf, String(name)...)
	for _, f := range fields {
		buf = append(buf, String(f)...)
	}
	return buf
}

// GameObject encodes a GameObject with one component.
func GameObject(name string, component int64) []byte {
	buf := le.AppendUint32(nil, 1)
	buf = append(buf, pptr(0, component)...)
	buf = le.AppendUint32(buf, 5)
	buf = append(buf, String(name)...)
	buf = le.AppendUint16(buf, 0)
	return append(buf, 1)
}

// MonoScript encodes a MonoScript.
func MonoScript(class, namespace, assembly string) []byte {
	buf := String(class)
	buf = le.AppendUint32(buf, 0)
	buf = append(buf, make([]byte, 16)...)
	buf = append(buf, String(class)...)
	buf = append(buf, String(namespace)...)
	return append(buf, String(assembly)...)
}

// Node is a file to place into a bundle.
type Node struct {
	Path  string
	Flags uint32
	Data  []byte
}

// Bundle builds a UnityFS version 8 bundle. Blocks of blockSize bytes are
// stored as LZ4 blocks made of literals only, which any LZ4 decoder
// accepts; the blocks info is LZ4-encoded the same way.
func Bundle(nodes []Node, blockSize int) []byte {
	var stream []byte
	var dir bytes.Buffer
	_ = binary.Write(&dir, binary.BigEndian, int32(len(nodes)))
	for _, n := range nodes {
		_ = binary.Write(&dir, binary.BigEndian, struct {
			Offset, Size int64
			Flags        uint32
		}{int64(len(stream)), int64(len(n.Data)), n.Flags})
		dir.WriteString(n.Path + "\x00")
		stream = append(stream, n.Data...)
	}

	var info, blocks bytes.Buffer
	info.Write(make([]byte, 16))
	count := (len(stream) + blockSize - 1) / blockSize
	_ = binary.Write(&info, binary.BigEndian, int32(count))
	for i := 0; i < len(stream); i += blockSize {
		chunk := stream[i:min(i+blockSize, len(stream))]
		enc := LiteralLZ4(chunk)
		_ = binary.Write(&info, binary.BigEndian, struct {
			USize, CSize uint32
			Flags        uint16
		}{uint32(len(chunk)), uint32(len(enc)), 3})
		blocks.Write(enc)
	}
	info.Write(dir.Bytes())
	encInfo := LiteralLZ4(info.Bytes())

	var out bytes.Buffer
	out.WriteString("UnityFS\x00")
	_ = binary.Write(&out, binary.BigEndian, uint32(8))
	out.WriteString("5.x.x\x002020.3.49f1\x00")
	sizePos := out.Len()
	_ = binary.Write(&out, binary.BigEndian, struct {
		Size                                    int64
		CompressedInfo, UncompressedInfo, Flags uint32
	}{0, uint32(len(encInfo)), uint32(info.Len()), 0x40 | 0x200 | 2})
	for out.Len()%16 != 0 {
		out.WriteByte(0)
	}
	out.Write(encInfo)
	for out.Len()%16 != 0 {
		out.WriteByte(0)
	}
	out.Write(blocks.Bytes())
	res := out.Bytes()
	binary.BigEndian.PutUint64(res[sizePos:], uint64(len(res)))
	return res
}

// LiteralLZ4 encodes data as a single LZ4 sequence of literals.
func LiteralLZ4(data []byte) []byte {
	n := len(data)
	if n < 15 {
		return append([]byte{byte(n << 4)}, data...)
	}
	out := []byte{0xf0}
	for rest := n - 15; ; rest -= 255 {
		if rest < 255 {
			out = append(out, byte(rest))
			break
		}
		out = append(out, 255)
	}
	return append(out, data...)
}

// TTFSpec describes a minimal synthetic TrueType font.
type TTFSpec struct {
	Family     string
	UnitsPerEm uint16
	Ascender   int16
	Descender  int16
	LineGap    int16
	// Chars maps characters to glyph indices.
	Chars map[rune]uint16
	// Format12 writes the character map as format 12 instead of 4.
	Format12 bool
	Kern     []KernPair
	// KernLength overrides the declared kern subtable length.
	KernLength uint16
}

// KernPair is one glyph pair of the 'kern' table.
type KernPair struct {
	Left, Right uint16
	Value       int16
}

// TTF builds a font with head, hhea, cmap, name and (optionally) kern
// tables.
func TTF(s TTFSpec) []byte {
	be := binary.BigEndian
	head := make([]byte, 54)
	be.PutUint16(head[18:], s.UnitsPerEm)
	hhea := make([]byte, 36)
	be.PutUint16(hhea[4:], uint16(s.Ascender))
	be.PutUint16(hhea[6:], uint16(s.Descender))
	be.PutUint16(hhea[8:], uint16(s.LineGap))

	tables := map[string][]byte{"head": head, "hhea": hhea, "cmap": ttfCmap(s), "name": ttfName(s.Family)}
	if s.Kern != nil {
		sub := make([]byte, 14, 14+6*len(s.Kern))
		length := s.KernLength
		if length == 0 {
			length = uint16(14 + 6*len(s.Kern))
		}
		be.PutUint16(sub[2:], length)
		be.PutUint16(sub[4:], 0x0001)
		be.PutUint16(sub[6:], uint16(len(s.Kern)))
		for _, p := range s.Kern {
			sub = be.AppendUint16(sub, p.Left)
			sub = be.AppendUint16(sub, p.Right)
			sub = be.AppendUint16(sub, uint16(p.Value))
		}
		tables["kern"] = append([]byte{0, 0, 0, 1}, sub...)
	}

	tags := []string{"cmap", "head", "hhea", "kern", "name"}
	var dir, body bytes.Buffer
	count := 0
	for _, tag := range tags {
		if _, ok := tables[tag]; ok {
			count++
		}
	}
	dir.Write([]byte{0, 1, 0, 0})
	_ = binary.Write(&dir, be, uint16(count))
	dir.Write(make([]byte, 6))
	offset := 12 + 16*count
	for _, tag := range tags {
		t, ok := tables[tag]
		if !ok {
			continue
		}
		dir.WriteString(tag)
		_ = binary.Write(&dir, be, uint32(0))
		_ = binary.Write(&dir, be, uint32(offset+body.Len()))
		_ = binary.Write(&dir, be, uint32(len(t)))
		body.Write(t)
		for body.Len()%4 != 0 {
			body.WriteByte(0)
		}
	}
	return append(dir.Bytes(), body.Bytes()...)
}

func ttfCmap(s TTFSpec) []byte {
	be := binary.BigEndian
	chars := make([]rune, 0, len(s.Chars))
	for r := range s.Chars {
		chars = append(chars, r)
	}
	slices.Sort(chars)
	var sub []byte
	if s.Format12 {
		sub = be.AppendUint16(nil, 12)
		sub = append(sub, 0, 0)
		sub = be.AppendUint32(sub, uint32(16+12*len(chars)))
		sub = be.AppendUint32(sub, 0)
		sub = be.AppendUint32(sub, uint32(len(chars)))
		for _, r := range chars {
			sub = be.AppendUint32(sub, uint32(r))
			sub = be.AppendUint32(sub, uint32(r))
			sub = be.AppendUint32(sub, uint32(s.Chars[r]))
		}
	} else {
		// One segment per character using idRangeOffset, plus the final
		// 0xFFFF segment.
		segs := len(chars) + 1
		var ends, starts, deltas, ranges, glyphs []byte
		for i, r := range chars {
			ends = be.AppendUint16(ends, uint16(r))
			starts = be.AppendUint16(starts, uint16(r))
			deltas = be.AppendUint16(deltas, 0)
			// Distance from this idRangeOffset entry to its glyph slot.
			ranges = be.AppendUint16(ranges, uint16(2*(segs-i)+2*i))
			glyphs = be.AppendUint16(glyphs, s.Chars[r])
		}
		ends = be.AppendUint16(ends, 0xffff)
		starts = be.AppendUint16(starts, 0xffff)
		deltas = be.AppendUint16(deltas, 1)
		ranges = be.AppendUint16(ranges, 0)
		sub = be.AppendUint16(nil, 4)
		sub = be.AppendUint16(sub, 0)
		sub = append(sub, 0, 0)
		sub = be.AppendUint16(sub, uint16(2*segs))
		sub = append(sub, make([]byte, 6)...)
		sub = append(sub, ends...)
		sub = append(sub, 0, 0)
		sub = append(sub, starts...)
		sub = append(sub, deltas...)
		sub = append(sub, ranges...)
		sub = append(sub, glyphs...)
		be.PutUint16(sub[2:], uint16(len(sub)))
	}
	cmap := be.AppendUint16(nil, 0)
	cmap = be.AppendUint16(cmap, 1)
	cmap = be.AppendUint16(cmap, 3)
	encoding := uint16(1)
	if s.Format12 {
		encoding = 10
	}
	cmap = be.AppendUint16(cmap, encoding)
	cmap = be.AppendUint32(cmap, 12)
	return append(cmap, sub...)
}

func ttfName(family string) []byte {
	be := binary.BigEndian
	units := utf16.Encode([]rune(family))
	name := be.AppendUint16(nil, 0)
	name = be.AppendUint16(name, 1)
	name = be.AppendUint16(name, 6+12)
	for _, v := range []uint16{3, 1, 0x409, 1, uint16(2 * len(units)), 0} {
		name = be.AppendUint16(name, v)
	}
	for _, u := range units {
		name = be.AppendUint16(name, u)
	}
	return name
}

// Font encodes a Font object with the given TTF data and kerning pairs
// (left, right, value triples).
func Font(name string, size float32, ttf []byte, kerning [][3]float32) []byte {
	f32 := func(b []byte, v float32) []byte { return le.AppendUint32(b, math.Float32bits(v)) }
	buf := String(name)
	buf = f32(buf, 16)                     // m_LineSpacing
	buf = append(buf, pptr(0, 5)...)       // m_DefaultMaterial
	buf = f32(buf, size)                   // m_FontSize
	buf = append(buf, pptr(0, 6)...)       // m_Texture
	buf = le.AppendUint32(buf, 0)          // m_AsciiStartOffset
	buf = f32(buf, 1)                      // m_Tracking
	buf = le.AppendUint32(buf, 0)          // m_CharacterSpacing
	buf = le.AppendUint32(buf, 1)          // m_CharacterPadding
	buf = le.AppendUint32(buf, 0xfffffffe) // m_ConvertCase
	buf = le.AppendUint32(buf, 1)          // one CharacterInfo
	buf = append(buf, make([]byte, 44)...)
	buf = le.AppendUint32(buf, uint32(len(kerning)))
	for _, k := range kerning {
		buf = le.AppendUint16(buf, uint16(k[0]))
		buf = le.AppendUint16(buf, uint16(k[1]))
		buf = f32(buf, k[2])
	}
	buf = f32(buf, 0.1)
	buf = le.AppendUint32(buf, uint32(len(ttf)))
	buf = append(buf, ttf...)
	for len(buf)%4 != 0 {
		buf = append(buf, 0)
	}
	buf = f32(buf, 12)
	buf = f32(buf, -4)
	buf = le.AppendUint32(buf, 0)
	buf = le.AppendUint32(buf, 1)
	buf = append(buf, String(name)...)
	buf = le.AppendUint32(buf, 1)
	buf = append(buf, pptr(1, 9)...)
	buf = le.AppendUint32(buf, 0)
	return append(buf, 1, 0)
}
