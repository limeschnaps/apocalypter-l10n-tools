package serialized

import (
	"encoding/binary"
	"fmt"
)

// ClassAudioClip is the class ID of AudioClip objects.
const ClassAudioClip = 83

// AudioCompressionVorbis is the m_CompressionFormat of Vorbis clips.
const AudioCompressionVorbis = 1

// StreamedResource locates data stored outside the serialized file: a
// file of the build or a bundle node, and a byte range in it.
type StreamedResource struct {
	Source string
	Offset uint64
	Size   uint64
}

// AudioClip is a decoded AudioClip object (Unity 2019.x-2022.x layout).
// The audio itself is an FMOD sound bank in Resource.
type AudioClip struct {
	Name              string
	LoadType          int32
	Channels          int32
	Frequency         int32
	BitsPerSample     int32
	Length            float32
	IsTrackerFormat   bool
	Ambisonic         bool
	SubsoundIndex     int32
	PreloadAudioData  bool
	LoadInBackground  bool
	Legacy3D          bool
	Resource          StreamedResource
	CompressionFormat int32
}

// ReadAudioClip decodes an AudioClip object and requires the data to be
// consumed exactly.
func ReadAudioClip(data []byte, order binary.ByteOrder) (*AudioClip, error) {
	r := &reader{data: data, order: order}
	c := &AudioClip{}
	c.Name = readString(r)
	c.LoadType = r.i32()
	c.Channels = r.i32()
	c.Frequency = r.i32()
	c.BitsPerSample = r.i32()
	c.Length = r.f32()
	c.IsTrackerFormat = r.u8() != 0
	c.Ambisonic = r.u8() != 0
	r.align(4)
	c.SubsoundIndex = r.i32()
	c.PreloadAudioData = r.u8() != 0
	c.LoadInBackground = r.u8() != 0
	c.Legacy3D = r.u8() != 0
	r.align(4)
	c.Resource.Source = readString(r)
	c.Resource.Offset = uint64(r.i64())
	c.Resource.Size = uint64(r.i64())
	c.CompressionFormat = r.i32()
	if r.err == nil && r.pos != len(data) {
		r.err = fmt.Errorf("%d trailing bytes", len(data)-r.pos)
	}
	if r.err != nil {
		return nil, fmt.Errorf("%w: AudioClip: %w", ErrFormat, r.err)
	}
	return c, nil
}

// Encode serializes the clip in the layout ReadAudioClip reads.
func (c *AudioClip) Encode(order ByteOrder) []byte {
	w := &writer{order: order}
	w.str(c.Name)
	w.u32(uint32(c.LoadType))
	w.u32(uint32(c.Channels))
	w.u32(uint32(c.Frequency))
	w.u32(uint32(c.BitsPerSample))
	w.f32(c.Length)
	w.buf = append(w.buf, boolByte(c.IsTrackerFormat), boolByte(c.Ambisonic))
	w.align()
	w.u32(uint32(c.SubsoundIndex))
	w.buf = append(w.buf, boolByte(c.PreloadAudioData), boolByte(c.LoadInBackground), boolByte(c.Legacy3D))
	w.align()
	w.str(c.Resource.Source)
	w.buf = order.AppendUint64(w.buf, c.Resource.Offset)
	w.buf = order.AppendUint64(w.buf, c.Resource.Size)
	w.u32(uint32(c.CompressionFormat))
	return w.buf
}
