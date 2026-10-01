// Package lz4 decodes LZ4 blocks as used by Unity asset bundles.
//
// Unity stores each bundle block as a raw LZ4 block (no frame header).
// LZ4 and LZ4HC produce the same block format, so one decoder serves both.
package lz4

import "errors"

// ErrCorrupt reports malformed compressed data.
var ErrCorrupt = errors.New("lz4: corrupt block")

// Decode decompresses src into dst, which must have exactly the
// uncompressed size of the block.
func Decode(dst, src []byte) error {
	si, di := 0, 0
	for si < len(src) {
		token := src[si]
		si++

		litLen := int(token >> 4)
		if litLen == 15 {
			n, next, err := readLength(src, si)
			if err != nil {
				return err
			}
			litLen += n
			si = next
		}
		if si+litLen > len(src) || di+litLen > len(dst) {
			return ErrCorrupt
		}
		copy(dst[di:], src[si:si+litLen])
		si += litLen
		di += litLen
		if si == len(src) {
			break
		}

		if si+2 > len(src) {
			return ErrCorrupt
		}
		offset := int(src[si]) | int(src[si+1])<<8
		si += 2
		if offset == 0 || offset > di {
			return ErrCorrupt
		}
		matchLen := int(token&0x0f) + 4
		if token&0x0f == 15 {
			n, next, err := readLength(src, si)
			if err != nil {
				return err
			}
			matchLen += n
			si = next
		}
		if di+matchLen > len(dst) {
			return ErrCorrupt
		}
		// Matches may overlap their own output, so copy forward byte-wise
		// when the distance is shorter than the length.
		start := di - offset
		if offset >= matchLen {
			copy(dst[di:di+matchLen], dst[start:start+matchLen])
		} else {
			for i := range matchLen {
				dst[di+i] = dst[start+i]
			}
		}
		di += matchLen
	}
	if di != len(dst) {
		return ErrCorrupt
	}
	return nil
}

func readLength(src []byte, si int) (int, int, error) {
	n := 0
	for {
		if si >= len(src) {
			return 0, 0, ErrCorrupt
		}
		b := src[si]
		si++
		n += int(b)
		if b != 255 {
			return n, si, nil
		}
	}
}
