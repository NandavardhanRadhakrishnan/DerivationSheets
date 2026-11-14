package requirements

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/diff"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
)

// Options say which flow change to read.
type Options struct {
	// Flow is the flow's folder, inside a git repository.
	Flow string
	// Dir is a folder in the repository to compare, the current one by
	// default; it is only used to find the repository.
	Dir string
	// Base and Head are revisions, as in diff.Options. An empty Head means the
	// working tree; an empty Base the merge-base of the default base and HEAD.
	Base, Head string
	// As is this repository's alias in the registry, used to find the default
	// base.
	As string
	// All includes label and formatting changes.
	All     bool
	Fetcher *sources.Fetcher
}

// Result is the requirements of one flow change.
type Result struct {
	Flow         string        `json:"flow"` // the flow's name
	Base         string        `json:"base"` // the commits compared, abbreviated
	Head         string        `json:"head"`
	Requirements []Requirement `json:"requirements"`
}

// Run compares the flow at the base revision with the flow at head, which is
// the working tree unless o.Head names a revision.
func Run(o Options) (*Result, error) {
	dir := o.Dir
	if dir == "" {
		dir = o.Flow
	}
	revs, err := store.NewRevs(dir)
	if err != nil {
		return nil, err
	}
	base, head, err := diff.ResolveRange(revs, diff.Options{Base: o.Base, Head: o.Head, As: o.As, Fetcher: o.Fetcher})
	if err != nil {
		return nil, err
	}
	top := revs.Top()
	if rel, err := filepath.Rel(filepath.Join(top, filepath.FromSlash(sources.CacheDir)), o.Flow); err == nil && !strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("%s is a fetched copy of another repository; run the command in the repository that holds the flow", o.Flow)
	}
	flowAbs, err := filepath.Abs(o.Flow)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(top, flowAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("%s is not inside the repository %s", o.Flow, top)
	}
	fe := o.Fetcher
	if fe == nil {
		fe = sources.NewFetcher(filepath.Join(top, sources.CacheDir))
	}
	origin := originURL(top)

	headRoot := top
	if o.Head != "" {
		tmp, err := exportTo(revs, head)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		headRoot = tmp
	}
	headA, err := analyseIn(headRoot, rel, origin, fe)
	if err != nil {
		return nil, fmt.Errorf("the flow at head: %w", err)
	}
	var baseA *flow.Analysis
	tmp, err := exportTo(revs, base)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(rel), flow.FileName)); err == nil {
		if baseA, err = analyseIn(tmp, rel, origin, fe); err != nil {
			return nil, fmt.Errorf("the flow at the base: %w", err)
		}
	}

	rs, err := Compare(baseA, headA, o.All)
	if err != nil {
		return nil, err
	}
	locate(rs, headA, fe, top, headRoot)
	return &Result{Flow: headA.Flow.Slug, Base: short(base), Head: short(head), Requirements: rs}, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func exportTo(revs *store.Revs, rev string) (string, error) {
	tmp, err := os.MkdirTemp("", "dsheets-rev-")
	if err != nil {
		return "", err
	}
	if err := revs.Export(rev, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	// The tree stands in for a repository: finding a repository's root, and
	// with it its dsheets.toml and registry, looks for a .git folder.
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return tmp, nil
}

// analyseIn analyses the flow at rel under root. A repository's own aliases
// read root instead of fetching their followed branch, as impact does, so
// both sides of the comparison see this repository as it is on that side.
func analyseIn(root, rel, origin string, fe *sources.Fetcher) (*flow.Analysis, error) {
	if origin != "" {
		fe = fe.WithSubstitute(&sources.Substitute{URL: origin, Root: root})
	}
	f, err := flow.Load(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	return flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, fe)), nil
}

func originURL(root string) string {
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// locate gives each Schema requirement the repository that owns its
// interface file, by its registry alias when it has one, and the file's path
// inside that repository.
//
// headRoot is where the head was read from: top, or an export of a revision
// of the repository at top, whose files belong to that repository.
func locate(rs []Requirement, a *flow.Analysis, fe *sources.Fetcher, top, headRoot string) {
	var reg map[string]sources.Source
	for i := range rs {
		if rs[i].Kind != Schema {
			continue
		}
		if reg == nil {
			reg, _ = sources.NewResolver(top, nil, fe).Registered()
		}
		root := sources.RepoRoot(filepath.Dir(rs[i].File))
		if r, err := filepath.Rel(headRoot, rs[i].File); err == nil && !strings.HasPrefix(r, "..") {
			root = top
			rs[i].File = filepath.Join(top, r) // so the rest reads it as a file of top
		}
		rs[i].Repo = filepath.Base(root)
		if url := originURL(root); url != "" {
			for alias, src := range reg {
				if sources.SameRepo(src.Git, url) {
					rs[i].Repo = alias
				}
			}
		}
		if p, err := filepath.Rel(root, rs[i].File); err == nil {
			rs[i].File = filepath.ToSlash(p)
		}
	}
}
