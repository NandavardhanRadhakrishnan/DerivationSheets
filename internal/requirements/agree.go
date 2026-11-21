package requirements

import (
	"fmt"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/agree"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
)

// applyCases decides Compute and Send items again on the PM's cases, run
// through the teams' own sheets (agree.Run, isolated), after Check has
// decided them on the sheets' text. Items Check could not tell are left
// alone; they include every item owned by the flow's own repository. Schema,
// Carry, Receive and "stop computing" items keep Check's result.
func applyCases(rs []Requirement, pm *flow.Analysis, rep *agree.Report) {
	if rep == nil {
		return
	}
	for i := range rs {
		q := &rs[i]
		if q.Status == StatusUndocumented {
			continue
		}
		switch {
		case q.Kind == Compute && !q.Remove:
			computeByCases(q, pm, rep.Checks)
		case q.Kind == Send:
			sendByCases(q, rep.Checks)
		}
	}
}

// computeByCases judges a Compute on the team's checks of fields that carry
// the row. A row no field carries is judged on the team's checks of the rows
// it feeds. With no such checks, or none the team's sheet was run for, the
// rule text decides, as Check left it.
func computeByCases(q *Requirement, pm *flow.Analysis, checks []agree.Check) {
	var rel []agree.Check
	for _, c := range checks {
		if c.Team == q.Team && c.Row == q.Row {
			rel = append(rel, c)
		}
	}
	judged := ""
	if len(rel) == 0 {
		below := descendants(pm, q.Row)
		var fields []string
		for _, c := range checks {
			if c.Team == q.Team && below[c.Row] {
				rel = append(rel, c)
				if !slices.Contains(fields, c.Field) {
					fields = append(fields, c.Field)
				}
			}
		}
		if len(rel) > 0 {
			judged = fmt.Sprintf("judged on %s, which other rows also feed", strings.Join(fields, ", "))
		}
	}
	if len(rel) == 0 {
		byRuleText(q, fmt.Sprintf("no case checks `%s` at a boundary of %s", q.Row, q.Team))
		return
	}
	if !slices.ContainsFunc(rel, func(c agree.Check) bool { return !c.NoFlow }) {
		byRuleText(q, rel[0].Note)
		return
	}
	if differ := withStatus(rel, agree.Differs); len(differ) > 0 {
		q.Status, q.Note, q.Cases = StatusOpen, joinNotes(differNote(differ, rel), judged), differ
		return
	}
	if unsure := withStatus(rel, agree.CannotTell); len(unsure) > 0 {
		note := unsure[0].Note
		if note == "" {
			note = "the PM's cases cannot tell"
		}
		q.Status, q.Note, q.Cases = StatusUndocumented, joinNotes(note, judged), unsure
		return
	}
	q.Status, q.Note, q.Cases = StatusDone, judged, nil
}

// sendByCases opens a Send Check closed when the field it sends differs, at
// its boundary, in some case. An agreeing or unknown value leaves Check's
// result.
func sendByCases(q *Requirement, checks []agree.Check) {
	if q.Status != StatusDone {
		return
	}
	var rel []agree.Check
	for _, c := range checks {
		if c.Role == q.Role && c.Stage == q.Stage && c.Field != "reject" && sameField(c.Field, q.Field) {
			rel = append(rel, c)
		}
	}
	if differ := withStatus(rel, agree.Differs); len(differ) > 0 {
		q.Status, q.Note, q.Cases = StatusOpen, differNote(differ, rel), differ
	}
}

// byRuleText keeps Check's result, saying why the cases could not decide.
func byRuleText(q *Requirement, why string) {
	q.Note = joinNotes(q.Note, "checked by rule text: "+why)
}

// descendants are the rows that use row, directly or through other rows.
func descendants(pm *flow.Analysis, row string) map[string]bool {
	out := map[string]bool{}
	if pm == nil || pm.Engine == nil || pm.Engine.Graph == nil {
		return out
	}
	next := []string{row}
	for len(next) > 0 {
		r := next[0]
		next = next[1:]
		for _, u := range pm.Engine.Graph.Users[r] {
			if !out[u] && u != row {
				out[u] = true
				next = append(next, u)
			}
		}
	}
	return out
}

func withStatus(cs []agree.Check, status string) []agree.Check {
	var out []agree.Check
	for _, c := range cs {
		if c.Status == status {
			out = append(out, c)
		}
	}
	return out
}

// differNote counts cases, not checks: a row may cross several fields.
func differNote(differ, all []agree.Check) string {
	return fmt.Sprintf("%d of %d cases differ", countCases(differ), countCases(all))
}

func countCases(cs []agree.Check) int {
	seen := map[string]bool{}
	for _, c := range cs {
		seen[c.Case] = true
	}
	return len(seen)
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}
