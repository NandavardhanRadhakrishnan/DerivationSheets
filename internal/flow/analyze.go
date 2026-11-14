package flow

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/analysis"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/iface"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/validate"
)

// Boundary roles.
const (
	RoleInput    = "input"
	RoleHandoff  = "handoff"
	RoleRequest  = "request"
	RoleResponse = "response"
	RoleOutput   = "output"
)

// Step roles.
const (
	StepProducer = "producer"
	StepCaller   = "caller"
	StepCallee   = "callee"
)

// Item statuses in a boundary report.
const (
	Carried = "carried" // needed downstream, and the interface carries it
	Missing = "missing" // needed downstream, and the interface lacks it
	Unused  = "unused"  // the interface carries it, but nothing downstream needs it
	Warn    = "warning" // carried, but the type or requiredness does not fit
)

// Analysis is the result of slicing a flow.
type Analysis struct {
	Flow   *Flow
	Sheet  *sheet.Sheet
	Engine *eval.Engine
	// SheetDir is where the end-to-end sheet was loaded from.
	SheetDir string
	// SheetRev is the commit the sheet came from, when it is in a git source.
	SheetRev string

	Boundaries []*Boundary
	Steps      []*Step
	// Place maps each derived row to the index of the step that computes it.
	Place map[string]int
	// Transit maps step index -> row -> "in" or "kept": the value reaches
	// that step without being computed there.
	Transit map[int]map[string]string
	// pinned holds the rows a step's computes list names.
	pinned map[string]bool

	Errors   []Problem
	Warnings []Problem
	// Revisions of git sources used.
	Revisions map[string]string

	overlay Overlay
}

// Pinned reports whether a step's computes list names the row. A derived row
// nobody names is placed by analysis, in the latest step able to serve it.
func (a *Analysis) Pinned(row string) bool { return a.pinned[row] }

// loadIface reads an interface file, from the overlay when it has it.
func (a *Analysis) loadIface(path string) (*iface.Interface, error) {
	if a.overlay != nil {
		if abs, err := filepath.Abs(path); err == nil {
			if data, ok := a.overlay[filepath.Clean(abs)]; ok {
				return iface.Parse(path, data)
			}
		}
	}
	return iface.Load(path)
}

// Boundary is one interface the flow crosses, in order.
type Boundary struct {
	Index   int
	Role    string
	StageID string
	Ref     string
	Iface   *iface.Interface
	Carries map[string]string // explicit field -> row
	// carry maps row ID -> the field carrying it.
	carry map[string]*Carry
	// need maps row ID -> descriptions of who needs it beyond this boundary.
	need  map[string][]string
	Items []Item
}

// Carry says how a row is carried by a boundary.
type Carry struct {
	Field *iface.Field
	Auto  bool // matched by name rather than listed in carries
}

// CarryOf returns how a boundary carries a row, or nil.
func (b *Boundary) CarryOf(row string) *Carry { return b.carry[row] }

// Needs reports whether a row must cross the boundary.
func (b *Boundary) Needs(row string) bool { return len(b.need[row]) > 0 }

// Item is one line of a boundary report.
type Item struct {
	Status    string
	Field     string
	Row       string
	Auto      bool
	Consumers []string
	Note      string
	// Fix is what would make a warning's field fit, when changing the
	// field would: "required", or "type:<type>".
	Fix string
}

// Step is a stretch of the flow computed by one team.
type Step struct {
	Index int
	Owner string
	Role  string
	// Rows computed here, in evaluation order.
	Rows []string
	// KeepsFrom is the caller's step before a call, for the step that
	// continues after the response; -1 otherwise.
	KeepsFrom int
	pins      []string
	pinsAt    string // flow.toml location of the computes list
}

// Overlay holds the content of interface files not yet saved, by cleaned
// absolute path. Analysis reads them instead of the files on disk.
type Overlay map[string][]byte

// Analyze slices a flow. resolve fetches interfaces from declared sources.
func Analyze(f *Flow, resolve *sources.Resolver) *Analysis {
	return AnalyzeWith(f, resolve, nil)
}

