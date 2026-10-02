// Package unityfs reads and rewrites UnityFS asset bundles such as the
// data.unity3d file of a compressed player build.
//
// A bundle is a header, a "blocks info" table and a stream of blocks. The
// blocks concatenate into one uncompressed stream, and the directory
// ("nodes") maps file names to ranges of that stream.
package unityfs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"

	"apocalypter-l10n-tools/internal/lz4"
)

// Archive flags from the bundle header.
const (
	flagCompressionMask     = 0x3f
	flagBlocksAndDirectory  = 0x40
	flagBlocksInfoAtEnd     = 0x80
	flagBlockInfoNeedPadAt  = 0x200
	blockCompressionMask    = 0x3f
	compressionNone         = 0
	compressionLZMA         = 1
	compressionLZ4          = 2
	compressionLZ4HC        = 3
	appendedBlockSize       = 1 << 17
	blockCacheSize          = 8
	signatureUnityFS        = "UnityFS"
	minSupportedVersion     = 6
	maxSupportedVersion     = 8
	versionAlignedBlockInfo = 7
)

// Errors returned by the package.
var (
	ErrFormat      = errors.New("unityfs: malformed bundle")
	ErrUnsupported = errors.New("unityfs: unsupported bundle feature")
)

// Header is the fixed part at the start of the bundle.
type Header struct {
	Signature     string
	Version       uint32
	UnityVersion  string
	UnityRevision string
	Size          int64
	Flags         uint32
}

// Block is one entry of the blocks table.
type Block struct {
	USize uint32
	CSize uint32
	Flags uint16

	uOffset int64
	cOffset int64
}

// Node is one file stored in the bundle.
type Node struct {
	Offset int64
	Size   int64
	Flags  uint32
	Path   string
}

// Bundle is an opened UnityFS archive. It reads lazily from the
// underlying io.ReaderAt and is safe for concurrent reads.
type Bundle struct {
	Header Header
	Nodes  []Node

	r          io.ReaderAt
	hash       [16]byte
	blocks     []Block
	dataOffset int64
	streamSize int64

	mu    sync.Mutex
	cache map[int][]byte
	lru   []int
}

// Open parses the bundle header, block table and directory.
func Open(r io.ReaderAt, size int64) (*Bundle, error) {
	br := bufio.NewReader(io.NewSectionReader(r, 0, size))
	cr := &countingReader{r: br}
	var h Header
	var err error
	if h.Signature, err = readCString(cr); err != nil {
		return nil, err
	}
	if h.Signature != signatureUnityFS {
		return nil, fmt.Errorf("%w: signature %q", ErrUnsupported, h.Signature)
	}
	var fixed struct {
		Version uint32
	}
	if err := binary.Read(cr, binary.BigEndian, &fixed); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	h.Version = fixed.Version
	if h.Version < minSupportedVersion || h.Version > maxSupportedVersion {
		return nil, fmt.Errorf("%w: bundle version %d", ErrUnsupported, h.Version)
	}
	if h.UnityVersion, err = readCString(cr); err != nil {
		return nil, err
	}
	if h.UnityRevision, err = readCString(cr); err != nil {
		return nil, err
	}
	var sizes struct {
		Size             int64
		CompressedInfo   uint32
		UncompressedInfo uint32
		Flags            uint32
	}
	if err := binary.Read(cr, binary.BigEndian, &sizes); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	h.Size, h.Flags = sizes.Size, sizes.Flags

	pos := cr.n
	if h.Version >= versionAlignedBlockInfo {
		pos = align(pos, 16)
	}
	infoOffset := pos
	if h.Flags&flagBlocksInfoAtEnd != 0 {
		infoOffset = size - int64(sizes.CompressedInfo)
	}
	if infoOffset < 0 || infoOffset+int64(sizes.CompressedInfo) > size {
		return nil, fmt.Errorf("%w: blocks info out of range", ErrFormat)
	}
	raw := make([]byte, sizes.CompressedInfo)
	if _, err := r.ReadAt(raw, infoOffset); err != nil {
		return nil, fmt.Errorf("%w: read blocks info: %w", ErrFormat, err)
	}
	info, err := decompress(raw, int(sizes.UncompressedInfo), h.Flags&flagCompressionMask)
	if err != nil {
		return nil, fmt.Errorf("blocks info: %w", err)
	}

	b := &Bundle{Header: h, r: r, cache: map[int][]byte{}}
	if err := b.parseInfo(info); err != nil {
		return nil, err
	}
	if h.Flags&flagBlocksInfoAtEnd == 0 {
		pos += int64(sizes.CompressedInfo)
	}
	if h.Flags&flagBlockInfoNeedPadAt != 0 {
		pos = align(pos, 16)
	}
	b.dataOffset = pos

	var c int64
	for i := range b.blocks {
		b.blocks[i].cOffset = c
		c += int64(b.blocks[i].CSize)
	}
	if b.dataOffset+c > size {
		return nil, fmt.Errorf("%w: block data exceeds file size", ErrFormat)
	}
	for _, n := range b.Nodes {
		if n.Offset < 0 || n.Size < 0 || n.Offset+n.Size > b.streamSize {
			return nil, fmt.Errorf("%w: node %q out of range", ErrFormat, n.Path)
		}
	}
	return b, nil
}

