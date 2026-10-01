package unityyaml

import (
	"errors"
	"testing"
)

// u builds a YAML \u escape without spelling it literally in the source.
func u(hex string) string { return `\` + "u" + hex }

func TestDecodeDouble(t *testing.T) {
	cases := map[string]string{
		`plain`:                   "plain",
		u("0410") + u("0411"):     "АБ",
		u("D83D") + u("DE00"):     string(rune(0x1F600)),
		`\U0001F600`:              string(rune(0x1F600)),
		u("D83D"):                 string(rune(0xFFFD)),
		`\x41\t\n\r\\\"\/\0`:      "A\t\n\r\\\"/\x00",
		`\N\_\L\P\e\a\b\v\f\ `:    string([]rune{0x85, 0xA0, 0x2028, 0x2029, 0x1B, 7, 8, 11, 12, 32}),
		"one  \n   two":           "one two",
		"one\n\n  two":            "one\ntwo",
		"one\n \n\n two":          "one\n\ntwo",
		"keep\\ \n  next":         "keep  next",
		"joined\\\n    \\ spaced": "joined spaced",
		"crlf\r\n  next":          "crlf next",
		"crlf\\\r\n  escaped":     "crlfescaped",
		"\\t \n x":                "\t x",
		u("041F") + u("0440") + u("0438") + " " + u("0432"): "При в",
	}
	for in, want := range cases {
		got, err := decodeDouble(in)
		if err != nil {
			t.Errorf("decodeDouble(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("decodeDouble(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeDoubleErrors(t *testing.T) {
	for _, in := range []string{`\`, `\q`, `\u12`, `\xZZ`} {
		if _, err := decodeDouble(in); !errors.Is(err, ErrSyntax) {
			t.Errorf("decodeDouble(%q) err = %v", in, err)
		}
	}
}

func TestDecodeSingle(t *testing.T) {
	cases := map[string]string{
		"it''s":           "it's",
		"a  \n   b":       "a b",
		"a\n\n  b":        "a\nb",
		"a\r\n  b''c":     "a b'c",
		"no newline here": "no newline here",
	}
	for in, want := range cases {
		if got := decodeSingle(in); got != want {
			t.Errorf("decodeSingle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncode(t *testing.T) {
	cases := map[string]string{
		"Hello world":         "Hello world",
		"123":                 "123",
		"":                    `""`,
		"true":                `"true"`,
		"~":                   `"~"`,
		" lead":               `" lead"`,
		"trail ":              `"trail "`,
		"-x":                  `"-x"`,
		"a: b":                `"a: b"`,
		"a #b":                `"a #b"`,
		"end:":                `"end:"`,
		"Привет":              `"` + u("041F") + u("0440") + u("0438") + u("0432") + u("0435") + u("0442") + `"`,
		string(rune(0x1F600)): `"` + u("D83D") + u("DE00") + `"`,
		"q\"\\\n\r\t":         `"q\"\\\n\r\t"`,
		"\x01":                `"\u0001"`,
	}
	for in, want := range cases {
		if got := Encode(in); got != want {
			t.Errorf("Encode(%q) = %s, want %s", in, got, want)
		}
	}
}
