// Package fsutil provides shared filesystem utilities.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// CleanEmptyDirs removes empty subdirectories under root (bottom-up).
// The root directory itself is not removed.
func CleanEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	slices.Reverse(dirs)
	for _, dir := range dirs {
		os.Remove(dir) // Only succeeds if empty.
	}
}
