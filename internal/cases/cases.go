// Package cases generates and reads cases.toml: test inputs with expected
// output payloads that together cover every decision-table rule, reject,
// situational condition, and isolate every intermediate row.
package cases

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/analysis"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/graph"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// FileName is the generated file inside a workflow folder.
const FileName = "cases.toml"

// DefaultReferenceTime is the request time used by generated cases, so
// output is stable from run to run. Dates of birth and similar inputs are
// placed relative to it.
const DefaultReferenceTime = "2026-11-02T10:00:00+05:30"

// Unknown is the placeholder for an expected value that depends on a text
// rule. People replace it by hand.
const Unknown = "?"

// MaxCandidates bounds the number of inputs evaluated.
const MaxCandidates = 60000

// Case is one test case.
type Case struct {
	ID             string
	Covers         []string
	Input          map[string]any
	Expected       []Field // in send order
	ExpectedReject string
}

// Field is one expected payload field, with a dotted name.
type Field struct {
	Name  string
	Value any
}

// Target is something the cases must cover.
type Target struct {
	Key  string
	Row  string
	Kind string // "table", "reject", "send-when", "input-when", "lookup-value", "isolation"
}

// Result of generation.
type Result struct {
	Cases     []Case
	Targets   []Target
	Uncovered []Target
	Evaluated int
}

// Targets lists the coverage targets of a sheet, in row order.
func Targets(e *eval.Engine) []Target {
	var ts []Target
	for _, r := range e.Graph.Order {
		if r.IsInput() && r.Usage == sheet.UsageSituational && r.When != "" {
			ts = append(ts, Target{r.ID + ":when:true", r.ID, "input-when"}, Target{r.ID + ":when:false", r.ID, "input-when"})
		}
		if r.Rule() == sheet.RuleTable {
			for _, rule := range r.Table.Rules {
				ts = append(ts, Target{r.ID + ":" + rule.Key(), r.ID, "table"})
			}
		}
		if r.Rule() == sheet.RuleLookup && r.Lookup.Inline != nil {
			for _, k := range r.Lookup.InlineOrder {
				ts = append(ts, Target{r.ID + ":" + k, r.ID, "lookup-value"})
			}
		}
		for _, rj := range r.Rejects {
			ts = append(ts, Target{r.ID + ":reject:" + rj.Code, r.ID, "reject"})
		}
		if r.Send != nil && r.Send.Usage == sheet.UsageSituational && r.Send.When != "" {
			ts = append(ts, Target{r.ID + ":send.when:true", r.ID, "send-when"}, Target{r.ID + ":send.when:false", r.ID, "send-when"})
		}
	}
	for _, r := range IsolationRows(e) {
		ts = append(ts, Target{r.ID + ":isolation", r.ID, "isolation"})
	}
	return ts
}

