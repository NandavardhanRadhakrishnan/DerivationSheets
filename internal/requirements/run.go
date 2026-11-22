package requirements

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/agree"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
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
	// Branch is the PM's branch the editor lines name. When empty and Head is
	// empty it is the current branch, and when Head names a branch, that one;
	// with a detached head there are no lines.
	Branch string
	// NoCheck skips reading the owners' sheets: every requirement stays open.
	NoCheck bool
}

// Result is the requirements of one flow change.
type Result struct {
	Flow         string        `json:"flow"` // the flow's name
	Base         string        `json:"base"` // the commits compared, abbreviated
	Head         string        `json:"head"`
	Requirements []Requirement `json:"requirements"`
	// ShowDone lists done requirements too, when printing.
	ShowDone bool `json:"-"`
	// Report is the run of the PM's cases behind the cases of the items, or nil.
	Report *agree.Report `json:"-"`
	// Editor is the command that opens each team's sheet in the editor on this
	// change, by team. A team with nothing to show has no line.
	Editor map[string]string `json:"editor,omitempty"`
}

// Counts is how many requirements are done, and how many there are.
func (r *Result) Counts() (done, total int) {
	for _, q := range r.Requirements {
		if q.Status == StatusDone {
			done++
		}
	}
	return done, len(r.Requirements)
}

// Prepared is a flow change read from git once: the requirements, all open,
// and what is needed to decide them. Nothing in it points into the temporary
// exports, which are gone by the time Prepare returns.
type Prepared struct {
	res     Result // the template: every item open
	headA   *flow.Analysis
	reg     map[string]sources.Source
	self    string
	fe      *sources.Fetcher
	filled  *cases.File // the PM's cases.toml, read at Prepare
	why     string      // why filled is missing
	noCheck bool
	branch  string // the PM's branch, "" when unknown
	flowRel string // the flow's folder relative to the self alias root, "" when unknown
	aliasAs string // this repository's registry alias, "" when unknown
}

// Analysis is the PM's flow at head.
func (p *Prepared) Analysis() *flow.Analysis { return p.headA }

// Run compares the flow at the base revision with the flow at head, which is
// the working tree unless o.Head names a revision.
func Run(o Options) (*Result, error) {
	p, err := Prepare(o)
	if err != nil {
		return nil, err
	}
	return p.Decide(nil), nil
}

// Prepare reads the change: it compares the flow at the base with the flow at
// head and locates the items, leaving them all open.
func Prepare(o Options) (*Prepared, error) {
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
	origin := sources.OriginURL(top)

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
	for i := range rs {
		rs[i].Status = StatusOpen
	}
	branch := o.Branch
	if branch == "" {
		if o.Head == "" {
			branch = currentBranch(top)
		} else if _, err := revs.Resolve("refs/heads/" + o.Head); err == nil {
			branch = o.Head
		}
	}
	p := &Prepared{
		res:     Result{Flow: headA.Flow.Slug, Base: short(base), Head: short(head), Requirements: rs},
		headA:   headA,
		fe:      fe,
		noCheck: o.NoCheck,
		branch:  branch,
	}
	if !o.NoCheck {
		p.reg, _ = sources.NewResolver(top, nil, fe).Registered() // no registry: nothing to check against
		// This repository's own sheets at the followed branch are the flow's base, so they cannot show owners have acted.
		p.self = sources.SelfAlias(p.reg, origin)
		// The alias the editor lines name: the one given, or found from the origin.
		if _, ok := p.reg[o.As]; ok {
			p.aliasAs = o.As
		} else {
			p.aliasAs = p.self
		}
		if src, ok := p.reg[p.aliasAs]; ok {
			if in, err := filepath.Rel(filepath.Join(top, filepath.FromSlash(sources.CleanDir(src.Dir))), flowAbs); err == nil && !strings.HasPrefix(in, "..") {
				p.flowRel = filepath.ToSlash(in)
			}
		}
		if byCases(rs) && headA.Engine != nil {
			p.filled, p.why = cases.ReadFilled(headA.SheetDir)
		}
	}
	return p, nil
}

// Decide checks the items against the owners' sheets, reading unsaved sheets
// and flows from ov (which may be nil). It never changes p, so it can be
// called again with another overlay.
func (p *Prepared) Decide(ov flow.Overlay) *Result {
	res := p.res
	rs := make([]Requirement, len(p.res.Requirements))
	copy(rs, p.res.Requirements)
	for i := range rs {
		rs[i].Needs = slices.Clone(rs[i].Needs)
		rs[i].Cases = nil
	}
	res.Requirements = rs
	if p.noCheck {
		return &res
	}
	Check(rs, p.reg, p.fe, p.self, ov)
	if byCases(rs) && p.headA.Engine != nil {
		rep := agree.Run(p.headA, agree.RegistryTeams(p.reg, p.fe, p.self, ov), agree.Options{Mode: agree.Isolated, Filled: p.filled})
		applyCases(rs, p.headA, rep)
		explainCases(rs, rep, p.why)
		res.Report = rep
	}
	res.Editor = p.editorLines(rs, res.Report)
	return &res
}

// editorLines is the command that opens each team's sheet in the editor on
// the PM's change. The sheet is the team's first matched one, relative to its
// repository, or the literal <sheet> when none was found.
func (p *Prepared) editorLines(rs []Requirement, rep *agree.Report) map[string]string {
	if p.branch == "" || p.aliasAs == "" || p.flowRel == "" {
		return nil
	}
	out := map[string]string{}
	for _, q := range rs {
		if _, ok := out[q.Team]; ok {
			continue
		}
		sheet := "<sheet>"
		var dirs []string
		if rep != nil {
			dirs = rep.Sheets[q.Team]
		}
		if len(dirs) > 0 {
			if r, err := filepath.Rel(sources.RepoRoot(dirs[0]), dirs[0]); err == nil && !strings.HasPrefix(r, "..") {
				sheet = filepath.ToSlash(r)
			}
		}
		out[q.Team] = fmt.Sprintf("dsheets serve --edit %s --flow %s:%s@%s", sheet, p.aliasAs, p.flowRel, p.branch)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// currentBranch is the branch checked out in dir, "" on a detached head.
func currentBranch(dir string) string {
	cmd := exec.Command("git", "symbolic-ref", "--short", "-q", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// byCases says some item is one the PM's cases may decide: a Compute or Send
// that Check could tell.
func byCases(rs []Requirement) bool {
	for _, q := range rs {
		if (q.Kind == Compute || q.Kind == Send) && q.Status != StatusUndocumented {
			return true
		}
	}
	return false
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
		// A fetched copy under top's own cache belongs to the repository it was
		// fetched from, even though it sits inside the working tree.
		if r, err := filepath.Rel(headRoot, rs[i].File); err == nil && !strings.HasPrefix(r, "..") &&
			!strings.HasPrefix(filepath.ToSlash(r), sources.CacheDir+"/") {
			root = top
			rs[i].File = filepath.Join(top, r) // so the rest reads it as a file of top
		}
		rs[i].Repo = filepath.Base(root)
		if url := sources.OriginURL(root); url != "" {
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
