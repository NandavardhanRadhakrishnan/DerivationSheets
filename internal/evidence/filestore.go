package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
)

// FileStore keeps one JSON file per run under <Root>/<sheet>/.
type FileStore struct {
	Root string
}

// DefaultRoot finds the repository root above dir (the nearest folder with
// .git) and returns its evidence folder. Without a repository it uses the
// current directory.
func DefaultRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err == nil {
		for d := abs; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
				return filepath.Join(d, brand.EvidenceDir)
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	return brand.EvidenceDir
}

func (f *FileStore) dir(sheet string) string { return filepath.Join(f.Root, sheet) }

// Save writes <Root>/<sheet>/v<version>-<id>.json.
func (f *FileStore) Save(run *Run) (string, error) {
	if err := os.MkdirAll(f.dir(run.Sheet), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(f.dir(run.Sheet), fmt.Sprintf("v%d-%s.json", run.Version, run.ID))
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

// Latest returns the newest run for the version. Run IDs start with a UTC
// timestamp, so the last file name in sorted order is the newest.
func (f *FileStore) Latest(sheet string, version int) (*Run, error) {
	entries, err := os.ReadDir(f.dir(sheet))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("v%d-", version)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	data, err := os.ReadFile(filepath.Join(f.dir(sheet), names[len(names)-1]))
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("%s: %v", names[len(names)-1], err)
	}
	return &run, nil
}
