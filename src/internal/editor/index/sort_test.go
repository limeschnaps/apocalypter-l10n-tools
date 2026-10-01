package index

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"apocalypter-l10n-tools/internal/editor/textkind"
)

func TestNaturalCompare(t *testing.T) {
	ordered := []string{"", "a", "a10b", "a[2]", "a[10]", "item 9", "item 09", "item 10", "item 10a", "Ω", "яблоко", "ягода", "ягода 2"}
	for i := range ordered {
		for j := range ordered {
			got := naturalCompare(ordered[i], ordered[j])
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got != want {
				t.Errorf("naturalCompare(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestParseSort(t *testing.T) {
	for _, s := range []string{"", "value", "kind", "path", "owner", "file"} {
		if got, err := ParseSort(s); err != nil || string(got) != s {
			t.Errorf("ParseSort(%q) = %q, %v", s, got, err)
		}
	}
	if _, err := ParseSort("size"); !errors.Is(err, ErrBadQuery) {
		t.Errorf("err = %v", err)
	}
}

func values(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}

func TestSearchSort(t *testing.T) {
	_, ix := newProject(t)
	unsorted := values(search(t, ix, Query{}).Entries)
	cases := []struct {
		sort Sort
		desc bool
		want []string
	}{
		// Case-insensitive: "hello from dll" < "hello world".
		{SortValue, false, []string{"First item", "GameSettings", "hello from dll", "Hello World", "orphan script", "Second item", "Welcome, player"}},
		{SortValue, true, []string{"Welcome, player", "Second item", "orphan script", "Hello World", "hello from dll", "GameSettings", "First item"}},
		// Equal keys keep the index order.
		{SortKind, false, []string{"Hello World", "First item", "Second item", "orphan script", "Welcome, player", "hello from dll", "GameSettings"}},
		// greeting, m_Name, m_hint, m_items[0], m_items[1], m_note, m_text.
		{SortPath, false, []string{"Welcome, player", "GameSettings", "hello from dll", "First item", "Second item", "orphan script", "Hello World"}},
		{SortFile, true, nil},
	}
	for _, tc := range cases {
		name := string(tc.sort)
		if tc.desc {
			name += " desc"
		}
		t.Run(name, func(t *testing.T) {
			got := values(search(t, ix, Query{Sort: tc.sort, Desc: tc.desc}).Entries)
			want := tc.want
			if tc.sort == SortFile {
				// Index order is file order; descending reverses it.
				want = slices.Clone(unsorted)
				slices.Reverse(want)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %q\nwant %q", got, want)
			}
		})
	}

	// Pages of a sorted query line up with the full sorted list.
	full := values(search(t, ix, Query{Sort: SortValue}).Entries)
	var paged []string
	for offset := 0; offset < len(full); offset += 2 {
		res := search(t, ix, Query{Sort: SortValue, Offset: offset, Limit: 2})
		if res.Total != len(full) || res.Offset != offset {
			t.Errorf("offset %d: %+v", offset, res)
		}
		paged = append(paged, values(res.Entries)...)
	}
	if !reflect.DeepEqual(paged, full) {
		t.Errorf("paged = %q, want %q", paged, full)
	}
	if res := search(t, ix, Query{Sort: SortValue, Offset: 100, Limit: 2}); len(res.Entries) != 0 || res.Total != len(full) {
		t.Errorf("past the end = %+v", res)
	}
}

func TestSortCompareKeys(t *testing.T) {
	a := &Entry{GameObject: "Menu", Script: "b.Label", File: "f", Line: 3, DocID: 1, Start: 5, Kind: textkind.Service}
	b := &Entry{GameObject: "menu", Script: "a.Label", File: "f", Line: 3, DocID: 1, Start: 9, Kind: textkind.Screen}
	if c := SortOwner.compare(a, b); c <= 0 {
		t.Errorf("owner ties break on script: %d", c)
	}
	if c := SortFile.compare(a, b); c >= 0 {
		t.Errorf("file ties break on position: %d", c)
	}
	if c := SortKind.compare(b, a); c >= 0 {
		t.Errorf("screen sorts before service: %d", c)
	}
	b.DocID, b.Line = 0, 4
	if SortFile.compare(a, b) >= 0 {
		t.Error("line orders before doc ID")
	}
	b.Line = 3
	if SortFile.compare(a, b) <= 0 {
		t.Error("doc ID orders before position")
	}
	if SortNone.compare(a, b) != 0 {
		t.Error("no sort compares equal")
	}
}

func BenchmarkNaturalComparePaths(b *testing.B) {
	x := "fsm.states[12].actionData.fsmStringParams[104].value"
	y := "fsm.states[12].actionData.fsmStringParams[98].value"
	for b.Loop() {
		naturalCompare(x, y)
	}
}

func TestSortCacheFollowsEdits(t *testing.T) {
	_, ix := newProject(t)
	before := values(search(t, ix, Query{Sort: SortValue}).Entries)
	if before[0] != "First item" {
		t.Fatalf("before = %q", before)
	}
	// Paging reuses the cached order.
	if got := values(search(t, ix, Query{Sort: SortValue, Offset: 1, Limit: 1}).Entries); !reflect.DeepEqual(got, before[1:2]) {
		t.Errorf("page 2 = %q", got)
	}
	target := search(t, ix, Query{Text: "First item"}).Entries[0]
	if _, err := ix.Apply(editFor(target, "Zebra")); err != nil {
		t.Fatal(err)
	}
	after := values(search(t, ix, Query{Sort: SortValue}).Entries)
	if after[0] == "First item" || after[len(after)-1] != "Zebra" {
		t.Errorf("after edit = %q", after)
	}
	// A different filter is a different cache key.
	if got := values(search(t, ix, Query{Sort: SortValue, Text: "item"}).Entries); !reflect.DeepEqual(got, []string{"Second item"}) {
		t.Errorf("filtered = %q", got)
	}
}

func TestSortCacheConcurrent(t *testing.T) {
	_, ix := newProject(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			q := Query{Sort: []Sort{SortValue, SortPath}[i%2], Desc: i%3 == 0, Offset: i % 4, Limit: 2, Service: true}
			for range 50 {
				if _, err := ix.Search(q); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}
