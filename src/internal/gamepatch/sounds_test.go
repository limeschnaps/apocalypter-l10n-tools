package gamepatch

import (
	"bytes"
	"errors"
	"testing"

	"apocalypter-l10n-tools/internal/serialized"
	"apocalypter-l10n-tools/internal/unitytest"
)

func ogg(rate int, samples int64) []byte {
	return unitytest.OggVorbis(unitytest.OggSpec{
		Channels: 1, Rate: rate, Samples: samples, Setup: []byte("books"),
		Packets: [][]byte{{0, 1}, {2, 3, 4}},
	})
}

func soundBundle(extra ...unitytest.Object) []byte {
	level0 := []unitytest.Object{
		{PathID: 5, ClassID: serialized.ClassAudioClip, Data: unitytest.AudioClip("shot_1", "sharedassets1.resource", 64, 100)},
		{PathID: 6, ClassID: serialized.ClassAudioClip, Data: unitytest.AudioClip("music", "sharedassets1.resource", 0, 64)},
	}
	return unitytest.Bundle([]unitytest.Node{
		{Path: "level0", Flags: nodeFlagSerialized, Data: unitytest.Serialized(append(level0, extra...), nil)},
		// A prefab copy of the clip in another file is replaced too.
		{Path: "level1", Flags: nodeFlagSerialized, Data: unitytest.Serialized([]unitytest.Object{
			{PathID: 9, ClassID: serialized.ClassAudioClip, Data: unitytest.AudioClip("shot_1", "sharedassets1.resource", 64, 100)},
		}, nil)},
	}, 256)
}

func readClip(t *testing.T, data []byte, file string, pathID int64) *serialized.AudioClip {
	t.Helper()
	b := openBundle(t, data)
	n, ok := b.Node(file)
	if !ok {
		t.Fatalf("no node %s", file)
	}
	raw, err := b.ReadNode(n)
	if err != nil {
		t.Fatal(err)
	}
	f, err := serialized.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := f.Object(pathID)
	c, err := serialized.ReadAudioClip(f.Data(o), f.ByteOrder())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReplaceSound(t *testing.T) {
	b := openBundle(t, soundBundle())
	sounds := []SoundReplacement{{Name: "shot_1", Data: ogg(22050, 11025)}, {Name: "music", Data: ogg(48000, 96000)}}
	rep, nodes, err := Apply(b, nil, Options{Sounds: sounds})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sounds) != 2 || len(rep.Sounds[0].Targets) != 2 || len(rep.Sounds[1].Targets) != 1 {
		t.Fatalf("report = %+v", rep.Sounds)
	}
	if r := rep.Sounds[0]; r.Channels != 1 || r.Rate != 22050 || r.Samples != 11025 {
		t.Errorf("shot_1 = %+v", r)
	}
	var out bytes.Buffer
	if err := Write(&out, b, nodes); err != nil {
		t.Fatal(err)
	}
	patched := openBundle(t, out.Bytes())
	node, ok := patched.Node(SoundsNode)
	if !ok || node.Flags != 0 {
		t.Fatalf("sounds node = %+v, %v", node, ok)
	}
	resource, err := patched.ReadNode(node)
	if err != nil {
		t.Fatal(err)
	}

	shot0, music := readClip(t, out.Bytes(), "level0", 5), readClip(t, out.Bytes(), "level0", 6)
	if shot1 := readClip(t, out.Bytes(), "level1", 9); *shot1 != *shot0 {
		t.Errorf("copies differ: %+v / %+v", shot0, shot1)
	}
	if shot0.Channels != 1 || shot0.Frequency != 22050 || shot0.Length != 0.5 || shot0.CompressionFormat != serialized.AudioCompressionVorbis {
		t.Errorf("shot_1 = %+v", shot0)
	}
	if !shot0.PreloadAudioData || !shot0.Legacy3D {
		t.Errorf("unrelated fields changed: %+v", shot0)
	}
	if music.Frequency != 48000 || music.Length != 2 {
		t.Errorf("music = %+v", music)
	}
	// Each clip points at its bank; banks start 32-aligned.
	for _, c := range []*serialized.AudioClip{shot0, music} {
		want, _, err := SoundBank(map[string][]byte{"shot_1": sounds[0].Data, "music": sounds[1].Data}[c.Name])
		if err != nil {
			t.Fatal(err)
		}
		r := c.Resource
		if r.Source != SoundsNode || r.Offset%soundAlign != 0 || r.Offset+r.Size > uint64(len(resource)) || !bytes.Equal(resource[r.Offset:r.Offset+r.Size], want) {
			t.Errorf("%s resource = %+v", c.Name, r)
		}
	}
	if shot0.Resource.Offset != 0 || music.Resource.Offset == 0 {
		t.Errorf("offsets = %d, %d", shot0.Resource.Offset, music.Resource.Offset)
	}

	// Without sounds no node is added.
	if _, nodes, err := Apply(openBundle(t, soundBundle()), nil, Options{}); err != nil || len(nodes) != 0 {
		t.Errorf("no sounds: %d nodes, %v", len(nodes), err)
	}
}

func TestReplaceSoundFailures(t *testing.T) {
	failures := map[string]struct {
		bundle []byte
		sound  SoundReplacement
		want   error
	}{
		"unknown name": {soundBundle(), SoundReplacement{Name: "nope", Data: ogg(22050, 1)}, ErrUnknownSound},
		"bad ogg":      {soundBundle(), SoundReplacement{Name: "shot_1", Data: []byte("junk")}, nil},
		"bad rate":     {soundBundle(), SoundReplacement{Name: "shot_1", Data: ogg(44000, 1)}, nil},
		"broken clip": {
			soundBundle(unitytest.Object{PathID: 7, ClassID: serialized.ClassAudioClip, Data: []byte{1, 2}}),
			SoundReplacement{Name: "shot_1", Data: ogg(22050, 1)}, serialized.ErrFormat,
		},
	}
	for name, c := range failures {
		_, nodes, err := Apply(openBundle(t, c.bundle), nil, Options{Sounds: []SoundReplacement{c.sound}})
		if err == nil || nodes != nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
