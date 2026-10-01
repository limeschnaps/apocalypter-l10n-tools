// Package gamepatch replays a patch journal against the serialized files
// of a Unity player build packed in a UnityFS bundle.
package gamepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"

	"apocalypter-l10n-tools/internal/md4"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unityfs"
)

// nodeFlagSerialized marks bundle nodes that hold serialized files.
const nodeFlagSerialized = 4

// ErrNoMatch reports a patch that matches nothing in the build.
var ErrNoMatch = errors.New("patch matches no object")

// ErrAmbiguous reports a patch that matches several objects in strict mode.
var ErrAmbiguous = errors.New("patch matches several objects")

// Target is one object a patch changed.
type Target struct {
	File   string
	PathID int64
}

// Result describes the outcome of one patch.
type Result struct {
	Patch   patch.Patch
	Targets []Target
	Err     error
}

// Options controls matching.
type Options struct {
	// Strict requires every patch to match exactly one object. Otherwise a
	// patch applies to every matching object: a prefab's text usually also
	// lives in baked copies inside scenes.
	Strict bool
	// Fonts replaces the TTF data of Font objects by name.
	Fonts []FontReplacement
}

// Report collects the outcome of Apply.
type Report struct {
	Patches []Result
	Fonts   []FontResult
}

// Component is a MonoBehaviour of the build.
type Component struct {
	File   string
	PathID int64
	// Owner is the GameObject name, or m_Name of a component without one.
	Owner  string
	Script serialized.MonoScript
	// FileID is the fileID Unity derives from the script class name; zero
	// when the script is unresolved.
	FileID int64
}

type state struct {
	bundle     *unityfs.Bundle
	files      map[string]*serialized.File
	current    map[string]map[int64][]byte
	byOwner    map[string][]*Component
	scriptByID map[string]map[int64]serialized.MonoScript
}

// Apply replays patches in order, then applies the font replacements, and
// returns a report and the new content of every changed bundle node. Any
// failure makes the whole run fail with a joined error and no nodes; the
// report still describes everything that was attempted.
func Apply(b *unityfs.Bundle, patches []patch.Patch, opts Options) (Report, map[string][]byte, error) {
	var rep Report
	st, err := load(b)
	if err != nil {
		return rep, nil, err
	}
	var errs []error
	for i, p := range patches {
		res := st.apply(p, opts)
		if res.Err != nil {
			errs = append(errs, fmt.Errorf("patch %d (%s %s): %w", i+1, p.File, p.Path, res.Err))
		}
		rep.Patches = append(rep.Patches, res)
	}
	for _, r := range opts.Fonts {
		res, err := st.replaceFont(r)
		if err != nil {
			errs = append(errs, err)
		}
		rep.Fonts = append(rep.Fonts, res)
	}
	if err := errors.Join(errs...); err != nil {
		return rep, nil, err
	}

	nodes := map[string][]byte{}
	for name, objects := range st.current {
		out, err := st.files[name].Rewrite(objects)
		if err != nil {
			return rep, nil, fmt.Errorf("rewrite %s: %w", name, err)
		}
		nodes[name] = out
	}
	return rep, nodes, nil
}

