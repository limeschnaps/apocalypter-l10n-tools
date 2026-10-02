package unitytest

import (
	"bytes"
	"math"
)

// OggSpec describes a synthetic Ogg Vorbis stream.
type OggSpec struct {
	Channels int
	Rate     int
	Samples  int64
	Setup    []byte
	// Packets are the audio packets; their first bit must be 0.
	Packets [][]byte
}

// OggVorbis builds an Ogg stream with the identification header on the
// first page, the comment and setup headers on the second and every audio
// packet on its own page, the last of which carries Samples as its
// granule position. The pages have valid checksums.
func OggVorbis(s OggSpec) []byte {
	ident := append([]byte("\x01vorbis"), 0, 0, 0, 0, byte(s.Channels))
	ident = le.AppendUint32(ident, uint32(s.Rate))
	ident = append(ident, make([]byte, 12)...)
	ident = append(ident, 0xb8, 1)
	comment := []byte("\x03vorbis\x04\x00\x00\x00test\x00\x00\x00\x00\x01")
	setup := append([]byte("\x05vorbis"), s.Setup...)

	var out bytes.Buffer
	seq := uint32(0)
	page := func(flags byte, granule int64, packets ...[]byte) {
		var lacing, body []byte
		for _, p := range packets {
			n := len(p)
			for ; n >= 255; n -= 255 {
				lacing = append(lacing, 255)
			}
			lacing = append(lacing, byte(n))
			body = append(body, p...)
		}
		h := append([]byte("OggS"), 0, flags)
		h = le.AppendUint64(h, uint64(granule))
		h = le.AppendUint32(h, 0x1234)
		h = le.AppendUint32(h, seq)
		h = le.AppendUint32(h, 0)
		h = append(h, byte(len(lacing)))
		h = append(h, lacing...)
		h = append(h, body...)
		le.PutUint32(h[22:], OggCRC(h))
		out.Write(h)
		seq++
	}
	page(2, 0, ident)
	page(0, 0, comment, setup)
	for i, p := range s.Packets {
		flags, granule := byte(0), int64(-1)
		if i == len(s.Packets)-1 {
			flags, granule = 4, s.Samples
		}
		page(flags, granule, p)
	}
	return out.Bytes()
}

// SetupPacket returns the setup header packet OggVorbis stores for setup.
func SetupPacket(setup []byte) []byte {
	return append([]byte("\x05vorbis"), setup...)
}

// OggCRC is the Ogg page checksum of a page whose checksum field is zero.
func OggCRC(page []byte) uint32 {
	var c uint32
	for _, b := range page {
		c ^= uint32(b) << 24
		for range 8 {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// AudioClip encodes an AudioClip object whose data lives in source.
func AudioClip(name, source string, offset, size uint64) []byte {
	buf := String(name)
	buf = le.AppendUint32(buf, 0)     // m_LoadType
	buf = le.AppendUint32(buf, 2)     // m_Channels
	buf = le.AppendUint32(buf, 22050) // m_Frequency
	buf = le.AppendUint32(buf, 16)    // m_BitsPerSample
	buf = le.AppendUint32(buf, math.Float32bits(3.5))
	buf = append(buf, 0, 0, 0, 0)
	buf = le.AppendUint32(buf, 0) // m_SubsoundIndex
	buf = append(buf, 1, 0, 1, 0) // preload, background, legacy 3D
	buf = append(buf, String(source)...)
	buf = le.AppendUint64(buf, offset)
	buf = le.AppendUint64(buf, size)
	return le.AppendUint32(buf, 1)
}
