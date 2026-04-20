package scan

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/3n3a/mediforge/internal/probe"
)

// Walk recursively scans root for files whose extensions are in exts,
// skipping hidden files (leading dot) so .original sidecars and
// .mediforge.tmp files don't get picked up.
func Walk(root string, exts []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if probe.HasMediaExtension(path, exts) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
