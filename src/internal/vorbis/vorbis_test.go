package vorbis

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

var le = binary.LittleEndian

func TestParseLibvorbisFile(t *testing.T) {
	// 0.1 s of a 440 Hz sine, mono, 44.1 kHz, encoded by ffmpeg's libvorbis
	// at quality 5.5.
	data, err := os.ReadFile("testdata/sine.ogg")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Channels != 1 || s.Rate != 44100 || s.Samples != 4410 || len(s.Packets) == 0 {
		t.Errorf("stream = %d ch, %d Hz, %d samples, %d packets", s.Channels, s.Rate, s.Samples, len(s.Packets))
	}
	// The setup header of the game's mono 44.1 kHz clips has this CRC.
	if crc := crc32.ChecksumIEEE(s.Setup); crc != 0xd6e0bbd4 {
		t.Errorf("setup CRC = %08x", crc)
	}
}

func TestParseSynthetic(t *testing.T) {
	long := append([]byte{0}, bytes.Repeat([]byte{7}, 600)...)
	data := unitytest.OggVorbis(unitytest.OggSpec{
		Channels: 2, Rate: 48000, Samples: 1234, Setup: []byte("books"),
		Packets: [][]byte{{0, 1}, {}, long, {2}},
	})
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Channels != 2 || s.Rate != 48000 || s.Samples != 1234 {
		t.Errorf("stream = %+v", s)
	}
	if !bytes.Equal(s.Setup, unitytest.SetupPacket([]byte("books"))) {
		t.Errorf("setup = %q", s.Setup)
	}
	// The empty packet is dropped; the long one spans several segments.
	if len(s.Packets) != 3 || !bytes.Equal(s.Packets[1], long) || !bytes.Equal(s.Packets[2], []byte{2}) {
		t.Errorf("packets = %d", len(s.Packets))
	}
}

// rawPage builds a page with the given flags, sequence number and lacing
// values over body.
func rawPage(flags byte, seq uint32, granule int64, lacing, body []byte) []byte {
	h := append([]byte("OggS"), 0, flags)
	h = le.AppendUint64(h, uint64(granule))
	h = le.AppendUint32(h, 0x1234)
	h = le.AppendUint32(h, seq)
	h = le.AppendUint32(h, 0)
	h = append(h, byte(len(lacing)))
	h = append(append(h, lacing...), body...)
	le.PutUint32(h[22:], unitytest.OggCRC(h))
	return h
}

func TestParseContinuedPacket(t *testing.T) {
	headers := unitytest.OggVorbis(unitytest.OggSpec{Channels: 1, Rate: 8000, Samples: 1, Setup: []byte("s"), Packets: [][]byte{{0}}})
	// Keep the two header pages, then split one audio packet over two
	// pages, with an empty page in between.
	firstAudio := bytes.LastIndex(headers, []byte("OggS"))
	audio := append([]byte{0}, bytes.Repeat([]byte{9}, 299)...)
	data := concat(headers[:firstAudio],
		rawPage(0, 2, -1, []byte{255}, audio[:255]),
		rawPage(1, 3, -1, nil, nil),
		rawPage(5, 4, 99, []byte{45}, audio[255:]),
	)
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Packets) != 1 || !bytes.Equal(s.Packets[0], audio) || s.Samples != 99 {
		t.Errorf("packets = %d, samples = %d", len(s.Packets), s.Samples)
	}
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func TestParseErrors(t *testing.T) {
	spec := unitytest.OggSpec{Channels: 1, Rate: 44100, Samples: 10, Setup: []byte("s"), Packets: [][]byte{{0}}}
	good := unitytest.OggVorbis(spec)
	headers := good[:bytes.LastIndex(good, []byte("OggS"))]
	ident := good[:bytes.Index(good[4:], []byte("OggS"))+4]
	withPackets := func(packets ...[]byte) []byte {
		s := spec
		s.Packets = packets
		return unitytest.OggVorbis(s)
	}
	corrupt := bytes.Clone(good)
	corrupt[len(corrupt)-1] ^= 1
	badVersion := bytes.Clone(ident)
	badVersion[28+7] = 1
	le.PutUint32(badVersion[22:], 0)
	le.PutUint32(badVersion[22:], unitytest.OggCRC(badVersion))
	cases := map[string][]byte{
		"not ogg":          []byte("RIFF....WAVE"),
		"empty":            nil,
		"truncated header": good[:20],
		"truncated body":   good[:len(good)-1],
		"bad checksum":     corrupt,
		"no first page":    rawPage(0, 0, 0, []byte{1}, []byte{1}),
		"chained":          concat(good, good),
		"sequence gap":     concat(headers, rawPage(4, 9, 10, []byte{1}, []byte{0})),
		"bad continuation": concat(headers, rawPage(5, 2, 10, []byte{1}, []byte{0})),
		"unterminated":     concat(headers, rawPage(4, 2, 10, []byte{255}, make([]byte, 255))),
		"headers only":     headers,
		"ident only":       ident,
		"bad version":      badVersion,
		"not audio":        withPackets([]byte{1}),
		"no audio":         withPackets([]byte{}),
		"no samples":       unitytest.OggVorbis(unitytest.OggSpec{Channels: 1, Rate: 1, Setup: []byte("s"), Packets: [][]byte{{0}}}),
		"no channels":      unitytest.OggVorbis(unitytest.OggSpec{Rate: 1, Samples: 1, Setup: []byte("s"), Packets: [][]byte{{0}}}),
		"comment missing": concat(ident,
			rawPage(0, 1, 0, []byte{8, 8}, []byte("\x05vorbis1\x05vorbis2"))),
		"setup missing": concat(ident,
			rawPage(0, 1, 0, []byte{8, 8}, []byte("\x03vorbis1\x03vorbis2"))),
	}
	for name, data := range cases {
		if _, err := Parse(data); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
