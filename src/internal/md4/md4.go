// Package md4 implements the MD4 hash (RFC 1320).
//
// MD4 is cryptographically broken. It is used here only because Unity
// derives the fileID of scripts compiled into DLLs from an MD4 digest.
package md4

import (
	"encoding/binary"
	"math/bits"
)

// Sum returns the MD4 digest of data.
func Sum(data []byte) [16]byte {
	msg := make([]byte, len(data), len(data)+72)
	copy(msg, data)
	msg = append(msg, 0x80)
	for len(msg)%64 != 56 {
		msg = append(msg, 0)
	}
	msg = binary.LittleEndian.AppendUint64(msg, uint64(len(data))*8)

	a, b, c, d := uint32(0x67452301), uint32(0xefcdab89), uint32(0x98badcfe), uint32(0x10325476)
	var x [16]uint32
	for chunk := msg; len(chunk) > 0; chunk = chunk[64:] {
		for i := range x {
			x[i] = binary.LittleEndian.Uint32(chunk[i*4:])
		}
		aa, bb, cc, dd := a, b, c, d

		f := func(x, y, z uint32) uint32 { return x&y | ^x&z }
		for _, i := range [16]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15} {
			s := [4]int{3, 7, 11, 19}[i%4]
			switch i % 4 {
			case 0:
				a = bits.RotateLeft32(a+f(b, c, d)+x[i], s)
			case 1:
				d = bits.RotateLeft32(d+f(a, b, c)+x[i], s)
			case 2:
				c = bits.RotateLeft32(c+f(d, a, b)+x[i], s)
			case 3:
				b = bits.RotateLeft32(b+f(c, d, a)+x[i], s)
			}
		}

		g := func(x, y, z uint32) uint32 { return x&y | x&z | y&z }
		for n, i := range [16]int{0, 4, 8, 12, 1, 5, 9, 13, 2, 6, 10, 14, 3, 7, 11, 15} {
			const k = 0x5a827999
			s := [4]int{3, 5, 9, 13}[n%4]
			switch n % 4 {
			case 0:
				a = bits.RotateLeft32(a+g(b, c, d)+x[i]+k, s)
			case 1:
				d = bits.RotateLeft32(d+g(a, b, c)+x[i]+k, s)
			case 2:
				c = bits.RotateLeft32(c+g(d, a, b)+x[i]+k, s)
			case 3:
				b = bits.RotateLeft32(b+g(c, d, a)+x[i]+k, s)
			}
		}

		h := func(x, y, z uint32) uint32 { return x ^ y ^ z }
		for n, i := range [16]int{0, 8, 4, 12, 2, 10, 6, 14, 1, 9, 5, 13, 3, 11, 7, 15} {
			const k = 0x6ed9eba1
			s := [4]int{3, 9, 11, 15}[n%4]
			switch n % 4 {
			case 0:
				a = bits.RotateLeft32(a+h(b, c, d)+x[i]+k, s)
			case 1:
				d = bits.RotateLeft32(d+h(a, b, c)+x[i]+k, s)
			case 2:
				c = bits.RotateLeft32(c+h(d, a, b)+x[i]+k, s)
			case 3:
				b = bits.RotateLeft32(b+h(c, d, a)+x[i]+k, s)
			}
		}

		a, b, c, d = a+aa, b+bb, c+cc, d+dd
	}

	var out [16]byte
	binary.LittleEndian.PutUint32(out[0:], a)
	binary.LittleEndian.PutUint32(out[4:], b)
	binary.LittleEndian.PutUint32(out[8:], c)
	binary.LittleEndian.PutUint32(out[12:], d)
	return out
}
