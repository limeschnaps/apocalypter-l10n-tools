package gamepatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apocalypter-l10n-tools/internal/unityfs"
)

const (
	// BundleName is the bundle file inside the *_Data directory.
	BundleName = "data.unity3d"
	// BackupSuffix marks the copy of the original bundle that
	// "patcher -in-place" keeps next to the patched one.
	BackupSuffix = ".orig"
)

// BundlePath accepts either the bundle itself or the *_Data directory and
// returns the bundle path.
func BundlePath(p string) (string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("game path: %w", err)
	}
	if info.IsDir() {
		p = filepath.Join(p, BundleName)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%w: expected %s (builds with loose .assets files are not supported)", err, BundleName)
		}
	}
	return p, nil
}

// ErrStaleBackup reports a backup that no longer matches the game: the
// bundle was not produced from it, typically because the game was updated
// or its files were verified, which restores an unpatched bundle.
var ErrStaleBackup = errors.New("backup does not match the game bundle")

// PristinePath returns the backup of the original bundle when it exists,
// otherwise bundle itself. Patch journals always apply to the original.
//
// A backup is used only while the bundle is a patched copy of it. After a
// game update the backup holds the previous version, and patching it
// would silently roll the game back, so PristinePath fails with
// ErrStaleBackup instead.
func PristinePath(bundle string) (string, error) {
	backup := bundle + BackupSuffix
	switch _, err := os.Stat(backup); {
	case errors.Is(err, fs.ErrNotExist):
		return bundle, nil
	case err != nil:
		return "", fmt.Errorf("stat backup: %w", err)
	}
	derived, err := extendsBackup(bundle, backup)
	if err != nil {
		return "", err
	}
	if !derived {
		return "", fmt.Errorf("%w: %s was not produced from %s (game updated or files verified?); delete the backup and patch again", ErrStaleBackup, bundle, backup)
	}
	return backup, nil
}

func extendsBackup(bundle, backup string) (bool, error) {
	cur, closeCur, err := openBundleFile(bundle)
	if err != nil {
		return false, err
	}
	defer closeCur()
	orig, closeOrig, err := openBundleFile(backup)
	if err != nil {
		return false, err
	}
	defer closeOrig()
	return cur.ExtendsBlocks(orig)
}

func openBundleFile(path string) (*unityfs.Bundle, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open bundle: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("stat bundle: %w", err)
	}
	b, err := unityfs.Open(f, info.Size())
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, func() { _ = f.Close() }, nil
}
