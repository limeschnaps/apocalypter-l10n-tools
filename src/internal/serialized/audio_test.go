package serialized

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestReadAudioClip(t *testing.T) {
	data, err := hex.DecodeString(strings.ReplaceAll(gameClip, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReadAudioClip(data, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	want := AudioClip{
		Name: "enemy_human_single_3", Channels: 1, Frequency: 44100, BitsPerSample: 16, Length: math.Float32frombits(0x4011d8f2),
		PreloadAudioData: true, Legacy3D: true,
		Resource:          StreamedResource{Source: "sharedassets1.resource", Offset: 0x21f88a0, Size: 0x6640},
		CompressionFormat: AudioCompressionVorbis,
	}
	if *c != want {
		t.Errorf("clip = %+v", *c)
	}
	if got := c.Encode(binary.LittleEndian); !bytes.Equal(got, data) {
		t.Errorf("Encode differs:\n% x\n% x", got, data)
	}

	for name, bad := range map[string][]byte{
		"truncated": data[:len(data)-1],
		"trailing":  append(bytes.Clone(data), 0, 0, 0, 0),
	} {
		if _, err := ReadAudioClip(bad, binary.LittleEndian); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// gameClip is the AudioClip enemy_human_single_3 of the game.
const gameClip = "14000000 656e656d795f68756d616e5f73696e676c655f33 00000000" +
	"01000000 44ac0000 10000000 f2d81140 00000000 00000000 01000100" +
	"16000000 73686172656461737365747331 2e7265736f75726365 0000" +
	"a0881f0200000000 4066000000000000 01000000"
