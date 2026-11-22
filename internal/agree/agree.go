// Package agree runs a product manager's generated cases through each team's
// own sheet and compares the values at the boundaries between teams.
package agree

import (
	"fmt"
	"slices"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
)

// Mode says what each team is fed.
type Mode int

const (
	Isolated Mode = iota // each team fed the PM's values at its incoming handoff
	EndToEnd             // each team fed the previous team's output
)

// Check statuses.
const (
	Agrees     = "agrees"
	Differs    = "differs"
	CannotTell = "cannot-tell"
)

// Check is one compared value: a boundary field, or a reject, in one case.
type Check struct {
	Case     string `json:"case"`
	Boundary int    `json:"-"`             // index in the PM analysis's Boundaries
	Role     string `json:"role"`          // flow.Role*
	Stage    string `json:"stage"`         // the PM's stage id
	Iface    string `json:"interface"`     // interface name
	Field    string `json:"field"`         // interface field, or "reject"
	Row      string `json:"row,omitempty"` // the PM row the field carries
	Team     string `json:"team"`          // team whose step produces the value
	Status   string `json:"status"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Note     string `json:"note,omitempty"`
	// NoFlow marks a cannot-tell check whose team has no own flow for the
	// segment (none matches; several matching is not NoFlow): its sheet was
	// not run at all, and Note says why.
	NoFlow bool `json:"-"`
}

// Report is the outcome of one agreement run.
type Report struct {
	Flow   string
	Mode   Mode
	Cases  int
	Checks []Check
	Notes  []string // what could not be read, once each
	// TeamNotes are the notes on each team's flows, by team, once each.
	TeamNotes map[string][]string
	// Inputs are the PM's cases' inputs, by case id.
	Inputs map[string]map[string]any
	// Sheets are the sheet folders of each team's matched own flows, sorted,
	// without duplicates.
	Sheets map[string][]string
}

// TeamFlows returns the analysed own flows of a team, and notes on what
// could not be read.
type TeamFlows func(team string) (flows []*flow.Analysis, notes []string)

// Options tune a run.
type Options struct {
	Mode    Mode
	RefTime time.Time   // generated cases' reference time; zero means cases.DefaultReferenceTime
	Filled  *cases.File // the PM's committed cases.toml, for "?" values; may be nil
}

// Run runs the PM's generated cases through each team's own sheet along the
// PM's flow and compares the values at every boundary that carries a PM row.
func Run(pm *flow.Analysis, teams TeamFlows, o Options) *Report {
	r := &Report{Flow: pm.Flow.Slug, Mode: o.Mode, TeamNotes: map[string][]string{}, Inputs: map[string]map[string]any{}, Sheets: map[string][]string{}}
	cs := pmCases(pm, o)
	r.Cases = len(cs)
	for _, c := range cs {
		r.Inputs[c.ID] = c.Input
	}
	ru := newRunner(pm, o.Mode, r)
	seen := map[string]bool{}
	for i, sg := range ru.segs {
		var flows []*flow.Analysis
		var notes []string
		if teams != nil {
			flows, notes = teams(sg.Team)
		}
		for _, n := range notes {
			if !seen[n] {
				seen[n] = true
				r.Notes = append(r.Notes, n)
			}
			if !slices.Contains(r.TeamNotes[sg.Team], n) {
				r.TeamNotes[sg.Team] = append(r.TeamNotes[sg.Team], n)
			}
		}
		ru.found[i], ru.notFound[i], ru.noFlow[i] = findFlow(pm, sg, flows)
		if m := ru.found[i]; m != nil && m.A.Engine == nil {
			ru.found[i], ru.notFound[i] = nil, fmt.Sprintf("the sheet of %s cannot be evaluated", m.A.Flow.Dir)
		}
		if m := ru.found[i]; m != nil && !slices.Contains(r.Sheets[sg.Team], m.A.SheetDir) {
			r.Sheets[sg.Team] = append(r.Sheets[sg.Team], m.A.SheetDir)
		}
	}
	for _, d := range r.Sheets {
		slices.Sort(d)
	}
	if o.Mode == EndToEnd {
		for _, c := range cs {
			ru.chain(c)
		}
		return r
	}
	for i := range ru.segs {
		for _, c := range cs {
			if !ru.rejectsBefore(ru.segs[i], c) {
				ru.run(i, c, ru.expected(c, pm.Boundaries[ru.segs[i].In]), true)
			}
		}
	}
	return r
}
