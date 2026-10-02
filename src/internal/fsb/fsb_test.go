package fsb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"

	"apocalypter-l10n-tools/internal/vorbis"
)

var le = binary.LittleEndian

func TestVorbis(t *testing.T) {
	setup := []byte("\x05vorbis-setup")
	s := &vorbis.Stream{Channels: 2, Rate: 48000, Samples: 100498, Setup: setup, Packets: [][]byte{{0, 1, 2}, {4}}}
	bank, err := Vorbis(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(bank[:4]) != "FSB5" || le.Uint32(bank[4:]) != 1 || le.Uint32(bank[8:]) != 1 {
		t.Fatalf("header = % x", bank[:12])
	}
	// The sample header is padded so the data starts at 96, as in the
	// game's banks with an empty seek table.
	shdr, names, size := le.Uint32(bank[12:]), le.Uint32(bank[16:]), le.Uint32(bank[20:])
	if shdr != 36 || names != 0 || le.Uint32(bank[24:]) != modeVorbis || le.Uint64(bank[28:]) != 1 {
		t.Errorf("sizes = %d %d, mode = %d", shdr, names, le.Uint32(bank[24:]))
	}
	mode := le.Uint64(bank[60:])
	if mode&1 != 1 || mode>>1&15 != 9 || mode>>5&1 != 1 || mode>>6&(1<<28-1) != 0 || mode>>34 != 100498 {
		t.Errorf("sample mode = %x", mode)
	}
	chunk := le.Uint32(bank[68:])
	if chunk&1 != 0 || chunk>>1&0xffffff != 8 || chunk>>25 != chunkVorbis {
		t.Errorf("chunk header = %x", chunk)
	}
	if le.Uint32(bank[72:]) != crc32.ChecksumIEEE(setup) || le.Uint32(bank[76:]) != 0 {
		t.Errorf("vorbis chunk = % x", bank[72:80])
	}
	data := bank[96:]
	want := []byte{3, 0, 0, 1, 2, 1, 0, 4}
	if int(size) != len(data) || size%dataAlign != 0 || !bytes.Equal(data[:len(want)], want) || !isZero(data[len(want):]) {
		t.Errorf("data = % x (size %d)", data, size)
	}

	// The bank depends only on the stream.
	again, _ := Vorbis(s)
	if !bytes.Equal(bank, again) {
		t.Error("bank is not deterministic")
	}
}

func isZero(b []byte) bool {
	return bytes.Count(b, []byte{0}) == len(b)
}

func TestVorbisErrors(t *testing.T) {
	good := vorbis.Stream{Channels: 1, Rate: 44100, Samples: 10, Packets: [][]byte{{0}}}
	cases := map[string]func(s *vorbis.Stream){
		"rate":         func(s *vorbis.Stream) { s.Rate = 44000 },
		"channels":     func(s *vorbis.Stream) { s.Channels = 6 },
		"no samples":   func(s *vorbis.Stream) { s.Samples = 0 },
		"long":         func(s *vorbis.Stream) { s.Samples = 1 << 30 },
		"large packet": func(s *vorbis.Stream) { s.Packets = [][]byte{make([]byte, 1<<16)} },
	}
	for name, mutate := range cases {
		s := good
		mutate(&s)
		if _, err := Vorbis(&s); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
