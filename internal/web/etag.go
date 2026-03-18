package web

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path/filepath"
)

// ComputeETags walks the filesystem and computes a quoted SHA-256 ETag
// for each file. the returned map is keyed by the file path relative
// to the root of fsys (e.g. "static/css/style.css").
func ComputeETags(fsys fs.FS) (map[string]string, error) {
	etags := make(map[string]string)

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		data, readErr := fs.ReadFile(fsys, path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}

		sum := sha256.Sum256(data)
		// use first 16 bytes for a shorter but still collision-resistant tag
		etags[filepath.ToSlash(path)] = fmt.Sprintf(`"%x"`, sum[:16])
		return nil
	})

	return etags, err
}
