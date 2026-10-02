// Package dictionary maps every distinct on-screen string of a game to its
// translation, so that a string repeated across many components is
// translated once.
//
// A dictionary is stored as two files in a localization directory:
//
//	translation.po   one gettext message per distinct string; translators
//	                 edit msgstr with any PO editor
//	translation.map  where each string was found, in the terms of the patch
//	                 journal, keyed by the ID of the string
//
// The editor writes both files; Patches turns the translated messages into
// journal records that the patcher applies after the journal itself.
package dictionary

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"apocalypter-l10n-tools/internal/patch"
)

// ErrFormat reports an invalid dictionary.
var ErrFormat = errors.New("dictionary: invalid dictionary")

// Location is one place a string was found. The patcher matches a patch by
// owner, script, path, kind, occurrence and the old value; File tells a
// translator where the string comes from.
type Location struct {
	File       string       `json:"file"`
	Path       string       `json:"path"`
	Owner      string       `json:"owner"`
	Script     patch.Script `json:"script"`
	Occurrence int          `json:"occurrence"`
	Kind       string       `json:"kind,omitempty"`
}

// Entry is one distinct string. An empty New leaves the string as is.
type Entry struct {
	Old     string
	New     string
	FoundIn []Location
}

// translated reports whether e changes the game.
func (e Entry) translated() bool { return e.New != "" && e.New != e.Old }

// component is the owner and script a patch applies to.
type component struct {
	owner  string
	script patch.Script
}

// target is what the patcher matches a patch by, apart from the old value.
type target struct {
	component
	path       string
	kind       string
	occurrence int
}

func (l Location) component() component { return component{l.Owner, l.Script} }

func (l Location) target() target { return target{l.component(), l.Path, l.Kind, l.Occurrence} }

// Build groups the strings found in a game into dictionary entries, one
// per distinct old value, sorted by it. Each found patch describes one
// string; its New is ignored.
func Build(found []patch.Patch) []Entry {
	byOld := map[string]*Entry{}
	for _, p := range found {
		e, ok := byOld[p.Old]
		if !ok {
			e = &Entry{Old: p.Old}
			byOld[p.Old] = e
		}
		e.FoundIn = append(e.FoundIn, Location{File: p.File, Path: p.Path, Owner: p.Owner, Script: p.Script, Occurrence: p.Occurrence, Kind: p.Kind})
	}
	entries := make([]Entry, 0, len(byOld))
	for _, e := range byOld {
		slices.SortFunc(e.FoundIn, compareLocations)
		e.FoundIn = slices.Compact(e.FoundIn)
		entries = append(entries, *e)
	}
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Old, b.Old) })
	return entries
}

func compareLocations(a, b Location) int {
	return cmp.Or(
		cmp.Compare(a.File, b.File),
		cmp.Compare(a.Owner, b.Owner),
		cmp.Compare(a.Path, b.Path),
		compareScripts(a.Script, b.Script),
		cmp.Compare(a.Occurrence, b.Occurrence),
		cmp.Compare(a.Kind, b.Kind),
	)
}

func compareScripts(a, b patch.Script) int {
	return cmp.Or(cmp.Compare(a.Assembly, b.Assembly), cmp.Compare(a.FileID, b.FileID), cmp.Compare(a.Class, b.Class))
}

// Patches turns the translated entries into journal records, in entry
// order.
//
// The patcher applies a patch to every component of the owner and script
// that holds the old value in the field at the path, in any file, so
// locations that differ only by file yield a single patch. Locations at
// different paths yield one patch each: they are usually different
// components of one GameObject, such as two FSMs. Without script layouts
// the patcher matches by occurrence instead and skips a patch whose
// component an equal patch with another path has already changed. Within
// an entry, higher occurrences go first: replacing an earlier one would
// renumber the rest.
//
// A translation that equals the old value of another translated entry at
// the same owner and script is rejected: the second entry would also
// replace the text the first one has just written.
func Patches(entries []Entry) ([]patch.Patch, error) {
	components := map[string]map[component]bool{}
	for _, e := range entries {
		if !e.translated() {
			continue
		}
		set := map[component]bool{}
		for _, l := range e.FoundIn {
			set[l.component()] = true
		}
		components[e.Old] = set
	}
	var out []patch.Patch
	for _, e := range entries {
		if !e.translated() {
			continue
		}
		for _, l := range e.FoundIn {
			if components[e.New][l.component()] {
				return nil, fmt.Errorf("%w: translation of %q equals old value %q of another entry at owner %q", ErrFormat, e.Old, e.New, l.Owner)
			}
		}
		locs := slices.Clone(e.FoundIn)
		slices.SortStableFunc(locs, func(a, b Location) int {
			return cmp.Or(
				cmp.Compare(b.Occurrence, a.Occurrence),
				cmp.Compare(a.Owner, b.Owner),
				compareScripts(a.Script, b.Script),
				cmp.Compare(a.File, b.File),
				cmp.Compare(a.Path, b.Path),
				cmp.Compare(a.Kind, b.Kind),
			)
		})
		seen := map[target]bool{}
		for _, l := range locs {
			if seen[l.target()] {
				continue
			}
			seen[l.target()] = true
			out = append(out, patch.Patch{
				File: l.File, Path: l.Path, Owner: l.Owner, Script: l.Script,
				Occurrence: l.Occurrence, Kind: l.Kind, Old: e.Old, New: e.New,
			})
		}
	}
	return out, nil
}
