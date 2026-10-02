package index

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"apocalypter-l10n-tools/internal/textkind"
)

// Sort names a result column to order by. The zero value keeps the index
// order: file by file, in the order of the objects inside each file.
type Sort string

// Columns results can be sorted by.
const (
	SortNone  Sort = ""
	SortValue Sort = "value"
	SortKind  Sort = "kind"
	SortPath  Sort = "path"
	SortOwner Sort = "owner" // GameObject, then script
	SortFile  Sort = "file"  // file, then position in it
)

// ParseSort validates a column name.
func ParseSort(s string) (Sort, error) {
	switch v := Sort(s); v {
	case SortNone, SortValue, SortKind, SortPath, SortOwner, SortFile:
		return v, nil
	default:
		return "", fmt.Errorf("%w: unknown sort column %q", ErrBadQuery, s)
	}
}

// kindRank orders kinds as the UI lists them: screen text first.
var kindRank = map[textkind.Kind]int{textkind.Screen: 0, textkind.Maybe: 1, textkind.Service: 2}

func (s Sort) compare(a, b *Entry) int {
	switch s {
	case SortValue:
		return naturalCompare(a.lower, b.lower)
	case SortKind:
		return cmp.Compare(kindRank[a.Kind], kindRank[b.Kind])
	case SortPath:
		return naturalCompare(a.Path, b.Path)
	case SortOwner:
		if c := naturalCompare(strings.ToLower(a.GameObject), strings.ToLower(b.GameObject)); c != 0 {
			return c
		}
		return naturalCompare(strings.ToLower(a.Script), strings.ToLower(b.Script))
	case SortFile:
		if c := naturalCompare(a.File, b.File); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.DocID, b.DocID); c != 0 {
			return c
		}
		return cmp.Compare(a.Start, b.Start)
	default:
		return 0
	}
}

// naturalCompare orders strings with runs of digits compared as numbers,
// so "item 9" sorts before "item 10" and "a[2]" before "a[10]".
func naturalCompare(a, b string) int {
	// Skip the common prefix byte by byte, backing up to the start of a
	// digit run it ends in, so the number there is compared whole.
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	if i == len(a) && i == len(b) {
		return 0
	}
	// Back up to a rune start: "б" and "г" share their first UTF-8 byte.
	for i > 0 && i < len(a) && !utf8.RuneStart(a[i]) {
		i--
	}
	for i > 0 && a[i-1] >= '0' && a[i-1] <= '9' {
		i--
	}
	a, b = a[i:], b[i:]
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if isDigit(ra) && isDigit(rb) {
			da, db := digitRun(a), digitRun(b)
			// Compare by value: strip leading zeros, then longer is larger.
			va, vb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := cmp.Compare(len(va), len(vb)); c != 0 {
				return c
			}
			if c := strings.Compare(va, vb); c != 0 {
				return c
			}
			// Equal values: fewer leading zeros first, for a total order.
			if c := cmp.Compare(da, db); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if ra != rb {
			return cmp.Compare(ra, rb)
		}
		a, b = a[na:], b[nb:]
	}
	return cmp.Compare(len(a), len(b))
}

func isDigit(r rune) bool { return r < utf8.RuneSelf && unicode.IsDigit(r) }

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// sortCache keeps the ordered matches of the last sorted query, so that
// paging through it does not sort again. Sorting all strings of a large
// build takes about a second.
type sortCache struct {
	mu   sync.Mutex
	key  Query // the query without Offset and Limit
	gen  uint64
	full *Result
}

// search runs q: collect adds every entry of the index to a Result with
// Result.add. The caller holds the index read lock, and gen identifies
// the index contents; it must change whenever entries do, because the
// cache holds pointers into them.
func (c *sortCache) search(gen uint64, q Query, collect func(*Result, Query)) Result {
	if q.Sort == SortNone {
		res := Result{Entries: []Entry{}}
		collect(&res, q)
		return res
	}
	key := q
	key.Offset, key.Limit = 0, 0

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.full == nil || c.gen != gen || c.key != key {
		full := &Result{}
		collect(full, key)
		slices.SortStableFunc(full.matches, func(a, b *Entry) int {
			if key.Desc {
				return key.Sort.compare(b, a)
			}
			return key.Sort.compare(a, b)
		})
		c.key, c.gen, c.full = key, gen, full
	}

	res := Result{
		Total: c.full.Total, Hidden: c.full.Hidden, HiddenMaybe: c.full.HiddenMaybe,
		Offset: q.Offset, Entries: []Entry{},
	}
	if q.Offset < len(c.full.matches) {
		for _, e := range c.full.matches[q.Offset:min(len(c.full.matches), q.Offset+q.Limit)] {
			res.Entries = append(res.Entries, *e)
		}
	}
	return res
}
