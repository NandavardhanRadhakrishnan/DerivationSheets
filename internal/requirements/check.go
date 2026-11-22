package requirements

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// fetcher is what Check needs from *sources.Fetcher: the owner's dsheets
// folder at the tip of the branch the registry follows.
type fetcher interface {
	Fetch(src sources.Source) (root, rev string, err error)
}

// Check sets Status and Note on every requirement from the owners' own sheets:
// a requirement is done when the owner's sheets already show the work, open
// when they do not, and undocumented when dsheets cannot tell (no owner in the
// registry, no sheets, a sheet it could not read, or the owner is the
// repository the flow itself is in, self). Schema requirements are left as
// they are: they close when the interface declares the field.
func Check(rs []Requirement, reg map[string]sources.Source, fe fetcher, self string, ov flow.Overlay) {
	repos := map[string]*repoSheets{}
	for i := range rs {
		q := &rs[i]
		if q.Kind == Schema {
			continue
		}
		aliases := sources.TeamAliases(q.Team, reg)
		if len(aliases) == 0 {
			q.Status, q.Note = StatusUndocumented, fmt.Sprintf("no repository for %s in the registry (name it with team = %q in .gitmodules)", q.Team, q.Team)
			continue
		}
		if self != "" && contains(aliases, self) {
			q.Status, q.Note = StatusUndocumented, fmt.Sprintf("%s is this repository, whose sheets are the flow's own base: dsheets cannot tell", self)
			continue
		}
		t := &teamSheets{aliases: aliases}
		for _, a := range aliases {
			rp := repos[a]
			if rp == nil {
				rp = readRepo(a, reg[a], fe, ov)
				repos[a] = rp
			}
			t.add(rp)
		}
		q.Status, q.Note = t.evidence(q)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// repoSheets is every row of every sheet in one owner repository's dsheets
// folder, with the sheets that could not be read.
type repoSheets struct {
	alias  string
	rows   []*sheet.Row
	failed []string // "<file>: <error>"
	err    error    // the repository could not be fetched
}

func readRepo(alias string, src sources.Source, fe fetcher, ov flow.Overlay) *repoSheets {
	o := &repoSheets{alias: alias}
	root, _, err := fe.Fetch(src)
	if err != nil {
		o.err = err
		return o
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			rel, _ := filepath.Rel(root, p)
			o.failed = append(o.failed, filepath.ToSlash(rel)+": "+err.Error())
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".dsheets") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == sheet.FileName {
			var s *sheet.Sheet
			var err error
			if data, ok := ov[flow.OverlayKey(p)]; ok {
				s, err = sheet.Parse(filepath.Dir(p), p, string(data))
			} else {
				s, err = sheet.Load(filepath.Dir(p))
			}
			if err != nil {
				rel, _ := filepath.Rel(root, p)
				o.failed = append(o.failed, filepath.ToSlash(rel)+": "+err.Error())
				return nil
			}
			o.rows = append(o.rows, s.Rows...)
		}
		return nil
	})
	return o
}

// teamSheets is what the repositories of one team hold together.
type teamSheets struct {
	aliases []string
	rows    []*sheet.Row
	failed  []string
	errs    []string
}

func (t *teamSheets) add(r *repoSheets) {
	if r.err != nil {
		t.errs = append(t.errs, fmt.Sprintf("could not read %s: %v", r.alias, r.err))
		return
	}
	t.rows = append(t.rows, r.rows...)
	for _, f := range r.failed {
		t.failed = append(t.failed, r.alias+"/"+f)
	}
}

func (t *teamSheets) name() string { return strings.Join(t.aliases, ", ") }

// byRow finds the team's rows named like the end-to-end row.
func (t *teamSheets) byRow(id string) (derived, inputs []*sheet.Row) {
	for _, r := range t.rows {
		if flow.Norm(r.ID) != flow.Norm(id) {
			continue
		}
		if r.IsInput() {
			inputs = append(inputs, r)
		} else {
			derived = append(derived, r)
		}
	}
	return derived, inputs
}

