package serialized

import (
	"slices"
	"testing"

	"apocalypter-l10n-tools/internal/unitytest"
)

func TestScanStrings(t *testing.T) {
	var data []byte
	data = le.AppendUint32(data, 0xdeadbeef) // skipped by from
	data = le.AppendUint32(data, 7)          // int field
	data = append(data, unitytest.String("Hello")...)
	data = le.AppendUint32(data, 1) // one-element int array that reads as "d"
	data = le.AppendUint32(data, 'd')
	data = append(data, unitytest.String("Привет, мир!\n")...)
	data = append(data, unitytest.String("42")...)     // no letter
	data = append(data, unitytest.String("a\x01b")...) // control character
	data = append(data, 3, 0, 0, 0, 'a', 'b', 'c', 1)  // non-zero padding
	data = append(data, 2, 0, 0, 0, 0xff, 0xfe, 0, 0)  // invalid UTF-8
	data = append(data, unitytest.String("OK")...)
	data = append(data, 9, 0, 0, 0, 'c', 'u', 't') // truncated

	got := ScanStrings(data, 4, le)
	var values []string
	for _, s := range got {
		values = append(values, s.Value)
	}
	if want := []string{"Hello", "Привет, мир!\n", "OK"}; !slices.Equal(values, want) {
		t.Fatalf("values = %q, want %q", values, want)
	}
	if got[0].Offset != 8 || got[0].Size != 12 {
		t.Errorf("first string at %d size %d", got[0].Offset, got[0].Size)
	}
	for _, s := range got {
		if string(data[s.Offset:s.Offset+s.Size]) != string(EncodeString(s.Value, le)) {
			t.Errorf("%q: bytes at offset do not encode the value", s.Value)
		}
	}
	if len(ScanStrings(data, len(data), le)) != 0 || len(ScanStrings(nil, 0, le)) != 0 {
		t.Error("scan past the end found strings")
	}
}
