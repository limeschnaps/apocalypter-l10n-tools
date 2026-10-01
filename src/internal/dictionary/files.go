package dictionary

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"apocalypter-l10n-tools/internal/po"
)

// File names of a dictionary in a localization directory.
const (
	MapName = "translation.map"
	POName  = "translation.po"
)

// maxLocationComments caps the locations listed for translators in a PO
// message; a string can appear in hundreds of places.
const maxLocationComments = 5

var languageRe = regexp.MustCompile(`^[a-z]{2,3}([_-][A-Za-z]{2,4})?$`)

// ID identifies a string in translation.map: the first 16 hex digits of
// the SHA-256 of the string. The map holds no text; the patcher finds the
// locations of a PO message by the ID of its msgid.
func ID(old string) string {
	sum := sha256.Sum256([]byte(old))
	return hex.EncodeToString(sum[:8])
}

// mapEntry is one translation.map record.
type mapEntry struct {
	ID      string     `json:"id"`
	FoundIn []Location `json:"found_in"`
}

// Header holds the fields of a new PO header.
type Header struct {
	// Project is the game or project name.
	Project string
	// Language is left out of the header when it is not a language code.
	Language string
}

// Encode returns the content of translation.map and translation.po for
// entries, in entry order.
//
// Translations, flags and translator comments of previous carry over by
// msgid, and so does its header; a file without one gets header. A translated
// message of previous whose string is gone is kept as obsolete, so its
// translation returns when the string does; such messages that were active
// in previous are returned as obsoleted.
func Encode(entries []Entry, previous *po.File, header Header) (mapData, poData []byte, obsoleted []po.Message, err error) {
	prev := map[string]po.Message{}
	if previous != nil {
		for _, m := range previous.Messages {
			if old, ok := prev[m.ID]; !ok || old.Obsolete {
				prev[m.ID] = m
			}
		}
	}

	f := &po.File{Header: header.message()}
	if previous != nil && previous.Header.Str != "" {
		f.Header = previous.Header
	}
	records := make([]mapEntry, 0, len(entries))
	ids := make(map[string]string, len(entries))
	for _, e := range entries {
		id := ID(e.Old)
		if other, ok := ids[id]; ok {
			return nil, nil, nil, fmt.Errorf("%w: strings %q and %q share ID %s", ErrFormat, other, e.Old, id)
		}
		ids[id] = e.Old
		records = append(records, mapEntry{ID: id, FoundIn: e.FoundIn})

		m := po.Message{ID: e.Old, Extracted: locationComments(e.FoundIn)}
		if p, ok := prev[e.Old]; ok {
			m.Str, m.Flags, m.Comments = p.Str, p.Flags, p.Comments
			delete(prev, e.Old)
		}
		f.Messages = append(f.Messages, m)
	}
	var obsolete []po.Message
	for _, m := range prev {
		if m.Str == "" {
			continue
		}
		if !m.Obsolete {
			obsoleted = append(obsoleted, m)
		}
		obsolete = append(obsolete, po.Message{Comments: m.Comments, Flags: m.Flags, ID: m.ID, Str: m.Str, Obsolete: true})
	}
	byID := func(a, b po.Message) int { return cmp.Compare(a.ID, b.ID) }
	slices.SortFunc(obsolete, byID)
	slices.SortFunc(obsoleted, byID)
	f.Messages = append(f.Messages, obsolete...)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Owner and field names stay readable, as in the patch journal.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(records); err != nil {
		return nil, nil, nil, fmt.Errorf("encode %s: %w", MapName, err)
	}
	return buf.Bytes(), po.Marshal(f), obsoleted, nil
}

func (h Header) message() po.Message {
	var s string
	if h.Project != "" {
		s += "Project-Id-Version: " + h.Project + "\n"
	}
	s += "MIME-Version: 1.0\nContent-Type: text/plain; charset=UTF-8\nContent-Transfer-Encoding: 8bit\n"
	if languageRe.MatchString(h.Language) {
		s += "Language: " + h.Language + "\n"
	}
	return po.Message{Str: s}
}

// locationComments lists where a string is used, for translators.
func locationComments(locs []Location) []string {
	var out []string
	for _, l := range locs[:min(len(locs), maxLocationComments)] {
		out = append(out, l.File+" | "+l.Owner+" | "+l.Path)
	}
	if n := len(locs) - maxLocationComments; n > 0 {
		out = append(out, "… +"+strconv.Itoa(n))
	}
	return out
}

// Decode joins translation.map and translation.po into entries, in map
// order. A message takes effect when its msgstr is set and it is not
// marked fuzzy; fuzzy counts the messages skipped for the mark. Every
// active message must have its ID in the map: a message the map does not
// know means the files were not generated together.
func Decode(mapData, poData []byte) (entries []Entry, fuzzy int, err error) {
	var records []mapEntry
	if err := json.Unmarshal(mapData, &records); err != nil {
		return nil, 0, fmt.Errorf("%w: %s: %w", ErrFormat, MapName, err)
	}
	index := make(map[string]int, len(records))
	for i, r := range records {
		switch _, dup := index[r.ID]; {
		case r.ID == "":
			return nil, 0, fmt.Errorf("%w: %s: entry %d has no id", ErrFormat, MapName, i+1)
		case dup:
			return nil, 0, fmt.Errorf("%w: %s: id %s repeats", ErrFormat, MapName, r.ID)
		case len(r.FoundIn) == 0:
			return nil, 0, fmt.Errorf("%w: %s: id %s has no locations", ErrFormat, MapName, r.ID)
		}
		index[r.ID] = i
	}
	f, err := po.Parse(poData)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", POName, err)
	}

	entries = make([]Entry, len(records))
	for i, r := range records {
		entries[i].FoundIn = r.FoundIn
	}
	seen := map[string]bool{}
	for _, m := range f.Messages {
		if m.Obsolete {
			continue
		}
		i, ok := index[ID(m.ID)]
		switch {
		case m.Context != "":
			return nil, 0, fmt.Errorf("%w: %s: msgctxt is not supported (msgid %q)", ErrFormat, POName, m.ID)
		case seen[m.ID]:
			return nil, 0, fmt.Errorf("%w: %s: msgid %q repeats", ErrFormat, POName, m.ID)
		case !ok:
			return nil, 0, fmt.Errorf("%w: msgid %q of %s is missing from %s; regenerate both files with 'editor dump'", ErrFormat, m.ID, POName, MapName)
		}
		seen[m.ID] = true
		entries[i].Old = m.ID
		if m.Fuzzy() {
			fuzzy++
		} else {
			entries[i].New = m.Str
		}
	}
	// Map records without a message stay untranslated; their text is
	// unknown, and Patches skips them.
	return slices.DeleteFunc(entries, func(e Entry) bool { return e.Old == "" }), fuzzy, nil
}

// Save writes the files returned by Encode into dir. Each file is
// replaced atomically, so an interrupted write never loses translations.
func Save(dir string, mapData, poData []byte) error {
	if err := writeAtomic(filepath.Join(dir, MapName), mapData); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, POName), poData)
}

func writeAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp %s: %w", filepath.Base(path), err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}
