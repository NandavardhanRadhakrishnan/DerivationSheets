// Package eval computes a sheet for one input. It is a reference evaluator
// for tests and documentation; the production logic stays in application
// code.
package eval

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/expr"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/graph"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// MissingInputCode is the reject code for a missing required input.
const MissingInputCode = "MISSING_INPUT"

// Value statuses in a trace.
const (
	StatusValue       = "value"        // provided or computed
	StatusDefault     = "default"      // input missing, default used
	StatusConfig      = "config"       // read from data/config.toml
	StatusNotRequired = "not-required" // situational input missing while not required
	StatusUnknown     = "unknown"      // cannot be computed: text rule, no design-time data, or unknown inputs
)

// Engine evaluates one sheet.
type Engine struct {
	Sheet *sheet.Sheet
	Graph *graph.Graph
	Expr  expr.Engine
	rt    *sheet.Row
}

// New prepares an engine. The sheet should already pass validation.
func New(s *sheet.Sheet) (*Engine, error) {
	ids := make([]string, 0, len(s.Rows))
	for _, r := range s.Rows {
		ids = append(ids, r.ID)
	}
	x, err := expr.NewCEL(ids)
	if err != nil {
		return nil, err
	}
	g := graph.Build(s, x)
	if g.Cycle != nil {
		return nil, fmt.Errorf("circular dependency: %s", strings.Join(g.Cycle, " -> "))
	}
	return &Engine{Sheet: s, Graph: g, Expr: x, rt: graph.RequestTimeRow(s)}, nil
}

// Input holds input values keyed by row ID.
type Input map[string]any

// Result is the outcome of one evaluation.
type Result struct {
	Values  map[string]any
	Status  map[string]string
	Steps   []*Step
	ByRow   map[string]*Step
	Payload map[string]any // output payload sent to the target; nested for dotted fields
	// CallPayloads holds what is sent to each call made on the way, by
	// stage ID (send.target).
	CallPayloads map[string]map[string]any
	Sent         []SentField
	Reject       *Rejection
	Err          *Error
}

// Step traces one row.
type Step struct {
	Row       string    `json:"row"`
	Rule      string    `json:"rule"` // "input" or a rule kind
	Value     any       `json:"value"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	TableRule int       `json:"table_rule,omitempty"` // 1-based rule that fired
	TableKey  string    `json:"table_key,omitempty"`
	LookupKey string    `json:"lookup_key,omitempty"`
	LookupAt  string    `json:"lookup_at,omitempty"` // file:line or "values"
	InputWhen *bool     `json:"input_when,omitempty"`
	Rejected  bool      `json:"rejected,omitempty"`
	Send      *SendStep `json:"send,omitempty"`
	// CallSends trace sends to calls made on the way.
	CallSends []*SendStep `json:"call_sends,omitempty"`
}

// SendStep traces a row's send rule.
type SendStep struct {
	Target string `json:"target,omitempty"` // a call stage; empty for the sheet's own target
	Field  string `json:"field"`
	Usage  string `json:"usage"`
	When   *bool  `json:"when,omitempty"`
	Sent   bool   `json:"sent"`
	Value  any    `json:"value,omitempty"`
	Reason string `json:"reason"`
	// Optional is set for sender-discretion fields that were sent.
	Optional bool `json:"optional,omitempty"`
}

// SentField is one field of the output payload.
type SentField struct {
	Field    string
	Row      string
	Value    any
	Optional bool
}

// Rejection is a rejected request.
type Rejection struct {
	Row     string `json:"row"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Index   int    `json:"index"` // reject index on the row; -1 for a missing input
}