// AnalyzeWith slices a flow, reading interface files from ov when it has
// them. The end-to-end sheet is always read from disk.
func AnalyzeWith(f *Flow, resolve *sources.Resolver, ov Overlay) *Analysis {
	a := &Analysis{Flow: f, Place: map[string]int{}, Transit: map[int]map[string]string{}, overlay: ov}
	a.Errors = append(a.Errors, f.Problems...)
	defer func() {
		// Revisions are kept on failure too: that is when they matter most.
		a.Revisions = resolve.Fetched()
		for _, w := range resolve.Warnings {
			a.warn(0, "%s", w)
		}
	}()
	dir, rev, err := resolve.Resolve(f.SheetRef)
	if err != nil {
		a.fail(f.Line("sheet"), "end-to-end sheet %s: %v", f.SheetRef, err)
		return a
	}
	a.SheetDir, a.SheetRev = dir, rev
	s, err := sheet.Load(dir)
	if err != nil {
		a.fail(0, "end-to-end sheet: %v", err)
		return a
	}
	if rep := validate.Validate(s); !rep.OK() {
		a.fail(f.Line("sheet"), "end-to-end sheet %s has %d errors; run validate on it", s.File, rep.Count(validate.Error))
		return a
	}
	e, err := eval.New(s)
	if err != nil {
		a.fail(0, "end-to-end sheet: %v", err)
		return a
	}
	a.Sheet, a.Engine = s, e
	// Structural checks report everything they find; the crossing analysis
	// only runs on a flow without structural errors, so it adds no noise.
	a.build(resolve)
	a.mapCarries()
	a.place()
	if len(a.Errors) > 0 {
		return a
	}
	a.walk()
	a.report()
	return a
}

func (a *Analysis) fail(line int, format string, args ...any) {
	a.Errors = append(a.Errors, Problem{File: a.Flow.File, Line: line, Message: fmt.Sprintf(format, args...)})
}

func (a *Analysis) warn(line int, format string, args ...any) {
	a.Warnings = append(a.Warnings, Problem{File: a.Flow.File, Line: line, Message: fmt.Sprintf(format, args...)})
}

// build lays out boundaries and steps: input, then for each stage the step
// before it and its boundary (or, for a call, the request, the callee's step
// and the response), then the output.
func (a *Analysis) build(resolve *sources.Resolver) {
	f := a.Flow
	addB := func(role, stageID string, spec BoundarySpec, loc ...string) {
		b := &Boundary{Index: len(a.Boundaries), Role: role, StageID: stageID, Ref: spec.Interface, Carries: spec.Carries}
		a.Boundaries = append(a.Boundaries, b)
		if spec.Interface == "" {
			return
		}
		path, rev, err := resolve.Resolve(spec.Interface)
		if err != nil {
			a.fail(f.Line(loc...), "%s: %v", spec.Interface, err)
			return
		}
		in, err := a.loadIface(path)
		if err != nil {
			a.fail(f.Line(loc...), "%v", err)
			return
		}
		in.Ref, in.Revision = spec.Interface, rev
		b.Iface = in
	}
	addS := func(owner, role string, pins []string, loc string) *Step {
		st := &Step{Index: len(a.Steps), Owner: owner, Role: role, KeepsFrom: -1, pins: pins, pinsAt: loc}
		a.Steps = append(a.Steps, st)
		return st
	}
	addB(RoleInput, "input", f.Input, "input", "interface")
	keep, caller, callID := -1, "", ""
	continueAfterCall := func(owner string, loc string) string {
		if keep < 0 {
			return owner
		}
		switch {
		case owner == "":
			owner = caller
		case owner != caller:
			a.fail(a.Flow.Line(strings.Split(loc, ".")...), "after the %s call, the caller (%s) continues with the response; this step names %s", callID, caller, owner)
		}
		return owner
	}
	for i, st := range f.Stages {
		loc := fmt.Sprintf("stage.%d", i)
		owner := continueAfterCall(st.Owner(), loc)
		switch st.Kind {
		case KindCall:
			s := addS(owner, StepCaller, st.Computes, loc+".computes")
			s.KeepsFrom = keep
			addB(RoleRequest, st.ID, st.Request, "stage", fmt.Sprint(i), "request", "interface")
			addS(st.Callee, StepCallee, st.CalleeComputes, loc+".callee_computes")
			addB(RoleResponse, st.ID, st.Response, "stage", fmt.Sprint(i), "response", "interface")
			keep, caller, callID = s.Index, owner, st.ID
		default:
			s := addS(owner, StepProducer, st.Computes, loc+".computes")
			s.KeepsFrom = keep
			addB(RoleHandoff, st.ID, st.BoundarySpec, "stage", fmt.Sprint(i), "interface")
			keep = -1
		}
	}
	owner := continueAfterCall(f.Output.Producer, "output")
	s := addS(owner, StepProducer, f.Output.Computes, "output.computes")
	s.KeepsFrom = keep
	addB(RoleOutput, "output", BoundarySpec{Interface: f.Output.Interface}, "output", "interface")
}

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func last(s string) string { return s[strings.LastIndex(s, ".")+1:] }