func (b *Bundle) parseInfo(info []byte) error {
	rd := bytes.NewReader(info)
	if _, err := io.ReadFull(rd, b.hash[:]); err != nil {
		return fmt.Errorf("%w: blocks info hash: %w", ErrFormat, err)
	}
	var count int32
	if err := binary.Read(rd, binary.BigEndian, &count); err != nil || count < 0 {
		return fmt.Errorf("%w: block count", ErrFormat)
	}
	b.blocks = make([]Block, 0, min(int(count), len(info)/10))
	var u int64
	for range count {
		var blk struct {
			USize, CSize uint32
			Flags        uint16
		}
		if err := binary.Read(rd, binary.BigEndian, &blk); err != nil {
			return fmt.Errorf("%w: block entry: %w", ErrFormat, err)
		}
		b.blocks = append(b.blocks, Block{USize: blk.USize, CSize: blk.CSize, Flags: blk.Flags, uOffset: u})
		u += int64(blk.USize)
	}
	b.streamSize = u

	if err := binary.Read(rd, binary.BigEndian, &count); err != nil || count < 0 {
		return fmt.Errorf("%w: node count", ErrFormat)
	}
	for range count {
		var n struct {
			Offset, Size int64
			Flags        uint32
		}
		if err := binary.Read(rd, binary.BigEndian, &n); err != nil {
			return fmt.Errorf("%w: node entry: %w", ErrFormat, err)
		}
		path, err := readCString(rd)
		if err != nil {
			return err
		}
		b.Nodes = append(b.Nodes, Node{Offset: n.Offset, Size: n.Size, Flags: n.Flags, Path: path})
	}
	return nil
}

// Node returns the node with the given path.
func (b *Bundle) Node(path string) (Node, bool) {
	i := slices.IndexFunc(b.Nodes, func(n Node) bool { return n.Path == path })
	if i < 0 {
		return Node{}, false
	}
	return b.Nodes[i], true
}

// ReadNode returns the full uncompressed content of n.
func (b *Bundle) ReadNode(n Node) ([]byte, error) {
	buf := make([]byte, n.Size)
	if _, err := b.ReadAt(buf, n.Offset); err != nil {
		return nil, fmt.Errorf("read node %q: %w", n.Path, err)
	}
	return buf, nil
}

// ReadAt reads from the uncompressed block stream.
func (b *Bundle) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("%w: negative offset", ErrFormat)
	}
	n := 0
	for n < len(p) {
		if off >= b.streamSize {
			return n, io.EOF
		}
		i := sort.Search(len(b.blocks), func(i int) bool {
			return b.blocks[i].uOffset+int64(b.blocks[i].USize) > off
		})
		data, err := b.block(i)
		if err != nil {
			return n, err
		}
		c := copy(p[n:], data[off-b.blocks[i].uOffset:])
		n += c
		off += int64(c)
	}
	return n, nil
}

