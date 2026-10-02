// Package fsb builds FMOD FSB5 sound banks in the form Unity stores
// AudioClip data in: one sample per bank.
//
// A Vorbis sample keeps only the audio packets of the stream, each
// prefixed with its uint16 length. FMOD restores the identification
// header from the sample header and looks the setup header up by its
// CRC-32 in a table built into the player, so the setup header must come
// from a stock libvorbis encoder.
package fsb

import (
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"

	"apocalypter-l10n-tools/internal/vorbis"
)

// ErrUnsupported reports a stream the sample header cannot describe.
var ErrUnsupported = errors.New("fsb: unsupported stream")

const (
	headerSize  = 60
	version     = 1
	modeVorbis  = 15
	chunkVorbis = 11
	sampleAlign = 32
	dataAlign   = 16
	maxSamples  = 1<<30 - 1
)

// frequencies maps sample rates to the 4-bit codes of the sample header;
// other rates need a frequency chunk.
var frequencies = map[int]uint64{
	8000: 1, 11000: 2, 11025: 3, 16000: 4, 22050: 5, 24000: 6,
	32000: 7, 44100: 8, 48000: 9, 96000: 10,
}

// Vorbis returns a bank holding s as its only sample. It supports mono
// and stereo streams at the rates of the sample header.
func Vorbis(s *vorbis.Stream) ([]byte, error) {
	freq, ok := frequencies[s.Rate]
	if !ok {
		return nil, fmt.Errorf("%w: sample rate %d Hz", ErrUnsupported, s.Rate)
	}
	if s.Channels < 1 || s.Channels > 2 {
		return nil, fmt.Errorf("%w: %d channels, need mono or stereo", ErrUnsupported, s.Channels)
	}
	if s.Samples <= 0 || s.Samples > maxSamples {
		return nil, fmt.Errorf("%w: %d samples", ErrUnsupported, s.Samples)
	}
	le := binary.LittleEndian

	var data []byte
	for i, p := range s.Packets {
		if len(p) > math.MaxUint16 {
			return nil, fmt.Errorf("%w: packet %d is %d bytes", ErrUnsupported, i+1, len(p))
		}
		data = le.AppendUint16(data, uint16(len(p)))
		data = append(data, p...)
	}
	data = pad(data, dataAlign)

	// Bit 0 announces a chunk, bits 1-4 hold the frequency code, bit 5
	// marks stereo, bits 6-33 the data offset in 16-byte units (always 0
	// for the only sample) and bits 34-63 the length in samples.
	mode := 1 | freq<<1 | uint64(s.Channels-1)<<5 | uint64(s.Samples)<<34
	// The Vorbis chunk holds the setup header CRC and a seek table; Unity
	// leaves the table empty for short clips, and decoding needs none.
	chunk := le.AppendUint32(nil, crc32.ChecksumIEEE(s.Setup))
	chunk = le.AppendUint32(chunk, 0)
	sample := le.AppendUint64(nil, mode)
	sample = le.AppendUint32(sample, uint32(len(chunk))<<1|chunkVorbis<<25)
	sample = append(sample, chunk...)
	// The sample data starts at a multiple of sampleAlign, as in Unity's
	// banks.
	sample = append(sample, make([]byte, (sampleAlign-(headerSize+len(sample))%sampleAlign)%sampleAlign)...)

	out := make([]byte, 0, headerSize+len(sample)+len(data))
	out = append(out, "FSB5"...)
	out = le.AppendUint32(out, version)
	out = le.AppendUint32(out, 1) // samples in the bank
	out = le.AppendUint32(out, uint32(len(sample)))
	out = le.AppendUint32(out, 0) // name table size
	out = le.AppendUint32(out, uint32(len(data)))
	out = le.AppendUint32(out, modeVorbis)
	// Flags as Unity writes them for Vorbis banks.
	out = le.AppendUint64(out, 1)
	hash := md5.Sum(data)
	out = append(out, hash[:]...)
	out = append(out, make([]byte, headerSize-len(out))...)
	out = append(out, sample...)
	return append(out, data...), nil
}

func pad(b []byte, to int) []byte {
	return append(b, make([]byte, (to-len(b)%to)%to)...)
}
