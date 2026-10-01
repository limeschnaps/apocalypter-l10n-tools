// Package unityyaml reads string fields of MonoBehaviour components from
// Unity text-serialized assets (.prefab, .unity, .asset).
//
// The parser does not build a generic YAML tree. It walks the block
// structure Unity emits and records the exact byte range of every scalar
// value, so an editor can replace one value without touching the rest of
// the file.
package unityyaml

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrNotText reports that the data is not a Unity text-serialized asset.
var ErrNotText = errors.New("unityyaml: not a text-serialized Unity asset")

// ErrSyntax reports content the parser cannot interpret safely.
var ErrSyntax = errors.New("unityyaml: syntax error")

const (
	classGameObject    = 1
	classMonoBehaviour = 114
)

// Field is a scalar value inside a MonoBehaviour document.
type Field struct {
	// Path addresses the value inside the component, e.g. "m_text" or
	// "entries[2].title".
	Path string
	// Value is the decoded scalar.
	Value string
	// Line is the 1-based line number where the value starts.
	Line int
	// Start and End delimit the bytes to replace when the value changes.
	// The range begins right after the ':' of the key (or the '-' of a
	// sequence item) and covers the whole scalar, including continuation
	// lines. The replacement is " " + Encode(newValue).
	Start int
	End   int
}

// MonoBehaviour is one component document (class ID 114).
type MonoBehaviour struct {
	ID           int64
	GameObjectID int64
	ScriptGUID   string
	ScriptFileID int64
	Fields       []Field
}

// Asset is the parsed content of one asset file.
type Asset struct {
	MonoBehaviours []MonoBehaviour
	// GameObjectNames maps GameObject document IDs to their m_Name.
	GameObjectNames map[int64]string
}

type line struct {
	start int
	end   int // excludes "\n" and a preceding "\r"
}

type frame struct {
	col   int
	name  string
	item  bool
	index int
	items int
}

type body struct {
	fields []Field
	// flows holds raw flow values ({...} or [...]) of top-level keys.
	flows map[string]string
}

var (
	fileIDRe = regexp.MustCompile(`fileID:\s*(-?\d+)`)
	guidRe   = regexp.MustCompile(`guid:\s*([0-9a-fA-F]{32})`)
)