func (b *Bundle) block(i int) ([]byte, error) {
	b.mu.Lock()
	if data, ok := b.cache[i]; ok {
		b.mu.Unlock()
		return data, nil
	}
	b.mu.Unlock()

	blk := b.blocks[i]
	raw := make([]byte, blk.CSize)
	if _, err := b.r.ReadAt(raw, b.dataOffset+blk.cOffset); err != nil {
		return nil, fmt.Errorf("%w: read block %d: %w", ErrFormat, i, err)
	}
	data, err := decompress(raw, int(blk.USize), uint32(blk.Flags&blockCompressionMask))
	if err != nil {
		return nil, fmt.Errorf("block %d: %w", i, err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.cache[i]; !ok {
		if len(b.lru) >= blockCacheSize {
			delete(b.cache, b.lru[0])
			b.lru = b.lru[1:]
		}
		b.cache[i] = data
		b.lru = append(b.lru, i)
	}
	return data, nil
}

// ExtendsBlocks reports whether b starts with the compressed blocks of
// orig, which is how every bundle WriteTo produces from orig looks. It
// compares the block tables and the raw bytes of orig's first and last
// blocks, so a bundle that Unity or Steam wrote anew is told apart without
// decompressing anything.
func (b *Bundle) ExtendsBlocks(orig *Bundle) (bool, error) {
	if len(b.blocks) < len(orig.blocks) {
		return false, nil
	}
	for i, blk := range orig.blocks {
		mine := b.blocks[i]
		if mine.USize != blk.USize || mine.CSize != blk.CSize || mine.Flags != blk.Flags {
			return false, nil
		}
	}
	if len(orig.blocks) == 0 {
		return true, nil
	}
	for _, i := range []int{0, len(orig.blocks) - 1} {
		blk := orig.blocks[i]
		want := make([]byte, blk.CSize)
		got := make([]byte, blk.CSize)
		if _, err := orig.r.ReadAt(want, orig.dataOffset+blk.cOffset); err != nil {
			return false, fmt.Errorf("%w: read block %d: %w", ErrFormat, i, err)
		}
		if _, err := b.r.ReadAt(got, b.dataOffset+b.blocks[i].cOffset); err != nil {
			return false, fmt.Errorf("%w: read block %d: %w", ErrFormat, i, err)
		}
		if !bytes.Equal(want, got) {
			return false, nil
		}
	}
	return true, nil
}

// WriteTo writes a copy of the bundle in which the nodes named in
// replace get new content. A path the bundle lacks becomes a new node
// without flags, the kind that holds raw resource data rather than a
// serialized file; new nodes follow the existing ones in path order.
//
// Original blocks are copied verbatim. Replacement contents are appended
// to the block stream as uncompressed blocks and the directory entries of
// the replaced nodes are pointed at them; their old bytes stay in the
// stream unreferenced. This keeps the output byte-identical to the input
// except for the directory and the appended data.
func (b *Bundle) WriteTo(w io.Writer, replace map[string][]byte) (int64, error) {
	nodes := slices.Clone(b.Nodes)
	var added []string
	for path := range replace {
		if _, ok := b.Node(path); !ok {
			added = append(added, path)
		}
	}
	slices.Sort(added)
	for _, path := range added {
		if path == "" {
			return 0, fmt.Errorf("%w: empty node path", ErrFormat)
		}
		nodes = append(nodes, Node{Path: path})
	}
	blocks := slices.Clone(b.blocks)
	var appended [][]byte
	next := b.streamSize
	for i, n := range nodes {
		data, ok := replace[n.Path]
		if !ok {
			continue
		}
		nodes[i].Offset, nodes[i].Size = next, int64(len(data))
		next += int64(len(data))
		appended = append(appended, data)
		for len(data) > 0 {
			chunk := data[:min(len(data), appendedBlockSize)]
			blocks = append(blocks, Block{USize: uint32(len(chunk)), CSize: uint32(len(chunk)), Flags: compressionNone})
			data = data[len(chunk):]
		}
	}

	info := encodeInfo(b.hash, blocks, nodes)
	h := b.Header
	flags := h.Flags&^(flagCompressionMask|flagBlocksInfoAtEnd) | flagBlocksAndDirectory

	var head bytes.Buffer
	head.WriteString(h.Signature + "\x00")
	_ = binary.Write(&head, binary.BigEndian, h.Version)
	head.WriteString(h.UnityVersion + "\x00")
	head.WriteString(h.UnityRevision + "\x00")
	sizePos := head.Len()
	_ = binary.Write(&head, binary.BigEndian, struct {
		Size                                    int64
		CompressedInfo, UncompressedInfo, Flags uint32
	}{0, uint32(len(info)), uint32(len(info)), flags})
	if h.Version >= versionAlignedBlockInfo {
		pad(&head, 16)
	}
	head.Write(info)
	if flags&flagBlockInfoNeedPadAt != 0 {
		pad(&head, 16)
	}

	var origData int64
	for _, blk := range b.blocks {
		origData += int64(blk.CSize)
	}
	total := int64(head.Len()) + origData + (next - b.streamSize)
	out := head.Bytes()
	binary.BigEndian.PutUint64(out[sizePos:], uint64(total))

	cw := &countingWriter{w: w}
	if _, err := cw.Write(out); err != nil {
		return cw.n, err
	}
	if _, err := io.Copy(cw, io.NewSectionReader(b.r, b.dataOffset, origData)); err != nil {
		return cw.n, fmt.Errorf("copy blocks: %w", err)
	}
	for _, data := range appended {
		if _, err := cw.Write(data); err != nil {
			return cw.n, err
		}
	}
	return cw.n, nil
}

func encodeInfo(hash [16]byte, blocks []Block, nodes []Node) []byte {
	var buf bytes.Buffer
	buf.Write(hash[:])
	_ = binary.Write(&buf, binary.BigEndian, int32(len(blocks)))
	for _, blk := range blocks {
		_ = binary.Write(&buf, binary.BigEndian, struct {
			USize, CSize uint32
			Flags        uint16
		}{blk.USize, blk.CSize, blk.Flags})
	}
	_ = binary.Write(&buf, binary.BigEndian, int32(len(nodes)))
	for _, n := range nodes {
		_ = binary.Write(&buf, binary.BigEndian, struct {
			Offset, Size int64
			Flags        uint32
		}{n.Offset, n.Size, n.Flags})
		buf.WriteString(n.Path + "\x00")
	}
	return buf.Bytes()
}

func decompress(src []byte, size int, compression uint32) ([]byte, error) {
	switch compression {
	case compressionNone:
		if len(src) != size {
			return nil, fmt.Errorf("%w: stored block size mismatch", ErrFormat)
		}
		return src, nil
	case compressionLZ4, compressionLZ4HC:
		dst := make([]byte, size)
		if err := lz4.Decode(dst, src); err != nil {
			return nil, err
		}
		return dst, nil
	case compressionLZMA:
		return nil, fmt.Errorf("%w: LZMA compression", ErrUnsupported)
	default:
		return nil, fmt.Errorf("%w: compression type %d", ErrUnsupported, compression)
	}
}

func readCString(r io.ByteReader) (string, error) {
	var buf []byte
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", fmt.Errorf("%w: unterminated string: %w", ErrFormat, err)
		}
		if c == 0 {
			return string(buf), nil
		}
		buf = append(buf, c)
	}
}

func align(n, to int64) int64 {
	return (n + to - 1) / to * to
}

func pad(buf *bytes.Buffer, to int) {
	for buf.Len()%to != 0 {
		buf.WriteByte(0)
	}
}

type countingReader struct {
	r *bufio.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
