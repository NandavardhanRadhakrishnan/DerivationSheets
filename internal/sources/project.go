package sources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ProjectFile is the repository settings file. At the repository root it can
// name a registry and declare [sources] for every flow in the repository.
const ProjectFile = "dsheets.toml"

// Project is what the repository root says about other repositories.
type Project struct {
	Root string
	// Registry lists every team repository; nil without one. A local
	// registry (path = ".") has an absolute Path.
	Registry *Source
	// Sources are aliases for every flow in the repository; Path is absolute.
	Sources map[string]Source
	// Submodules are this repository's own submodules, by name, so plain
	// paths into them can be pointed at the alias instead.
	Submodules map[string]Submodule
}

// Submodule is one of a repository's own submodules.
type Submodule struct {
	Path string // absolute folder of the submodule
	Dir  string // its dsheets folder, if the .gitmodules entry names one
}

// LoadProject reads the dsheets.toml and .gitmodules at a repository root.
// Either may be missing.
func LoadProject(root string) (*Project, error) {
	p := &Project{Root: root, Sources: map[string]Source{}, Submodules: map[string]Submodule{}}
	file := filepath.Join(root, ProjectFile)
	data, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var raw map[string]any
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("%s: %v", file, err)
		}
		if v, ok := raw["registry"]; ok {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: registry must be a table such as { git = \"…\" } or { path = \".\" }", file)
			}
			if err := knownKeys(file, "registry", m, "git", "ref", "path"); err != nil {
				return nil, err
			}
			src := sourceFrom("registry", m, root)
			if src.Git == "" && src.Path == "" {
				return nil, fmt.Errorf("%s: registry needs git or path", file)
			}
			p.Registry = &src
		}
		if v, ok := raw["sources"]; ok {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: [sources] must be a table of aliases", file)
			}
			for alias, sv := range m {
				sm, ok := sv.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s: sources.%s must be a table such as { git = \"…\" }", file, alias)
				}
				if err := knownKeys(file, "sources."+alias, sm, "git", "ref", "dir", "path"); err != nil {
					return nil, err
				}
				p.Sources[alias] = sourceFrom(alias, sm, root)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, ".gitmodules")); err == nil {
		for _, m := range parseModules(string(data)) {
			if m.path != "" {
				p.Submodules[m.name] = Submodule{Path: filepath.Join(root, filepath.FromSlash(m.path)), Dir: m.dir}
			}
		}
	}
	return p, nil
}

// knownKeys rejects keys a source does not take, so a typo such as
// branch for ref is not silently ignored.
func knownKeys(file, where string, m map[string]any, keys ...string) error {
	for k := range m {
		ok := false
		for _, want := range keys {
			ok = ok || k == want
		}
		if !ok {
			hint := ""
			if k == "branch" {
				hint = "; the branch to follow goes in ref"
			}
			return fmt.Errorf("%s: %s.%s: unknown key; expected %s%s", file, where, k, strings.Join(keys, ", "), hint)
		}
	}
	return nil
}

// SourceFrom reads one [sources] entry. A relative path is resolved against
// base.
func sourceFrom(alias string, m map[string]any, base string) Source {
	s := Source{Alias: alias}
	s.Git, _ = m["git"].(string)
	s.Ref, _ = m["ref"].(string)
	s.Dir, _ = m["dir"].(string)
	if p, _ := m["path"].(string); p != "" {
		s.Path = join(base, p)
	}
	return s
}

// submoduleOf finds the submodule a path lies in, returning its name and the
// path relative to its dsheets folder.
func (p *Project) submoduleOf(path string) (name, rel string, ok bool) {
	names := make([]string, 0, len(p.Submodules))
	for n := range p.Submodules {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sm := p.Submodules[n]
		base := filepath.Join(sm.Path, filepath.FromSlash(sm.Dir))
		r, err := filepath.Rel(base, path)
		if err == nil && r != ".." && !filepath.IsAbs(r) && !startsUp(r) {
			return n, filepath.ToSlash(r), true
		}
	}
	return "", "", false
}

func startsUp(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && os.IsPathSeparator(rel[2])
}