// Parse extracts MonoBehaviour fields and GameObject names from data.
func Parse(data []byte) (*Asset, error) {
	if !bytes.HasPrefix(data, []byte("%YAML")) {
		return nil, ErrNotText
	}
	lines := splitLines(data)
	asset := &Asset{GameObjectNames: map[int64]string{}}

	for i := 0; i < len(lines); {
		if !isDocStart(data, lines[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && !isDocStart(data, lines[j]) {
			j++
		}
		if err := asset.addDocument(data, lines, i, j); err != nil {
			return nil, err
		}
		i = j
	}
	return asset, nil
}

func (a *Asset) addDocument(data []byte, lines []line, from, to int) error {
	hdr := lines[from]
	class, id, err := parseDocHeader(data[hdr.start:hdr.end])
	if err != nil {
		return fmt.Errorf("line %d: %w", from+1, err)
	}
	if class != classMonoBehaviour && class != classGameObject {
		return nil
	}
	// lines[from+1] holds the type name, e.g. "MonoBehaviour:".
	b, err := parseBody(data, lines, min(from+2, to), to)
	if err != nil {
		return err
	}

	if class == classGameObject {
		for _, f := range b.fields {
			if f.Path == "m_Name" {
				a.GameObjectNames[id] = f.Value
			}
		}
		return nil
	}

	mb := MonoBehaviour{ID: id, Fields: b.fields}
	if m := fileIDRe.FindStringSubmatch(b.flows["m_GameObject"]); m != nil {
		mb.GameObjectID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	script := b.flows["m_Script"]
	if m := fileIDRe.FindStringSubmatch(script); m != nil {
		mb.ScriptFileID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := guidRe.FindStringSubmatch(script); m != nil {
		mb.ScriptGUID = strings.ToLower(m[1])
	}
	a.MonoBehaviours = append(a.MonoBehaviours, mb)
	return nil
}

// parseDocHeader parses "--- !u!114 &123456789" with an optional
// trailing "stripped".
func parseDocHeader(hdr []byte) (class int, id int64, err error) {
	parts := strings.Fields(string(hdr))
	if len(parts) < 3 || !strings.HasPrefix(parts[1], "!u!") || !strings.HasPrefix(parts[2], "&") {
		return 0, 0, fmt.Errorf("%w: bad document header %q", ErrSyntax, hdr)
	}
	class, err = strconv.Atoi(strings.TrimPrefix(parts[1], "!u!"))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: bad class id in %q", ErrSyntax, hdr)
	}
	id, err = strconv.ParseInt(strings.TrimPrefix(parts[2], "&"), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: bad document id in %q", ErrSyntax, hdr)
	}
	return class, id, nil
}

func parseBody(data []byte, lines []line, from, to int) (body, error) {
	b := body{flows: map[string]string{}}
	var stack []frame
	limit := len(data)
	if to < len(lines) {
		limit = lines[to].start
	}

	for li := from; li < to; {
		ln := lines[li]
		text := data[ln.start:ln.end]
		indent := leadingSpaces(text)
		if indent == len(text) || text[indent] == '#' {
			li++
			continue
		}
		col := indent
		pos := ln.start + indent

		if isDash(text[indent:]) {
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.col > col || (top.item && top.col == col) {
					stack = stack[:len(stack)-1]
					continue
				}
				break
			}
			if len(stack) == 0 {
				return b, fmt.Errorf("%w: line %d: sequence item without a parent key", ErrSyntax, li+1)
			}
			parent := &stack[len(stack)-1]
			idx := parent.items
			parent.items++
			stack = append(stack, frame{col: col, item: true, index: idx})

			valStart := pos + 1
			k := valStart
			for k < ln.end && data[k] == ' ' {
				k++
			}
			if k == ln.end {
				// "-" alone starts a nested mapping when deeper lines follow;
				// otherwise it is an empty string item, which Unity writes as
				// "- ".
				if !hasDeeperLine(data, lines, li+1, to, col) {
					b.fields = append(b.fields, Field{Path: pathOf(stack), Line: li + 1, Start: valStart, End: ln.end})
				}
				li++
				continue
			}
			switch data[k] {
			case '{', '[', '|', '>', '&', '*', '!':
				// Flow collections such as "- {fileID: 42}", block scalars,
				// anchors, aliases and tags are not string items.
				li = skipContinuation(data, lines, li+1, to, col)
				continue
			}
			if _, _, ok := splitKey(data[k:ln.end]); !ok {
				next, err := b.addScalar(data, lines, li, k, valStart, col, to, limit, pathOf(stack))
				if err != nil {
					return b, err
				}
				li = next
				continue
			}
			col = k - ln.start
			pos = k
		}

		key, colon, ok := splitKey(data[pos:ln.end])
		if !ok {
			return b, fmt.Errorf("%w: line %d: unexpected content", ErrSyntax, li+1)
		}
		for len(stack) > 0 && stack[len(stack)-1].col >= col {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, frame{col: col, name: key})

		valStart := pos + colon + 1
		vpos := valStart
		for vpos < ln.end && data[vpos] == ' ' {
			vpos++
		}
		if vpos == ln.end {
			if isContainer(data, lines, li+1, to, col) {
				li++
				continue
			}
			b.fields = append(b.fields, Field{Path: pathOf(stack), Line: li + 1, Start: valStart, End: ln.end})
			li++
			continue
		}

		switch data[vpos] {
		case '{', '[':
			if len(stack) == 1 {
				b.flows[key] = string(data[vpos:ln.end])
			}
			li = skipContinuation(data, lines, li+1, to, col)
			continue
		case '|', '>', '&', '*', '!':
			// Block scalars, anchors, aliases and tags: Unity does not emit
			// them for MonoBehaviour fields, so they are not editable.
			li = skipContinuation(data, lines, li+1, to, col)
			continue
		}
		next, err := b.addScalar(data, lines, li, vpos, valStart, col, to, limit, pathOf(stack))
		if err != nil {
			return b, err
		}
		li = next
	}
	return b, nil
}

func (b *body) addScalar(data []byte, lines []line, li, vpos, valStart, parentCol, to, limit int, path string) (int, error) {
	value, end, next, err := scanScalar(data, lines, li, vpos, parentCol, to, limit)
	if err != nil {
		return 0, fmt.Errorf("line %d: %w", li+1, err)
	}
	b.fields = append(b.fields, Field{Path: path, Value: value, Line: li + 1, Start: valStart, End: end})
	return next, nil
}

// scanScalar reads the scalar starting at offset vpos on line li. It
// returns the decoded value, the end offset of the raw scalar and the
// index of the first line after it.
func scanScalar(data []byte, lines []line, li, vpos, parentCol, to, limit int) (string, int, int, error) {
	switch data[vpos] {
	case '"', '\'':
		quote := data[vpos]
		closing, err := findClosingQuote(data, vpos+1, limit, quote)
		if err != nil {
			return "", 0, 0, err
		}
		raw := string(data[vpos+1 : closing])
		var value string
		if quote == '"' {
			value, err = decodeDouble(raw)
		} else {
			value = decodeSingle(raw)
		}
		if err != nil {
			return "", 0, 0, err
		}
		next := li
		for next < to && lines[next].end < closing {
			next++
		}
		return value, closing + 1, next + 1, nil
	}

	first := bytes.TrimRight(data[vpos:lines[li].end], " \t")
	var sb strings.Builder
	sb.Write(first)
	end := vpos + len(first)
	next := li + 1
	blanks := 0
	for k := li + 1; k < to; k++ {
		text := data[lines[k].start:lines[k].end]
		indent := leadingSpaces(text)
		if indent == len(text) {
			blanks++
			continue
		}
		if indent <= parentCol {
			break
		}
		content := bytes.TrimRight(text[indent:], " \t")
		if blanks == 0 {
			sb.WriteByte(' ')
		} else {
			sb.WriteString(strings.Repeat("\n", blanks))
		}
		sb.Write(content)
		blanks = 0
		end = lines[k].start + indent + len(content)
		next = k + 1
	}
	return sb.String(), end, next, nil
}

func findClosingQuote(data []byte, from, limit int, quote byte) (int, error) {
	for i := from; i < limit; i++ {
		switch {
		case quote == '"' && data[i] == '\\':
			i++
		case data[i] == quote:
			if quote == '\'' && i+1 < limit && data[i+1] == '\'' {
				i++
				continue
			}
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: unterminated quoted scalar", ErrSyntax)
}

// isContainer reports whether the key at column col, which has no inline
// value, owns the following lines as a nested mapping or sequence.
func isContainer(data []byte, lines []line, from, to, col int) bool {
	for k := from; k < to; k++ {
		text := data[lines[k].start:lines[k].end]
		indent := leadingSpaces(text)
		if indent == len(text) {
			continue
		}
		return indent > col || (indent == col && isDash(text[indent:]))
	}
	return false
}

// hasDeeperLine reports whether the next non-blank line is indented more
// than col.
func hasDeeperLine(data []byte, lines []line, from, to, col int) bool {
	for k := from; k < to; k++ {
		text := data[lines[k].start:lines[k].end]
		indent := leadingSpaces(text)
		if indent == len(text) {
			continue
		}
		return indent > col
	}
	return false
}

func skipContinuation(data []byte, lines []line, from, to, col int) int {
	k := from
	for k < to {
		text := data[lines[k].start:lines[k].end]
		indent := leadingSpaces(text)
		if indent < len(text) && indent <= col {
			break
		}
		k++
	}
	return k
}

// splitKey splits "key: value" or "key:" and returns the key and the
// offset of the colon.
func splitKey(s []byte) (string, int, bool) {
	if len(s) == 0 || s[0] == '"' || s[0] == '\'' || s[0] == '{' || s[0] == '[' {
		return "", 0, false
	}
	idx := bytes.Index(s, []byte(": "))
	if idx < 0 {
		trimmed := bytes.TrimRight(s, " \t")
		if len(trimmed) == 0 || trimmed[len(trimmed)-1] != ':' {
			return "", 0, false
		}
		idx = len(trimmed) - 1
	}
	if idx == 0 {
		return "", 0, false
	}
	return string(s[:idx]), idx, true
}

func pathOf(stack []frame) string {
	var sb strings.Builder
	for _, f := range stack {
		if f.item {
			sb.WriteByte('[')
			sb.WriteString(strconv.Itoa(f.index))
			sb.WriteByte(']')
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(f.name)
	}
	return sb.String()
}

func splitLines(data []byte) []line {
	var lines []line
	start := 0
	for i, c := range data {
		if c != '\n' {
			continue
		}
		lines = append(lines, newLine(data, start, i))
		start = i + 1
	}
	if start < len(data) {
		lines = append(lines, newLine(data, start, len(data)))
	}
	return lines
}

func newLine(data []byte, start, end int) line {
	if end > start && data[end-1] == '\r' {
		end--
	}
	return line{start: start, end: end}
}

func isDocStart(data []byte, ln line) bool {
	return bytes.HasPrefix(data[ln.start:ln.end], []byte("--- "))
}

func isDash(s []byte) bool {
	return len(s) > 0 && s[0] == '-' && (len(s) == 1 || s[1] == ' ')
}

func leadingSpaces(s []byte) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}
