// Package sources resolves references to files in other teams' repositories.
// A flow declares sources by alias:
//
//	[sources]
//	shipping = { git = "https://example.com/shipping-contracts.git", ref = "v3.2.0" }
//	orders   = { path = "../../orders-contracts" }
//
// and refers to their files as "shipping:interfaces/quote.toml". A reference
// without an alias is a path relative to the flow folder. Git sources are
// cloned into a git-ignored cache at the repository root, pinned to ref.
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
	Path  string // local folder, relative to the flow folder
}

// Resolver turns references into local file paths.
type Resolver struct {
	// Base is the folder that relative references and local sources are
	// resolved against: the flow folder.
	Base    string
	Sources map[string]Source
	// Cache is the folder git sources are cloned into.
	Cache string
	// Offline forbids network access; missing clones are an error.
	Offline bool
	// Refresh fetches git sources even when a clone exists.
	Refresh bool

	fetcher  *Fetcher
	resolved map[string]string // alias -> local root
	revs     map[string]string // alias -> commit
}

// NewResolver prepares a resolver for a flow folder.
func NewResolver(base string, srcs map[string]Source) *Resolver {
	return &Resolver{Base: base, Sources: srcs, Cache: filepath.Join(repoRoot(base), CacheDir),
		resolved: map[string]string{}, revs: map[string]string{}}
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
		return join(r.Base, rel), "", nil
	}
	root, err := r.root(alias)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(root, filepath.FromSlash(rel)), r.revs[alias], nil
}

func (r *Resolver) root(alias string) (string, error) {
	if root, ok := r.resolved[alias]; ok {
		return root, nil
	}
	src, ok := r.Sources[alias]
	if !ok {
		return "", fmt.Errorf("unknown source %q; declare it under [sources]", alias)
	}
	var root string
	switch {
	case src.Path != "":
		root = join(r.Base, src.Path)
		if _, err := os.Stat(root); err != nil {
			return "", fmt.Errorf("source %q: %v", alias, err)
		}
	case src.Git != "":
		if r.fetcher == nil {
			r.fetcher = NewFetcher(r.Cache)
			r.fetcher.Offline = r.Offline
		}
		var err error
		var rev string
		if root, rev, err = r.fetcher.Fetch(src); err != nil {
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

// repoRoot is the nearest folder at or above dir holding .git, or dir.
func repoRoot(dir string) string {
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
