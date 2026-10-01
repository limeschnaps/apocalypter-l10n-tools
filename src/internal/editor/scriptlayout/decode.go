package scriptlayout

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// ErrMismatch reports object data that does not follow the layout. It
// usually means the layout rules do not match how the build serialized
// the script.
var ErrMismatch = errors.New("scriptlayout: data does not match layout")

// String is a string field found by Decode.
type String struct {
	// Path names the field the way Unity YAML does, e.g.
	// "fsm.states[1].name".
	Path string
	// Offset and Size locate the encoded string, including its length
	// prefix and padding.
	Offset int
	Size   int
	Value  string
}

// Decode walks the script fields of an object that start at offset and
// returns every string. The layout must consume the data exactly.
func Decode(layout *Node, data []byte, offset int, order binary.ByteOrder) ([]String, error) {
	strs, _, err := DecodeWith(layout, data, offset, order, nil)
	return strs, err
}

// DecodeWith is Decode that also returns the raw bytes of primitive fields
// and primitive arrays whose path keep accepts: the value itself, or the
// elements of an array without its length. keep may be nil.
func DecodeWith(layout *Node, data []byte, offset int, order binary.ByteOrder, keep func(path string) bool) ([]String, map[string][]byte, error) {
	d := &decoder{data: data, pos: offset, order: order, keep: keep}
	for _, f := range layout.Fields {
		d.value(f, f.Name)
		if d.err != nil {
			return nil, nil, d.err
		}
	}
	if d.pos != len(data) {
		return nil, nil, fmt.Errorf("%w: layout ends at %d of %d bytes", ErrMismatch, d.pos, len(data))
	}
	return d.out, d.raw, nil
}

type decoder struct {
	data  []byte
	pos   int
	order binary.ByteOrder
	out   []String
	err   error
	keep  func(string) bool
	raw   map[string][]byte
}

// record keeps n bytes at the current position under path.
func (d *decoder) record(path string, n int) {
	if d.keep == nil || n < 0 || n > len(d.data)-d.pos || !d.keep(path) {
		return
	}
	if d.raw == nil {
		d.raw = map[string][]byte{}
	}
	d.raw[path] = d.data[d.pos : d.pos+n]
}

func (d *decoder) fail(path, format string, args ...any) {
	if d.err == nil {
		d.err = fmt.Errorf("%w: %s at %d: %s", ErrMismatch, path, d.pos, fmt.Sprintf(format, args...))
	}
}

func (d *decoder) skip(path string, n int) {
	if n < 0 || n > len(d.data)-d.pos {
		d.fail(path, "%d bytes past the end", n)
		return
	}
	d.pos += n
}

func (d *decoder) align() {
	d.pos = (d.pos + 3) &^ 3
	if d.pos > len(d.data) {
		d.fail("", "alignment past the end")
	}
}

func (d *decoder) u32(path string) int {
	if len(d.data)-d.pos < 4 {
		d.fail(path, "truncated length")
		return 0
	}
	v := d.order.Uint32(d.data[d.pos:])
	d.pos += 4
	if v > uint32(len(d.data)) {
		d.fail(path, "implausible length %d", v)
		return 0
	}
	return int(v)
}

func (d *decoder) value(n *Node, path string) {
	if d.err != nil {
		return
	}
	switch n.Kind {
	case KindPrimitive:
		d.record(path, n.Size)
		d.skip(path, n.Size)
	case KindString:
		start := d.pos
		size := d.u32(path)
		if d.err != nil {
			return
		}
		if size > len(d.data)-d.pos {
			d.fail(path, "string of %d bytes past the end", size)
			return
		}
		s := d.data[d.pos : d.pos+size]
		if !utf8.Valid(s) {
			d.fail(path, "invalid UTF-8")
			return
		}
		d.pos += size
		d.align()
		d.out = append(d.out, String{Path: path, Offset: start, Size: d.pos - start, Value: string(s)})
		return
	case KindArray:
		count := d.u32(path)
		if d.err != nil {
			return
		}
		if n.Elem.Kind == KindPrimitive {
			d.record(path, count*n.Elem.Size)
			d.skip(path, count*n.Elem.Size)
		} else {
			for i := range count {
				d.value(n.Elem, path+"["+strconv.Itoa(i)+"]")
				if d.err != nil {
					return
				}
			}
		}
	case KindStruct:
		for _, f := range n.Fields {
			d.value(f, path+"."+f.Name)
		}
	}
	if n.Align {
		d.align()
	}
}
