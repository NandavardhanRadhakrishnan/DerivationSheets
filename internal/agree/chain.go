package agree

import (
	"fmt"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// none stands for "no reject" in a reject check.
const none = "(none)"

// runner holds what every case of one run shares.
type runner struct {
	pm       *flow.Analysis
	mode     Mode
	report   *Report
	segs     []segment
	found    []*match          // each segment's team flow, or nil
	notFound []string          // why a segment has no team flow
	bySource map[string]string // the PM's input rows by source
}

func newRunner(pm *flow.Analysis, mode Mode, r *Report) *runner {
	ru := &runner{pm: pm, mode: mode, report: r, segs: segments(pm), bySource: map[string]string{}}
	ru.found = make([]*match, len(ru.segs))
	ru.notFound = make([]string, len(ru.segs))
	for _, row := range pm.Sheet.Rows {
		if _, has := ru.bySource[row.Source]; row.IsInput() && !has {
			ru.bySource[row.Source] = row.ID
		}
	}
	return ru
}

// payload is what crosses a boundary in one case: the values on the wire,
// and why a field that belongs there is not known.
type payload struct {
	values  map[string]any
	pending map[string]string
}

// outcome is how a segment's run ended: with a payload, a reject (by the
// team or by a callee it called), or neither, and why.
type outcome struct {
	out    payload
	ok     bool
	reject string
	why    string
}

// chain runs one case end to end. Each segment is fed what the segment
// before it sent; a callee segment runs only through its caller.
func (ru *runner) chain(c pmCase) {
	var in payload
	started, broken := false, ""
	for i, sg := range ru.segs {
		if sg.Callee {
			continue
		}
		if ru.rejectsBefore(sg, c) {
			return
		}
		if !started {
			in, started = ru.expected(c, ru.pm.Boundaries[sg.In]), true
		}
		if broken != "" {
			ru.cannotTell(i, c, broken, nil)
			continue
		}
		o := ru.run(i, c, in, true)
		switch {
		case o.reject != "":
			return
		case !o.ok:
			broken = fmt.Sprintf("%s sent no %s: %s", sg.Team, ifaceName(ru.pm.Boundaries[sg.Out]), o.why)
		default:
			in = o.out
		}
	}
}

// expected is the PM's payload on a boundary: every field that carries a
// known PM value. Any other field is pending, so a team reading it cannot
// tell rather than reject for a missing input.
func (ru *runner) expected(c pmCase, b *flow.Boundary) payload {
	p := payload{values: map[string]any{}, pending: map[string]string{}}
	if b.Iface == nil {
		return p
	}
	for _, f := range b.Iface.Fields {
		if v, ok := wire(ru.pm, c, b, f.Name); ok {
			eval.SetPath(p.values, f.Name, v)
		} else {
			p.pending[f.Name] = fmt.Sprintf("the PM's case has no value for %s on %s", f.Name, b.Iface.Name)
		}
	}
	return p
}

// run evaluates segment i's team sheet for one case, fed in, resolving its
// calls through the callee segments. emit says whether this run reports
// checks: a callee run through its caller in isolated mode does not, since
// the callee's own run reports.
func (ru *runner) run(i int, c pmCase, in payload, emit bool) outcome {
	sg := ru.segs[i]
	if emit && ru.rejectsBefore(sg, c) {
		emit = false
	}
	m := ru.found[i]
	if m == nil {
		if emit {
			ru.cannotTell(i, c, ru.notFound[i], nil)
		}
		return outcome{why: ru.notFound[i]}
	}
	a := m.A
	resp := map[string]payload{}  // call responses, by the team's stage id
	failed := map[string]string{} // calls left unanswered, and why
	made := map[int]bool{}        // PM request indexes of the calls made
	var res *eval.Result
	for pass := 0; pass <= len(sg.Calls); pass++ {
		res = a.Engine.Evaluate(ru.inputs(a, m, c, in, resp, failed))
		if res.Reject != nil || res.Err != nil {
			break
		}
		progressed := false
		for _, req := range sg.Calls {
			id := m.StageOf[req]
			if made[req] || requestGap(a, res, id) != "" {
				continue
			}
			made[req], progressed = true, true
			sent := res.CallPayloads[id]
			if emit {
				ru.compare(c, req, sg.Team, sent, a, res, id)
			}
			k := ru.calleeOf(req)
			if k < 0 {
				failed[id] = fmt.Sprintf("the call %s has no callee step", id)
				continue
			}
			if sent == nil {
				sent = map[string]any{}
			}
			o := ru.run(k, c, payload{values: sent}, emit && ru.mode == EndToEnd)
			switch {
			case o.reject != "":
				// The callee's own run reports its reject.
				if emit && ru.mode == Isolated && ru.rejectsIn(sg, c) {
					ck := ru.rejectBase(i, c)
					ck.Status = CannotTell
					ck.Note = fmt.Sprintf("the call %s rejected first, with %s", ru.pm.Boundaries[req].StageID, o.reject)
					ru.add(ck)
				}
				return outcome{reject: o.reject}
			case !o.ok:
				failed[id] = fmt.Sprintf("the call %s is not answered: %s", id, o.why)
			default:
				resp[id] = o.out
			}
		}
		if !progressed {
			break
		}
	}
	// Calls never made: evaluate once more so what depends on them says why.
	if res.Reject == nil && res.Err == nil {
		more := false
		for _, req := range sg.Calls {
			if id := m.StageOf[req]; !made[req] {
				failed[id] = fmt.Sprintf("the call %s is not made: %s", id, requestGap(a, res, id))
				more = true
			}
		}
		if more {
			res = a.Engine.Evaluate(ru.inputs(a, m, c, in, resp, failed))
		}
	}
	switch {
	case res.Err != nil:
		why := fmt.Sprintf("the sheet %s fails at row %s: %s", a.Sheet.Slug, res.Err.Row, res.Err.Message)
		if emit {
			ru.cannotTell(i, c, why, made)
		}
		return outcome{why: why}
	case res.Reject != nil:
		if emit {
			ru.teamReject(i, c, a, res.Reject)
		}
		return outcome{reject: res.Reject.Code}
	}
	if emit {
		ru.missedReject(i, c, a, res)
		for _, req := range sg.Calls {
			if made[req] {
				continue
			}
			id := m.StageOf[req]
			ru.compare(c, req, sg.Team, res.CallPayloads[id], a, res, id)
			if k := ru.calleeOf(req); k >= 0 && ru.mode == EndToEnd {
				ru.cannotTell(k, c, failed[id], nil)
			}
		}
		ru.compare(c, sg.Out, sg.Team, res.Payload, a, res, "")
	}
	return outcome{ok: true, out: ru.sent(sg.Team, a, res, ru.pm.Boundaries[sg.Out])}
}

// inputs builds the team sheet's input from its rows' sources.
func (ru *runner) inputs(a *flow.Analysis, m *match, c pmCase, in payload, resp map[string]payload, failed map[string]string) eval.Input {
	inp := eval.Input{}
	called := map[string]bool{}
	for _, id := range m.StageOf {
		called[id] = true
	}
	for _, r := range a.Sheet.Rows {
		if !r.IsInput() {
			continue
		}
		switch r.SourceKind() {
		case "request":
			feed(inp, r.ID, in, r.SourcePath())
		case "call":
			id, field, _ := strings.Cut(r.SourcePath(), ".")
			p, answered := resp[id]
			switch {
			case answered:
				feed(inp, r.ID, p, field)
			case failed[id] != "":
				inp[r.ID] = eval.Pending{Reason: failed[id]}
			case called[id]:
				inp[r.ID] = eval.Pending{Reason: fmt.Sprintf("the call %s is not answered yet", id)}
			default:
				inp[r.ID] = eval.Pending{Reason: fmt.Sprintf("the PM's flow shows no call %s", id)}
			}
		default: // context, config, db
			if pr := ru.bySource[r.Source]; pr != "" && c.Res.Status[pr] != eval.StatusUnknown {
				if v, ok := c.Res.Values[pr]; ok {
					inp[r.ID] = v
					continue
				}
			}
			// config and db fall back to the team's design-time data.
			if r.SourceKind() == "context" {
				inp[r.ID] = eval.Pending{Reason: "the PM's case has no value for " + r.Source}
			}
		}
	}
	return inp
}

// feed sets an input from a payload path: its value, pending when the
// value is not known, or nothing when the payload lacks it.
func feed(inp eval.Input, id string, p payload, path string) {
	if v, ok := eval.GetPath(p.values, path); ok {
		inp[id] = v
	} else if why, ok := p.pending[path]; ok {
		inp[id] = eval.Pending{Reason: why}
	}
}

// sent is what a team put on its outgoing boundary, with the fields it
// could not compute pending.
func (ru *runner) sent(team string, a *flow.Analysis, res *eval.Result, b *flow.Boundary) payload {
	p := payload{values: res.Payload, pending: map[string]string{}}
	if b.Iface == nil {
		return p
	}
	for _, f := range b.Iface.Fields {
		if _, ok := eval.GetPath(res.Payload, f.Name); ok {
			continue
		}
		if r := sender(a.Sheet, "", f.Name); r != nil {
			if why := notKnown(a, res, r, ""); why != "" {
				p.pending[f.Name] = fmt.Sprintf("%s cannot compute %s: %s", team, f.Name, why)
			}
		}
	}
	return p
}

// want is one expected field value.
type want struct {
	field string
	v     any
}

// wants are the PM's expected values on boundary bi in a case: none past
// the PM's reject.
func (ru *runner) wants(c pmCase, bi int) []want {
	b := ru.pm.Boundaries[bi]
	if _, p, ok := ru.pmReject(c); (ok && bi > p) || b.Iface == nil {
		return nil
	}
	var out []want
	for _, f := range b.Iface.Fields {
		if v, ok := wire(ru.pm, c, b, f.Name); ok {
			out = append(out, want{f.Name, v})
		}
	}
	return out
}

// compare checks what a team sent on boundary bi (to target, a team stage
// id, or "" for its own output) against the PM's values.
func (ru *runner) compare(c pmCase, bi int, team string, got map[string]any, a *flow.Analysis, res *eval.Result, target string) {
	for _, w := range ru.wants(c, bi) {
		ck := ru.base(c, bi, team, w.field)
		ck.Expected = w.v
		if v, ok := eval.GetPath(got, w.field); ok {
			ck.Actual, ck.Status = v, Differs
			if sheet.Equal(w.v, v) {
				ck.Status = Agrees
			}
		} else {
			ck.Status, ck.Note = absent(a, res, target, w.field)
		}
		ru.add(ck)
	}
}

// absent judges a field the team did not send: cannot tell when its value
// is not known, differs when the team knowingly does not send it.
func absent(a *flow.Analysis, res *eval.Result, target, field string) (status, note string) {
	r := sender(a.Sheet, target, field)
	if r == nil {
		return Differs, fmt.Sprintf("no row of the sheet %s sends %s", a.Sheet.Slug, field)
	}
	if why := notKnown(a, res, r, target); why != "" {
		return CannotTell, why
	}
	if ss := sendStep(res, r.ID, target); ss != nil {
		return Differs, "not sent: " + ss.Reason
	}
	return Differs, "not sent"
}

// cannotTell reports every expected value of segment i in a case as cannot
// tell, except the requests of the calls already made. End to end, it
// reports the callees of those calls too, since they run only through it.
func (ru *runner) cannotTell(i int, c pmCase, note string, made map[int]bool) {
	sg := ru.segs[i]
	if ru.rejectsBefore(sg, c) {
		return
	}
	if ru.rejectsIn(sg, c) {
		ck := ru.rejectBase(i, c)
		ck.Status, ck.Note = CannotTell, note
		ru.add(ck)
	}
	unknown := func(bi int) {
		for _, w := range ru.wants(c, bi) {
			ck := ru.base(c, bi, sg.Team, w.field)
			ck.Expected, ck.Status, ck.Note = w.v, CannotTell, note
			ru.add(ck)
		}
	}
	for _, req := range sg.Calls {
		if made[req] {
			continue
		}
		unknown(req)
		if k := ru.calleeOf(req); k >= 0 && ru.mode == EndToEnd {
			ru.cannotTell(k, c, "the call is not made: "+note, nil)
		}
	}
	unknown(sg.Out)
}

// teamReject reports a team's reject.
func (ru *runner) teamReject(i int, c pmCase, a *flow.Analysis, rj *eval.Rejection) {
	sg := ru.segs[i]
	ck := ru.rejectBase(i, c)
	ck.Actual = rj.Code
	if rj.Code == eval.MissingInputCode {
		ck.Note = missingNote(a, rj.Row)
	}
	row, _, pmRejects := ru.pmReject(c)
	switch {
	case ru.rejectsIn(sg, c):
		ck.Status = Differs
		if rj.Code == ck.Expected {
			ck.Status = Agrees
		}
	case pmRejects && ru.mode == EndToEnd && rj.Code == c.Res.Reject.Code:
		// End to end, the chain rejecting with the PM's code agrees,
		// whichever team rejects.
		ck.Row, ck.Expected, ck.Status = row, rj.Code, Agrees
		if ck.Note == "" {
			ck.Note = "the PM's sheet rejects later, at " + row
		}
	default:
		ck.Expected, ck.Status = none, Differs
	}
	ru.add(ck)
}

// missedReject reports a PM reject placed in segment i that the team's
// sheet did not make: differs, or cannot tell when a team row with a
// reject could not be computed.
func (ru *runner) missedReject(i int, c pmCase, a *flow.Analysis, res *eval.Result) {
	if !ru.rejectsIn(ru.segs[i], c) {
		return
	}
	ck := ru.rejectBase(i, c)
	ck.Status, ck.Actual = Differs, none
	for _, r := range a.Engine.Graph.Order {
		if len(r.Rejects) > 0 && res.Status[r.ID] == eval.StatusUnknown {
			ck.Status, ck.Actual = CannotTell, nil
			ck.Note = fmt.Sprintf("the reject on %s cannot be checked: %s", r.ID, why(a.Engine, res, r.ID))
			break
		}
	}
	ru.add(ck)
}

// missingNote names the source a MISSING_INPUT reject reads and the
// interface it was to come from.
func missingNote(a *flow.Analysis, row string) string {
	r := a.Sheet.ByID[row]
	if r == nil {
		return ""
	}
	var b *flow.Boundary
	field := r.SourcePath()
	switch r.SourceKind() {
	case "request":
		b = a.Boundaries[0]
	case "call":
		var id string
		id, field, _ = strings.Cut(field, ".")
		for _, x := range a.Boundaries {
			if x.Role == flow.RoleResponse && x.StageID == id {
				b = x
			}
		}
	}
	switch {
	case b == nil || b.Iface == nil:
		return fmt.Sprintf("reads %s, which no interface of the flow carries", r.Source)
	case b.Iface.Field(field) == nil:
		return fmt.Sprintf("reads %s, which %s does not carry", r.Source, b.Iface.Name)
	}
	return fmt.Sprintf("reads %s, which was not sent on %s", r.Source, b.Iface.Name)
}

// requestGap says why a call's request is not complete yet, or "".
func requestGap(a *flow.Analysis, res *eval.Result, target string) string {
	for _, r := range a.Engine.Graph.Order {
		for _, sd := range r.CallSends {
			if sd.Target != target {
				continue
			}
			if why := notKnown(a, res, r, target); why != "" {
				return sd.Field + ": " + why
			}
		}
	}
	return ""
}

// sender is the team row that sends a field to target ("" for the sheet's
// own output), or nil.
func sender(s *sheet.Sheet, target, field string) *sheet.Row {
	for _, r := range s.Rows {
		if target == "" && r.Send != nil && r.Send.Field == field {
			return r
		}
		for _, sd := range r.CallSends {
			if target != "" && sd.Target == target && sd.Field == field {
				return r
			}
		}
	}
	return nil
}

// sendStep is the trace of a row's send to target.
func sendStep(res *eval.Result, row, target string) *eval.SendStep {
	st := res.ByRow[row]
	if st == nil {
		return nil
	}
	if target == "" {
		return st.Send
	}
	for _, ss := range st.CallSends {
		if ss.Target == target {
			return ss
		}
	}
	return nil
}

// notKnown says why a row's send to target is not known, or "" when it is:
// sent, or knowingly not sent.
func notKnown(a *flow.Analysis, res *eval.Result, r *sheet.Row, target string) string {
	if res.Status[r.ID] == eval.StatusUnknown {
		return why(a.Engine, res, r.ID)
	}
	if ss := sendStep(res, r.ID, target); ss != nil && !ss.Sent && ss.Usage == sheet.UsageSituational && ss.When == nil {
		return ss.Reason
	}
	return ""
}

// why follows an unknown row to the unknown value it depends on and gives
// that value's reason, naming it when it is another row.
func why(e *eval.Engine, res *eval.Result, row string) string {
	start := row
	for range e.Graph.Order {
		next := ""
		if r := e.Sheet.ByID[row]; r != nil && !r.IsInput() {
			for _, d := range e.Graph.Deps[row] {
				if res.Status[d] == eval.StatusUnknown {
					next = d
					break
				}
			}
		}
		if next == "" {
			break
		}
		row = next
	}
	detail := ""
	if st := res.ByRow[row]; st != nil {
		detail = st.Detail
	}
	if row != start {
		return row + ": " + detail
	}
	return detail
}

// pmReject is the PM case's reject row and the step it is placed in. An
// input row is not placed: it is checked where the flow starts, step 0.
func (ru *runner) pmReject(c pmCase) (row string, step int, ok bool) {
	if c.Res == nil || c.Res.Reject == nil {
		return "", 0, false
	}
	row = c.Res.Reject.Row
	return row, ru.pm.Place[row], true
}

// rejectsBefore says the PM case rejects before the segment: nothing
// reaches it.
func (ru *runner) rejectsBefore(sg segment, c pmCase) bool {
	_, p, ok := ru.pmReject(c)
	return ok && p < sg.Steps[0]
}

// rejectsIn says the PM case rejects in one of the segment's steps.
func (ru *runner) rejectsIn(sg segment, c pmCase) bool {
	_, p, ok := ru.pmReject(c)
	return ok && slices.Contains(sg.Steps, p)
}

func (ru *runner) calleeOf(req int) int {
	for k, sg := range ru.segs {
		if sg.Callee && sg.In == req {
			return k
		}
	}
	return -1
}

// base is a check of a field on boundary bi.
func (ru *runner) base(c pmCase, bi int, team, field string) Check {
	b := ru.pm.Boundaries[bi]
	ck := Check{Case: c.ID, Boundary: bi, Role: b.Role, Stage: b.StageID, Iface: ifaceName(b), Field: field, Team: team}
	if field != "reject" {
		ck.Row = b.RowOf(field)
	}
	return ck
}

// rejectBase is a reject check of segment i, at its outgoing boundary,
// expecting the PM's reject when it is placed in the segment.
func (ru *runner) rejectBase(i int, c pmCase) Check {
	sg := ru.segs[i]
	ck := ru.base(c, sg.Out, sg.Team, "reject")
	if ru.rejectsIn(sg, c) {
		ck.Row, ck.Expected = c.Res.Reject.Row, c.Res.Reject.Code
	}
	return ck
}

func (ru *runner) add(ck Check) { ru.report.Checks = append(ru.report.Checks, ck) }
