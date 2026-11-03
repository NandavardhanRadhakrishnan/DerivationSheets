// Package analysis checks decision tables and lookup data for missing and
// overlapping cases. It enumerates one representative value for each region
// that the cells in a column distinguish, then evaluates every combination.
package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Other stands for "any value not named in this column". Only "-" and "!="
// cells match it.
type Other struct{}

func (Other) String() string { return "any other value" }

// MaxCombinations bounds the enumeration.
const MaxCombinations = 200000

// Points returns representative values for a column. When domain is non-nil
// it is the complete set of values the column can take, such as the results
// of an upstream decision table. Otherwise points are derived from the cells:
// every literal and bound, a value on each side, and a value between each
// pair of bounds. Two integer bounds that differ by one are treated as
// adjacent, so bands such as [36..45] and [46..55] leave no gap. When bounded
// is set, numeric points stay within the smallest and largest bound, and no
// "other" value is added for strings.
func Points(cells []sheet.Cell, domain []any, bounded bool) []any {
	if domain != nil {
		return domain
	}
	var nums []float64
	var times []time.Time
	var strs []string
	hasBool := false
	seenS := map[string]bool{}
	add := func(v any) {
		switch x := v.(type) {
		case float64:
			nums = append(nums, x)
		case time.Time:
			times = append(times, x)
		case bool:
			hasBool = true
		case string:
			if !seenS[x] {
				seenS[x] = true
				strs = append(strs, x)
			}
		}
	}
	for _, c := range cells {
		switch c.Kind {
		case sheet.CellLiteral, sheet.CellCompare:
			add(c.Value)
		case sheet.CellRange:
			add(c.Lo)
			add(c.Hi)
		}
	}
	var pts []any
	if hasBool {
		pts = append(pts, true, false)
	}
	pts = append(pts, numberPoints(nums, bounded)...)
	pts = append(pts, timePoints(times, bounded)...)
	for _, s := range strs {
		pts = append(pts, s)
	}
	if (len(strs) > 0 || len(pts) == 0) && !bounded {
		pts = append(pts, Other{})
	}
	return pts
}

func numberPoints(xs []float64, bounded bool) []any {
	if len(xs) == 0 {
		return nil
	}
	sort.Float64s(xs)
	xs = uniqF(xs)
	var pts []any
	if !bounded {
		pts = append(pts, xs[0]-1)
	}
	for i, x := range xs {
		pts = append(pts, x)
		if i+1 < len(xs) {
			y := xs[i+1]
			if isInt(x) && isInt(y) && y-x == 1 {
				continue
			}
			pts = append(pts, (x+y)/2)
		}
	}
	if !bounded {
		pts = append(pts, xs[len(xs)-1]+1)
	}
	return pts
}

func timePoints(ts []time.Time, bounded bool) []any {
	if len(ts) == 0 {
		return nil
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	var pts []any
	if !bounded {
		pts = append(pts, ts[0].AddDate(0, 0, -1))
	}
	for i, t := range ts {
		if i > 0 && t.Equal(ts[i-1]) {
			continue
		}
		pts = append(pts, t)
		if i+1 < len(ts) {
			u := ts[i+1]
			if u.Sub(t) > 24*time.Hour {
				pts = append(pts, t.Add(u.Sub(t)/2))
			}
		}
	}
	if !bounded {
		pts = append(pts, ts[len(ts)-1].AddDate(0, 0, 1))
	}
	return pts
}

func uniqF(xs []float64) []float64 {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func isInt(x float64) bool { return x == math.Trunc(x) }

// Match reports whether a cell matches a representative point.
func Match(c sheet.Cell, v any) bool {
	if _, ok := v.(Other); ok {
		return c.Kind == sheet.CellAny || c.Kind == sheet.CellCompare && c.Op == "!="
	}
	return c.Match(v)
}

// Combination is one point per column.
type Combination []any

// Describe renders a combination as "col=value, ...".
func Describe(cols []string, combo Combination) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c + " = " + FormatPoint(combo[i])
	}
	return strings.Join(parts, ", ")
}

// FormatPoint renders a representative value.
func FormatPoint(v any) string {
	if o, ok := v.(Other); ok {
		return o.String()
	}
	if f, ok := v.(float64); ok && !isInt(f) {
		return fmt.Sprintf("%g", f)
	}
	return sheet.FormatValue(v)
}

