// Package ttf reads the parts of a TrueType/OpenType font that Unity uses
// when it imports a dynamic font: vertical metrics, the character map and
// the legacy 'kern' table.
package ttf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"unicode/utf16"
)

// ErrFormat reports data that is not a usable font.
var ErrFormat = errors.New("ttf: malformed font")

var be = binary.BigEndian

// Font holds the parsed tables.
type Font struct {
	Family     string
	UnitsPerEm uint16
	Ascender   int16
	Descender  int16
	LineGap    int16

	cmap map[rune]uint16
	kern []glyphPair
}

type glyphPair struct {
	left, right uint16
	value       int16
}

// KerningPair is a kerning adjustment between two characters in font
// units.
type KerningPair struct {
	Left, Right rune
	Value       int16
}

// Parse reads a TrueType (0x00010000, "true") or CFF-based ("OTTO") font.
func Parse(data []byte) (*Font, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("%w: too short", ErrFormat)
	}
	switch be.Uint32(data) {
	case 0x00010000, 0x74727565, 0x4f54544f:
	default:
		return nil, fmt.Errorf("%w: unknown signature % x", ErrFormat, data[:4])
	}
	tables := map[string][]byte{}
	count := int(be.Uint16(data[4:]))
	if len(data) < 12+16*count {
		return nil, fmt.Errorf("%w: truncated table directory", ErrFormat)
	}
	for i := range count {
		rec := data[12+16*i:]
		off, length := int(be.Uint32(rec[8:])), int(be.Uint32(rec[12:]))
		if off < 0 || length < 0 || off+length > len(data) {
			return nil, fmt.Errorf("%w: table %q out of range", ErrFormat, rec[:4])
		}
		tables[string(rec[:4])] = data[off : off+length]
	}

	f := &Font{}
	head, hhea := tables["head"], tables["hhea"]
	if len(head) < 54 || len(hhea) < 36 {
		return nil, fmt.Errorf("%w: missing head or hhea table", ErrFormat)
	}
	f.UnitsPerEm = be.Uint16(head[18:])
	if f.UnitsPerEm == 0 {
		return nil, fmt.Errorf("%w: unitsPerEm is zero", ErrFormat)
	}
	f.Ascender = int16(be.Uint16(hhea[4:]))
	f.Descender = int16(be.Uint16(hhea[6:]))
	f.LineGap = int16(be.Uint16(hhea[8:]))

	var err error
	if f.cmap, err = parseCmap(tables["cmap"]); err != nil {
		return nil, err
	}
	if f.kern, err = parseKern(tables["kern"]); err != nil {
		return nil, err
	}
	f.Family = parseFamily(tables["name"])
	return f, nil
}

// Has reports whether the font maps r to a glyph.
func (f *Font) Has(r rune) bool {
	_, ok := f.cmap[r]
	return ok
}

// LineHeight returns ascender - descender + line gap in em.
func (f *Font) LineHeight() float64 {
	return float64(int(f.Ascender)-int(f.Descender)+int(f.LineGap)) / float64(f.UnitsPerEm)
}

// Glyph returns the glyph index for r.
func (f *Font) Glyph(r rune) (uint16, bool) {
	g, ok := f.cmap[r]
	return g, ok
}

