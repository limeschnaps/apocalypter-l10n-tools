// Package patch defines the journal of text edits that the editor records
// and the game patcher replays against the original build.
package patch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const journalVersion = 1

// ErrFormat reports an unreadable journal.
var ErrFormat = errors.New("patch: malformed journal")

// Script identifies the MonoScript of a component. Scripts compiled into
// a DLL are identified by assembly file name and the fileID Unity derives
// from the class name; scripts exported as .cs files by class name.
type Script struct {
	Assembly string `json:"assembly,omitempty"`
	FileID   int64  `json:"fileId,omitempty"`
	Class    string `json:"class,omitempty"`
}

// Patch replaces one string value inside a MonoBehaviour.
//
// The game build has no stable link to the exported YAML, so the target is
// found by content: a MonoBehaviour whose owner (GameObject name, or the
// component's own m_Name when it has no GameObject) and script match, and
// whose serialized strings contain Old. Occurrence selects among equal
// strings inside one component, in serialization order.
type Patch struct {
	File       string `json:"file"`
	Path       string `json:"path"`
	Owner      string `json:"owner"`
	Script     Script `json:"script"`
	Occurrence int    `json:"occurrence"`
	Old        string `json:"old"`
	New        string `json:"new"`
}

type journal struct {
	Version int     `json:"version"`
	Patches []Patch `json:"patches"`
}

// Load reads the patches in path. A missing file yields no patches.
func Load(path string) ([]Patch, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	return Parse(data)
}

// Parse decodes journal content.
func Parse(data []byte) ([]Patch, error) {
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if j.Version != journalVersion {
		return nil, fmt.Errorf("%w: version %d", ErrFormat, j.Version)
	}
	return j.Patches, nil
}

// Append adds p to the journal at path, creating it when needed.
func Append(path string, p Patch) error {
	patches, err := Load(path)
	if err != nil {
		return err
	}
	data, err := Marshal(append(patches, p))
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// Marshal encodes patches as journal content.
func Marshal(patches []Patch) ([]byte, error) {
	if patches == nil {
		patches = []Patch{}
	}
	data, err := json.MarshalIndent(journal{Version: journalVersion, Patches: patches}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode journal: %w", err)
	}
	return append(data, '\n'), nil
}

func writeAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp journal: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write journal: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close journal: %w", err)
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("chmod journal: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace journal: %w", err)
	}
	return nil
}
