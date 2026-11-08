package editor

import (
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// FlowPanel is what the flow page's panel shows, from a sliced flow: each
// step and boundary with the place in flow.toml that writes it, and each
// value row with where it is computed, where it could be, and its marks.
type FlowPanel struct {
	Steps      []PanelStep         `json:"steps"`
	Boundaries []PanelBoundary     `json:"boundaries"`
	Rows       map[string]PanelRow `json:"rows"`
}

// PanelStep is a team step. Stage is the index of the stage whose Key
// lists the step's rows, or -1 for the output's step.
type PanelStep struct {
	Index int    `json:"index"`
	Label string `json:"label"`
	Team  string `json:"team"`
	Role  string `json:"role"`
	Stage int    `json:"stage"`
	Key   string `json:"key"` // "computes" or "callee_computes"
}

// PanelBoundary is an interface the flow crosses. Part says which table of
// flow.toml names it: "input", "handoff" (the stage itself), "request",
// "response" or "output"; Stage is the stage's index, or -1.
type PanelBoundary struct {
	Index    int          `json:"index"`
	Label    string       `json:"label"`
	Stage    int          `json:"stage"`
	Part     string       `json:"part"`
	Ref      string       `json:"ref"`
	Name     string       `json:"name,omitempty"`
	Owner    string       `json:"owner,omitempty"`
	Imported string       `json:"imported,omitempty"`
	Path     string       `json:"path,omitempty"`      // store path, when this repository's file can be edited
	ReadOnly string       `json:"read_only,omitempty"` // why it cannot
	Fields   []PanelField `json:"fields"`
}

// PanelField is an interface field and the row it carries, if any.
type PanelField struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required"`
	Local    bool   `json:"local,omitempty"`
	Row      string `json:"row,omitempty"`
}

// PanelRow is a value row of the flow.
type PanelRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Input  bool   `json:"input,omitempty"`
	Step   int    `json:"step"` // -1 for an input
	Pinned bool   `json:"pinned,omitempty"`
	// Can lists the steps the row can be computed in without a value
	// being needed before it is computed.
	Can   []int       `json:"can,omitempty"`
	Marks []PanelMark `json:"marks,omitempty"`
}

// PanelMark is a ✕ or ⚠ for the row at a boundary.
type PanelMark struct {
	Boundary int    `json:"boundary"`
	Status   string `json:"status"`
	Field    string `json:"field"`
	Note     string `json:"note"`
	Fix      string `json:"fix,omitempty"`
}

