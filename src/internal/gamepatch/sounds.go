package gamepatch

import (
	"errors"
	"fmt"

	"apocalypter-l10n-tools/internal/fsb"
	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/vorbis"
)

// SoundsNode is the bundle node that holds the sound banks of replaced
// AudioClips. The player resolves a resource path against the bundle
// before the *_Data directory, as it does for the bundle's .resS nodes.
const SoundsNode = "l10n.resource"

// soundAlign is the alignment of each bank in SoundsNode, the one Unity
// uses in its .resource files.
const soundAlign = 32

// ErrUnknownSound reports a sound replacement whose name matches no
// AudioClip.
var ErrUnknownSound = errors.New("no AudioClip object with this name")

// SoundReplacement swaps the audio of every AudioClip named Name for the
// Ogg Vorbis file in Data.
type SoundReplacement struct {
	Name string
	Data []byte
}

// SoundResult describes the outcome of one sound replacement.
type SoundResult struct {
	Name     string
	Channels int
	Rate     int
	Samples  int64
	Targets  []Target
}

// SoundBank converts an Ogg Vorbis file into the FSB5 bank an AudioClip
// stores and returns it with the parsed stream.
func SoundBank(data []byte) ([]byte, *vorbis.Stream, error) {
	s, err := vorbis.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	bank, err := fsb.Vorbis(s)
	if err != nil {
		return nil, nil, err
	}
	return bank, s, nil
}

// replaceSound appends the bank made from r.Data to the SoundsNode
// content and points every AudioClip named r.Name at it.
func (st *state) replaceSound(r SoundReplacement) (SoundResult, error) {
	res := SoundResult{Name: r.Name}
	bank, s, err := SoundBank(r.Data)
	if err != nil {
		return res, fmt.Errorf("sound %q: %w", r.Name, err)
	}
	res.Channels, res.Rate, res.Samples = s.Channels, s.Rate, s.Samples
	st.sounds = append(st.sounds, make([]byte, (soundAlign-len(st.sounds)%soundAlign)%soundAlign)...)
	resource := serialized.StreamedResource{Source: SoundsNode, Offset: uint64(len(st.sounds)), Size: uint64(len(bank))}
	st.sounds = append(st.sounds, bank...)
	for _, name := range st.fileNames() {
		f := st.files[name]
		for _, o := range f.Objects {
			if o.ClassID != serialized.ClassAudioClip {
				continue
			}
			clip, err := serialized.ReadAudioClip(st.objectData(name, o.PathID), f.ByteOrder())
			if err != nil {
				return res, fmt.Errorf("%s AudioClip %d: %w", name, o.PathID, err)
			}
			if clip.Name != r.Name {
				continue
			}
			clip.Channels, clip.Frequency, clip.BitsPerSample = int32(s.Channels), int32(s.Rate), 16
			clip.Length = float32(s.Samples) / float32(s.Rate)
			clip.IsTrackerFormat, clip.Ambisonic, clip.SubsoundIndex = false, false, 0
			clip.Resource = resource
			clip.CompressionFormat = serialized.AudioCompressionVorbis
			st.set(name, o.PathID, clip.Encode(f.ByteOrder()))
			res.Targets = append(res.Targets, Target{File: name, PathID: o.PathID})
		}
	}
	if len(res.Targets) == 0 {
		return res, fmt.Errorf("%q: %w", r.Name, ErrUnknownSound)
	}
	return res, nil
}
