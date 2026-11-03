// Package verify compares actual output payloads, captured by QA for each
// case, with the expected outputs in cases.toml, and turns the comparison
// into evidence per row and per branch.
package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/evidence"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Actual is what the system under test did for one case.
type Actual struct {
	Payload map[string]any
	// Calls holds the captured request of each call made on the way, by
	// stage ID. A call that is absent was not captured.
	Calls  map[string]map[string]any
	Reject string
}

// ReadResults reads results.json. Accepted shapes:
//
//	{ "cases": { "<case-id>": { "payload": { ... }, "calls": { "<stage>": { ... } } } } }
//	{ "<case-id>": { "reject": "DESTINATION_NOT_SERVED" } }
//	{ "<case-id>": { ...payload fields... } }
//
// An entry is a bare payload unless all its keys are "payload", "calls" or
// "reject".
func ReadResults(path string) (map[string]Actual, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if inner, ok := raw["cases"].(map[string]any); ok && len(raw) == 1 {
		raw = inner
	}
	out := map[string]Actual{}
	for id, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: case %q is not an object", path, id)
		}
		var a Actual
		if !structured(m) {
			a.Payload = sheet.Normalize(m).(map[string]any)
			out[id] = a
			continue
		}
		if m["reject"] != nil {
			a.Reject = fmt.Sprint(m["reject"])
		}
		if m["payload"] != nil {
			p, ok := m["payload"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: case %q payload is not an object", path, id)
			}
			a.Payload = sheet.Normalize(p).(map[string]any)
		}
		if m["calls"] != nil {
			calls, ok := m["calls"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: case %q calls is not an object", path, id)
			}
			a.Calls = map[string]map[string]any{}
			for t, v := range calls {
				p, ok := v.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s: case %q call %q is not an object", path, id, t)
				}
				a.Calls[t] = sheet.Normalize(p).(map[string]any)
			}
		}
		if a.Reject != "" && a.Payload != nil {
			return nil, fmt.Errorf("%s: case %q has both a payload and a reject", path, id)
		}
		out[id] = a
	}
	return out, nil
}

// structured reports whether a results entry uses the payload, calls and
// reject keys rather than being a bare payload.
func structured(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	for k := range m {
		if k != "payload" && k != "calls" && k != "reject" {
			return false
		}
	}
	return true
}

// Report is the outcome of a verify run.
type Report struct {
	Run *evidence.Run
	// Problems are contradictions, one line each, naming case and field.
	Problems []string
	// Notes are non-failing remarks, such as stale cases.
	Notes []string
}

// Verify compares results with cases and builds an evidence run.
func Verify(e *eval.Engine, cf *cases.File, results map[string]Actual, source string, now time.Time) *Report {
	s := e.Sheet
	run := &evidence.Run{
		Sheet:    s.Slug,
		Version:  s.Version,
		ID:       now.UTC().Format("20060102T150405Z") + "-test",
		Kind:     evidence.KindTest,
		Created:  now.UTC(),
		Source:   source,
		Rows:     map[string]*evidence.Tally{},
		Branches: map[string]*evidence.Tally{},
	}
	rep := &Report{Run: run}
	if cf.Version != s.Version {
		rep.Notes = append(rep.Notes, fmt.Sprintf("cases.toml is for version %d but sheet.toml is version %d; run `cases` again", cf.Version, s.Version))
	}
	sender := map[cases.SendKey]*sheet.Row{}
	var callOrder []string
	for _, r := range e.Graph.Order {
		for _, k := range cases.SendKeys(r) {
			sender[k] = r
			if k.Target != "" && !slices.Contains(callOrder, k.Target) {
				callOrder = append(callOrder, k.Target)
			}
		}
	}
	uncaptured := map[string]int{} // call stage -> cases that expect it but have no captured request
	expecting := map[string]int{}
	for _, c := range cf.Cases {
		actual, ok := results[c.ID]
		if !ok {
			rep.Notes = append(rep.Notes, fmt.Sprintf("no result for case %q", c.ID))
			continue
		}
		res := e.Evaluate(eval.Input(c.Input))
		if res.Err != nil {
			rep.Notes = append(rep.Notes, fmt.Sprintf("case %q cannot be evaluated: %v", c.ID, res.Err))
			continue
		}
		if stale := staleExpectation(c, res); stale != "" {
			rep.Notes = append(rep.Notes, fmt.Sprintf("case %q: %s; cases.toml may be out of date", c.ID, stale))
		}
		run.Observations++
		if c.ExpectedReject == "" && actual.Reject == "" {
			for _, call := range c.Calls {
				expecting[call.Target]++
				if _, ok := actual.Calls[call.Target]; !ok {
					uncaptured[call.Target]++
				}
			}
		}
		v := &caseVerdict{e: e, c: c, res: res, actual: actual, sender: sender, run: run, rep: rep}
		v.apply()
	}
	for _, t := range callOrder {
		if n := uncaptured[t]; n > 0 {
			rep.Notes = append(rep.Notes, fmt.Sprintf("no captured request for call %s in %d of %d cases; its fields were not checked there", t, n, expecting[t]))
		}
	}
	run.Notes = rep.Notes
	return rep
}

