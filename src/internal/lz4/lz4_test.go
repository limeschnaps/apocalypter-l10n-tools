package lz4

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	long := strings.Repeat("x", 300)
	cases := map[string]struct {
		src  []byte
		want string
	}{
		"empty":   {[]byte{0x00}, ""},
		"literal": {append([]byte{0x50}, "hello"...), "hello"},
		// 300 literals: 15 + 255 + 30.
		"long literal": {append([]byte{0xf0, 255, 30}, long...), long},
		"overlapping match": {
			[]byte{0x35, 'a', 'b', 'c', 3, 0, 0x10, 'X'},
			"abcabcabcabcX",
		},
		"plain match": {
			append(append([]byte{0x80}, "abcdefgh"...), 8, 0, 0x10, 'Z'),
			"abcdefghabcdZ",
		},
		"long match": {
			[]byte{0x1f, 'a', 1, 0, 3, 0x10, 'b'},
			"a" + strings.Repeat("a", 22) + "b",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dst := make([]byte, len(tc.want))
			if err := Decode(dst, tc.src); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !bytes.Equal(dst, []byte(tc.want)) {
				t.Errorf("got %q, want %q", dst, tc.want)
			}
		})
	}
}

func TestDecodeCorrupt(t *testing.T) {
	cases := map[string]struct {
		src  []byte
		size int
	}{
		"literal overrun":    {[]byte{0x50, 'a'}, 5},
		"dst too small":      {append([]byte{0x50}, "hello"...), 3},
		"dst too large":      {append([]byte{0x50}, "hello"...), 6},
		"truncated offset":   {[]byte{0x10, 'a', 1}, 8},
		"zero offset":        {[]byte{0x10, 'a', 0, 0, 0x00}, 8},
		"offset before data": {[]byte{0x10, 'a', 2, 0, 0x00}, 8},
		"match overrun":      {[]byte{0x1f, 'a', 1, 0, 50}, 8},
		"truncated length":   {[]byte{0xf0, 255}, 300},
		"truncated match len": {
			[]byte{0x1f, 'a', 1, 0},
			30,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Decode(make([]byte, tc.size), tc.src); !errors.Is(err, ErrCorrupt) {
				t.Errorf("err = %v", err)
			}
		})
	}
}
