package diff

import (
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
)

// Options say what to compare.
type Options struct {
	Dir string // a folder inside the repository
	// Base and Head are revisions, compared directly (as `git diff base
	// head`). An empty Head means HEAD. An empty Base means the merge-base
	// of the default branch and Head, which is what a pull request shows.
	Base, Head string
	// Cases also compares cases.toml files, which are generated.
	Cases bool
}

// CasesChange is the difference between two versions of a generated
// cases.toml, by case id.
type CasesChange struct {
	Sheet  string
	Path   string
	Status Status
	Cases  []RowChange
}

// Report is everything that changed between two revisions of the
// repository: the repository is the team, so there is one group.
type Report struct {
	Repo       string // the repository's folder name
	Base, Head string // the commits compared, abbreviated
	Sheets     []SheetChange
	Flows      []FlowChange
	Interfaces []InterfaceChange
	Cases      []CasesChange
}

// file is one path as it was at the base and at the head; a nil side means
// the file did not exist there.
type file struct {
	path, oldPath string
	base, head    []byte
}

// Run compares two revisions of the repository that contains o.Dir. Only
// files that changed between them are read.
func Run(o Options) (*Report, error) {
	revs, err := store.NewRevs(o.Dir)
	if err != nil {
		return nil, err
	}
	headRev := o.Head
	if headRev == "" {
		headRev = "HEAD"
	}
	head, err := revs.Resolve(headRev)
	if err != nil {
		return nil, err
	}
	var base string
	if o.Base != "" {
		if base, err = revs.Resolve(o.Base); err != nil {
			return nil, err
		}
	} else {
		def, err := revs.DefaultBranch()
		if err != nil {
			return nil, err
		}
		if base, err = revs.MergeBase(def, head); err != nil {
			return nil, err
		}
	}
	changes, err := revs.Changed(base, head)
	if err != nil {
		return nil, err
	}

	var sheets, flows, ifaces, cases []file
	for _, c := range changes {
		kind := classify(c.Path, o.Cases)
		if kind == "" {
			continue
		}
		f := file{path: c.Path}
		if c.Status != 'A' {
			if f.base, err = readAt(revs, base, c.Path); err != nil {
				return nil, err
			}
		}
		if c.Status != 'D' {
			if f.head, err = readAt(revs, head, c.Path); err != nil {
				return nil, err
			}
		}
		switch kind {
		case "sheet":
			sheets = append(sheets, f)
		case "flow":
			flows = append(flows, f)
		case "cases":
			cases = append(cases, f)
		case "toml":
			if isInterface(f) {
				ifaces = append(ifaces, f)
			}
		}
	}

	r := &Report{Repo: filepath.Base(revs.Top()), Base: short(base), Head: short(head)}
	for _, f := range pair(sheets, "sheet") {
		sc, err := DiffSheet(f.base, f.head)
		if err != nil {
			return nil, err
		}
		sc.Path, sc.OldPath = f.path, f.oldPath
		if sc.Status != Changed || sc.OldPath != "" || len(sc.Meta)+len(sc.Rows) > 0 || sc.TextOnly || sc.Unparseable != "" {
			r.Sheets = append(r.Sheets, sc)
		}
	}
	for _, f := range pair(flows, "flow") {
		fc, err := DiffFlow(f.base, f.head)
		if err != nil {
			return nil, err
		}
		fc.Path, fc.OldPath = f.path, f.oldPath
		if fc.Status != Changed || fc.OldPath != "" || len(fc.Stages)+len(fc.Boundaries)+len(fc.Computes) > 0 || fc.TextOnly || fc.Unparseable != "" {
			r.Flows = append(r.Flows, fc)
		}
	}
	for _, f := range ifaces {
		ic, err := DiffInterface(f.base, f.head)
		if err != nil {
			return nil, err
		}
		ic.Path = f.path
		if ic.Status != Changed || len(ic.Meta)+len(ic.Fields) > 0 || ic.TextOnly || ic.Unparseable != "" {
			r.Interfaces = append(r.Interfaces, ic)
		}
	}
	for _, f := range cases {
		cc := diffCases(f)
		if cc.Status != Changed || len(cc.Cases) > 0 {
			r.Cases = append(r.Cases, cc)
		}
	}
	return r, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func readAt(revs *store.Revs, rev, p string) ([]byte, error) {
	data, err := revs.ReadAt(rev, p)
	if errors.Is(err, store.ErrNotInRev) {
		return nil, nil
	}
	return data, err
}

// classify says what a changed path might be: "sheet", "flow", "cases", a
// "toml" file that may be an interface, or "" for everything dsheets never
// compares (other files, data folders, hidden folders).
func classify(p string, withCases bool) string {
	parts := strings.Split(p, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.HasPrefix(part, ".") || part == "data" {
			return ""
		}
	}
	name := parts[len(parts)-1]
	switch {
	case name == sheet.FileName:
		return "sheet"
	case name == flow.FileName:
		return "flow"
	case name == "cases.toml":
		if withCases {
			return "cases"
		}
		return ""
	case path.Ext(name) == ".toml":
		return "toml"
	}
	return ""
}

// isInterface reports whether either side of a TOML file names an
// `interface` at its top level; other TOML files are not dsheets files.
func isInterface(f file) bool {
	for _, data := range [][]byte{f.base, f.head} {
		m, _ := decodeSide(data)
		if s, _ := m["interface"].(string); s != "" {
			return true
		}
	}
	return false
}

// pair joins a deleted and an added file that carry the same name under
// key (`sheet` or `flow`), so moving a folder is one changed file and not a
// deletion plus an addition. Results are sorted by path.
func pair(files []file, key string) []file {
	nameOf := func(data []byte) string {
		m, _ := decodeSide(data)
		s, _ := m[key].(string)
		return s
	}
	removed, added := map[string][]int{}, map[string][]int{}
	for i, f := range files {
		switch {
		case f.base != nil && f.head == nil:
			removed[nameOf(f.base)] = append(removed[nameOf(f.base)], i)
		case f.base == nil && f.head != nil:
			added[nameOf(f.head)] = append(added[nameOf(f.head)], i)
		}
	}
	skip := map[int]bool{}
	var out []file
	for name, rs := range removed {
		as := added[name]
		if name == "" || len(rs) != 1 || len(as) != 1 {
			continue
		}
		d, a := files[rs[0]], files[as[0]]
		out = append(out, file{path: a.path, oldPath: d.path, base: d.base, head: a.head})
		skip[rs[0]], skip[as[0]] = true, true
	}
	for i, f := range files {
		if !skip[i] {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func diffCases(f file) CasesChange {
	bm, _ := decodeSide(f.base)
	hm, _ := decodeSide(f.head)
	cc := CasesChange{Path: f.path}
	cc.Sheet, _ = hm["sheet"].(string)
	if cc.Sheet == "" {
		cc.Sheet, _ = bm["sheet"].(string)
	}
	switch {
	case f.base == nil:
		cc.Status = Added
	case f.head == nil:
		cc.Status = Removed
	default:
		cc.Status = Changed
	}
	cc.Cases = diffRows(rowsOf(bm, "case"), rowsOf(hm, "case"))
	return cc
}