// Missing returns the runes of s that the font cannot display, in order
// and without duplicates.
func (f *Font) Missing(s string) []rune {
	var out []rune
	for _, r := range s {
		if !f.Has(r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// KerningPairs expands the glyph pairs of the 'kern' table to character
// pairs in table order. A glyph shared by several characters yields a
// pair for each of them, in ascending character order.
func (f *Font) KerningPairs() []KerningPair {
	byGlyph := map[uint16][]rune{}
	for r, g := range f.cmap {
		byGlyph[g] = append(byGlyph[g], r)
	}
	for _, rs := range byGlyph {
		slices.Sort(rs)
	}
	var out []KerningPair
	for _, p := range f.kern {
		for _, l := range byGlyph[p.left] {
			for _, r := range byGlyph[p.right] {
				out = append(out, KerningPair{Left: l, Right: r, Value: p.value})
			}
		}
	}
	return out
}

func parseCmap(t []byte) (map[rune]uint16, error) {
	if len(t) < 4 {
		return nil, fmt.Errorf("%w: missing cmap table", ErrFormat)
	}
	// Prefer the full Unicode repertoire (format 12) over the BMP subtable.
	var best []byte
	bestRank := 0
	n := int(be.Uint16(t[2:]))
	for i := range n {
		rec := 4 + 8*i
		if rec+8 > len(t) {
			return nil, fmt.Errorf("%w: truncated cmap", ErrFormat)
		}
		platform, encoding := be.Uint16(t[rec:]), be.Uint16(t[rec+2:])
		off := int(be.Uint32(t[rec+4:]))
		if off+4 > len(t) {
			return nil, fmt.Errorf("%w: cmap subtable out of range", ErrFormat)
		}
		format := be.Uint16(t[off:])
		unicode := platform == 0 || (platform == 3 && (encoding == 1 || encoding == 10))
		rank := 0
		switch {
		case unicode && format == 12:
			rank = 2
		case unicode && format == 4:
			rank = 1
		}
		if rank > bestRank {
			best, bestRank = t[off:], rank
		}
	}
	switch bestRank {
	case 2:
		return parseCmap12(best)
	case 1:
		return parseCmap4(best)
	default:
		return nil, fmt.Errorf("%w: no Unicode cmap subtable", ErrFormat)
	}
}

func parseCmap4(t []byte) (map[rune]uint16, error) {
	if len(t) < 14 {
		return nil, fmt.Errorf("%w: truncated cmap format 4", ErrFormat)
	}
	segs := int(be.Uint16(t[6:])) / 2
	endAt, startAt := 14, 16+2*segs
	deltaAt, rangeAt := startAt+2*segs, startAt+4*segs
	if rangeAt+2*segs > len(t) {
		return nil, fmt.Errorf("%w: truncated cmap format 4", ErrFormat)
	}
	m := map[rune]uint16{}
	for s := range segs {
		end, start := int(be.Uint16(t[endAt+2*s:])), int(be.Uint16(t[startAt+2*s:]))
		delta, ro := be.Uint16(t[deltaAt+2*s:]), int(be.Uint16(t[rangeAt+2*s:]))
		for c := start; c <= end && c != 0xffff; c++ {
			var g uint16
			if ro == 0 {
				g = uint16(c) + delta
			} else {
				at := rangeAt + 2*s + ro + 2*(c-start)
				if at+2 > len(t) {
					return nil, fmt.Errorf("%w: cmap glyph index out of range", ErrFormat)
				}
				if g = be.Uint16(t[at:]); g != 0 {
					g += delta
				}
			}
			if g != 0 {
				m[rune(c)] = g
			}
		}
	}
	return m, nil
}

func parseCmap12(t []byte) (map[rune]uint16, error) {
	if len(t) < 16 {
		return nil, fmt.Errorf("%w: truncated cmap format 12", ErrFormat)
	}
	groups := int(be.Uint32(t[12:]))
	if groups < 0 || 16+12*groups > len(t) {
		return nil, fmt.Errorf("%w: truncated cmap format 12", ErrFormat)
	}
	m := map[rune]uint16{}
	for i := range groups {
		g := t[16+12*i:]
		start, end, glyph := be.Uint32(g), be.Uint32(g[4:]), be.Uint32(g[8:])
		if end < start || end > 0x10ffff {
			return nil, fmt.Errorf("%w: bad cmap group", ErrFormat)
		}
		for c := start; c <= end; c++ {
			if id := glyph + (c - start); id != 0 {
				m[rune(c)] = uint16(id)
			}
		}
	}
	return m, nil
}

// parseKern reads horizontal format 0 subtables of a version 0 'kern'
// table; other layouts are ignored like Unity's importer does.
func parseKern(t []byte) ([]glyphPair, error) {
	if len(t) < 4 || be.Uint16(t) != 0 {
		return nil, nil
	}
	var out []glyphPair
	pos := 4
	for range int(be.Uint16(t[2:])) {
		if pos+6 > len(t) {
			return nil, fmt.Errorf("%w: truncated kern subtable", ErrFormat)
		}
		length, coverage := int(be.Uint16(t[pos+2:])), be.Uint16(t[pos+4:])
		if length < 6 {
			return nil, fmt.Errorf("%w: bad kern subtable length", ErrFormat)
		}
		// The declared length is honoured even when a large subtable
		// overflowed the 16-bit field, as FreeType (and therefore Unity)
		// does; only pairs inside it are used.
		sub := t[pos:min(pos+length, len(t))]
		horizontalFormat0 := coverage>>8 == 0 && coverage&0x01 != 0 && coverage&0x04 == 0
		if horizontalFormat0 && len(sub) >= 14 {
			n := min(int(be.Uint16(sub[6:])), (len(sub)-14)/6)
			for i := range n {
				p := sub[14+6*i:]
				out = append(out, glyphPair{left: be.Uint16(p), right: be.Uint16(p[2:]), value: int16(be.Uint16(p[4:]))})
			}
		}
		pos += length
	}
	return out, nil
}

// parseFamily returns name ID 1, preferring the Windows Unicode record.
func parseFamily(t []byte) string {
	if len(t) < 6 {
		return ""
	}
	count, strings := int(be.Uint16(t[2:])), int(be.Uint16(t[4:]))
	var mac string
	for i := range count {
		rec := 6 + 12*i
		if rec+12 > len(t) {
			break
		}
		platform, nameID := be.Uint16(t[rec:]), be.Uint16(t[rec+6:])
		length, off := int(be.Uint16(t[rec+8:])), int(be.Uint16(t[rec+10:]))
		start := strings + off
		if nameID != 1 || start+length > len(t) {
			continue
		}
		raw := t[start : start+length]
		switch platform {
		case 0, 3:
			u := make([]uint16, len(raw)/2)
			for j := range u {
				u[j] = be.Uint16(raw[2*j:])
			}
			return string(utf16.Decode(u))
		case 1:
			if mac == "" {
				mac = string(raw)
			}
		}
	}
	return mac
}
