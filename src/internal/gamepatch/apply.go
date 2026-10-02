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
	"strings"

	"apocalypter-l10n-tools/internal/md4"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/scriptlayout"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/textkind"
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
	// Layouts decodes components by field path. A component whose layout
	// it resolves and decodes matches a patch only when the field at
	// Patch.Path holds Old and has the textkind Patch.Kind names: other
	// FSMs of the same GameObject often hold the same string as an object,
	// variable or event name. Without Layouts, and for components it
	// cannot decode, Patch.Occurrence selects the string instead.
	Layouts *scriptlayout.Resolver
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
	// replaced records the replacements made by occurrence, so a patch
	// that differs from an earlier one only by path does not replace the
	// next equal string of the component.
	replaced map[replacement]bool
	// decoded caches the fields of decoded components until they change.
	decoded map[objectKey]decoding
}

type objectKey struct {
	file   string
	pathID int64
}

// decoding is the outcome of decoding one component: ok is false when
// its layout is unknown or does not match the data.
type decoding struct {
	fields []field
	ok     bool
}

// field is a decoded string field and its textkind.
type field struct {
	scriptlayout.String
	kind textkind.Kind
}

// replacement is one string replaced by occurrence in one component.
type replacement struct {
	file       string
	pathID     int64
	occurrence int
	old, new   string
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
		replaced:   map[replacement]bool{},
		decoded:    map[objectKey]decoding{},
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
	var byOccurrence []bool
	covered := false
	for _, c := range st.byOwner[p.Owner] {
		if !scriptMatches(p.Script, c) {
			continue
		}
		pos, decoded := st.fieldOffset(c, p, opts.Layouts)
		if !decoded {
			key := replacement{c.File, c.PathID, p.Occurrence, p.Old, p.New}
			if st.replaced[key] {
				covered = true
				continue
			}
			needle := serialized.EncodeString(p.Old, st.files[c.File].ByteOrder())
			pos = findOccurrence(st.data(c), needle, p.Occurrence)
		}
		if pos < 0 {
			continue
		}
		matches = append(matches, c)
		positions = append(positions, pos)
		byOccurrence = append(byOccurrence, !decoded)
	}
	switch {
	case len(matches) == 0 && covered:
		return res
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
		if byOccurrence[i] {
			st.replaced[replacement{c.File, c.PathID, p.Occurrence, p.Old, p.New}] = true
		}
		res.Targets = append(res.Targets, Target{File: c.File, PathID: c.PathID})
	}
	return res
}

// heuristicPath prefixes the paths editor gives strings it found without
// a script layout; they name no field.
const heuristicPath = "str["

// namePath is m_Name, which precedes the fields a layout decodes.
const namePath = "m_Name"

// fieldOffset returns the offset of the string field of c at p.Path when
// it holds p.Old and has the kind p.Kind names, or -1. decoded is false
// when layouts cannot decode c or p names no field, and the caller must
// fall back to p.Occurrence.
func (st *state) fieldOffset(c *Component, p patch.Patch, layouts *scriptlayout.Resolver) (offset int, decoded bool) {
	if layouts == nil || p.Path == namePath || strings.HasPrefix(p.Path, heuristicPath) {
		return -1, false
	}
	d := st.decode(c, layouts)
	if !d.ok {
		return -1, false
	}
	for _, f := range d.fields {
		if f.Path != p.Path || f.Value != p.Old {
			continue
		}
		if p.Kind != "" && f.kind.String() != p.Kind {
			return -1, true
		}
		return f.Offset, true
	}
	return -1, true
}

// decode returns the string fields of c with their kinds, classified the
// way editor classifies them.
func (st *state) decode(c *Component, layouts *scriptlayout.Resolver) decoding {
	key := objectKey{c.File, c.PathID}
	if d, ok := st.decoded[key]; ok {
		return d
	}
	d := st.decodeFields(c, layouts)
	st.decoded[key] = d
	return d
}

func (st *state) decodeFields(c *Component, layouts *scriptlayout.Resolver) decoding {
	if c.Script.ClassName == "" {
		return decoding{}
	}
	layout, err := layouts.Script(c.Script.AssemblyName, c.Script.Namespace, c.Script.ClassName)
	if err != nil {
		return decoding{}
	}
	order := st.files[c.File].ByteOrder()
	data := st.data(c)
	h, err := serialized.ReadMonoBehaviourHeader(data, order)
	if err != nil {
		return decoding{}
	}
	needed := func(path string) bool { ok, _ := textkind.NumberField(path); return ok }
	strs, raw, err := scriptlayout.DecodeWith(layout, data, h.FieldsOffset, order, needed)
	if err != nil {
		return decoding{}
	}
	comp := textkind.Component{
		Script:  textkind.Script{Assembly: c.Script.AssemblyName, Namespace: c.Script.Namespace, Class: c.Script.ClassName},
		Strings: make([]textkind.Field, 0, len(strs)+1),
	}
	comp.Strings = append(comp.Strings, textkind.Field{Path: namePath, Value: h.Name})
	for _, s := range strs {
		comp.Strings = append(comp.Strings, textkind.Field{Path: s.Path, Value: s.Value})
	}
	if len(raw) > 0 {
		comp.Numbers = make(map[string][]int32, len(raw))
		for path, b := range raw {
			_, array := textkind.NumberField(path)
			comp.Numbers[path] = textkind.ParseRawNumber(b, array, order)
		}
	}
	kinds := textkind.Classify(comp)[1:]
	fields := make([]field, len(strs))
	for i, s := range strs {
		fields[i] = field{s, kinds[i]}
	}
	return decoding{fields: fields, ok: true}
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
	delete(st.decoded, objectKey{file, pathID})
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