// isLocal reports rows available wherever they are used: context, database
// and config inputs belong to the step that reads them.
func isLocal(r *sheet.Row) bool {
	return r.IsInput() && r.SourceKind() != "request" && r.SourceKind() != "call"
}

// callSource splits a call.<stage>.<field> source.
func callSource(r *sheet.Row) (stage, field string, ok bool) {
	if !r.IsInput() || r.SourceKind() != "call" {
		return "", "", false
	}
	stage, field, ok = strings.Cut(r.SourcePath(), ".")
	return stage, field, ok && field != ""
}

// callSend returns the row's send to a call stage, or nil.
func callSend(r *sheet.Row, stage string) *sheet.Send {
	for _, sd := range r.CallSends {
		if sd.Target == stage {
			return sd
		}
	}
	return nil
}

// mapCarries decides which row each interface field carries: listed in
// carries, or matched by name.
func (a *Analysis) mapCarries() {
	for _, b := range a.Boundaries {
		b.carry = map[string]*Carry{}
		b.need = map[string][]string{}
		if b.Iface == nil {
			continue
		}
		loc := a.carriesLine(b)
		explicit := map[string]bool{}
		fields := make([]string, 0, len(b.Carries))
		for f := range b.Carries {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, fname := range fields {
			rowID := b.Carries[fname]
			fd := b.Iface.Field(fname)
			r := a.Sheet.ByID[rowID]
			switch {
			case fd == nil:
				a.fail(loc, "carries %q, but %s has no such field", fname, b.Iface.Name)
			case r == nil:
				a.fail(loc, "carries %s = %q, but the sheet has no such row", fname, rowID)
			default:
				b.carry[rowID] = &Carry{Field: fd}
				explicit[fname] = true
			}
		}
		for i := range b.Iface.Fields {
			fd := &b.Iface.Fields[i]
			if explicit[fd.Name] || fd.Type == "object" {
				continue
			}
			var cands []*sheet.Row
			for _, r := range a.Sheet.Rows {
				if b.carry[r.ID] != nil || isLocal(r) {
					continue
				}
				if exact, ok := matches(b, fd.Name, r); ok && exact {
					// A call.* source or a send to this call names the field
					// exactly; it beats any match by name.
					cands = []*sheet.Row{r}
					break
				} else if ok {
					cands = append(cands, r)
				}
			}
			switch len(cands) {
			case 0:
			case 1:
				exact, _ := matches(b, fd.Name, cands[0])
				b.carry[cands[0].ID] = &Carry{Field: fd, Auto: !exact && b.Role != RoleOutput && b.Role != RoleInput}
			default:
				ids := make([]string, len(cands))
				for k, c := range cands {
					ids[k] = c.ID
				}
				a.warn(loc, "%s field %s matches several rows (%s); list it under carries", b.Iface.Name, fd.Name, strings.Join(ids, ", "))
			}
		}
	}
}

// matches says whether a field name refers to a row. The input interface
// matches request paths exactly and the output matches send fields exactly;
// boundaries in between match by name, ignoring case and separators.
//
// In a team's own piece, a call's request and response are matched exactly:
// rows sent with target = <stage> by their send field, and rows read from
// call.<stage>.<field> by that field. exact reports such a match.
func matches(b *Boundary, field string, r *sheet.Row) (exact, ok bool) {
	switch b.Role {
	case RoleInput:
		return true, r.SourceKind() == "request" && r.SourcePath() == field
	case RoleOutput:
		return true, r.Send != nil && r.Send.Field == field
	case RoleRequest:
		if sd := callSend(r, b.StageID); sd != nil {
			return true, sd.Field == field
		}
	case RoleResponse:
		if stage, f, isCall := callSource(r); isCall && stage == b.StageID {
			return true, f == field
		}
	}
	if _, _, isCall := callSource(r); isCall {
		return false, false // a call's response is only matched at that call
	}
	return false, nameMatch(field, r)
}

func nameMatch(field string, r *sheet.Row) bool {
	n, nl := norm(field), norm(last(field))
	keys := []string{norm(r.ID)}
	if r.Send != nil {
		keys = append(keys, norm(r.Send.Field))
	}
	if r.IsInput() {
		keys = append(keys, norm(r.SourcePath()), norm(last(r.SourcePath())))
	}
	for _, k := range keys {
		if k == n || k == nl {
			return true
		}
	}
	return false
}

func (a *Analysis) carriesLine(b *Boundary) int {
	switch b.Role {
	case RoleInput:
		return a.Flow.Line("input", "carries")
	case RoleOutput:
		return a.Flow.Line("output", "interface")
	}
	for i, st := range a.Flow.Stages {
		if st.ID != b.StageID {
			continue
		}
		switch b.Role {
		case RoleRequest:
			return a.Flow.Line("stage", fmt.Sprint(i), "request", "carries")
		case RoleResponse:
			return a.Flow.Line("stage", fmt.Sprint(i), "response", "carries")
		}
		return a.Flow.Line("stage", fmt.Sprint(i), "carries")
	}
	return 0
}

// place assigns each derived row to a step: the step whose computes list
// names it, or else the latest step that is not a callee, early enough for
// every row that uses it and every boundary that carries it.
func (a *Analysis) place() {
	pinned := map[string]bool{}
	a.pinned = pinned
	for _, st := range a.Steps {
		for _, id := range st.pins {
			loc := a.Flow.Line(strings.Split(st.pinsAt, ".")...)
			r := a.Sheet.ByID[id]
			switch {
			case r == nil:
				a.fail(loc, "computes %q, but the sheet has no such row", id)
			case r.IsInput():
				a.fail(loc, "computes %q, but it is an input (source %s), not a derived row", id, r.Source)
			case pinned[id]:
				a.fail(loc, "%q is listed as computed by more than one step", id)
			default:
				pinned[id] = true
				a.Place[id] = st.Index
			}
		}
	}
	lastStep := len(a.Steps) - 1
	order := a.Engine.Graph.Order
	for i := len(order) - 1; i >= 0; i-- {
		r := order[i]
		if r.IsInput() || pinned[r.ID] {
			continue
		}
		bound := lastStep
		for _, u := range a.Engine.Graph.Users[r.ID] {
			if p, ok := a.Place[u]; ok && p < bound {
				bound = p
			}
		}
		for _, b := range a.Boundaries {
			if b.Role != RoleInput && b.Role != RoleOutput && b.carry[r.ID] != nil && b.Index-1 < bound {
				bound = b.Index - 1
			}
		}
		for bound > 0 && a.Steps[bound].Role == StepCallee {
			bound--
		}
		if bound < 0 {
			bound = 0
		}
		a.Place[r.ID] = bound
	}
	for _, r := range order {
		if p, ok := a.Place[r.ID]; ok {
			a.Steps[p].Rows = append(a.Steps[p].Rows, r.ID)
		}
	}
}

// walk follows every value from where it is produced to each place that
// needs it, and records the boundaries it must cross. A value the caller
// already holds is kept across a call instead of crossing it.
func (a *Analysis) walk() {
	outIdx := len(a.Steps) // consumers past the last step need the output boundary
	describe := func(id string, step int) string {
		if step == outIdx {
			return "the output"
		}
		r := a.Sheet.ByID[id]
		owner := a.Steps[step].Owner
		if owner == "" {
			owner = fmt.Sprintf("step %d", step+1)
		}
		return fmt.Sprintf("%s (%s)", r.DisplayName(), owner)
	}
	need := func(d *sheet.Row, consumer string, c int) {
		if isLocal(d) {
			return
		}
		o := -1
		if !d.IsInput() {
			o = a.Place[d.ID]
		}
		if stage, _, isCall := callSource(d); isCall {
			// A response field enters at the call's response: it starts in
			// the callee's step.
			o = -2
			for _, b := range a.Boundaries {
				if b.Role == RoleResponse && b.StageID == stage {
					o = b.Index - 1
				}
			}
			if o == -2 {
				a.fail(0, "%s reads %s, but the flow has no call stage %q", d.ID, d.Source, stage)
				return
			}
		}
		if o > c {
			a.fail(a.Flow.Line(strings.Split(a.Steps[o].pinsAt, ".")...),
				"%s is computed by %s, but %s needs it earlier; move it to an earlier step",
				d.DisplayName(), a.ownerOf(o), consumer)
			return
		}
		for j := o; j < c; {
			if j >= 0 && j+1 < len(a.Boundaries)-1 && a.Boundaries[j+1].Role == RoleRequest && c >= j+2 && o != j+1 {
				j += 2 // the caller keeps it across the call
				a.mark(j, d.ID, "kept", o)
				continue
			}
			b := a.Boundaries[j+1]
			if !slices.Contains(b.need[d.ID], consumer) {
				b.need[d.ID] = append(b.need[d.ID], consumer)
			}
			j++
			if j < outIdx {
				a.mark(j, d.ID, "in", o)
			}
		}
	}
	for _, r := range a.Engine.Graph.Order {
		p, ok := a.Place[r.ID]
		if !ok {
			continue
		}
		for _, d := range a.Engine.Graph.Deps[r.ID] {
			need(a.Sheet.ByID[d], describe(r.ID, p), p)
		}
	}
	for _, r := range a.Engine.Graph.Order {
		if r.Send != nil {
			need(r, "the output", outIdx)
		}
		for _, sd := range r.CallSends {
			found := false
			for _, b := range a.Boundaries {
				if b.Role == RoleRequest && b.StageID == sd.Target {
					found = true
					callee := a.Steps[b.Index].Team()
					need(r, callee+" (callee)", b.Index)
				}
			}
			if !found {
				a.fail(0, "%s is sent to %q, but the flow has no call stage with that id", r.ID, sd.Target)
			}
		}
	}
}

func (a *Analysis) mark(step int, row, how string, origin int) {
	if step == origin {
		return
	}
	if a.Transit[step] == nil {
		a.Transit[step] = map[string]string{}
	}
	if a.Transit[step][row] == "" {
		a.Transit[step][row] = how
	}
}

func (a *Analysis) ownerOf(step int) string {
	if o := a.Steps[step].Owner; o != "" {
		return o
	}
	return fmt.Sprintf("step %d", step+1)
}

// report builds each boundary's items.
func (a *Analysis) report() {
	domains := analysis.Domains(a.Sheet)
	for _, b := range a.Boundaries {
		if b.Iface == nil {
			continue
		}
		used := map[string]bool{}
		for _, r := range a.Engine.Graph.Order {
			consumers := b.need[r.ID]
			c := b.carry[r.ID]
			switch {
			case len(consumers) == 0 && c != nil:
				used[c.Field.Name] = true
				b.Items = append(b.Items, Item{Status: Unused, Field: c.Field.Name, Row: r.ID, Auto: c.Auto,
					Note: "carries " + r.DisplayName() + ", but nothing after this boundary needs it"})
			case len(consumers) == 0:
			case c == nil:
				b.Items = append(b.Items, Item{Status: Missing, Field: proposedField(r), Row: r.ID, Consumers: consumers,
					Note: a.missingNote(b, r)})
			default:
				used[c.Field.Name] = true
				it := Item{Status: Carried, Field: c.Field.Name, Row: r.ID, Auto: c.Auto, Consumers: consumers}
				if note, fix := fit(b, c.Field, r, domains); note != "" {
					it.Status, it.Note, it.Fix = Warn, note, fix
				}
				b.Items = append(b.Items, it)
			}
		}
		for _, fd := range b.Iface.Fields {
			if used[fd.Name] || fd.Type == "object" || hasChildUsed(fd.Name, used) {
				continue
			}
			it := Item{Status: Unused, Field: fd.Name, Note: "not used by this flow"}
			// A required field nothing in the flow produces: a gap to decide
			// on a proposed interface; on an existing one, its producer
			// already sets it (an ID, a timestamp) outside this flow.
			if b.Role != RoleInput && fd.Required {
				if b.Iface.Status == iface.StatusProposed {
					it.Status, it.Note = Missing, "required, but nothing in the flow produces it"
				} else {
					it.Note = "required; its producer sets it outside this flow"
				}
			}
			b.Items = append(b.Items, it)
		}
		rank := map[string]int{Missing: 0, Warn: 1, Carried: 2, Unused: 3}
		sort.SliceStable(b.Items, func(i, j int) bool { return rank[b.Items[i].Status] < rank[b.Items[j].Status] })
	}
}

func hasChildUsed(name string, used map[string]bool) bool {
	for u := range used {
		if strings.HasPrefix(u, name+".") {
			return true
		}
	}
	return false
}

// proposedField is the name suggested for a missing field: the row's send
// field when it has one, else its ID in camelCase.
func proposedField(r *sheet.Row) string {
	if r.Send != nil && r.Send.Field != "" {
		return last(r.Send.Field)
	}
	if r.SourceKind() == "request" {
		return last(r.SourcePath())
	}
	parts := strings.Split(r.ID, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func (a *Analysis) missingNote(b *Boundary, r *sheet.Row) string {
	switch {
	case b.Role == RoleOutput || b.Role == RoleInput:
		return "not in the interface"
	case b.Iface.Status == iface.StatusExisting && b.Iface.Owner != "":
		return fmt.Sprintf("not in the interface; ask %s to add it", b.Iface.Owner)
	}
	return "not in the interface; add it"
}

// fit checks a carried field against the row: requiredness and type. fix
// says what change to the field would make it fit, when one would.
func fit(b *Boundary, fd *iface.Field, r *sheet.Row, domains map[string][]any) (note, fix string) {
	always := !(r.IsInput() && r.Usage == sheet.UsageSituational) && !(r.Send != nil && r.Send.Usage == sheet.UsageSituational && b.Role == RoleOutput)
	if !fd.Required && always && b.Role != RoleOutput {
		return fmt.Sprintf("optional, but the flow always needs %s", r.DisplayName()), "required"
	}
	if t := rowType(r); t != "" && fd.Type != "" && !sameType(t, fd.Type) {
		return fmt.Sprintf("type is %s, but %s is %s", fd.Type, r.DisplayName(), t), "type:" + t
	}
	if len(fd.Enum) > 0 {
		var outside []string
		for _, v := range domains[r.ID] {
			if s := sheet.FormatValue(v); !slices.Contains(fd.Enum, s) && !slices.Contains(rejected(r), s) {
				outside = append(outside, s)
			}
		}
		if len(outside) > 0 {
			return fmt.Sprintf("allows %s, but %s can be %s", strings.Join(fd.Enum, ", "), r.DisplayName(), strings.Join(outside, ", ")), ""
		}
	}
	return "", ""
}

func sameType(a, b string) bool {
	if a == b {
		return true
	}
	// Dates often travel as strings.
	return (a == "date" || a == "datetime") && b == "string" || (b == "date" || b == "datetime") && a == "string"
}

// rowType returns the row's type where it is known.
func rowType(r *sheet.Row) string {
	if r.IsInput() {
		return r.Type
	}
	var vals []any
	switch {
	case r.Table != nil:
		for _, rule := range r.Table.Rules {
			vals = append(vals, rule.Result)
		}
	case r.Lookup != nil && r.Lookup.Inline != nil:
		for _, v := range r.Lookup.Inline {
			vals = append(vals, v)
		}
	case r.Lookup != nil && r.Lookup.Data != nil:
		for _, e := range r.Lookup.Data.Entries {
			vals = append(vals, e.Value)
		}
	}
	t := ""
	for _, v := range vals {
		var vt string
		switch v.(type) {
		case float64:
			vt = "number"
		case bool:
			vt = "boolean"
		case string:
			vt = "string"
		default:
			return ""
		}
		if t != "" && t != vt {
			return ""
		}
		t = vt
	}
	if r.Send != nil && r.Send.Map != nil {
		return "" // mapped to target codes; the sent type may differ
	}
	return t
}

// Counts tallies items by status across all boundaries.
func (a *Analysis) Counts() map[string]int {
	out := map[string]int{}
	for _, b := range a.Boundaries {
		for _, it := range b.Items {
			out[it.Status]++
		}
	}
	return out
}

var rejectEq = regexp.MustCompile(`^\s*([a-z][a-z0-9_]*)\s*==\s*(?:"([^"]*)"|'([^']*)'|([A-Za-z0-9_.\-]+))\s*$`)

// rejected lists values a row can never pass on, because a reject of the
// form `row == value` on the row itself stops the request first.
func rejected(r *sheet.Row) []string {
	var out []string
	for _, rj := range r.Rejects {
		m := rejectEq.FindStringSubmatch(rj.When)
		if m != nil && m[1] == r.ID {
			out = append(out, m[2]+m[3]+m[4])
		}
	}
	return out
}

// RoleLabel names the boundary's role in the flow. The HTML view and the
// text report use the same words.
func (b *Boundary) RoleLabel() string {
	return map[string]string{
		RoleInput: "Input", RoleHandoff: "Handoff", RoleRequest: "Call request",
		RoleResponse: "Call response", RoleOutput: "Output",
	}[b.Role]
}

// Label is "Step n", counting from 1.
func (s *Step) Label() string { return fmt.Sprintf("Step %d", s.Index+1) }

// Team is the owner, or a placeholder when flow.toml names none.
func (s *Step) Team() string {
	if s.Owner != "" {
		return s.Owner
	}
	return "No team named"
}

// RoleLabel names the step's role in the words flow.toml uses: producer,
// caller, callee. A caller picking up again after its call is "Caller
// (continued)".
func (s *Step) RoleLabel() string {
	switch {
	case s.KeepsFrom >= 0:
		return "Caller (continued)"
	case s.Role == StepCaller:
		return "Caller"
	case s.Role == StepCallee:
		return "Callee"
	}
	return "Producer"
}

// Reason is the one-line explanation of an item, shared by the text
// report and the view: for a missing value, who needs it, then any note
// beyond "not in the interface" (whom to ask, where it could come from).
func (it Item) Reason() string {
	if it.Status != Missing || it.Row == "" {
		return it.Note
	}
	r := "missing; needed by " + strings.Join(it.Consumers, ", ")
	extra := strings.TrimPrefix(strings.TrimPrefix(it.Note, "not in the interface"), "; ")
	if extra != "" {
		r += "; " + extra
	}
	return r
}

// sortItems orders a boundary's items: missing, warnings, carried, unused.
func sortItems(b *Boundary) {
	rank := map[string]int{Missing: 0, Warn: 1, Carried: 2, Unused: 3}
	sort.SliceStable(b.Items, func(i, j int) bool { return rank[b.Items[i].Status] < rank[b.Items[j].Status] })
}

// RowType is the row's type where it is known: an input's declared type,
// or the one type every result of a table or lookup has.
func RowType(r *sheet.Row) string { return rowType(r) }
