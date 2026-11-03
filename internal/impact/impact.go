// Package impact finds the flows in other teams' repositories that a change
// to this repository breaks. Each flow that reads this repository is checked
// twice, against the base branch and against the working tree, and only
// what the change introduces is reported.
package impact

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// Options say what to check.
type Options struct {
	Root    string // the repository being changed (its working tree)
	As      string // its alias in the registry; "" = found from the origin remote
	Base    string // the branch to compare with; "" = the branch the registry follows
	Fetcher *sources.Fetcher
}

// Finding is one problem in a consuming flow.
type Finding struct {
	Repo      string `json:"repo"`      // the consuming repository's alias
	Flow      string `json:"flow"`      // the flow, as "alias:path"
	Interface string `json:"interface"` // interface name; "" for flow errors
	Owner     string `json:"owner"`
	Where     string `json:"where"` // "input", "output" or "stage <id>"
	Field     string `json:"field"`
	Status    string `json:"status"` // flow.Missing, flow.Warn, or "error"
	Message   string `json:"message"`
}

// Skipped is a repository or flow that could not be checked.
type Skipped struct {
	Repo   string `json:"repo"`
	Flow   string `json:"flow,omitempty"`
	Reason string `json:"reason"`
}

// Pinned is a flow that reads this repository at a tag or commit, so this
// change does not affect it until that team moves the pin.
type Pinned struct {
	Repo string `json:"repo"`
	Flow string `json:"flow"`
	Ref  string `json:"ref"`
}

// Report is the result of a check.
type Report struct {
	Self       string    `json:"self"`
	New        []Finding `json:"new"`
	Resolved   []Finding `json:"resolved"`
	Pinned     []Pinned  `json:"pinned"`
	NotChecked []Skipped `json:"not_checked"`
	// Failing are flows with errors before this change: only their errors
	// are compared, since their fields cannot be checked.
	Failing []Skipped `json:"failing"`
	Checked int       `json:"checked"`
	// Views are the affected flows, analysed with this change, by flow name.
	Views map[string]*flow.Analysis `json:"-"`
}

// ExitCode is 1 when the change breaks something, 3 when nothing new breaks
// but some consumers could not be checked, and 0 otherwise.
func (r *Report) ExitCode() int {
	switch {
	case len(r.New) > 0:
		return 1
	case len(r.NotChecked) > 0:
		return 3
	}
	return 0
}

// Status of a finding that is a flow error rather than a field.
const StatusError = "error"

// Run checks every flow in other registered repositories that reads this
// repository.
func Run(o Options) (*Report, error) {
	root := sources.RepoRoot(o.Root)
	res := sources.NewResolver(root, nil, o.Fetcher)
	reg, err := res.Registered()
	if err != nil {
		return nil, err
	}
	if reg == nil {
		return nil, fmt.Errorf("impact needs a registry; set registry in %s at the repository root", sources.ProjectFile)
	}
	self, err := findSelf(o, root, reg)
	if err != nil {
		return nil, err
	}
	selfSrc := reg[self]
	selfSrc.Dir = sources.CleanDir(selfSrc.Dir)
	r := &Report{Self: self, Views: map[string]*flow.Analysis{}}

	// The base branch of this repository, fetched like any other source.
	baseSrc := selfSrc
	if o.Base != "" {
		baseSrc.Ref = o.Base
	}
	baseDir, _, err := o.Fetcher.Fetch(baseSrc)
	if err != nil {
		return nil, fmt.Errorf("fetching %s at its base branch: %v", self, err)
	}
	baseRoot := strings.TrimSuffix(baseDir, filepath.FromSlash(selfSrc.Dir))
	before := o.Fetcher.WithSubstitute(&sources.Substitute{URL: selfSrc.Git, Root: filepath.Clean(baseRoot)})
	after := o.Fetcher.WithSubstitute(&sources.Substitute{URL: selfSrc.Git, Root: root})
	// Local roots in messages are replaced by the alias, so the same
	// problem reads the same before and after.
	paths := map[string]string{
		filepath.Join(root, filepath.FromSlash(selfSrc.Dir)): self + ":",
		baseDir: self + ":",
	}

	for _, repo := range consumers(res, reg, self) {
		repoRoot, _, err := o.Fetcher.Fetch(repo.src)
		if repo.local != "" {
			repoRoot, err = repo.local, nil
		}
		if err != nil {
			r.NotChecked = append(r.NotChecked, Skipped{Repo: repo.alias, Reason: err.Error()})
			continue
		}
		for _, dir := range flowDirs(repoRoot) {
			name := repo.alias + ":" + rel(repoRoot, dir)
			f, err := flow.Load(dir)
			if err != nil {
				continue // not a flow that loads; that team's own checks report it
			}
			reads, pin := readsSelf(f, selfSrc, o.Fetcher)
			if !reads {
				continue
			}
			if pin != "" {
				r.Pinned = append(r.Pinned, Pinned{Repo: repo.alias, Flow: name, Ref: pin})
				continue
			}
			b := analyze(dir, before)
			if b == nil {
				r.NotChecked = append(r.NotChecked, Skipped{Repo: repo.alias, Flow: name, Reason: "does not load at the base branch"})
				continue
			}
			a := analyze(dir, after)
			r.Checked++
			was, now := findings(repo.alias, name, b, paths), findings(repo.alias, name, a, paths)
			if len(b.Errors) > 0 {
				// Fields were never compared before, so only errors can be.
				r.Failing = append(r.Failing, Skipped{Repo: repo.alias, Flow: name, Reason: normalise(b.Errors[0].Message, paths)})
				now = errorsOnly(now)
			}
			added, gone := diff(was, now), diff(now, was)
			if len(added) > 0 {
				r.Views[name] = a
			}
			r.New = append(r.New, added...)
			r.Resolved = append(r.Resolved, gone...)
		}
	}
	sortFindings(r.New)
	sortFindings(r.Resolved)
	return r, nil
}