// Error is an evaluation failure, such as a lookup with no matching entry.
type Error struct {
	Row     string `json:"row"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("row %q: %s", e.Row, e.Message) }

// Evaluate computes every row in dependency order, applies rejects and send
// rules, and returns the payload or a rejection with a full trace.
func (e *Engine) Evaluate(in Input) *Result {
	res := &Result{
		Values: map[string]any{},
		Status: map[string]string{},
		ByRow:  map[string]*Step{},
	}
	var ctx expr.Context
	for _, r := range e.Graph.Order {
		st := &Step{Row: r.ID, Rule: r.Rule()}
		if r.IsInput() {
			st.Rule = "input"
		}
		res.Steps = append(res.Steps, st)
		res.ByRow[r.ID] = st

		known := true
		for _, d := range e.Graph.Deps[r.ID] {
			if res.Status[d] == StatusUnknown {
				known = false
			}
		}
		var err error
		switch {
		case r.IsInput():
			err = e.input(r, in, res, st, ctx)
		case !known:
			st.Status = StatusUnknown
			st.Detail = "depends on a value that cannot be computed"
		default:
			err = e.derive(r, res, st, ctx)
		}
		if err != nil {
			res.Err = &Error{Row: r.ID, Message: err.Error()}
			st.Detail = err.Error()
			return res
		}
		if res.Reject != nil {
			return res
		}
		res.Status[r.ID] = st.Status
		if st.Status != StatusUnknown {
			res.Values[r.ID] = st.Value
		}
		if r == e.rt {
			if t, ok := st.Value.(time.Time); ok {
				ctx = expr.Context{RequestTime: t, HasRequestTime: true}
			}
		}
		if st.Status != StatusUnknown {
			rj := e.rejects(r, res, ctx)
			if res.Err != nil {
				return res
			}
			if rj != nil {
				st.Rejected = true
				res.Reject = rj
				return res
			}
		}
	}
	e.send(res, ctx)
	return res
}

func (e *Engine) input(r *sheet.Row, in Input, res *Result, st *Step, ctx expr.Context) error {
	if r.Usage == sheet.UsageSituational && r.When != "" {
		b, err := e.evalBool(r.When, res.Values, ctx)
		if err != nil {
			return fmt.Errorf("when: %v", err)
		}
		st.InputWhen = &b
	}
	if v, ok := in[r.ID]; ok && v != nil {
		cv, err := Coerce(r.Type, v)
		if err != nil {
			return err
		}
		st.Value, st.Status = cv, StatusValue
		return nil
	}
	if r.SourceKind() == "config" {
		if v, ok := e.Sheet.Config[r.SourcePath()]; ok {
			st.Value, st.Status = v, StatusConfig
			st.Detail = "from " + sheet.ConfigFile
			return nil
		}
	}
	if r.HasDefault {
		st.Value, st.Status = r.Default, StatusDefault
		st.Detail = "missing; default used"
		return nil
	}
	if r.SourceKind() == "db" || r.SourceKind() == "config" {
		st.Status = StatusUnknown
		st.Detail = "no design-time value"
		return nil
	}
	if st.InputWhen != nil && !*st.InputWhen {
		st.Value, st.Status = nil, StatusNotRequired
		st.Detail = "missing; not required because `when` is false"
		return nil
	}
	st.Status = StatusValue
	st.Detail = "required input is missing"
	res.Reject = &Rejection{Row: r.ID, Code: MissingInputCode, Message: r.DisplayName() + " is required", Index: -1}
	st.Rejected = true
	return nil
}

func (e *Engine) derive(r *sheet.Row, res *Result, st *Step, ctx expr.Context) error {
	switch r.Rule() {
	case sheet.RuleFormula:
		p, err := e.Expr.Compile(r.Formula)
		if err != nil {
			return err
		}
		v, err := p.Eval(res.Values, ctx)
		if err != nil {
			return fmt.Errorf("formula: %v", err)
		}
		st.Value, st.Status = v, StatusValue
	case sheet.RuleTable:
		return e.table(r, res, st)
	case sheet.RuleLookup:
		return e.lookup(r, res, st)
	case sheet.RuleText:
		st.Status = StatusUnknown
		st.Detail = "text rule; not computable"
	default:
		return fmt.Errorf("row has no single rule")
	}
	return nil
}

func (e *Engine) table(r *sheet.Row, res *Result, st *Step) error {
	t := r.Table
	vals := make([]any, len(t.Columns))
	for i, c := range t.Columns {
		vals[i] = res.Values[c]
	}
	var hits []int
	for i, rule := range t.Rules {
		if MatchCells(rule.Cells, vals) {
			hits = append(hits, i)
			if t.HitPolicy == "first" {
				break
			}
		}
	}
	desc := describeInputs(t.Columns, vals)
	switch {
	case len(hits) == 0:
		return fmt.Errorf("no decision-table rule matches %s", desc)
	case len(hits) > 1:
		return fmt.Errorf("decision-table rules %s all match %s under the unique hit policy", joinInts(hits), desc)
	}
	rule := t.Rules[hits[0]]
	st.Value, st.Status = rule.Result, StatusValue
	st.TableRule = hits[0] + 1
	st.TableKey = rule.Key()
	st.Detail = fmt.Sprintf("rule %d %s => %s", hits[0]+1, rule.Key(), sheet.FormatValue(rule.Result))
	return nil
}

func (e *Engine) lookup(r *sheet.Row, res *Result, st *Step) error {
	l := r.Lookup
	vals := make([]any, len(l.Key))
	for i, k := range l.Key {
		vals[i] = res.Values[k]
	}
	desc := describeInputs(l.Key, vals)
	st.LookupKey = desc
	if !l.Available() {
		st.Status = StatusUnknown
		st.Detail = "no design-time values for " + l.From
		return nil
	}
	if l.Inline != nil {
		if len(vals) != 1 {
			return fmt.Errorf("inline values need exactly one key column")
		}
		k := sheet.FormatValue(vals[0])
		v, ok := l.Inline[k]
		if !ok {
			return fmt.Errorf("%s has no value for %s", l.From, desc)
		}
		st.Value, st.Status = v, StatusValue
		st.LookupAt = "values." + k
		st.Detail = fmt.Sprintf("%s[%s] = %s", l.From, k, sheet.FormatValue(v))
		return nil
	}
	for _, en := range l.Data.Entries {
		if MatchCells(en.Cells, vals) {
			st.Value, st.Status = en.Value, StatusValue
			st.LookupAt = fmt.Sprintf("%s:%d", relPath(e.Sheet.Dir, l.Data.File), en.Line)
			st.Detail = fmt.Sprintf("%s entry [%s] at %s = %s", l.From, en.Key(), st.LookupAt, sheet.FormatValue(en.Value))
			return nil
		}
	}
	return fmt.Errorf("%s has no entry for %s", l.From, desc)
}

func (e *Engine) rejects(r *sheet.Row, res *Result, ctx expr.Context) *Rejection {
	for i, rj := range r.Rejects {
		b, err := e.evalBool(rj.When, res.Values, ctx)
		if err != nil {
			res.Err = &Error{Row: r.ID, Message: fmt.Sprintf("reject %d: %v", i+1, err)}
			return nil
		}
		if b {
			return &Rejection{Row: r.ID, Code: rj.Code, Message: rj.Message, Index: i}
		}
	}
	return nil
}

func (e *Engine) send(res *Result, ctx expr.Context) {
	res.Payload = map[string]any{}
	res.CallPayloads = map[string]map[string]any{}
	for _, r := range e.Graph.Order {
		if sd := r.Send; sd != nil {
			ss, v, ok := e.sendOne(r, sd, res, ctx)
			res.ByRow[r.ID].Send = ss
			if res.Err != nil {
				return
			}
			if ok {
				setPath(res.Payload, sd.Field, v)
				res.Sent = append(res.Sent, SentField{Field: sd.Field, Row: r.ID, Value: v, Optional: ss.Optional})
			}
		}
		for _, sd := range r.CallSends {
			ss, v, ok := e.sendOne(r, sd, res, ctx)
			ss.Target = sd.Target
			res.ByRow[r.ID].CallSends = append(res.ByRow[r.ID].CallSends, ss)
			if res.Err != nil {
				return
			}
			if ok {
				if res.CallPayloads[sd.Target] == nil {
					res.CallPayloads[sd.Target] = map[string]any{}
				}
				setPath(res.CallPayloads[sd.Target], sd.Field, v)
			}
		}
	}
}

// sendOne applies one send rule, returning its trace, the value to send,
// and whether it is sent.
func (e *Engine) sendOne(r *sheet.Row, sd *sheet.Send, res *Result, ctx expr.Context) (*SendStep, any, bool) {
	ss := &SendStep{Field: sd.Field, Usage: sd.Usage}
	required := sd.Usage != sheet.UsageSituational
	if !required {
		b, err := e.evalBool(sd.When, res.Values, ctx)
		if err != nil {
			ss.Reason = "when cannot be evaluated: " + err.Error()
			return ss, nil, false
		}
		ss.When = &b
		required = b
	}
	v, known := res.Values[r.ID]
	if res.Status[r.ID] == StatusUnknown {
		known = false
	}
	switch {
	case !required && sd.Otherwise != sheet.OtherwiseSenderDiscretion:
		ss.Reason = "when is false; do not send"
		return ss, nil, false
	case !known:
		ss.Reason = "value cannot be computed"
		return ss, nil, false
	case v == nil && required:
		ss.Reason = "required, but the value is empty"
		return ss, nil, false
	case v == nil:
		ss.Reason = "sender discretion; value is empty, not sent"
		return ss, nil, false
	}
	if sd.Map != nil {
		mv, ok := sd.Map[sheet.FormatValue(v)]
		if !ok {
			res.Err = &Error{Row: r.ID, Message: fmt.Sprintf("send.map has no entry for %s", sheet.FormatValue(v))}
			return ss, nil, false
		}
		v = mv
	}
	ss.Sent, ss.Value = true, v
	ss.Optional = !required
	switch {
	case !required:
		ss.Reason = "when is false; sent at sender's discretion"
	case sd.Usage == sheet.UsageSituational:
		ss.Reason = "when is true; required"
	default:
		ss.Reason = "required"
	}
	return ss, v, true
}

func (e *Engine) evalBool(src string, vars map[string]any, ctx expr.Context) (bool, error) {
	p, err := e.Expr.Compile(src)
	if err != nil {
		return false, err
	}
	v, err := p.Eval(vars, ctx)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%q gives %v, not true or false", src, sheet.FormatValue(v))
	}
	return b, nil
}

// MatchCells reports whether every cell matches its value.
func MatchCells(cells []sheet.Cell, vals []any) bool {
	if len(cells) != len(vals) {
		return false
	}
	for i, c := range cells {
		if !c.Match(vals[i]) {
			return false
		}
	}
	return true
}

// Coerce converts an input value to the row's declared type.
func Coerce(typ string, v any) (any, error) {
	v = sheet.Normalize(v)
	switch typ {
	case "date", "datetime":
		switch x := v.(type) {
		case time.Time:
			return x, nil
		case string:
			if t, ok := sheet.ParseTime(x); ok {
				return t, nil
			}
		}
		return nil, fmt.Errorf("%v is not a %s", sheet.FormatValue(v), typ)
	case "number":
		switch x := v.(type) {
		case float64:
			return x, nil
		case string:
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f, nil
			}
		}
		return nil, fmt.Errorf("%v is not a number", sheet.FormatValue(v))
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			if b, err := strconv.ParseBool(x); err == nil {
				return b, nil
			}
		}
		return nil, fmt.Errorf("%v is not a boolean", sheet.FormatValue(v))
	case "string":
		if s, ok := v.(string); ok {
			return s, nil
		}
		return sheet.FormatValue(v), nil
	}
	return v, nil
}

// FromSources builds an Input from a request document and a context map,
// using each input row's source path. db and config rows are left to the
// design-time data.
func FromSources(s *sheet.Sheet, request, context map[string]any) Input {
	in := Input{}
	for _, r := range s.Rows {
		var root map[string]any
		switch r.SourceKind() {
		case "request":
			root = request
		case "context":
			root = context
		default:
			continue
		}
		if v, ok := getPath(root, r.SourcePath()); ok {
			in[r.ID] = v
		}
	}
	return in
}

func getPath(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

// GetPath reads a dotted field from a payload.
func GetPath(m map[string]any, path string) (any, bool) { return getPath(m, path) }

func describeInputs(cols []string, vals []any) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c + "=" + sheet.FormatValue(vals[i])
	}
	return strings.Join(parts, ", ")
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x + 1)
	}
	return strings.Join(parts, ", ")
}

func relPath(base, p string) string {
	if r, err := relSlash(base, p); err == nil {
		return r
	}
	return p
}
