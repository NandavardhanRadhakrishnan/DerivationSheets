// Package sources resolves references to files in other teams' repositories.
// References read "alias:path". An alias is looked up in the flow's own
// [sources], then in the [sources] of the dsheets.toml at the repository
// root, then in the registry that file names:
//
//	registry = { git = "https://example.com/dsheets-registry.git" }
//
//	[sources]
//	shipping = { git = "https://example.com/shipping-service.git", ref = "v3.2.0", dir = "sheets" }
//	orders   = { path = "../../orders-service" }
//
// A reference without an alias is a path relative to the flow folder. Git
// sources follow their branch (the default one without ref) unless ref pins a
// tag or commit, and are fetched shallow and sparse into a git-ignored cache
// at the root of the repository where the command runs.
package sources

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CacheDir is the cache folder, relative to the repository root.
const CacheDir = ".dsheets/sources"

// Source is one declared source.
type Source struct {
	Alias string
	Git   string // clone URL
	Ref   string // branch to follow, or tag or commit to pin; "" = the default branch
	Dir   string // folder inside the repository that the alias means; "" = the root
	Path  string // local folder; relative to the flow folder in a flow's [sources]
}

// Resolver turns references into local file paths.
type Resolver struct {
	// Base is the folder that relative references and a flow's local sources
	// are resolved against: the flow folder.
	Base string
	// Sources are the flow's own [sources].
	Sources map[string]Source
	// Project is the repository's dsheets.toml; nil when there is none.
	Project *Project
	Fetcher *Fetcher
	// Warnings collects advice found while resolving, such as a plain path
	// into a submodule.
	Warnings []string

	registry    map[string]Source
	registryErr error
	registryRan bool
	resolved    map[string]string // alias -> local root
	revs        map[string]string // alias -> commit
}

// NewResolver prepares a resolver for a flow folder, reading the dsheets.toml
// of the repository the folder is in. A nil fetcher clones into that
// repository's cache.
func NewResolver(base string, flowSources map[string]Source, f *Fetcher) *Resolver {
	root := RepoRoot(base)
	r := &Resolver{Base: base, Sources: flowSources, Fetcher: f,
		resolved: map[string]string{}, revs: map[string]string{}}
	if r.Fetcher == nil {
		r.Fetcher = NewFetcher(filepath.Join(root, CacheDir))
	}
	p, err := LoadProject(root)
	if err != nil {
		r.Warnings = append(r.Warnings, err.Error())
	}
	r.Project = p
	return r
}

// Split separates "alias:path" into its parts. Windows drive letters such as
// "C:\x" are not aliases.
func Split(ref string) (alias, path string) {
	i := strings.Index(ref, ":")
	drive := i == 1 && len(ref) > 2 && (ref[2] == '\\' || ref[2] == '/')
	if i < 1 || drive || strings.ContainsAny(ref[:i], `/\.`) {
		return "", ref
	}
	return ref[:i], ref[i+1:]
}

// Resolve returns the local path of a reference, fetching its source when
// needed, and the source revision for git sources.
func (r *Resolver) Resolve(ref string) (path, revision string, err error) {
	alias, rel := Split(ref)
	if alias == "" {
		path = join(r.Base, rel)
		r.checkSubmodule(rel, path)
		return path, "", nil
	}
	root, err := r.root(alias)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(root, filepath.FromSlash(rel)), r.revs[alias], nil
}

// checkSubmodule warns when a plain path reads a folder of one of this
// repository's submodules: that is the commit the submodule records, not
// the branch, and impact cannot see it as a reference to that team.
func (r *Resolver) checkSubmodule(ref, path string) {
	if r.Project == nil {
		return
	}
	name, rel, ok := r.Project.submoduleOf(path)
	if !ok {
		return
	}
	w := fmt.Sprintf("%s is inside submodule %s, which reads its recorded commit; refer to it as %s:%s", ref, name, name, rel)
	for _, x := range r.Warnings {
		if x == w {
			return
		}
	}
	r.Warnings = append(r.Warnings, w)
}

// Lookup returns the source an alias names: the flow's [sources] first, then
// the repository's, then the registry. Only the registry is fetched.
func (r *Resolver) Lookup(alias string) (Source, error) {
	if src, ok := r.Sources[alias]; ok {
		src.Alias = alias
		if src.Path != "" {
			src.Path = join(r.Base, src.Path)
		}
		return src, nil
	}
	if r.Project != nil {
		if src, ok := r.Project.Sources[alias]; ok {
			return src, nil
		}
	}
	reg, err := r.Registered()
	if err != nil {
		return Source{}, err
	}
	if src, ok := reg[alias]; ok {
		return src, nil
	}
	return Source{}, fmt.Errorf("unknown source %q; declare it under [sources] or add it to the registry", alias)
}

// Registered returns every registry entry by alias, or nil when the
// repository names no registry.
func (r *Resolver) Registered() (map[string]Source, error) {
	if r.registryRan {
		return r.registry, r.registryErr
	}
	r.registryRan = true
	if r.Project == nil || r.Project.Registry == nil {
		return nil, nil
	}
	reg := *r.Project.Registry
	root, base := reg.Path, reg.Path
	if root == "" {
		var err error
		if root, _, err = r.Fetcher.Fetch(reg); err != nil {
			r.registryErr = fmt.Errorf("registry: %v", err)
			return nil, r.registryErr
		}
		base = reg.Git
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitmodules"))
	if err != nil {
		r.registryErr = fmt.Errorf("registry %s has no .gitmodules: %v", base, err)
		return nil, r.registryErr
	}
	r.registry, r.registryErr = ParseGitmodules(string(data), base)
	if r.registryErr != nil {
		r.registryErr = fmt.Errorf("registry %s: %v", base, r.registryErr)
	}
	return r.registry, r.registryErr
}

func (r *Resolver) root(alias string) (string, error) {
	if root, ok := r.resolved[alias]; ok {
		return root, nil
	}
	src, err := r.Lookup(alias)
	if err != nil {
		return "", err
	}
	var root string
	switch {
	case src.Path != "":
		root = filepath.Join(src.Path, filepath.FromSlash(src.Dir))
		if _, err := os.Stat(root); err != nil {
			return "", fmt.Errorf("source %q: %v", alias, err)
		}
	case src.Git != "":
		var rev string
		if root, rev, err = r.Fetcher.Fetch(src); err != nil {
			return "", fmt.Errorf("source %q: %v", alias, err)
		}
		r.revs[alias] = rev
	default:
		return "", fmt.Errorf("source %q needs git or path", alias)
	}
	r.resolved[alias] = root
	return root, nil
}

// Fetched lists the resolved revision of each git source that was used.
func (r *Resolver) Fetched() map[string]string { return r.revs }

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// RepoRoot is the nearest folder at or above dir holding .git, or dir.
func RepoRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return abs
		}
	}
}

// join resolves p against base unless p is already absolute, as exported
// flows on another drive refer to interfaces by absolute path.
func join(base, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
