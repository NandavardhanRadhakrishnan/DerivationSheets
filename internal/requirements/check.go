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
// registry, no sheets, or the owner could not be read). Schema requirements
// are left as they are: they close when the interface declares the field.
func Check(rs []Requirement, reg map[string]sources.Source, fe fetcher) {
	owners := map[string]*ownerSheets{}
	for i := range rs {
		q := &rs[i]
		if q.Kind == Schema {
			continue
		}
		alias, ok := ownerAlias(q.Team, reg)
		if !ok {
			q.Status, q.Note = StatusUndocumented, fmt.Sprintf("no repository for %s in the registry (name it with team = %q in .gitmodules)", q.Team, q.Team)
			continue
		}
		o := owners[alias]
		if o == nil {
			o = readOwner(alias, reg[alias], fe)
			owners[alias] = o
		}
		switch {
		case o.err != nil:
			q.Status, q.Note = StatusUndocumented, fmt.Sprintf("could not read %s: %v", alias, o.err)
		case len(o.rows) == 0:
			q.Status, q.Note = StatusUndocumented, "no sheets in "+alias
		default:
			q.Status, q.Note = o.evidence(q)
		}
	}
}

// ownerAlias is the registry repository of a team: the entry whose team key
// names it, else the alias equal to the first word of the team's name.
func ownerAlias(team string, reg map[string]sources.Source) (string, bool) {
	for alias, src := range reg {
		if src.Team != "" && strings.EqualFold(src.Team, team) {
			return alias, true
		}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(team), " ")
	first = strings.ToLower(first)
	if _, ok := reg[first]; ok && first != "" {
		return first, true
	}
	return "", false
}

// ownerSheets is every row of every sheet in an owner's dsheets folder.
type ownerSheets struct {
	alias string
	rows  []*sheet.Row
	err   error
}

func readOwner(alias string, src sources.Source, fe fetcher) *ownerSheets {
	o := &ownerSheets{alias: alias}
	root, _, err := fe.Fetch(src)
	if err != nil {
		o.err = err
		return o
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".dsheets") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == sheet.FileName {
			if s, err := sheet.Load(filepath.Dir(p)); err == nil {
				o.rows = append(o.rows, s.Rows...)
			}
		}
		return nil
	})
	return o
}

// byRow finds the owner's rows named like the end-to-end row.
func (o *ownerSheets) byRow(id string) (derived, inputs []*sheet.Row) {
	for _, r := range o.rows {
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

// evidence decides one requirement against the owner's rows.
func (o *ownerSheets) evidence(q *Requirement) (status, note string) {
	derived, inputs := o.byRow(q.Row)
	switch {
	case q.Kind == Compute && q.Remove:
		if len(derived) > 0 {
			return StatusOpen, fmt.Sprintf("%s still derives `%s`", o.alias, q.Row)
		}
		return StatusDone, ""
	case q.Kind == Compute:
		for _, r := range derived {
			if sameRule(RuleText(r), q.Rule) {
				return StatusDone, ""
			}
		}
		switch {
		case len(derived) > 0:
			return StatusOpen, fmt.Sprintf("%s's sheet has `%s` but its rule differs from the flow's", o.alias, q.Row)
		case len(inputs) > 0:
			return StatusOpen, fmt.Sprintf("%s reads `%s` as an input but does not derive it", o.alias, q.Row)
		}
		return StatusOpen, fmt.Sprintf("no row `%s` in %s's sheets", q.Row, o.alias)
	case q.Kind == Send:
		for _, r := range o.rows {
			sends := append([]*sheet.Send{r.Send}, r.CallSends...)
			for _, s := range sends {
				if s != nil && sameField(s.Field, q.Field) {
					return StatusDone, ""
				}
			}
		}
		return StatusOpen, fmt.Sprintf("no row in %s's sheets sends `%s`", o.alias, q.Field)
	case q.Kind == Receive:
		for _, r := range o.rows {
			if r.IsInput() && flow.NameMatch(q.Field, r) {
				return StatusDone, ""
			}
		}
		return StatusOpen, fmt.Sprintf("no input row in %s's sheets reads `%s`", o.alias, q.Field)
	case q.Kind == Carry:
		if len(derived)+len(inputs) > 0 {
			return StatusDone, ""
		}
		return StatusOpen, fmt.Sprintf("no row `%s` in %s's sheets", q.Row, o.alias)
	}
	return StatusOpen, ""
}

// sameRule compares two rule texts ignoring runs of whitespace.
func sameRule(a, b string) bool {
	return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
}

// sameField matches interface fields by name, ignoring case and separators,
// whole or by their last part (`order.weightKg` and `weightKg`).
func sameField(a, b string) bool {
	if flow.Norm(a) == flow.Norm(b) {
		return true
	}
	return flow.Norm(lastPart(a)) == flow.Norm(lastPart(b))
}

func lastPart(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
