package gamepatch

import (
	"bytes"
	"cmp"
	"slices"

	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unityfs"
)

// Build is a player build loaded into memory. Patches applied to it change
// only the in-memory copies of the objects; the bundle stays untouched.
type Build struct {
	st *state
}

// Load reads every serialized file of the bundle into memory.
func Load(b *unityfs.Bundle) (*Build, error) {
	st, err := load(b)
	if err != nil {
		return nil, err
	}
	return &Build{st: st}, nil
}

// Components returns all MonoBehaviours ordered by file and path ID.
func (b *Build) Components() []Component {
	var out []Component
	for _, list := range b.st.byOwner {
		for _, c := range list {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(x, y Component) int {
		return cmp.Or(cmp.Compare(x.File, y.File), cmp.Compare(x.PathID, y.PathID))
	})
	return out
}

// Scripts returns the number of MonoScripts in the build.
func (b *Build) Scripts() int {
	n := 0
	for _, s := range b.st.scriptByID {
		n += len(s)
	}
	return n
}

// Data returns the current bytes of a component, including patches
// applied to the build.
func (b *Build) Data(c Component) []byte {
	return b.st.data(&c)
}

// ByteOrder returns the byte order of a serialized file of the build.
func (b *Build) ByteOrder(file string) serialized.ByteOrder {
	return b.st.files[file].ByteOrder()
}

// Apply applies p to the in-memory objects the same way Apply patches a
// bundle: every matching component changes unless opts.Strict is set.
func (b *Build) Apply(p patch.Patch, opts Options) Result {
	return b.st.apply(p, opts)
}

// Occurrence returns the Patch.Occurrence that selects the string encoded
// at offset in the component data, or false when no such string is there.
// It counts matches the way the patcher searches for them.
func Occurrence(data []byte, offset int, value string, order serialized.ByteOrder) (int, bool) {
	needle := serialized.EncodeString(value, order)
	if offset < 0 || !bytes.HasPrefix(data[min(offset, len(data)):], needle) {
		return 0, false
	}
	for n := 0; ; n++ {
		switch pos := findOccurrence(data, needle, n); {
		case pos == offset:
			return n, true
		case pos < 0 || pos > offset:
			return 0, false
		}
	}
}
