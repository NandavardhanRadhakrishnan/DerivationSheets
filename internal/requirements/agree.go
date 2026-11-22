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
// the row. A row no field carries is judged on the team's checks of the
// nearest rows it feeds that have any. With no such checks, or none the
// team's sheet was run for, the rule text decides, as Check left it.
func computeByCases(q *Requirement, pm *flow.Analysis, checks []agree.Check) {
	var rel []agree.Check
	for _, c := range checks {
		if c.Team == q.Team && c.Row == q.Row {
			rel = append(rel, c)
		}
	}
	judged := ""
	if len(rel) == 0 {
		checked := map[string]bool{}
		for _, c := range checks {
			if c.Team == q.Team && c.Row != "" {
				checked[c.Row] = true
			}
		}
		below := nearest(pm, q.Row, checked)
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
	// Done only when every check agrees; anything else is not evidence.
	var unsure []agree.Check
	for _, c := range rel {
		if c.Status != agree.Agrees {
			unsure = append(unsure, c)
		}
	}
	if len(unsure) > 0 {
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
		if c.Role == q.Role && c.Stage == q.Stage && c.Field != "reject" && SameField(c.Field, q.Field) {
			rel = append(rel, c)
		}
	}
	if differ := withStatus(rel, agree.Differs); len(differ) > 0 {
		q.Status, q.Note, q.Cases = StatusOpen, differNote(differ, rel), differ
	}
}

// ruleText starts the note on an item the cases could not decide.
const ruleText = "checked by rule text: "

// byRuleText keeps Check's result, saying why the cases could not decide.
func byRuleText(q *Requirement, why string) {
	q.Note = joinNotes(q.Note, ruleText+why)
}

// nearest are the rows that use row, directly or through other rows, and
// are checked, stopping along each path at the first checked row: a row
// further down is also fed by the rows in between.
func nearest(pm *flow.Analysis, row string, checked map[string]bool) map[string]bool {
	out := map[string]bool{}
	if pm == nil || pm.Engine == nil || pm.Engine.Graph == nil {
		return out
	}
	seen := map[string]bool{row: true}
	next := []string{row}
	for len(next) > 0 {
		r := next[0]
		next = next[1:]
		for _, u := range pm.Engine.Graph.Users[r] {
			if seen[u] {
				continue
			}
			seen[u] = true
			if checked[u] {
				out[u] = true
			} else {
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

// explainCases adds to the items the notes of the run that explain them: on
// an item the rule text decided, the notes on its team's flows, such as why
// they were left out; on an item the cases could not tell, why the PM's
// filled values were not used.
func explainCases(rs []Requirement, rep *agree.Report, filledWhy string) {
	for i := range rs {
		q := &rs[i]
		switch {
		case q.Status == StatusUndocumented && len(q.Cases) > 0 && filledWhy != "":
			q.Note = joinNotes(q.Note, filledWhy+", so the cases ran without its filled values")
		case strings.Contains(q.Note, ruleText) && rep != nil:
			for _, n := range rep.TeamNotes[q.Team] {
				q.Note = joinNotes(q.Note, n)
			}
		}
	}
}