// Panel describes a sliced flow for the panel. It returns nil when the
// end-to-end sheet could not be loaded.
func (ed *Editor) Panel(a *flow.Analysis) *FlowPanel {
	if a == nil || a.Sheet == nil || a.Engine == nil {
		return nil
	}
	f := a.Flow
	p := &FlowPanel{Rows: map[string]PanelRow{}}

	// Steps and boundaries come in the order flow.Analyze lays them out.
	type at struct {
		stage int
		key   string
	}
	var stepAt []at
	type bat struct {
		stage int
		part  string
	}
	bounds := []bat{{-1, "input"}}
	for i, st := range f.Stages {
		if st.Kind == flow.KindCall {
			stepAt = append(stepAt, at{i, "computes"}, at{i, "callee_computes"})
			bounds = append(bounds, bat{i, "request"}, bat{i, "response"})
		} else {
			stepAt = append(stepAt, at{i, "computes"})
			bounds = append(bounds, bat{i, "handoff"})
		}
	}
	stepAt = append(stepAt, at{-1, "computes"})
	bounds = append(bounds, bat{-1, "output"})
	if len(stepAt) != len(a.Steps) || len(bounds) != len(a.Boundaries) {
		return nil // the analysis stopped before laying out the flow
	}
	for i, s := range a.Steps {
		p.Steps = append(p.Steps, PanelStep{Index: i, Label: s.Label(), Team: s.Team(), Role: s.RoleLabel(), Stage: stepAt[i].stage, Key: stepAt[i].key})
	}
	for i, b := range a.Boundaries {
		pb := PanelBoundary{Index: i, Label: b.RoleLabel(), Stage: bounds[i].stage, Part: bounds[i].part, Ref: b.Ref, Fields: []PanelField{}}
		pb.Path, pb.ReadOnly = ed.IfacePath(f, b.Ref)
		if b.Iface != nil {
			pb.Name, pb.Owner, pb.Imported = b.Iface.Name, b.Iface.Owner, b.Iface.Imported
			carried := map[string]string{}
			for _, it := range b.Items {
				if it.Row != "" && it.Status != flow.Missing {
					carried[it.Field] = it.Row
				}
			}
			for _, fd := range b.Iface.Fields {
				pb.Fields = append(pb.Fields, PanelField{Name: fd.Name, Type: fd.Type, Required: fd.Required, Local: fd.Local, Row: carried[fd.Name]})
			}
		}
		p.Boundaries = append(p.Boundaries, pb)
	}

	pinned := map[string]bool{}
	for _, st := range f.Stages {
		for _, id := range append(slices.Clone(st.Computes), st.CalleeComputes...) {
			pinned[id] = true
		}
	}
	for _, id := range f.Output.Computes {
		pinned[id] = true
	}
	lo := ed.earliest(a, pinned)
	last := len(a.Steps) - 1
	for _, r := range a.Engine.Graph.Order {
		if r.IsInput() && r.SourceKind() != "request" {
			continue // the page shows only values that can cross a boundary
		}
		pr := PanelRow{ID: r.ID, Name: r.DisplayName(), Type: flow.RowType(r), Input: r.IsInput(), Step: -1}
		if !r.IsInput() {
			pr.Step, pr.Pinned = a.Place[r.ID], pinned[r.ID]
			hi := last
			for _, u := range a.Engine.Graph.Users[r.ID] {
				if s, ok := a.Place[u]; ok && s < hi {
					hi = s
				}
			}
			for _, sd := range r.CallSends {
				for _, b := range a.Boundaries {
					if b.Role == flow.RoleRequest && b.StageID == sd.Target && b.Index-1 < hi {
						hi = b.Index - 1
					}
				}
			}
			for s := lo(r.ID); s <= hi; s++ {
				pr.Can = append(pr.Can, s)
			}
		}
		for _, b := range a.Boundaries {
			for _, it := range b.Items {
				if it.Row == r.ID && (it.Status == flow.Missing || it.Status == flow.Warn) {
					pr.Marks = append(pr.Marks, PanelMark{Boundary: b.Index, Status: it.Status, Field: it.Field, Note: it.Reason(), Fix: it.Fix})
				}
			}
		}
		p.Rows[r.ID] = pr
	}
	return p
}

// earliest returns, for a derived row, the first step every value it needs
// can reach: a row that is pinned stays where it is, and a row placed
// automatically can move as early as what it needs allows.
func (ed *Editor) earliest(a *flow.Analysis, pinned map[string]bool) func(string) int {
	memo := map[string]int{}
	var lo func(id string) int
	avail := func(r *sheet.Row) int {
		switch {
		case r.IsInput() && r.SourceKind() == "call":
			stage, _, _ := strings.Cut(r.SourcePath(), ".")
			for _, b := range a.Boundaries {
				if b.Role == flow.RoleResponse && b.StageID == stage {
					return b.Index - 1
				}
			}
			return 0
		case r.IsInput():
			return 0
		case pinned[r.ID]:
			return a.Place[r.ID]
		}
		return lo(r.ID)
	}
	lo = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		memo[id] = 0 // a cycle cannot happen in a sheet that validates
		m := 0
		for _, d := range a.Engine.Graph.Deps[id] {
			if r := a.Sheet.ByID[d]; r != nil {
				m = max(m, avail(r))
			}
		}
		memo[id] = m
		return m
	}
	return lo
}
