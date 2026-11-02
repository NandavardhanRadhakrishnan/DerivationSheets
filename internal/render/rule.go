package render

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"derivationsheets/internal/analysis"
	"derivationsheets/internal/sheet"
)

// maxCaseList is the most lookup entries shown inline as a case list.
const maxCaseList = 8

// maxGridSide is the most partitions per axis in an inline 2D grid.
const maxGridSide = 6

func (b *builder) rule(r *sheet.Row) template.HTML {
	if r.IsInput() {
		return b.inputRule(r)
	}
	var out string
	switch r.Rule() {
	case sheet.RuleFormula:
		out = `<span class="formula">` + string(exprHTML(r.Formula, b.ref)) + `</span>`
	case sheet.RuleTable:
		out = b.tableHTML(r)
	case sheet.RuleLookup:
		out = b.lookupHTML(r)
	case sheet.RuleText:
		out = `<span class="text-rule">` + html.EscapeString(r.Text) + `</span> <span class="tag warn">open item</span>`
	default:
		out = `<span class="tag danger">no single rule</span>`
	}
	return template.HTML(out + b.foldedTags(r))
}

// inputRule describes requiredness and defaults of an input.
func (b *builder) inputRule(r *sheet.Row) template.HTML {
	var parts []string
	if r.Usage == sheet.UsageSituational {
		parts = append(parts, fmt.Sprintf("required when %s%s", exprHTML(r.When, b.ref), b.branchMark(r.ID+":when:true")))
	}
	if r.HasDefault {
		parts = append(parts, "if missing: <b>"+html.EscapeString(displayValue(r.Default))+"</b>")
	}
	if r.SourceKind() == "config" || r.SourceKind() == "db" {
		if v, ok := b.s.Config[r.SourcePath()]; ok && r.SourceKind() == "config" {
			parts = append(parts, sourceTag("config", r.SourcePath(), sheet.FormatValue(v)))
		}
	}
	return template.HTML(strings.Join(parts, "; "))
}

// foldedTags renders config and db inputs that feed only this row.
func (b *builder) foldedTags(r *sheet.Row) string {
	var tags []string
	for _, d := range b.e.Graph.Deps[r.ID] {
		if !b.hidden[d] {
			continue
		}
		in := b.s.ByID[d]
		val := ""
		if v, ok := b.s.Config[in.SourcePath()]; ok && in.SourceKind() == "config" {
			val = sheet.FormatValue(v)
		}
		tags = append(tags, sourceTag(in.SourceKind(), in.SourcePath(), val))
	}
	if len(tags) == 0 {
		return ""
	}
	return " " + strings.Join(tags, " ")
}

func sourceTag(kind, path, value string) string {
	label := strings.ToUpper(kind)
	if kind == "config" {
		label = "config"
	}
	text := html.EscapeString(path)
	if value != "" {
		text += " = " + html.EscapeString(value)
	}
	return fmt.Sprintf(`<span class="src"><span class="src-kind">%s</span> %s</span>`, label, text)
}

// displayValue renders a value for reading: booleans as Yes and No.
func displayValue(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "Yes"
		}
		return "No"
	case analysis.Other:
		return "other"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) >= 10000 {
			return thousands(int(x))
		}
	}
	return sheet.FormatValue(v)
}

func cellLabel(c sheet.Cell) string {
	switch c.Kind {
	case sheet.CellAny:
		return "any"
	case sheet.CellLiteral:
		return displayValue(c.Value)
	case sheet.CellCompare:
		op := map[string]string{"<": "<", "<=": "≤", ">": ">", ">=": "≥", "!=": "≠"}[c.Op]
		return op + " " + displayValue(c.Value)
	case sheet.CellRange:
		return c.Raw
	}
	return c.Raw
}

func (b *builder) tableHTML(r *sheet.Row) string {
	t := r.Table
	policy := ""
	if t.HitPolicy == "first" {
		policy = ` <span class="tag">first match</span>`
	}
	switch len(t.Columns) {
	case 1:
		var items []string
		for _, rule := range t.Rules {
			key := r.ID + ":" + rule.Key()
			items = append(items, fmt.Sprintf(`<span class="case %s">%s <b>%s</b>%s</span>`,
				b.branchClass(key), html.EscapeString(cellLabel(rule.Cells[0])), html.EscapeString(displayValue(rule.Result)), b.branchMark(key)))
		}
		return `<span class="cases">` + strings.Join(items, ` <span class="sep">·</span> `) + `</span>` + policy
	case 2:
		if g := b.grid2D(r); g != "" {
			return g + policy
		}
	}
	return b.compactTable(r) + policy
}