// evidence decides one requirement against the team's rows.
func (t *teamSheets) evidence(q *Requirement) (status, note string) {
	if len(t.rows) == 0 {
		switch {
		case len(t.errs) > 0:
			return StatusUndocumented, strings.Join(t.errs, "; ")
		case len(t.failed) > 0:
			return StatusUndocumented, "could not read sheets: " + strings.Join(t.failed, "; ")
		}
		return StatusUndocumented, "no sheets in " + t.name()
	}
	derived, inputs := t.byRow(q.Row)
	switch {
	case q.Kind == Compute && q.Remove:
		if len(derived) > 0 {
			return StatusOpen, fmt.Sprintf("%s still derives `%s`", t.name(), q.Row)
		}
		// Absence is only evidence when every sheet could be read.
		if len(t.failed) > 0 || len(t.errs) > 0 {
			return StatusUndocumented, "could not read " + strings.Join(append(append([]string{}, t.failed...), t.errs...), "; ") + ": cannot tell whether it is still derived"
		}
		return StatusDone, ""
	case q.Kind == Compute:
		want := q.RuleKey
		key := RuleKey
		if want == "" {
			want, key = q.Rule, RuleText
		}
		for _, r := range derived {
			if sameRule(key(r), want) {
				return StatusDone, ""
			}
		}
		switch {
		case len(derived) > 0:
			return StatusOpen, fmt.Sprintf("%s's sheet has `%s` but its rule differs from the flow's", t.name(), q.Row)
		case len(inputs) > 0:
			return StatusOpen, fmt.Sprintf("%s reads `%s` as an input but does not derive it", t.name(), q.Row)
		}
		return StatusOpen, fmt.Sprintf("no row `%s` in %s's sheets", q.Row, t.name())
	case q.Kind == Send:
		for _, r := range t.rows {
			if sends(r, q) {
				return StatusDone, ""
			}
		}
		return StatusOpen, fmt.Sprintf("no row in %s's sheets sends `%s`", t.name(), q.Field)
	case q.Kind == Receive:
		for _, r := range t.rows {
			if reads(r, q) {
				return StatusDone, ""
			}
		}
		return StatusOpen, fmt.Sprintf("no input row in %s's sheets reads `%s`", t.name(), q.Field)
	case q.Kind == Carry:
		if len(derived)+len(inputs) > 0 {
			return StatusDone, ""
		}
		return StatusOpen, fmt.Sprintf("no row `%s` in %s's sheets", q.Row, t.name())
	}
	return StatusOpen, ""
}

// sends reports whether the row sends the field the way the boundary needs: a
// call request is filled by a send to that stage, anything else by a send to
// the sheet's own target.
func sends(r *sheet.Row, q *Requirement) bool {
	if q.Role == flow.RoleRequest {
		for _, s := range r.CallSends {
			if s != nil && s.Target == q.Stage && sameField(s.Field, q.Field) {
				return true
			}
		}
		return false
	}
	return r.Send != nil && sameField(r.Send.Field, q.Field)
}

// reads reports whether the row is an input that reads the field the way the
// boundary delivers it: a call's response by call.<stage>.<field>, anything
// else from the request. A value read from config, a database or the context
// is not the field.
func reads(r *sheet.Row, q *Requirement) bool {
	if !r.IsInput() {
		return false
	}
	if q.Role == flow.RoleResponse {
		stage, field, ok := strings.Cut(r.SourcePath(), ".")
		return r.SourceKind() == "call" && ok && stage == q.Stage && sameField(field, q.Field)
	}
	return r.SourceKind() == "request" && sameField(r.SourcePath(), q.Field)
}

// sameRule compares two rule texts ignoring runs of whitespace.
func sameRule(a, b string) bool {
	return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
}

// sameField matches interface fields by name, ignoring case and separators.
// When only one of them is a dotted path, its last part may stand for the
// whole (`country` and `shipping.country`); two paths must match whole.
func sameField(a, b string) bool {
	if flow.Norm(a) == flow.Norm(b) {
		return true
	}
	dotted := func(s string) bool { return strings.Contains(s, ".") }
	if dotted(a) != dotted(b) {
		return flow.Norm(lastPart(a)) == flow.Norm(lastPart(b))
	}
	return false
}

func lastPart(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
