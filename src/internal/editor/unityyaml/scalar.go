package unityyaml

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var simpleEscapes = map[byte]rune{
	'0': 0, 'a': 7, 'b': 8, 't': '\t', '\t': '\t', 'n': '\n', 'v': 11,
	'f': 12, 'r': '\r', 'e': 27, ' ': ' ', '"': '"', '/': '/', '\\': '\\',
	'N': 0x85, '_': 0xA0, 'L': 0x2028, 'P': 0x2029,
}

var hexEscapeLen = map[byte]int{'x': 2, 'u': 4, 'U': 8}

// decodeDouble decodes the body of a double-quoted scalar, including
// escape sequences and YAML line folding.
func decodeDouble(s string) (string, error) {
	var buf []byte
	// Bytes before protected come from escapes and survive the trimming of
	// trailing whitespace at a line break.
	protected := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 >= len(s) {
				return "", fmt.Errorf("%w: dangling backslash", ErrSyntax)
			}
			n := s[i+1]
			if n == '\n' || n == '\r' {
				i = skipLineBreak(s, i+1)
				i = skipBlanks(s, i)
				protected = len(buf)
				continue
			}
			r, width, err := decodeEscape(s[i:])
			if err != nil {
				return "", err
			}
			buf = utf8.AppendRune(buf, r)
			protected = len(buf)
			i += width
		case c == '\n' || c == '\r':
			for len(buf) > protected && (buf[len(buf)-1] == ' ' || buf[len(buf)-1] == '\t') {
				buf = buf[:len(buf)-1]
			}
			i, buf = fold(s, i, buf)
			protected = len(buf)
		default:
			buf = append(buf, c)
			i++
		}
	}
	return string(buf), nil
}

// decodeEscape decodes one escape sequence at the start of s and returns
// the rune and the number of bytes consumed. UTF-16 surrogate pairs
// written as two \u escapes are combined into one rune.
func decodeEscape(s string) (rune, int, error) {
	n := s[1]
	if r, ok := simpleEscapes[n]; ok {
		return r, 2, nil
	}
	size, ok := hexEscapeLen[n]
	if !ok {
		return 0, 0, fmt.Errorf("%w: unknown escape \\%c", ErrSyntax, n)
	}
	if len(s) < 2+size {
		return 0, 0, fmt.Errorf("%w: truncated escape \\%c", ErrSyntax, n)
	}
	v, err := strconv.ParseUint(s[2:2+size], 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: bad escape %q", ErrSyntax, s[:2+size])
	}
	r := rune(v)
	width := 2 + size
	if utf16.IsSurrogate(r) && strings.HasPrefix(s[width:], `\u`) && len(s) >= width+6 {
		if low, err := strconv.ParseUint(s[width+2:width+6], 16, 32); err == nil {
			if pair := utf16.DecodeRune(r, rune(low)); pair != utf8.RuneError {
				return pair, width + 6, nil
			}
		}
	}
	if !utf8.ValidRune(r) {
		r = utf8.RuneError
	}
	return r, width, nil
}

// decodeSingle decodes the body of a single-quoted scalar.
func decodeSingle(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\n") {
		return strings.ReplaceAll(s, "''", "'")
	}
	var buf []byte
	for i := 0; i < len(s); {
		if s[i] != '\n' {
			buf = append(buf, s[i])
			i++
			continue
		}
		for len(buf) > 0 && (buf[len(buf)-1] == ' ' || buf[len(buf)-1] == '\t') {
			buf = buf[:len(buf)-1]
		}
		i, buf = fold(s, i, buf)
	}
	return strings.ReplaceAll(string(buf), "''", "'")
}

// fold handles a line break at s[i] inside a flow scalar: a single break
// becomes a space, and each following empty line becomes a newline.
func fold(s string, i int, buf []byte) (int, []byte) {
	i = skipLineBreak(s, i)
	breaks := 0
	for {
		i = skipBlanks(s, i)
		if i < len(s) && (s[i] == '\n' || s[i] == '\r') {
			breaks++
			i = skipLineBreak(s, i)
			continue
		}
		break
	}
	if breaks == 0 {
		return i, append(buf, ' ')
	}
	return i, append(buf, strings.Repeat("\n", breaks)...)
}

func skipLineBreak(s string, i int) int {
	if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
		return i + 2
	}
	return i + 1
}

func skipBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// Encode renders v as a single-line YAML scalar that Unity reads back as
// the same string. Simple ASCII values stay plain; everything else is
// double-quoted with non-ASCII characters escaped as \uXXXX, the way the
// Unity serializer writes them, so a later re-save by Unity produces a
// minimal diff.
func Encode(v string) string {
	if isPlainSafe(v) {
		return v
	}
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			switch {
			case r >= 0x20 && r < 0x7f:
				sb.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&sb, `\u%04X`, r)
			default:
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(&sb, `\u%04X\u%04X`, hi, lo)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

var reservedPlain = map[string]bool{
	"~": true, "null": true, "true": true, "false": true,
	"yes": true, "no": true, "on": true, "off": true, "y": true, "n": true,
}

func isPlainSafe(v string) bool {
	if v == "" || reservedPlain[strings.ToLower(v)] {
		return false
	}
	if strings.ContainsRune(" -?:,[]{}#&*!|>'\"%@`", rune(v[0])) {
		return false
	}
	if v[len(v)-1] == ' ' || v[len(v)-1] == ':' || strings.Contains(v, ": ") || strings.Contains(v, " #") {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] >= 0x7f {
			return false
		}
	}
	return true
}
