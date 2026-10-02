// Package vorbis reads Ogg Vorbis files: the stream parameters, the setup
// header and the audio packets. It does not decode audio.
package vorbis

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrFormat reports data that is not a supported Ogg Vorbis stream.
var ErrFormat = errors.New("vorbis: invalid Ogg Vorbis stream")

// Stream is a parsed Ogg Vorbis stream.
type Stream struct {
	Channels int
	Rate     int
	// Samples is the length in samples per channel: the granule position
	// of the last page.
	Samples int64
	// Setup is the setup header packet.
	Setup []byte
	// Packets are the non-empty audio packets in stream order.
	Packets [][]byte
}

const (
	pageHeaderSize  = 27
	flagContinued   = 1
	flagFirst       = 2
	identPacketSize = 30
	packetIdent     = 1
	packetComment   = 3
	packetSetup     = 5
)

var capture = []byte("OggS")

// Parse reads a single logical Ogg Vorbis stream. Page checksums are
// verified, and chained or multiplexed streams are rejected.
func Parse(data []byte) (*Stream, error) {
	packets, granule, err := readPackets(data)
	if err != nil {
		return nil, err
	}
	if len(packets) < 3 {
		return nil, fmt.Errorf("%w: %d packets, need the three headers", ErrFormat, len(packets))
	}
	s := &Stream{Samples: granule}
	if err := s.readIdent(packets[0]); err != nil {
		return nil, err
	}
	if !isHeader(packets[1], packetComment) {
		return nil, fmt.Errorf("%w: second packet is not the comment header", ErrFormat)
	}
	if !isHeader(packets[2], packetSetup) {
		return nil, fmt.Errorf("%w: third packet is not the setup header", ErrFormat)
	}
	s.Setup = packets[2]
	for i, p := range packets[3:] {
		if len(p) == 0 {
			continue
		}
		if p[0]&1 != 0 {
			return nil, fmt.Errorf("%w: packet %d is not an audio packet", ErrFormat, i+4)
		}
		s.Packets = append(s.Packets, p)
	}
	if len(s.Packets) == 0 || s.Samples <= 0 {
		return nil, fmt.Errorf("%w: no audio", ErrFormat)
	}
	return s, nil
}

func (s *Stream) readIdent(p []byte) error {
	if !isHeader(p, packetIdent) || len(p) != identPacketSize {
		return fmt.Errorf("%w: first packet is not the identification header", ErrFormat)
	}
	le := binary.LittleEndian
	if v := le.Uint32(p[7:]); v != 0 {
		return fmt.Errorf("%w: Vorbis version %d", ErrFormat, v)
	}
	s.Channels, s.Rate = int(p[11]), int(le.Uint32(p[12:]))
	if s.Channels == 0 || s.Rate == 0 || p[29]&1 == 0 {
		return fmt.Errorf("%w: bad identification header", ErrFormat)
	}
	return nil
}

func isHeader(p []byte, kind byte) bool {
	return len(p) >= 7 && p[0] == kind && string(p[1:7]) == "vorbis"
}

// readPackets splits the pages of one logical stream into packets and
// returns them with the granule position of the last page.
func readPackets(data []byte) ([][]byte, int64, error) {
	var packets [][]byte
	var pending []byte
	var serial, seq uint32
	var granule int64
	open := false
	for pos, page := 0, 0; pos < len(data); page++ {
		if len(data)-pos < pageHeaderSize || !bytes.Equal(data[pos:pos+4], capture) || data[pos+4] != 0 {
			return nil, 0, fmt.Errorf("%w: no Ogg page at offset %d", ErrFormat, pos)
		}
		h := data[pos:]
		flags := h[5]
		segments := int(h[26])
		headerSize := pageHeaderSize + segments
		if len(h) < headerSize {
			return nil, 0, fmt.Errorf("%w: truncated page at offset %d", ErrFormat, pos)
		}
		bodySize := 0
		for _, n := range h[pageHeaderSize:headerSize] {
			bodySize += int(n)
		}
		if len(h) < headerSize+bodySize {
			return nil, 0, fmt.Errorf("%w: truncated page at offset %d", ErrFormat, pos)
		}
		h = h[:headerSize+bodySize]
		if pageCRC(h) != binary.LittleEndian.Uint32(h[22:]) {
			return nil, 0, fmt.Errorf("%w: page %d checksum mismatch", ErrFormat, page)
		}
		pageSerial, pageSeq := binary.LittleEndian.Uint32(h[14:]), binary.LittleEndian.Uint32(h[18:])
		switch {
		case page == 0 && flags&flagFirst == 0:
			return nil, 0, fmt.Errorf("%w: first page does not start a stream", ErrFormat)
		case page > 0 && (flags&flagFirst != 0 || pageSerial != serial):
			return nil, 0, fmt.Errorf("%w: chained or multiplexed streams are not supported", ErrFormat)
		case page > 0 && pageSeq != seq+1:
			return nil, 0, fmt.Errorf("%w: page %d is out of sequence", ErrFormat, page)
		case (flags&flagContinued != 0) != open:
			return nil, 0, fmt.Errorf("%w: page %d breaks packet continuation", ErrFormat, page)
		}
		serial, seq = pageSerial, pageSeq
		if g := int64(binary.LittleEndian.Uint64(h[6:])); g != -1 {
			granule = g
		}
		body := h[headerSize:]
		for _, n := range h[pageHeaderSize:headerSize] {
			pending = append(pending, body[:n]...)
			body = body[n:]
			if n < 255 {
				packets = append(packets, pending)
				pending = nil
			}
		}
		if segments > 0 {
			open = h[headerSize-1] == 255
		}
		pos += len(h)
	}
	if open {
		return nil, 0, fmt.Errorf("%w: last packet is unterminated", ErrFormat)
	}
	return packets, granule, nil
}

var crcTable = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for range 8 {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
}()

// pageCRC is the Ogg page checksum: CRC-32 with polynomial 0x04c11db7,
// no reflection and a zero initial value, computed with the checksum
// field itself zeroed.
func pageCRC(page []byte) uint32 {
	var c uint32
	for i, b := range page {
		if i >= 22 && i < 26 {
			b = 0
		}
		c = c<<8 ^ crcTable[byte(c>>24)^b]
	}
	return c
}
