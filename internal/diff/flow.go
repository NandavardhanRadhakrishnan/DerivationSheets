package diff

import (
	"bytes"
	"sort"
	"strconv"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
)

// BoundaryChange is what changed where a flow crosses from one team to
// another. Where is "input", "output", or "stage <id> <role>" with role
// handoff, request or response (the words impact findings use).
type BoundaryChange struct {
	Where string `json:"where,omitempty"`
	// Interface has a Path only when the boundary's interface reference
	// changed.
	Interface FieldChange `json:"interface,omitzero"`
	// Added and Dropped are fields the boundary now carries or no longer
	// carries; Rewired are fields that now carry a different row.
	Added   []string      `json:"added,omitempty"`
	Dropped []string      `json:"dropped,omitempty"`
	Rewired []FieldChange `json:"rewired,omitempty"`
}

// ComputeChange is a row whose computing team changed. Before and After are
// the team names as the flow writes them; empty means nobody computed it.
type ComputeChange struct {
	Row    string `json:"row,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// FlowChange is the difference between two versions of one flow.
type FlowChange struct {
	Name        string           `json:"name,omitempty"` // the flow's own `flow` name
	Path        string           `json:"path,omitempty"`
	OldPath     string           `json:"old_path,omitempty"`
	Status      Status           `json:"status,omitempty"`
	Stages      []FieldChange    `json:"stages,omitempty"` // stages added or removed, or whose kind or team changed
	Boundaries  []BoundaryChange `json:"boundaries,omitempty"`
	Computes    []ComputeChange  `json:"computes,omitempty"`
	TextOnly    bool             `json:"text_only,omitempty"`
	Unparseable string           `json:"unparseable,omitempty"` // "base" or "head"
}

// DiffFlow compares two versions of a flow.toml. A nil side means the flow
// did not exist there.
func DiffFlow(base, head []byte) (FlowChange, error) {
	var fc FlowChange
	bf, bBad := parseFlow(base)
	hf, hBad := parseFlow(head)
	switch {
	case bBad:
		fc.Unparseable = "base"
	case hBad:
		fc.Unparseable = "head"
	}
	fc.Name = hf.Slug
	if fc.Name == "" {
		fc.Name = bf.Slug
	}
	switch {
	case base == nil:
		fc.Status = Added
	case head == nil:
		fc.Status = Removed
	default:
		fc.Status = Changed
	}
	if fc.Unparseable != "" {
		return fc, nil
	}

	fc.Stages = diffStages(bf, hf)
	if base != nil && head != nil {
		fc.Boundaries = diffBoundaries(bf, hf)
	}
	fc.Computes = diffComputes(bf, hf)
	fc.TextOnly = textOnly(base, head)
	return fc, nil
}

func parseFlow(data []byte) (*flow.Flow, bool) {
	if data == nil {
		return &flow.Flow{}, false
	}
	f, err := flow.Parse(".", flow.FileName, data)
	if err != nil {
		return &flow.Flow{}, true
	}
	return f, false
}

// textOnly reports that two non-empty versions decode to the same content
// and differ only in formatting or comments.
func textOnly(base, head []byte) bool {
	if base == nil || head == nil || bytes.Equal(base, head) {
		return false
	}
	bm, _ := decodeSide(base)
	hm, _ := decodeSide(head)
	return equalValues(bm, hm)
}

func stageByID(f *flow.Flow) map[string]flow.StageSpec {
	m := map[string]flow.StageSpec{}
	for _, s := range f.Stages {
		m[s.ID] = s
	}
	return m
}

func stageSummary(s flow.StageSpec) string { return s.Kind + " by " + s.Owner() }

func diffStages(b, h *flow.Flow) []FieldChange {
	before := stageByID(b)
	inHead := map[string]bool{}
	var out []FieldChange
	for _, s := range h.Stages {
		inHead[s.ID] = true
		old, ok := before[s.ID]
		if !ok {
			out = append(out, FieldChange{Path: "stage " + s.ID, After: stageSummary(s)})
			continue
		}
		for _, a := range []struct{ name, before, after string }{
			{"kind", old.Kind, s.Kind},
			{"producer", old.Producer, s.Producer},
			{"caller", old.Caller, s.Caller},
			{"callee", old.Callee, s.Callee},
		} {
			if a.before != a.after {
				out = append(out, FieldChange{Path: "stage " + s.ID + "." + a.name, Before: a.before, After: a.after})
			}
		}
	}
	for _, s := range b.Stages {
		if !inHead[s.ID] {
			out = append(out, FieldChange{Path: "stage " + s.ID, Before: stageSummary(s)})
		}
	}
	return out
}

type boundary struct {
	where string
	spec  flow.BoundarySpec
}

// boundaries lists the places a flow crosses between teams, in flow order.
func boundaries(f *flow.Flow) []boundary {
	out := []boundary{{"input", f.Input}}
	for _, s := range f.Stages {
		if s.Kind == flow.KindCall {
			out = append(out,
				boundary{"stage " + s.ID + " request", s.Request},
				boundary{"stage " + s.ID + " response", s.Response})
			continue
		}
		out = append(out, boundary{"stage " + s.ID + " handoff", s.BoundarySpec})
	}
	return append(out, boundary{"output", flow.BoundarySpec{Interface: f.Output.Interface}})
}

func diffBoundaries(b, h *flow.Flow) []BoundaryChange {
	before := map[string]flow.BoundarySpec{}
	for _, x := range boundaries(b) {
		before[x.where] = x.spec
	}
	var out []BoundaryChange
	for _, x := range boundaries(h) {
		old, ok := before[x.where]
		if !ok {
			continue
		}
		bc := BoundaryChange{Where: x.where}
		if old.Interface != x.spec.Interface {
			bc.Interface = FieldChange{Path: "interface", Before: old.Interface, After: x.spec.Interface}
		}
		for _, name := range sortedKeys(x.spec.Carries) {
			oldRow, had := old.Carries[name]
			switch {
			case !had:
				bc.Added = append(bc.Added, name)
			case oldRow != x.spec.Carries[name]:
				bc.Rewired = append(bc.Rewired, FieldChange{Path: name, Before: oldRow, After: x.spec.Carries[name]})
			}
		}
		for _, name := range sortedKeys(old.Carries) {
			if _, has := x.spec.Carries[name]; !has {
				bc.Dropped = append(bc.Dropped, name)
			}
		}
		if bc.Interface.Path != "" || len(bc.Added)+len(bc.Dropped)+len(bc.Rewired) > 0 {
			out = append(out, bc)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// computedBy maps each row to the team that computes it: a stage's owner,
// a call's callee for callee_computes, or the output's producer.
func computedBy(f *flow.Flow) map[string]string {
	m := map[string]string{}
	for _, s := range f.Stages {
		for _, r := range s.Computes {
			m[r] = s.Owner()
		}
		for _, r := range s.CalleeComputes {
			m[r] = s.Callee
		}
	}
	for _, r := range f.Output.Computes {
		m[r] = f.Output.Producer
	}
	return m
}

func diffComputes(b, h *flow.Flow) []ComputeChange {
	before, after := computedBy(b), computedBy(h)
	rows := map[string]bool{}
	for r := range before {
		rows[r] = true
	}
	for r := range after {
		rows[r] = true
	}
	names := make([]string, 0, len(rows))
	for r := range rows {
		names = append(names, r)
	}
	sort.Strings(names)
	var out []ComputeChange
	for _, r := range names {
		if before[r] != after[r] {
			out = append(out, ComputeChange{Row: r, Before: before[r], After: after[r]})
		}
	}
	return out
}

// InterfaceChange is the difference between two versions of an interface
// file: the fields that cross a boundary.
type InterfaceChange struct {
	Name        string        `json:"name,omitempty"` // the interface's own `interface` name
	Path        string        `json:"path,omitempty"`
	OldPath     string        `json:"old_path,omitempty"`
	Status      Status        `json:"status,omitempty"`
	Meta        []FieldChange `json:"meta,omitempty"`
	Fields      []FieldChange `json:"fields,omitempty"`
	TextOnly    bool          `json:"text_only,omitempty"`
	Unparseable string        `json:"unparseable,omitempty"` // "base" or "head"
}

var interfaceLabels = map[string]bool{"title": true, "description": true, "imported": true}

// DiffInterface compares two versions of an interface file. A nil side
// means the file did not exist there. Fields are matched by name.
func DiffInterface(base, head []byte) (InterfaceChange, error) {
	var ic InterfaceChange
	bm, bBad := decodeSide(base)
	hm, hBad := decodeSide(head)
	switch {
	case bBad:
		ic.Unparseable = "base"
	case hBad:
		ic.Unparseable = "head"
	}
	ic.Name, _ = hm["interface"].(string)
	if ic.Name == "" {
		ic.Name, _ = bm["interface"].(string)
	}
	switch {
	case base == nil:
		ic.Status = Added
	case head == nil:
		ic.Status = Removed
	default:
		ic.Status = Changed
	}
	if ic.Unparseable != "" {
		return ic, nil
	}
	for _, f := range diffMaps("", without(bm, "field", "interface"), without(hm, "field", "interface"), sheetListKey) {
		f.Label = interfaceLabels[firstSegment(f.Path)]
		ic.Meta = append(ic.Meta, f)
	}
	ic.Fields = diffInterfaceFields(fieldsOf(bm), fieldsOf(hm))
	ic.TextOnly = textOnly(base, head)
	return ic, nil
}

type namedField struct {
	name  string
	field map[string]any
}

func fieldsOf(m map[string]any) []namedField {
	list, _ := m["field"].([]any)
	var out []namedField
	for i, e := range list {
		f, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := f["name"].(string)
		if name == "" {
			name = "#" + strconv.Itoa(i+1)
		}
		out = append(out, namedField{name, f})
	}
	return out
}

func fieldSummary(f map[string]any) string {
	s, _ := f["type"].(string)
	if req, _ := f["required"].(bool); req {
		s += ", required"
	}
	return s
}

func diffInterfaceFields(before, after []namedField) []FieldChange {
	byName := map[string]map[string]any{}
	for _, f := range before {
		byName[f.name] = f.field
	}
	inAfter := map[string]bool{}
	var out []FieldChange
	for _, f := range after {
		inAfter[f.name] = true
		old, ok := byName[f.name]
		if !ok {
			out = append(out, FieldChange{Path: "field " + f.name, After: fieldSummary(f.field)})
			continue
		}
		for _, c := range diffMaps("field["+f.name+"]", without(old, "name"), without(f.field, "name"), sheetListKey) {
			c.Label = c.Path == "field["+f.name+"].description"
			out = append(out, c)
		}
	}
	for _, f := range before {
		if !inAfter[f.name] {
			out = append(out, FieldChange{Path: "field " + f.name, Before: fieldSummary(f.field)})
		}
	}
	return out
}