// staleExpectation reports when the evaluator no longer agrees with the
// stored expectation, which means the sheet changed after cases.toml was
// generated.
func staleExpectation(c cases.Case, res *eval.Result) string {
	if c.ExpectedReject != "" {
		if res.Reject == nil || res.Reject.Code != c.ExpectedReject {
			return "the sheet no longer rejects with " + c.ExpectedReject
		}
		return ""
	}
	if res.Reject != nil {
		return "the sheet now rejects with " + res.Reject.Code
	}
	got := cases.Observed(res)
	for _, x := range c.Expectations() {
		if x.Value == cases.Unknown {
			continue
		}
		if v, ok := got[x.Key]; ok && !sheet.Equal(v, x.Value) {
			return fmt.Sprintf("the sheet now computes %s = %s, not %s", x.Key.Label(), sheet.FormatValue(v), sheet.FormatValue(x.Value))
		}
	}
	return ""
}

type caseVerdict struct {
	e      *eval.Engine
	c      cases.Case
	res    *eval.Result
	actual Actual
	sender map[cases.SendKey]*sheet.Row
	run    *evidence.Run
	rep    *Report
}

type fieldCheck struct {
	key      cases.SendKey
	row      string
	ok       bool
	expected any
	actual   any
	note     string
}

func (v *caseVerdict) apply() {
	if v.c.ExpectedReject != "" {
		v.applyReject()
		return
	}
	if v.actual.Reject != "" {
		// Expected a payload, got a reject: every row that sends is contradicted.
		for _, f := range v.c.Expected {
			k := cases.SendKey{Field: f.Name}
			if r := v.sender[k]; r != nil {
				v.contradict(r.ID, contradiction(v.c.ID, k, f.Value, "reject "+v.actual.Reject, ""), nil)
			}
		}
		return
	}
	checks := v.compare()
	// Each row is judged by the compared fields it influences: the fields it
	// sends itself, to the target or to calls, and fields sent by rows
	// downstream of it.
	for _, st := range v.res.Steps {
		row := st.Row
		desc := v.e.Graph.Descendants(row)
		var own, influenced, bad []fieldCheck
		for _, ch := range checks {
			switch {
			case ch.row == row:
				own = append(own, ch)
			case !desc[ch.row]:
				continue
			}
			influenced = append(influenced, ch)
			if !ch.ok {
				bad = append(bad, ch)
			}
		}
		if len(influenced) == 0 {
			continue
		}
		branches := v.branchesOf(row)
		ownBad := slices.IndexFunc(own, func(ch fieldCheck) bool { return !ch.ok })
		switch {
		case ownBad >= 0:
			ch := own[ownBad]
			v.contradict(row, contradiction(v.c.ID, ch.key, ch.expected, ch.actual, ch.note), branches)
		case len(own) == 0 && len(bad) == len(influenced):
			// Every field downstream disagrees, so this row is the likeliest cause.
			fields := make([]string, len(bad))
			for i, b := range bad {
				fields[i] = b.key.Label()
			}
			v.contradict(row, contradiction(v.c.ID, bad[0].key, bad[0].expected, bad[0].actual,
				fmt.Sprintf("not sent; every field it feeds disagrees (%s)", joinFields(fields))), branches)
		default:
			v.match(row, branches)
		}
	}
}

func contradiction(caseID string, k cases.SendKey, expected, actual any, note string) evidence.Contradiction {
	return evidence.Contradiction{Case: caseID, Call: k.Target, Field: k.Field, Expected: expected, Actual: actual, Note: note}
}