// Result of a coverage check.
type Result struct {
	// Missing lists combinations that no rule or entry matches.
	Missing []Combination
	// Overlaps lists pairs of rule or entry indexes that both match some
	// combination, with one example combination each.
	Overlaps []Overlap
	// Skipped is set when there were too many combinations to check.
	Skipped bool
	// Total is the number of combinations checked.
	Total int
}

// Overlap is a pair of rules that match the same input.
type Overlap struct {
	A, B    int
	Example Combination
}

// Check enumerates every combination of column points and matches it against
// rows of cells.
func Check(rows [][]sheet.Cell, points [][]any) Result {
	total := 1
	for _, p := range points {
		total *= max(len(p), 1)
		if total > MaxCombinations {
			return Result{Skipped: true}
		}
	}
	res := Result{Total: total}
	seen := map[[2]int]bool{}
	combo := make(Combination, len(points))
	var rec func(col int)
	rec = func(col int) {
		if col == len(points) {
			var hits []int
			for i, cells := range rows {
				ok := len(cells) == len(combo)
				for j := 0; ok && j < len(cells); j++ {
					ok = Match(cells[j], combo[j])
				}
				if ok {
					hits = append(hits, i)
				}
			}
			if len(hits) == 0 {
				res.Missing = append(res.Missing, append(Combination{}, combo...))
			}
			for a := 0; a < len(hits); a++ {
				for b := a + 1; b < len(hits); b++ {
					k := [2]int{hits[a], hits[b]}
					if !seen[k] {
						seen[k] = true
						res.Overlaps = append(res.Overlaps, Overlap{A: hits[a], B: hits[b], Example: append(Combination{}, combo...)})
					}
				}
			}
			return
		}
		for _, p := range points[col] {
			combo[col] = p
			rec(col + 1)
		}
	}
	rec(0)
	return res
}

// Domains infers the complete set of values a row can take, where that is
// knowable: booleans, the results of a decision table, and the values of a
// lookup. Rows whose domain is open are absent.
func Domains(s *sheet.Sheet) map[string][]any {
	out := map[string][]any{}
	for _, r := range s.Rows {
		var vals []any
		switch {
		case r.IsInput() && r.Type == "boolean":
			vals = []any{true, false}
		case r.Rule() == sheet.RuleTable && r.Table != nil:
			for _, rule := range r.Table.Rules {
				vals = appendUniq(vals, rule.Result)
			}
		case r.Rule() == sheet.RuleLookup && r.Lookup != nil && r.Lookup.Available():
			if r.Lookup.Inline != nil {
				for _, k := range r.Lookup.InlineOrder {
					vals = appendUniq(vals, r.Lookup.Inline[k])
				}
			} else {
				for _, e := range r.Lookup.Data.Entries {
					vals = appendUniq(vals, e.Value)
				}
			}
		default:
			continue
		}
		if len(vals) > 0 {
			out[r.ID] = vals
		}
	}
	return out
}

func appendUniq(xs []any, v any) []any {
	for _, x := range xs {
		if sheet.Equal(x, v) {
			return xs
		}
	}
	return append(xs, v)
}

// TablePoints returns representative points for each column of a decision
// table, using inferred domains where known.
func TablePoints(t *sheet.Table, domains map[string][]any) [][]any {
	points := make([][]any, len(t.Columns))
	for j, col := range t.Columns {
		var cells []sheet.Cell
		for _, rule := range t.Rules {
			if j < len(rule.Cells) {
				cells = append(cells, rule.Cells[j])
			}
		}
		points[j] = Points(cells, domains[col], false)
	}
	return points
}

// CheckTable checks a decision table. Overlaps only matter under the unique
// hit policy; under "first" they are dropped from the result.
func CheckTable(t *sheet.Table, domains map[string][]any) Result {
	rows := make([][]sheet.Cell, len(t.Rules))
	for i, r := range t.Rules {
		rows[i] = r.Cells
	}
	res := Check(rows, TablePoints(t, domains))
	if t.HitPolicy == "first" {
		res.Overlaps = nil
	}
	return res
}

// CheckLookupData checks lookup entries for overlaps, and for gaps within the
// range the data covers.
func CheckLookupData(d *sheet.LookupData) Result {
	if d == nil {
		return Result{}
	}
	rows := make([][]sheet.Cell, len(d.Entries))
	for i, e := range d.Entries {
		rows[i] = e.Cells
	}
	points := make([][]any, len(d.Columns))
	for j := range d.Columns {
		var cells []sheet.Cell
		for _, e := range d.Entries {
			if j < len(e.Cells) {
				cells = append(cells, e.Cells[j])
			}
		}
		points[j] = Points(cells, nil, true)
	}
	return Check(rows, points)
}
