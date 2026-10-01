package md4

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Test vectors from RFC 1320, appendix A.5.
func TestSum(t *testing.T) {
	cases := map[string]string{
		"":                           "31d6cfe0d16ae931b73c59d7e0c089c0",
		"a":                          "bde52cb31de33e46245e05fbdbd6fb24",
		"abc":                        "a448017aaf21d8525fc10ae87aa6729d",
		"message digest":             "d9130a8164549fe818874806e1c7014b",
		"abcdefghijklmnopqrstuvwxyz": "d79e1c308aa5bbcdeea8ed63df412da9",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789": "043f8582f241db351ce627e153e7f0e4",
		strings.Repeat("1234567890", 8):                                  "e33b4ddc9c38f2199c3e7b164fcc0536",
	}
	for in, want := range cases {
		sum := Sum([]byte(in))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("Sum(%q) = %s, want %s", in, got, want)
		}
	}
}