func (v *caseVerdict) applyReject() {
	code := v.c.ExpectedReject
	rj := v.res.Reject
	rows := []string{rj.Row}
	for id := range v.e.Graph.Ancestors(rj.Row) {
		rows = append(rows, id)
	}
	sort.Strings(rows)
	branch := fmt.Sprintf("%s:reject:%s", rj.Row, code)
	if v.actual.Reject == code {
		for _, id := range rows {
			var br []string
			if id == rj.Row {
				br = []string{branch}
			}
			v.match(id, br)
		}
		return
	}
	got := any("a payload")
	if v.actual.Reject != "" {
		got = "reject " + v.actual.Reject
	}
	v.contradict(rj.Row, evidence.Contradiction{Case: v.c.ID, Field: "reject", Expected: code, Actual: got}, []string{branch})
}

// compare checks expected fields against the actual payload and captured
// call requests, and flags fields that were sent although the sheet says not
// to send them. Fields of a call whose request was not captured are skipped.
func (v *caseVerdict) compare() []fieldCheck {
	got := cases.ObservedIn(v.actual.Payload, v.actual.Calls)
	captured := func(k cases.SendKey) bool {
		if k.Target == "" {
			return true
		}
		_, ok := v.actual.Calls[k.Target]
		return ok
	}
	var out []fieldCheck
	expected := map[cases.SendKey]bool{}
	for _, x := range v.c.Expectations() {
		expected[x.Key] = true
		r := v.sender[x.Key]
		if r == nil || x.Value == cases.Unknown || !captured(x.Key) {
			continue
		}
		val, present := got[x.Key]
		ch := fieldCheck{key: x.Key, row: r.ID, expected: x.Value, actual: val}
		st := v.sendStep(r, x.Key)
		switch {
		case !present && st != nil && st.Optional:
			ch.ok = true // sender's discretion
		case !present:
			ch.actual = "(missing)"
		default:
			ch.ok = sheet.Equal(val, x.Value)
		}
		out = append(out, ch)
	}
	// Fields the sheet did not send must be absent.
	for _, r := range v.e.Graph.Order {
		for _, k := range cases.SendKeys(r) {
			if expected[k] || !captured(k) {
				continue
			}
			st := v.sendStep(r, k)
			if st == nil || st.Reason == "value cannot be computed" {
				continue
			}
			val, present := got[k]
			ch := fieldCheck{key: k, row: r.ID, ok: !present, expected: "(not sent)", actual: val}
			if present {
				ch.note = "sent although " + st.Reason
			}
			out = append(out, ch)
		}
	}
	return out
}

// sendStep returns the trace of one of a row's sends.
func (v *caseVerdict) sendStep(r *sheet.Row, k cases.SendKey) *eval.SendStep {
	st := v.res.ByRow[r.ID]
	if k.Target == "" {
		return st.Send
	}
	for _, cs := range st.CallSends {
		if cs.Target == k.Target && cs.Field == k.Field {
			return cs
		}
	}
	return nil
}

// branchesOf returns the branch keys this case exercised on a row.
func (v *caseVerdict) branchesOf(row string) []string {
	var out []string
	for _, k := range cases.Covers(v.e, v.res) {
		if len(k) > len(row) && k[:len(row)+1] == row+":" {
			out = append(out, k)
		}
	}
	return out
}

func (v *caseVerdict) tally(m map[string]*evidence.Tally, key string) *evidence.Tally {
	t := m[key]
	if t == nil {
		t = &evidence.Tally{}
		m[key] = t
	}
	return t
}

func (v *caseVerdict) match(row string, branches []string) {
	t := v.tally(v.run.Rows, row)
	t.Exercised++
	t.Matched++
	for _, b := range branches {
		bt := v.tally(v.run.Branches, b)
		bt.Exercised++
		bt.Matched++
	}
}

func (v *caseVerdict) contradict(row string, c evidence.Contradiction, branches []string) {
	t := v.tally(v.run.Rows, row)
	t.Exercised++
	t.Contradictions = append(t.Contradictions, c)
	for _, b := range branches {
		bt := v.tally(v.run.Branches, b)
		bt.Exercised++
		bt.Contradictions = append(bt.Contradictions, c)
	}
	field := cases.SendKey{Target: c.Call, Field: c.Field}.Label()
	msg := fmt.Sprintf("case %q, field %s: expected %s, got %s (row %q)", c.Case, field, sheet.FormatValue(c.Expected), sheet.FormatValue(c.Actual), row)
	if c.Note != "" {
		msg += "; " + c.Note
	}
	if !slices.Contains(v.rep.Problems, msg) {
		v.rep.Problems = append(v.rep.Problems, msg)
	}
}

func joinFields(fs []string) string {
	out := ""
	for i, f := range fs {
		if i > 0 {
			out += ", "
		}
		out += f
	}
	return out
}