// findSelf is the registry alias of the repository being changed.
func findSelf(o Options, root string, reg map[string]sources.Source) (string, error) {
	if o.As != "" {
		if _, ok := reg[o.As]; !ok {
			return "", fmt.Errorf("%s is not in the registry", o.As)
		}
		return o.As, nil
	}
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	origin := strings.TrimSpace(string(out))
	if err == nil {
		for alias, src := range reg {
			if sources.SameRepo(src.Git, origin) {
				return alias, nil
			}
		}
	}
	return "", fmt.Errorf("this repository's origin (%s) is not in the registry; name its alias with --as", origin)
}

type consumer struct {
	alias string
	src   sources.Source
	local string // a local registry's folder, used as is
}

// consumers lists every other registered repository, then the registry
// itself, which may hold cross-team flows.
func consumers(res *sources.Resolver, reg map[string]sources.Source, self string) []consumer {
	var out []consumer
	for alias, src := range reg {
		if alias != self {
			out = append(out, consumer{alias: alias, src: src})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].alias < out[j].alias })
	if p := res.Project.Registry; p != nil {
		c := consumer{alias: "registry", src: *p}
		if p.Path != "" {
			c.local = p.Path
		}
		out = append(out, c)
	}
	return out
}

// flowDirs finds every folder holding a flow.toml.
func flowDirs(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".dsheets") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == flow.FileName {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// readsSelf reports whether a flow refers to the repository and, when every
// alias it reads it through is pinned, the ref it pins it to. A flow that
// also follows the branch through another alias is checked.
func readsSelf(f *flow.Flow, self sources.Source, fe *sources.Fetcher) (reads bool, pin string) {
	res := sources.NewResolver(f.Dir, f.Sources, fe)
	for _, alias := range f.Aliases() {
		src, err := res.Lookup(alias)
		if err != nil || src.Git == "" || !sources.SameRepo(src.Git, self.Git) {
			continue
		}
		reads = true
		if followed, err := fe.Followed(src); err != nil || followed {
			return true, ""
		}
		if pin == "" {
			pin = src.Ref
		}
	}
	return reads, pin
}

func analyze(dir string, fe *sources.Fetcher) *flow.Analysis {
	f, err := flow.Load(dir)
	if err != nil {
		return nil
	}
	return flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, fe))
}

// findings lists every missing or unfit field and every error of a flow.
func findings(repo, name string, a *flow.Analysis, paths map[string]string) []Finding {
	var out []Finding
	for _, p := range a.Errors {
		out = append(out, Finding{Repo: repo, Flow: name, Status: StatusError, Message: normalise(p.Message, paths)})
	}
	for _, b := range a.Boundaries {
		if b.Iface == nil {
			continue
		}
		where := b.Role
		if b.Role != flow.RoleInput && b.Role != flow.RoleOutput {
			where = "stage " + b.StageID
		}
		for _, it := range b.Items {
			if it.Status != flow.Missing && it.Status != flow.Warn {
				continue
			}
			out = append(out, Finding{Repo: repo, Flow: name, Interface: b.Iface.Name, Owner: b.Iface.Owner,
				Where: where, Field: it.Field, Status: it.Status, Message: it.Reason()})
		}
	}
	return out
}

func errorsOnly(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Status == StatusError {
			out = append(out, f)
		}
	}
	return out
}

// diff lists the findings in b that are not in a.
func diff(a, b []Finding) []Finding {
	seen := map[Finding]bool{}
	for _, f := range a {
		seen[f] = true
	}
	var out []Finding
	for _, f := range b {
		if !seen[f] {
			out = append(out, f)
		}
	}
	return out
}

// normalise replaces local folders in a message by the alias they stand for.
func normalise(msg string, paths map[string]string) string {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		for _, form := range []string{k + string(os.PathSeparator), filepath.ToSlash(k) + "/"} {
			if i := strings.Index(msg, form); i >= 0 {
				rest := msg[i+len(form):]
				end := strings.IndexAny(rest, " :\n")
				if end < 0 {
					end = len(rest)
				}
				msg = msg[:i] + paths[k] + filepath.ToSlash(rest[:end]) + rest[end:]
			}
		}
	}
	return msg
}

func rel(root, dir string) string {
	r, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(r)
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Flow != b.Flow {
			return a.Flow < b.Flow
		}
		if a.Where != b.Where {
			return a.Where < b.Where
		}
		return a.Field < b.Field
	})
}
