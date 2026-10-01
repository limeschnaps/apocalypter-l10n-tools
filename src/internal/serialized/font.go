package serialized

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ClassFont is the class ID of Font objects.
const ClassFont = 128

// characterInfoSize is the serialized size of one CharacterInfo entry
// (index, uv rect, vert rect, advance, flipped + padding).
const characterInfoSize = 44

// KerningValue is one m_KerningValues entry: a character pair and the
// adjustment in pixels at the font's import size.
type KerningValue struct {
	Left, Right uint16
	Value       float32
}

// Font is a decoded Font object (Unity 2019.x-2022.x layout).
type Font struct {
	Name              string
	LineSpacing       float32
	DefaultMaterial   PPtr
	FontSize          float32
	Texture           PPtr
	AsciiStartOffset  int32
	Tracking          float32
	CharacterSpacing  int32
	CharacterPadding  int32
	ConvertCase       int32
	CharacterRects    [][]byte
	Kerning           []KerningValue
	PixelScale        float32
	FontData          []byte
	Ascent            float32
	Descent           float32
	DefaultStyle      uint32
	FontNames         []string
	FallbackFonts     []PPtr
	RenderingMode     int32
	LegacyBounds      bool
	RoundAdvanceValue bool
}

// ReadFont decodes a Font object and requires the data to be consumed
// exactly, which guards against layout differences in other Unity
// versions.
func ReadFont(data []byte, order binary.ByteOrder) (*Font, error) {
	r := &reader{data: data, order: order}
	f := &Font{}
	f.Name = readString(r)
	f.LineSpacing = r.f32()
	f.DefaultMaterial = readPPtr(r)
	f.FontSize = r.f32()
	f.Texture = readPPtr(r)
	f.AsciiStartOffset = r.i32()
	f.Tracking = r.f32()
	f.CharacterSpacing = r.i32()
	f.CharacterPadding = r.i32()
	f.ConvertCase = r.i32()
	for range r.countOf(characterInfoSize) {
		f.CharacterRects = append(f.CharacterRects, r.take(characterInfoSize))
	}
	for range r.countOf(8) {
		f.Kerning = append(f.Kerning, KerningValue{Left: r.u16(), Right: r.u16(), Value: r.f32()})
	}
	f.PixelScale = r.f32()
	f.FontData = r.take(r.count())
	r.align(4)
	f.Ascent = r.f32()
	f.Descent = r.f32()
	f.DefaultStyle = r.u32()
	for range r.countOf(4) {
		f.FontNames = append(f.FontNames, readString(r))
	}
	for range r.countOf(12) {
		f.FallbackFonts = append(f.FallbackFonts, readPPtr(r))
	}
	f.RenderingMode = r.i32()
	f.LegacyBounds = r.u8() != 0
	f.RoundAdvanceValue = r.u8() != 0
	if r.err == nil && r.pos != len(data) {
		r.err = fmt.Errorf("%d trailing bytes", len(data)-r.pos)
	}
	if r.err != nil {
		return nil, fmt.Errorf("%w: Font: %w", ErrFormat, r.err)
	}
	return f, nil
}

// Encode serializes the font in the layout ReadFont reads.
func (f *Font) Encode(order ByteOrder) []byte {
	w := &writer{order: order}
	w.str(f.Name)
	w.f32(f.LineSpacing)
	w.pptr(f.DefaultMaterial)
	w.f32(f.FontSize)
	w.pptr(f.Texture)
	w.u32(uint32(f.AsciiStartOffset))
	w.f32(f.Tracking)
	w.u32(uint32(f.CharacterSpacing))
	w.u32(uint32(f.CharacterPadding))
	w.u32(uint32(f.ConvertCase))
	w.u32(uint32(len(f.CharacterRects)))
	for _, c := range f.CharacterRects {
		w.buf = append(w.buf, c...)
	}
	w.u32(uint32(len(f.Kerning)))
	for _, k := range f.Kerning {
		w.buf = order.AppendUint16(w.buf, k.Left)
		w.buf = order.AppendUint16(w.buf, k.Right)
		w.f32(k.Value)
	}
	w.f32(f.PixelScale)
	w.u32(uint32(len(f.FontData)))
	w.buf = append(w.buf, f.FontData...)
	w.align()
	w.f32(f.Ascent)
	w.f32(f.Descent)
	w.u32(f.DefaultStyle)
	w.u32(uint32(len(f.FontNames)))
	for _, n := range f.FontNames {
		w.str(n)
	}
	w.u32(uint32(len(f.FallbackFonts)))
	for _, p := range f.FallbackFonts {
		w.pptr(p)
	}
	w.u32(uint32(f.RenderingMode))
	w.buf = append(w.buf, boolByte(f.LegacyBounds), boolByte(f.RoundAdvanceValue))
	return w.buf
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

type writer struct {
	buf   []byte
	order ByteOrder
}

func (w *writer) u32(v uint32)  { w.buf = w.order.AppendUint32(w.buf, v) }
func (w *writer) f32(v float32) { w.u32(math.Float32bits(v)) }
func (w *writer) str(s string)  { w.buf = append(w.buf, EncodeString(s, w.order)...) }
func (w *writer) pptr(p PPtr) {
	w.u32(uint32(p.FileID))
	w.buf = w.order.AppendUint64(w.buf, uint64(p.PathID))
}
func (w *writer) align()       { w.buf = append(w.buf, make([]byte, (4-len(w.buf)%4)%4)...) }
func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) countOf(size int) int {
	n := r.i32()
	if r.err == nil && (n < 0 || int(n)*size > len(r.data)-r.pos) {
		r.err = fmt.Errorf("implausible count %d", n)
	}
	if r.err != nil {
		return 0
	}
	return int(n)
}

func (r *reader) u16() uint16 {
	if b := r.take(2); b != nil {
		return r.order.Uint16(b)
	}
	return 0
}