func load(b *unityfs.Bundle) (*state, error) {
	st := &state{
		bundle:     b,
		files:      map[string]*serialized.File{},
		current:    map[string]map[int64][]byte{},
		byOwner:    map[string][]*Component{},
		scriptByID: map[string]map[int64]serialized.MonoScript{},
	}
	for _, n := range b.Nodes {
		if n.Flags&nodeFlagSerialized == 0 {
			continue
		}
		data, err := b.ReadNode(n)
		if err != nil {
			return nil, err
		}
		f, err := serialized.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Path, err)
		}
		st.files[n.Path] = f
	}
	for name, f := range st.files {
		if err := st.indexScripts(name, f); err != nil {
			return nil, err
		}
	}
	for name, f := range st.files {
		if err := st.indexComponents(name, f); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func (st *state) indexScripts(name string, f *serialized.File) error {
	scripts := map[int64]serialized.MonoScript{}
	for _, o := range f.Objects {
		if o.ClassID != serialized.ClassMonoScript {
			continue
		}
		s, err := serialized.ReadMonoScript(f.Data(o), f.ByteOrder())
		if err != nil {
			return fmt.Errorf("%s MonoScript %d: %w", name, o.PathID, err)
		}
		scripts[o.PathID] = s
	}
	st.scriptByID[name] = scripts
	return nil
}

func (st *state) indexComponents(name string, f *serialized.File) error {
	for _, o := range f.Objects {
		if o.ClassID != serialized.ClassMonoBehaviour {
			continue
		}
		h, err := serialized.ReadMonoBehaviourHeader(f.Data(o), f.ByteOrder())
		if err != nil {
			return fmt.Errorf("%s MonoBehaviour %d: %w", name, o.PathID, err)
		}
		owner := h.Name
		if h.GameObject.PathID != 0 {
			if owner, err = st.gameObjectName(name, f, h.GameObject); err != nil {
				return fmt.Errorf("%s MonoBehaviour %d: %w", name, o.PathID, err)
			}
		}
		c := &Component{File: name, PathID: o.PathID, Owner: owner}
		if s, ok := st.resolveScript(name, f, h.Script); ok {
			c.Script = s
			c.FileID = ScriptFileID(s.Namespace, s.ClassName)
		}
		st.byOwner[owner] = append(st.byOwner[owner], c)
	}
	return nil
}

func (st *state) gameObjectName(name string, f *serialized.File, ref serialized.PPtr) (string, error) {
	file, fname, ok := st.external(name, f, ref.FileID)
	if !ok {
		return "", nil
	}
	o, ok := file.Object(ref.PathID)
	if !ok || o.ClassID != serialized.ClassGameObject {
		return "", fmt.Errorf("missing GameObject %s:%d", fname, ref.PathID)
	}
	return serialized.ReadGameObjectName(file.Data(o), file.ByteOrder())
}

func (st *state) resolveScript(name string, f *serialized.File, ref serialized.PPtr) (serialized.MonoScript, bool) {
	_, fname, ok := st.external(name, f, ref.FileID)
	if !ok {
		return serialized.MonoScript{}, false
	}
	s, ok := st.scriptByID[fname][ref.PathID]
	return s, ok
}

// external resolves a PPtr file index to a serialized file of the bundle.
// Externals are matched by base name because the paths stored in them
// ("archive:/...", "Library/...") differ from bundle node names.
func (st *state) external(name string, f *serialized.File, fileID int32) (*serialized.File, string, bool) {
	if fileID == 0 {
		return f, name, true
	}
	if int(fileID) > len(f.Externals) || fileID < 0 {
		return nil, "", false
	}
	base := path.Base(f.Externals[fileID-1].Path)
	for n, file := range st.files {
		if path.Base(n) == base {
			return file, n, true
		}
	}
	return nil, "", false
}

func (st *state) apply(p patch.Patch, opts Options) Result {
	res := Result{Patch: p}
	var matches []*Component
	var positions []int
	for _, c := range st.byOwner[p.Owner] {
		if !scriptMatches(p.Script, c) {
			continue
		}
		needle := serialized.EncodeString(p.Old, st.files[c.File].ByteOrder())
		pos := findOccurrence(st.data(c), needle, p.Occurrence)
		if pos < 0 {
			continue
		}
		matches = append(matches, c)
		positions = append(positions, pos)
	}
	switch {
	case len(matches) == 0:
		res.Err = ErrNoMatch
		return res
	case opts.Strict && len(matches) > 1:
		res.Err = fmt.Errorf("%w: %d objects", ErrAmbiguous, len(matches))
		return res
	}
	for i, c := range matches {
		f := st.files[c.File]
		old := st.data(c)
		oldLen := len(serialized.EncodeString(p.Old, f.ByteOrder()))
		updated := slices.Concat(old[:positions[i]], serialized.EncodeString(p.New, f.ByteOrder()), old[positions[i]+oldLen:])
		if _, err := serialized.ReadMonoBehaviourHeader(updated, f.ByteOrder()); err != nil {
			res.Err = err
			return res
		}
		st.set(c.File, c.PathID, updated)
		res.Targets = append(res.Targets, Target{File: c.File, PathID: c.PathID})
	}
	return res
}

func (st *state) data(c *Component) []byte {
	return st.objectData(c.File, c.PathID)
}

// objectData returns the current bytes of an object, including earlier
// changes made during this run.
func (st *state) objectData(file string, pathID int64) []byte {
	if d, ok := st.current[file][pathID]; ok {
		return d
	}
	f := st.files[file]
	o, _ := f.Object(pathID)
	return f.Data(o)
}

func (st *state) set(file string, pathID int64, data []byte) {
	if st.current[file] == nil {
		st.current[file] = map[int64][]byte{}
	}
	st.current[file][pathID] = data
}

func scriptMatches(s patch.Script, c *Component) bool {
	switch {
	case s.Assembly != "":
		return c.Script.AssemblyName == s.Assembly && c.FileID == s.FileID
	case s.Class != "":
		return c.Script.ClassName == s.Class
	default:
		return true
	}
}

// findOccurrence returns the offset of the n-th (0-based) occurrence of
// needle in the component data after the fixed PPtr fields, or -1. The
// search starts at m_Name, which the YAML field numbering also counts.
func findOccurrence(data, needle []byte, n int) int {
	const nameOffset = 28
	if len(data) < nameOffset || n < 0 {
		return -1
	}
	from := nameOffset
	for {
		i := bytes.Index(data[from:], needle)
		if i < 0 {
			return -1
		}
		if n == 0 {
			return from + i
		}
		n--
		from += i + 1
	}
}

// ScriptFileID returns the fileID Unity assigns to a script class compiled
// into a DLL: the first four bytes of MD4("s\0\0\0" + namespace + class)
// read as a little-endian int32.
func ScriptFileID(namespace, class string) int64 {
	sum := md4.Sum([]byte("s\x00\x00\x00" + namespace + class))
	return int64(int32(binary.LittleEndian.Uint32(sum[:4])))
}

// Write writes the patched bundle to w.
func Write(w io.Writer, b *unityfs.Bundle, nodes map[string][]byte) error {
	if _, err := b.WriteTo(w, nodes); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	return nil
}
