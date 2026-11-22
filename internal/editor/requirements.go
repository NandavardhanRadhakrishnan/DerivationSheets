package editor

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/requirements"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// ReqView is what the Requirements panel shows.
type ReqView struct {
	Flow  string    `json:"flow"` // the --flow reference
	Base  string    `json:"base"`
	Head  string    `json:"head"`
	Teams []string  `json:"teams"`         // this repository's teams in the flow
	Why   string    `json:"why,omitempty"` // why Teams is empty
	Items []ReqItem `json:"items"`
}

// ReqItem is one requirement in the panel: this team's own, or another
// team's that passes through a call this team serves.
type ReqItem struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"`
	Team   string    `json:"team"`
	What   string    `json:"what"`
	Rule   string    `json:"rule"`
	Status string    `json:"status"`
	Note   string    `json:"note"`
	Mine   bool      `json:"mine"`
	Via    string    `json:"via,omitempty"` // another team's item: "the call to <stage>"
	Cases  []ReqCase `json:"cases,omitempty"`
	Rows   []string  `json:"rows,omitempty"` // rows of this sheet producing its fields, in sheet order
}

// ReqCase is one failing or undecided case behind an item.
type ReqCase struct {
	Case     string `json:"case"`
	Input    string `json:"input"` // "k = v, ..." sorted by key
	Field    string `json:"field"`
	Status   string `json:"status"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Note     string `json:"note"`
}

// RequirementsView is the Requirements panel for the PM's change ref, decided
// in res against the draft: the items of teams (this repository's teams in
// pm, the PM's flow at head), then other teams' Compute and Send items whose
// rows cross a call one of teams serves. With no teams, why says why and
// every item is listed as not this team's. draft is the sheet being edited,
// and may be nil: then no item has rows.
func RequirementsView(ref string, res *requirements.Result, pm *flow.Analysis, teams []string, why string, draft *eval.Engine) *ReqView {
	v := &ReqView{Flow: ref, Teams: slices.Clone(teams), Why: why, Items: []ReqItem{}}
	if v.Teams == nil {
		v.Teams = []string{}
	}
	if res == nil {
		return v
	}
	v.Base, v.Head = res.Base, res.Head
	var inputs map[string]map[string]any
	if res.Report != nil {
		inputs = res.Report.Inputs
	}
	var mine, others []ReqItem
	for _, q := range res.Requirements {
		switch {
		case len(teams) == 0:
			others = append(others, newItem(q, inputs))
		case slices.Contains(teams, q.Team):
			it := newItem(q, inputs)
			it.Mine = true
			it.Rows = ownRows(draft, q)
			mine = append(mine, it)
		default:
			if stage := viaStage(pm, teams, q); stage != "" {
				it := newItem(q, inputs)
				it.Via = "the call to " + stage
				it.Rows = responseRows(draft, pm, stage)
				others = append(others, it)
			}
		}
	}
	byStatus(mine)
	byStatus(others)
	v.Items = append(append(v.Items, mine...), others...)
	return v
}

func newItem(q requirements.Requirement, inputs map[string]map[string]any) ReqItem {
	it := ReqItem{ID: q.ID, Kind: string(q.Kind), Team: q.Team, What: q.What, Rule: q.Rule, Status: q.Status, Note: q.Note}
	if it.Status == "" {
		it.Status = requirements.StatusOpen // an empty Status reads as open
	}
	for _, c := range q.Cases {
		it.Cases = append(it.Cases, ReqCase{
			Case: c.Case, Input: inputLine(inputs[c.Case]), Field: c.Field, Status: c.Status,
			Expected: requirements.Shown(c.Expected), Actual: requirements.Shown(c.Actual), Note: c.Note,
		})
	}
	return it
}

// inputLine is a case's input as "k = v, ...", sorted by key.
func inputLine(in map[string]any) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s = %s", k, requirements.Shown(in[k]))
	}
	return strings.Join(parts, ", ")
}

// statusRank orders items within a group: open, undocumented, then done.
var statusRank = map[string]int{requirements.StatusOpen: 0, requirements.StatusUndocumented: 1, requirements.StatusDone: 2}

func byStatus(items []ReqItem) {
	sort.SliceStable(items, func(i, j int) bool { return statusRank[items[i].Status] < statusRank[items[j].Status] })
}

// viaStage is the PM call stage through which another team's item reaches
// teams: a call whose callee is one of teams and whose caller is the item's
// team, carrying the item's row or one of its ancestors on its request or
// response. Only Compute (not a removal) and Send items qualify. "" when none.
func viaStage(pm *flow.Analysis, teams []string, q requirements.Requirement) string {
	if pm == nil || q.Row == "" || !(q.Kind == requirements.Send || (q.Kind == requirements.Compute && !q.Remove)) {
		return ""
	}
	rows := []string{q.Row}
	if pm.Engine != nil {
		for id := range pm.Engine.Graph.Ancestors(q.Row) {
			rows = append(rows, id)
		}
	}
	for _, b := range pm.Boundaries {
		// Step i produces boundary i+1: the request sits between the caller
		// and the callee.
		if b.Role != flow.RoleRequest || b.Index < 1 || b.Index >= len(pm.Steps) {
			continue
		}
		caller, callee := pm.Steps[b.Index-1], pm.Steps[b.Index]
		if callee.Role != flow.StepCallee || !slices.Contains(teams, callee.Team()) || caller.Team() != q.Team {
			continue
		}
		resp := boundaryOf(pm, flow.RoleResponse, b.StageID)
		for _, r := range rows {
			if b.CarryOf(r) != nil || (resp != nil && resp.CarryOf(r) != nil) {
				return b.StageID
			}
		}
	}
	return ""
}

func boundaryOf(pm *flow.Analysis, role, stage string) *flow.Boundary {
	for _, b := range pm.Boundaries {
		if b.Role == role && b.StageID == stage {
			return b
		}
	}
	return nil
}

// ownRows are the rows of draft producing an item of this team: those that
// send a field of its cases or its own field, and the row named like its
// row, with their derived ancestors.
func ownRows(draft *eval.Engine, q requirements.Requirement) []string {
	if draft == nil {
		return nil
	}
	type target struct{ role, stage, field string }
	var want []target
	for _, c := range q.Cases {
		if c.Field != "reject" {
			want = append(want, target{c.Role, c.Stage, c.Field})
		}
	}
	if q.Field != "" {
		want = append(want, target{q.Role, q.Stage, q.Field})
	}
	var seeds []string
	for _, r := range draft.Sheet.Rows {
		if q.Row != "" && flow.Norm(r.ID) == flow.Norm(q.Row) {
			seeds = append(seeds, r.ID)
			continue
		}
		for _, w := range want {
			if sendsField(r, w.role, w.stage, w.field) {
				seeds = append(seeds, r.ID)
				break
			}
		}
	}
	return producing(draft, seeds)
}

// responseRows are the rows of draft that send a field of the stage's
// response, with their derived ancestors.
func responseRows(draft *eval.Engine, pm *flow.Analysis, stage string) []string {
	if draft == nil || pm == nil {
		return nil
	}
	resp := boundaryOf(pm, flow.RoleResponse, stage)
	if resp == nil || resp.Iface == nil {
		return nil
	}
	var seeds []string
	for _, r := range draft.Sheet.Rows {
		for _, f := range resp.Iface.Fields {
			if sendsField(r, flow.RoleResponse, stage, f.Name) {
				seeds = append(seeds, r.ID)
				break
			}
		}
	}
	return producing(draft, seeds)
}

// sendsField reports whether the row sends the field the way the boundary
// needs: a call request by a send to that stage, anything else by the
// sheet's own send.
func sendsField(r *sheet.Row, role, stage, field string) bool {
	if role == flow.RoleRequest {
		for _, s := range r.CallSends {
			if s != nil && s.Target == stage && requirements.SameField(s.Field, field) {
				return true
			}
		}
		return false
	}
	return r.Send != nil && requirements.SameField(r.Send.Field, field)
}

// producing is seeds and their ancestors in draft, inputs left out, in sheet
// order.
func producing(draft *eval.Engine, seeds []string) []string {
	in := map[string]bool{}
	for _, id := range seeds {
		in[id] = true
		for a := range draft.Graph.Ancestors(id) {
			in[a] = true
		}
	}
	var out []string
	for _, r := range draft.Sheet.Rows {
		if in[r.ID] && !r.IsInput() {
			out = append(out, r.ID)
			delete(in, r.ID) // a row listed twice in the file is shown once
		}
	}
	return out
}