// compactTable renders a standard decision table.
func (b *builder) compactTable(r *sheet.Row) string {
	t := r.Table
	var sb strings.Builder
	sb.WriteString(`<table class="dt"><thead><tr>`)
	for _, c := range t.Columns {
		sb.WriteString("<th>" + b.refText(c) + "</th>")
	}
	sb.WriteString(`<th class="res">→</th></tr></thead><tbody>`)
	for _, rule := range t.Rules {
		key := r.ID + ":" + rule.Key()
		sb.WriteString(`<tr class="` + b.branchClass(key) + `">`)
		for _, c := range rule.Cells {
			cls := ""
			if c.Kind == sheet.CellAny {
				cls = ` class="any"`
			}
			sb.WriteString("<td" + cls + ">" + html.EscapeString(cellLabel(c)) + "</td>")
		}
		sb.WriteString(`<td class="res">` + html.EscapeString(displayValue(rule.Result)) + b.branchMark(key) + "</td></tr>")
	}
	sb.WriteString("</tbody></table>")
	return sb.String()
}

// segment is one region of an axis in a 2D grid.
type segment struct {
	rep   any
	label string
	// lo/hi describe numeric or time regions for merging labels.
	lo, hi       any
	loInc, hiInc bool
	ordered      bool
}

// grid2D renders a two-input table with one input on each axis, or "" when
// an axis has too many regions.
func (b *builder) grid2D(r *sheet.Row) string {
	t := r.Table
	axes := [2][]segment{}
	for j := range 2 {
		var cells []sheet.Cell
		for _, rule := range t.Rules {
			cells = append(cells, rule.Cells[j])
		}
		axes[j] = segments(cells, b.domains[t.Columns[j]])
	}
	fire := func(a, c segment) int {
		for i, rule := range t.Rules {
			if analysis.Match(rule.Cells[0], a.rep) && analysis.Match(rule.Cells[1], c.rep) {
				return i
			}
		}
		return -1
	}
	// Merge neighbouring ordered regions that behave the same.
	axes[0] = merge(axes[0], func(x segment) []int {
		out := make([]int, len(axes[1]))
		for i, y := range axes[1] {
			out[i] = fire(x, y)
		}
		return out
	})
	axes[1] = merge(axes[1], func(y segment) []int {
		out := make([]int, len(axes[0]))
		for i, x := range axes[0] {
			out[i] = fire(x, y)
		}
		return out
	})
	if len(axes[0]) > maxGridSide || len(axes[1]) > maxGridSide {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<table class="g2"><thead><tr><th class="corner">` + b.refText(t.Columns[0]) + ` ╲ ` + b.refText(t.Columns[1]) + `</th>`)
	for _, y := range axes[1] {
		sb.WriteString("<th>" + html.EscapeString(y.label) + "</th>")
	}
	sb.WriteString("</tr></thead><tbody>")
	for _, x := range axes[0] {
		sb.WriteString("<tr><th>" + html.EscapeString(x.label) + "</th>")
		for _, y := range axes[1] {
			i := fire(x, y)
			if i < 0 {
				sb.WriteString(`<td class="gap" title="no rule">—</td>`)
				continue
			}
			key := r.ID + ":" + t.Rules[i].Key()
			sb.WriteString(fmt.Sprintf(`<td class="%s" title="rule %d">%s%s</td>`, b.branchClass(key), i+1,
				html.EscapeString(displayValue(t.Rules[i].Result)), b.branchMark(key)))
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</tbody></table>")
	return sb.String()
}

// segments splits an axis into regions: each value of a known domain, each
// literal (plus "other"), or for numbers and times the points and intervals
// between the bounds the cells name.
func segments(cells []sheet.Cell, domain []any) []segment {
	if domain != nil {
		out := make([]segment, len(domain))
		for i, v := range domain {
			out[i] = segment{rep: v, label: displayValue(v)}
		}
		return out
	}
	var nums []float64
	var times []time.Time
	for _, c := range cells {
		for _, v := range []any{c.Value, c.Lo, c.Hi} {
			switch x := v.(type) {
			case float64:
				nums = append(nums, x)
			case time.Time:
				times = append(times, x)
			}
		}
	}
	switch {
	case len(nums) > 0:
		sort.Float64s(nums)
		nums = slices.Compact(nums)
		vals := make([]any, len(nums))
		for i, x := range nums {
			vals[i] = x
		}
		return ordered(vals, func(a, c any) any { return (a.(float64) + c.(float64)) / 2 },
			func(a any) any { return a.(float64) - 1 }, func(a any) any { return a.(float64) + 1 })
	case len(times) > 0:
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		times = slices.CompactFunc(times, func(a, c time.Time) bool { return a.Equal(c) })
		vals := make([]any, len(times))
		for i, x := range times {
			vals[i] = x
		}
		return ordered(vals, func(a, c any) any { return a.(time.Time).Add(c.(time.Time).Sub(a.(time.Time)) / 2) },
			func(a any) any { return a.(time.Time).AddDate(0, 0, -1) }, func(a any) any { return a.(time.Time).AddDate(0, 0, 1) })
	}
	var out []segment
	for _, p := range analysis.Points(cells, nil, false) {
		out = append(out, segment{rep: p, label: displayValue(p)})
	}
	return out
}

func ordered(bounds []any, mid func(a, b any) any, below, above func(any) any) []segment {
	var out []segment
	out = append(out, segment{rep: below(bounds[0]), hi: bounds[0], ordered: true})
	for i, x := range bounds {
		out = append(out, segment{rep: x, lo: x, hi: x, loInc: true, hiInc: true, ordered: true})
		if i+1 < len(bounds) {
			out = append(out, segment{rep: mid(x, bounds[i+1]), lo: x, hi: bounds[i+1], ordered: true})
		}
	}
	out = append(out, segment{rep: above(bounds[len(bounds)-1]), lo: bounds[len(bounds)-1], ordered: true})
	for i := range out {
		out[i].label = rangeLabel(out[i])
	}
	return out
}

// merge joins neighbouring ordered segments whose behaviour is identical.
func merge(segs []segment, behaviour func(segment) []int) []segment {
	if len(segs) == 0 || !segs[0].ordered {
		return segs
	}
	var out []segment
	var prev []int
	for _, s := range segs {
		cur := behaviour(s)
		if len(out) > 0 && slices.Equal(prev, cur) {
			last := &out[len(out)-1]
			last.hi, last.hiInc = s.hi, s.hiInc
			last.label = rangeLabel(*last)
			continue
		}
		out = append(out, s)
		prev = cur
	}
	return out
}

func rangeLabel(s segment) string {
	f := func(v any) string { return displayValue(v) }
	switch {
	case s.lo == nil && s.hi == nil:
		return "any"
	case s.lo == nil:
		if s.hiInc {
			return "≤ " + f(s.hi)
		}
		return "< " + f(s.hi)
	case s.hi == nil:
		if s.loInc {
			return "≥ " + f(s.lo)
		}
		return "> " + f(s.lo)
	case sheet.Equal(s.lo, s.hi):
		return f(s.lo)
	}
	lo, hi := "> ", " and < "
	if s.loInc {
		lo = "≥ "
	}
	if s.hiInc {
		hi = " and ≤ "
	}
	if s.loInc && s.hiInc {
		return f(s.lo) + "–" + f(s.hi)
	}
	return lo + f(s.lo) + hi + f(s.hi)
}

func (b *builder) lookupHTML(r *sheet.Row) string {
	l := r.Lookup
	tag := sourceTag(l.SourceKind(), l.SourcePath(), "")
	if _, shared := b.pseudo[l.From]; shared {
		tag = fmt.Sprintf(`<span class="src"><span class="src-kind">%s</span> %s<sup>%d</sup></span>`,
			strings.ToUpper(l.SourceKind()), html.EscapeString(l.SourcePath()), b.num[b.pseudo[l.From]])
	}
	keys := make([]string, len(l.Key))
	for i, k := range l.Key {
		keys[i] = b.refText(k)
	}
	by := "Look up by " + joinAnd(keys)
	switch {
	case l.Inline != nil && len(l.Key) == 1:
		var items []string
		for _, k := range l.InlineOrder {
			key := r.ID + ":" + k
			items = append(items, fmt.Sprintf(`<span class="case %s">%s <b>%s</b>%s</span>`, b.branchClass(key),
				html.EscapeString(displayValue(sheet.ParseText(k))), html.EscapeString(displayValue(l.Inline[k])), b.branchMark(key)))
		}
		return `<span class="cases">` + strings.Join(items, ` <span class="sep">·</span> `) + `</span> ` + tag
	case l.Data != nil && len(l.Key) == 1 && len(l.Data.Entries) <= maxCaseList && len(l.Data.Entries) > 0:
		var items []string
		for _, en := range l.Data.Entries {
			items = append(items, fmt.Sprintf(`<span class="case">%s <b>%s</b></span>`,
				html.EscapeString(cellLabel(en.Cells[0])), html.EscapeString(displayValue(en.Value))))
		}
		return by + `: <span class="cases">` + strings.Join(items, ` <span class="sep">·</span> `) + `</span> ` + tag
	case l.Data != nil:
		return fmt.Sprintf(`%s %s <span class="muted">%d entries in %s</span>`, by, tag, len(l.Data.Entries), html.EscapeString(relData(b.s.Dir, l.Data.File)))
	case !l.Available():
		return by + " " + tag + ` <span class="tag warn">no design-time values</span>`
	}
	return by + " " + tag
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func relData(dir, path string) string {
	if r, err := filepathRel(dir, path); err == nil {
		return r
	}
	return path
}
