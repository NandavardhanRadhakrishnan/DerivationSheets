// Package verify compares actual partner payloads, captured by QA for each
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

	"derivationsheets/internal/cases"
	"derivationsheets/internal/eval"
	"derivationsheets/internal/evidence"
	"derivationsheets/internal/sheet"
)

// Actual is what the system under test did for one case.
type Actual struct {
	Payload map[string]any
	Reject  string
}

// ReadResults reads results.json. Accepted shapes:
//
//	{ "cases": { "<case-id>": { "payload": { ... } } } }
//	{ "<case-id>": { "reject": "AGE_LIMIT" } }
//	{ "<case-id>": { ...payload fields... } }
//
// An entry is a bare payload unless its only key is "payload" or "reject".
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
		switch {
		case len(m) == 1 && m["reject"] != nil:
			a.Reject = fmt.Sprint(m["reject"])
		case len(m) == 1 && m["payload"] != nil:
			p, ok := m["payload"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: case %q payload is not an object", path, id)
			}
			a.Payload = p
		default:
			a.Payload = m
		}
		if a.Payload != nil {
			a.Payload = sheet.Normalize(a.Payload).(map[string]any)
		}
		out[id] = a
	}
	return out, nil
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
	sender := map[string]*sheet.Row{} // field -> row
	for _, r := range s.Rows {
		if r.Send != nil {
			sender[r.Send.Field] = r
		}
	}
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
		v := &caseVerdict{e: e, c: c, res: res, actual: actual, sender: sender, run: run, rep: rep}
		v.apply()
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
	flat := cases.Flatten(res.Payload)
	for _, f := range c.Expected {
		if f.Value == cases.Unknown {
			continue
		}
		if v, ok := flat[f.Name]; ok && !sheet.Equal(v, f.Value) {
			return fmt.Sprintf("the sheet now computes %s = %s, not %s", f.Name, sheet.FormatValue(v), sheet.FormatValue(f.Value))
		}
	}
	return ""
}

type caseVerdict struct {
	e      *eval.Engine
	c      cases.Case
	res    *eval.Result
	actual Actual
	sender map[string]*sheet.Row
	run    *evidence.Run
	rep    *Report
}

type fieldCheck struct {
	field    string
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
			if r := v.sender[f.Name]; r != nil {
				v.contradict(r.ID, evidence.Contradiction{Case: v.c.ID, Field: f.Name, Expected: f.Value, Actual: "reject " + v.actual.Reject}, nil)
			}
		}
		return
	}
	checks := v.compare()
	// Each row is judged by the compared fields it influences: its own sent
	// field, and fields sent by rows downstream of it.
	for _, st := range v.res.Steps {
		row := st.Row
		desc := v.e.Graph.Descendants(row)
		var own *fieldCheck
		var influenced []fieldCheck
		for i := range checks {
			ch := checks[i]
			if ch.row == row {
				own = &checks[i]
				influenced = append(influenced, ch)
			} else if desc[ch.row] {
				influenced = append(influenced, ch)
			}
		}
		if len(influenced) == 0 {
			continue
		}
		var bad []fieldCheck
		for _, ch := range influenced {
			if !ch.ok {
				bad = append(bad, ch)
			}
		}
		branches := v.branchesOf(row)
		switch {
		case own != nil && !own.ok:
			v.contradict(row, evidence.Contradiction{Case: v.c.ID, Field: own.field, Expected: own.expected, Actual: own.actual, Note: own.note}, branches)
		case own == nil && len(bad) == len(influenced):
			// Every field downstream disagrees, so this row is the likeliest cause.
			fields := make([]string, len(bad))
			for i, b := range bad {
				fields[i] = b.field
			}
			v.contradict(row, evidence.Contradiction{Case: v.c.ID, Field: bad[0].field, Expected: bad[0].expected, Actual: bad[0].actual,
				Note: fmt.Sprintf("not sent; every field it feeds disagrees (%s)", joinFields(fields))}, branches)
		default:
			v.match(row, branches)
		}
	}
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

// compare checks expected fields against the actual payload, and flags
// fields that were sent although the sheet says not to send them.
func (v *caseVerdict) compare() []fieldCheck {
	flat := cases.Flatten(v.actual.Payload)
	var out []fieldCheck
	expected := map[string]bool{}
	for _, f := range v.c.Expected {
		expected[f.Name] = true
		r := v.sender[f.Name]
		if r == nil || f.Value == cases.Unknown {
			continue
		}
		got, present := flat[f.Name]
		ch := fieldCheck{field: f.Name, row: r.ID, expected: f.Value, actual: got}
		st := v.res.ByRow[r.ID].Send
		switch {
		case !present && st != nil && st.Optional:
			ch.ok = true // sender's discretion
		case !present:
			ch.actual = "(missing)"
		default:
			ch.ok = sheet.Equal(got, f.Value)
		}
		out = append(out, ch)
	}
	// Fields the sheet did not send must be absent.
	for _, r := range v.e.Graph.Order {
		if r.Send == nil || expected[r.Send.Field] {
			continue
		}
		st := v.res.ByRow[r.ID].Send
		if st == nil || st.Reason == "value cannot be computed" {
			continue
		}
		got, present := flat[r.Send.Field]
		ch := fieldCheck{field: r.Send.Field, row: r.ID, ok: !present, expected: "(not sent)", actual: got}
		if present {
			ch.note = "sent although " + st.Reason
		}
		out = append(out, ch)
	}
	return out
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
	msg := fmt.Sprintf("case %q, field %s: expected %s, got %s (row %q)", c.Case, c.Field, sheet.FormatValue(c.Expected), sheet.FormatValue(c.Actual), row)
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