// IsolationRows are computable derived rows that are not sent, so a wrong
// value only shows through rows downstream.
func IsolationRows(e *eval.Engine) []*sheet.Row {
	var out []*sheet.Row
	for _, r := range e.Graph.Order {
		if r.IsInput() || r.Send != nil || r.Rule() == sheet.RuleText || len(e.Graph.Users[r.ID]) == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Covers lists the targets an evaluation exercised.
func Covers(e *eval.Engine, res *eval.Result) []string {
	var out []string
	for _, st := range res.Steps {
		r := e.Sheet.ByID[st.Row]
		if st.InputWhen != nil {
			out = append(out, fmt.Sprintf("%s:when:%t", st.Row, *st.InputWhen))
		}
		if st.TableKey != "" {
			out = append(out, st.Row+":"+st.TableKey)
		}
		if k, ok := strings.CutPrefix(st.LookupAt, "values."); ok {
			out = append(out, st.Row+":"+k)
		}
		if st.Send != nil && st.Send.When != nil && r.Send.Usage == sheet.UsageSituational {
			out = append(out, fmt.Sprintf("%s:send.when:%t", st.Row, *st.Send.When))
		}
	}
	if res.Reject != nil && res.Reject.Index >= 0 {
		out = append(out, res.Reject.Row+":reject:"+res.Reject.Code)
	}
	return out
}

type candidate struct {
	input  map[string]any
	res    *eval.Result
	covers []string
	order  int
}

// Generate builds the smallest practical set of cases.
func Generate(e *eval.Engine, refTime time.Time) *Result {
	targets := Targets(e)
	inputs, pools := Pools(e, refTime)
	cands := enumerate(e, inputs, pools)

	out := &Result{Targets: targets, Evaluated: len(cands)}
	need := map[string]bool{}
	for _, t := range targets {
		if t.Kind != "isolation" {
			need[t.Key] = true
		}
	}
	var chosen []*candidate
	covered := map[string]bool{}
	// Start with the most typical case: the first candidate that is not
	// rejected. Then add cases greedily by new coverage.
	for _, c := range cands {
		if c.res.Reject == nil {
			chosen = append(chosen, c)
			for _, k := range c.covers {
				covered[k] = true
			}
			break
		}
	}
	for {
		var best *candidate
		bestN := 0
		for _, c := range cands {
			n := 0
			for _, k := range c.covers {
				if need[k] && !covered[k] {
					n++
				}
			}
			if n > bestN {
				best, bestN = c, n
			}
		}
		if best == nil {
			break
		}
		chosen = append(chosen, best)
		for _, k := range best.covers {
			covered[k] = true
		}
	}
	cases := make([]Case, len(chosen))
	for i, c := range chosen {
		cases[i] = toCase(e, c)
	}
	// Isolation pairs.
	for _, r := range IsolationRows(e) {
		key := r.ID + ":isolation"
		if a, b, ok := findPair(e, chosen, r); ok {
			addCover(&cases[a], key)
			addCover(&cases[b], key)
			covered[key] = true
			continue
		}
		base, alt := isolate(e, chosen, cands, r)
		if alt == nil {
			continue
		}
		chosen = append(chosen, alt)
		nc := toCase(e, alt)
		nc.ID = "isolate-" + slug(r.ID)
		addCover(&nc, key)
		addCover(&cases[base], key)
		cases = append(cases, nc)
		covered[key] = true
	}
	for _, t := range targets {
		if !covered[t.Key] {
			out.Uncovered = append(out.Uncovered, t)
		}
	}
	nameCases(e, cases, targets)
	out.Cases = cases
	return out
}

func addCover(c *Case, key string) {
	if !slices.Contains(c.Covers, key) {
		c.Covers = append(c.Covers, key)
	}
}

func toCase(e *eval.Engine, c *candidate) Case {
	cs := Case{Input: c.input, Covers: c.covers}
	if c.res.Reject != nil {
		cs.ExpectedReject = c.res.Reject.Code
		return cs
	}
	for _, r := range e.Graph.Order {
		if r.Send == nil {
			continue
		}
		st := c.res.ByRow[r.ID].Send
		switch {
		case st != nil && st.Sent:
			cs.Expected = append(cs.Expected, Field{r.Send.Field, st.Value})
		case st != nil && st.Reason == "value cannot be computed":
			cs.Expected = append(cs.Expected, Field{r.Send.Field, Unknown})
		}
	}
	return cs
}

// findPair looks for two chosen, non-rejected cases whose inputs differ only
// in ancestors of r, with different values of r and different payloads.
func findPair(e *eval.Engine, chosen []*candidate, r *sheet.Row) (int, int, bool) {
	anc := e.Graph.Ancestors(r.ID)
	for i := range chosen {
		for j := i + 1; j < len(chosen); j++ {
			if isolates(e, chosen[i], chosen[j], r, anc) {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

func isolates(e *eval.Engine, a, b *candidate, r *sheet.Row, anc map[string]bool) bool {
	if a.res.Reject != nil || b.res.Reject != nil || a.res.Err != nil || b.res.Err != nil {
		return false
	}
	for k, v := range a.input {
		if !sheet.Equal(v, b.input[k]) && !anc[k] {
			return false
		}
	}
	va, oka := a.res.Values[r.ID]
	vb, okb := b.res.Values[r.ID]
	if !oka || !okb || sheet.Equal(va, vb) {
		return false
	}
	// The payloads must differ in a field that r feeds, so the difference is
	// one that a wrong value in r would cause.
	fa, fb := Flatten(a.res.Payload), Flatten(b.res.Payload)
	desc := e.Graph.Descendants(r.ID)
	for _, x := range e.Graph.Order {
		if x.Send == nil || !desc[x.ID] {
			continue
		}
		wa, ina := fa[x.Send.Field]
		wb, inb := fb[x.Send.Field]
		if ina && inb && !sheet.Equal(wa, wb) {
			return true
		}
	}
	return false
}

// isolate finds a candidate that, paired with a chosen case, isolates r. It
// prefers changing as few inputs as possible, then changing as few payload
// fields as possible.
func isolate(e *eval.Engine, chosen, cands []*candidate, r *sheet.Row) (int, *candidate) {
	anc := e.Graph.Ancestors(r.ID)
	bestBase, bestScore := -1, math.MaxInt
	var best *candidate
	for i, base := range chosen {
		for _, c := range cands {
			if !isolates(e, base, c, r, anc) {
				continue
			}
			changed := 0
			for k, v := range base.input {
				if !sheet.Equal(v, c.input[k]) {
					changed++
				}
			}
			score := changed*1000 + diffFields(base.res.Payload, c.res.Payload)
			if score < bestScore {
				bestBase, bestScore, best = i, score, c
			}
		}
		if best != nil {
			break // pair with the earliest chosen case that works
		}
	}
	return bestBase, best
}

func payloadEqual(a, b map[string]any) bool { return diffFields(a, b) == 0 }

func diffFields(a, b map[string]any) int {
	fa, fb := Flatten(a), Flatten(b)
	n := 0
	for k, v := range fa {
		if w, ok := fb[k]; !ok || !sheet.Equal(v, w) {
			n++
		}
	}
	for k := range fb {
		if _, ok := fa[k]; !ok {
			n++
		}
	}
	return n
}

// Flatten turns a nested payload into dotted field names.
func Flatten(m map[string]any) map[string]any {
	out := map[string]any{}
	var rec func(prefix string, m map[string]any)
	rec = func(prefix string, m map[string]any) {
		for k, v := range m {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			if sub, ok := v.(map[string]any); ok {
				rec(key, sub)
			} else {
				out[key] = v
			}
		}
	}
	rec("", m)
	return out
}

// Pools returns the input rows to vary and candidate values for each.
func Pools(e *eval.Engine, refTime time.Time) ([]*sheet.Row, [][]any) {
	s := e.Sheet
	consts := numericConstants(e)
	var rows []*sheet.Row
	var pools [][]any
	for _, r := range e.Graph.Order {
		if !r.IsInput() || r.SourceKind() == "db" || r.SourceKind() == "config" {
			continue
		}
		var pool []any
		switch {
		case r.Source == graph.RequestTimeSource:
			pool = []any{refTime}
		case r.Type == "boolean":
			pool = []any{false, true}
		case r.Type == "date" || r.Type == "datetime":
			pool = datePool(refTime, consts)
		default:
			pool = valuePool(s, r, consts)
		}
		rows = append(rows, r)
		pools = append(pools, pool)
	}
	return rows, pools
}

// numericConstants collects numbers written in the sheet: in decision-table
// cells, lookup key cells, and expressions.
func numericConstants(e *eval.Engine) []float64 {
	var out []float64
	add := func(v any) {
		if f, ok := v.(float64); ok && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	addCell := func(c sheet.Cell) {
		add(c.Value)
		add(c.Lo)
		add(c.Hi)
	}
	for _, r := range e.Sheet.Rows {
		if r.Table != nil {
			for _, rule := range r.Table.Rules {
				for _, c := range rule.Cells {
					addCell(c)
				}
			}
		}
		if r.Lookup != nil && r.Lookup.Data != nil {
			for _, en := range r.Lookup.Data.Entries {
				for _, c := range en.Cells {
					addCell(c)
				}
			}
		}
		for _, le := range graph.Expressions(r) {
			for _, m := range numberToken.FindAllString(le.Expr, -1) {
				if f, err := strconv.ParseFloat(m, 64); err == nil {
					add(f)
				}
			}
		}
	}
	sort.Float64s(out)
	return out
}

var numberToken = regexp.MustCompile(`\b\d+(\.\d+)?\b`)

// datePool places dates so that whole-year differences from the reference
// time hit each plausible constant and its neighbours. This is a heuristic
// for ages and tenures computed with years_between.
func datePool(ref time.Time, consts []float64) []any {
	years := map[int]bool{}
	for _, c := range consts {
		if c != math.Trunc(c) || c < 0 || c > 120 {
			continue
		}
		for _, d := range []int{-1, 0, 1} {
			if y := int(c) + d; y >= 0 {
				years[y] = true
			}
		}
	}
	if len(years) == 0 {
		years[30] = true
	}
	ys := make([]int, 0, len(years))
	for y := range years {
		ys = append(ys, y)
	}
	// Years near the middle of the candidates come first, so the first
	// cases look typical.
	sort.Ints(ys)
	mid := ys[len(ys)/2]
	sort.Slice(ys, func(i, j int) bool {
		di, dj := math.Abs(float64(ys[i]-mid)), math.Abs(float64(ys[j]-mid))
		if di != dj {
			return di < dj
		}
		return ys[i] < ys[j]
	})
	day := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
	pool := make([]any, len(ys))
	for i, y := range ys {
		// One month before the anniversary, so the difference is exactly y.
		pool[i] = day.AddDate(-y, -1, 0)
	}
	return pool
}

// valuePool collects values for a string or number input from the cells that
// test it: decision-table columns and lookup key columns. Lookup data gives
// values inside the ranges it covers; decision-table comparisons also give a
// value on each side of every bound, so rules such as "< 100" are reachable.
func valuePool(s *sheet.Sheet, r *sheet.Row, consts []float64) []any {
	var cells, tableCells []sheet.Cell
	var keys []string
	for _, x := range s.Rows {
		if x.Table != nil {
			if j := slices.Index(x.Table.Columns, r.ID); j >= 0 {
				for _, rule := range x.Table.Rules {
					if j < len(rule.Cells) {
						tableCells = append(tableCells, rule.Cells[j])
					}
				}
			}
		}
		if x.Lookup != nil {
			if j := slices.Index(x.Lookup.Key, r.ID); j >= 0 {
				if x.Lookup.Data != nil {
					for _, en := range x.Lookup.Data.Entries {
						if j < len(en.Cells) {
							cells = append(cells, en.Cells[j])
						}
					}
				}
				if x.Lookup.Inline != nil {
					keys = append(keys, x.Lookup.InlineOrder...)
				}
			}
		}
	}
	var pool []any
	points := analysis.Points(cells, nil, true)
	if len(tableCells) > 0 {
		points = append(points, analysis.Points(tableCells, nil, false)...)
	}
	bounds := map[string]bool{}
	for _, c := range append(cells, tableCells...) {
		for _, v := range []any{c.Value, c.Lo, c.Hi} {
			if v != nil {
				bounds[sheet.FormatValue(v)] = true
			}
		}
	}
	// Values strictly inside a range read as more typical than its bounds,
	// so they come first.
	sort.SliceStable(points, func(i, j int) bool {
		return !bounds[sheet.FormatValue(points[i])] && bounds[sheet.FormatValue(points[j])]
	})
	for _, p := range points {
		if _, other := p.(analysis.Other); !other {
			pool = appendUniq(pool, p)
		}
	}
	for _, k := range keys {
		pool = appendUniq(pool, sheet.ParseText(k))
	}
	if r.HasDefault {
		pool = appendUniq(pool, r.Default)
	}
	if len(pool) == 0 {
		switch r.Type {
		case "number":
			pool = []any{100.0}
			for _, c := range consts {
				pool = appendUniq(pool, c)
			}
		default:
			pool = []any{"example"}
		}
	}
	return pool
}

func appendUniq(xs []any, v any) []any {
	for _, x := range xs {
		if sheet.Equal(x, v) {
			return xs
		}
	}
	return append(xs, v)
}

// enumerate evaluates the cartesian product of the pools, or a deterministic
// sample of it when it is too large.
func enumerate(e *eval.Engine, rows []*sheet.Row, pools [][]any) []*candidate {
	total := 1
	for _, p := range pools {
		total *= max(len(p), 1)
		if total > math.MaxInt32 {
			break
		}
	}
	step := 1
	if total > MaxCandidates {
		step = total/MaxCandidates + 1
		for gcd(step, total) != 1 {
			step++
		}
	}
	n := min(total, MaxCandidates)
	var out []*candidate
	idx := 0
	for k := 0; k < n; k++ {
		in := map[string]any{}
		rem := idx
		for i := len(rows) - 1; i >= 0; i-- {
			p := pools[i]
			in[rows[i].ID] = p[rem%len(p)]
			rem /= len(p)
		}
		idx = (idx + step) % total
		res := e.Evaluate(eval.Input(in))
		if res.Err != nil {
			continue
		}
		out = append(out, &candidate{input: in, res: res, covers: Covers(e, res), order: k})
	}
	return out
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// nameCases gives each case an ID from the first target it covers.
func nameCases(e *eval.Engine, cases []Case, targets []Target) {
	used := map[string]int{}
	claimed := map[string]bool{}
	for i := range cases {
		c := &cases[i]
		if c.ID == "" {
			c.ID = describe(e, c, targets, claimed)
		}
		for _, k := range c.Covers {
			claimed[k] = true
		}
		base := c.ID
		used[base]++
		if used[base] > 1 {
			c.ID = fmt.Sprintf("%s-%d", base, used[base])
		}
	}
}

func describe(e *eval.Engine, c *Case, targets []Target, claimed map[string]bool) string {
	if c.ExpectedReject != "" {
		return "reject-" + slug(c.ExpectedReject)
	}
	pick := func(skipClaimed bool) string {
		for _, t := range targets {
			if !slices.Contains(c.Covers, t.Key) || (skipClaimed && claimed[t.Key]) {
				continue
			}
			r := e.Sheet.ByID[t.Row]
			switch t.Kind {
			case "table":
				for _, rule := range r.Table.Rules {
					if t.Key == r.ID+":"+rule.Key() {
						return slug(r.ID) + "-" + slug(sheet.FormatValue(rule.Result))
					}
				}
			case "send-when":
				if strings.HasSuffix(t.Key, ":true") {
					return slug(r.ID) + "-sent"
				}
				return slug(r.ID) + "-not-sent"
			case "input-when":
				return slug(r.ID) + "-" + slug(t.Key[strings.LastIndex(t.Key, ":")+1:])
			case "lookup-value":
				return slug(r.ID) + "-" + slug(t.Key[len(r.ID)+1:])
			}
		}
		return ""
	}
	if id := pick(true); id != "" {
		return id
	}
	if id := pick(false); id != "" {
		return id
	}
	return "case"
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
