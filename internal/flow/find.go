package flow

import (
	"io/fs"
	"path/filepath"
	"sort"
)

// FindDirs finds every folder under root holding a flow.toml, skipping .git
// and .dsheets, sorted.
func FindDirs(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".dsheets") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == FileName {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(out)
	return out
}
