package unityfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

var testNodes = []unitytest.Node{
	{Path: "level0", Flags: 4, Data: []byte(strings.Repeat("level0-data;", 20))},
	{Path: "level0.resS", Flags: 0, Data: []byte("resource")},
	{Path: "sharedassets0.assets", Flags: 4, Data: []byte(strings.Repeat("shared;", 9))},
}

// flagsOffset is where the flags field sits in unitytest bundles.
const flagsOffset = 46

func open(t *testing.T, data []byte) *Bundle {
	t.Helper()
	b, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return b
}

func TestOpenAndRead(t *testing.T) {
	b := open(t, unitytest.Bundle(testNodes, 7))
	if b.Header.UnityRevision != "2020.3.49f1" || b.Header.Version != 8 {
		t.Errorf("header = %+v", b.Header)
	}
	if len(b.Nodes) != len(testNodes) {
		t.Fatalf("nodes = %+v", b.Nodes)
	}
	for _, want := range testNodes {
		n, ok := b.Node(want.Path)
		if !ok || n.Flags != want.Flags {
			t.Fatalf("node %s = %+v", want.Path, n)
		}
		got, err := b.ReadNode(n)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.Data) {
			t.Errorf("%s = %q", want.Path, got)
		}
	}
	if _, ok := b.Node("missing"); ok {
		t.Error("found missing node")
	}
	if _, err := b.ReadAt(make([]byte, 1), -1); !errors.Is(err, ErrFormat) {
		t.Errorf("negative offset err = %v", err)
	}
	if n, err := b.ReadAt(make([]byte, 10), b.streamSize-3); n != 3 || !errors.Is(err, io.EOF) {
		t.Errorf("read past end = %d, %v", n, err)
	}
}

func TestWriteToReplacesNodes(t *testing.T) {
	src := unitytest.Bundle(testNodes, 16)
	b := open(t, src)
	replacement := bytes.Repeat([]byte("new level0 content "), appendedBlockSize/10)

	var out bytes.Buffer
	n, err := b.WriteTo(&out, map[string][]byte{"level0": replacement})
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != int64(out.Len()) {
		t.Errorf("reported %d bytes, wrote %d", n, out.Len())
	}

	patched := open(t, out.Bytes())
	if patched.Header.Size != int64(out.Len()) {
		t.Errorf("header size = %d, file = %d", patched.Header.Size, out.Len())
	}
	for _, want := range testNodes {
		node, _ := patched.Node(want.Path)
		got, err := patched.ReadNode(node)
		if err != nil {
			t.Fatal(err)
		}
		expected := want.Data
		if want.Path == "level0" {
			expected = replacement
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("%s differs after rewrite", want.Path)
		}
	}
	if len(patched.blocks) <= len(b.blocks)+1 {
		t.Errorf("expected replacement split into several blocks, got %d blocks", len(patched.blocks))
	}
	// Original compressed blocks are copied verbatim.
	orig := src[b.dataOffset:]
	if !bytes.Equal(out.Bytes()[patched.dataOffset:patched.dataOffset+int64(len(orig))], orig) {
		t.Error("original blocks were not copied verbatim")
	}

	if _, err := b.WriteTo(io.Discard, map[string][]byte{"nope": nil}); !errors.Is(err, ErrFormat) {
		t.Errorf("unknown node err = %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	good := unitytest.Bundle(testNodes, 16)
	mutate := func(f func([]byte) []byte) []byte {
		return f(bytes.Clone(good))
	}
	cases := map[string]struct {
		data []byte
		want error
	}{
		"signature": {mutate(func(d []byte) []byte { d[0] = 'X'; return d }), ErrUnsupported},
		"version": {mutate(func(d []byte) []byte {
			binary.BigEndian.PutUint32(d[8:], 3)
			return d
		}), ErrUnsupported},
		"lzma info": {mutate(func(d []byte) []byte {
			binary.BigEndian.PutUint32(d[flagsOffset:], 0x40|0x200|1)
			return d
		}), ErrUnsupported},
		"stored info size mismatch": {mutate(func(d []byte) []byte {
			binary.BigEndian.PutUint32(d[flagsOffset:], 0x40|0x200)
			return d
		}), ErrFormat},
		"info out of range": {mutate(func(d []byte) []byte {
			binary.BigEndian.PutUint32(d[38:], 1<<30)
			return d
		}), ErrFormat},
		"truncated blocks": {good[:len(good)-5], ErrFormat},
		"truncated header": {good[:20], ErrFormat},
		"no terminator":    {[]byte("UnityFS"), ErrFormat},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(bytes.NewReader(tc.data), int64(len(tc.data))); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseInfoErrors(t *testing.T) {
	var b Bundle
	if err := b.parseInfo(make([]byte, 5)); !errors.Is(err, ErrFormat) {
		t.Errorf("short hash err = %v", err)
	}
	info := append(make([]byte, 16), 0, 0, 0, 1)
	if err := b.parseInfo(info); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated block err = %v", err)
	}
	info = append(make([]byte, 16), 0, 0, 0, 0)
	if err := b.parseInfo(info); !errors.Is(err, ErrFormat) {
		t.Errorf("missing node count err = %v", err)
	}
	info = append(make([]byte, 16), 0, 0, 0, 0, 0, 0, 0, 1)
	if err := b.parseInfo(info); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated node err = %v", err)
	}
	info = append(info, make([]byte, 20)...)
	info = append(info, 'x')
	if err := b.parseInfo(info); !errors.Is(err, ErrFormat) {
		t.Errorf("unterminated path err = %v", err)
	}
}

func TestDecompress(t *testing.T) {
	if _, err := decompress([]byte{1, 2}, 2, 9); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unknown compression err = %v", err)
	}
	if _, err := decompress([]byte{0x50, 'a'}, 5, compressionLZ4); err == nil {
		t.Error("expected corrupt LZ4 error")
	}
}

func TestExtendsBlocks(t *testing.T) {
	orig := open(t, unitytest.Bundle(testNodes, 16))
	var patched bytes.Buffer
	if _, err := orig.WriteTo(&patched, map[string][]byte{"level0": []byte("replaced")}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		data []byte
		want bool
	}{
		"patched copy": {patched.Bytes(), true},
		"same bundle":  {unitytest.Bundle(testNodes, 16), true},
		"other blocks": {unitytest.Bundle(testNodes, 32), false},
		"fewer blocks": {unitytest.Bundle(testNodes[:1], 16), false},
	}
	changed := slices.Clone(testNodes)
	changed[2] = unitytest.Node{Path: changed[2].Path, Flags: 4, Data: []byte(strings.Repeat("SHARED;", 9))}
	cases["same sizes, other bytes"] = struct {
		data []byte
		want bool
	}{unitytest.Bundle(changed, 16), false}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := open(t, tc.data).ExtendsBlocks(orig)
			if err != nil || got != tc.want {
				t.Errorf("ExtendsBlocks = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	empty := open(t, unitytest.Bundle(nil, 16))
	if ok, err := orig.ExtendsBlocks(empty); err != nil || !ok {
		t.Errorf("empty original = %v, %v", ok, err)
	}
}
