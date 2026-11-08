package render

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/analysis"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// maxCaseList is the most lookup entries shown inline as a case list.
const maxCaseList = 8

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

// tableHTML draws every decision table the same way, whatever its number
// of inputs: one row per rule, numbered as validation messages number them,
// one column per input, then the result, and the hit policy under it. It is
// the shape the file and the editor use.
func (b *builder) tableHTML(r *sheet.Row) string {
	t := r.Table
	var sb strings.Builder
	sb.WriteString(`<table class="dt"><thead><tr><th class="rn" title="rule">#</th>`)
	for _, c := range t.Columns {
		sb.WriteString("<th>" + b.refText(c) + "</th>")
	}
	sb.WriteString(`<th class="res">→</th></tr></thead><tbody>`)
	for i, rule := range t.Rules {
		key := r.ID + ":" + rule.Key()
		fmt.Fprintf(&sb, `<tr class="%s" data-rule="%d"><td class="rn">%d</td>`, b.branchClass(key), i+1, i+1)
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
	if t.HitPolicy == "first" {
		sb.WriteString(`<span class="tag">first match</span>`)
	} else {
		sb.WriteString(`<span class="tag">one rule per case</span>`)
	}
	return sb.String()
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
