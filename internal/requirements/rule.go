package requirements

import (
	"fmt"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// RuleText is a derived row's rule in one canonical text: what an owner needs
// to implement it, and what two rules are compared by. Input rows have none.
// Runs of whitespace in a formula count as one space.
func RuleText(r *sheet.Row) string {
	var lines []string
	switch {
	case r.IsInput():
		return ""
	case r.Formula != "":
		lines = append(lines, "formula: "+strings.Join(strings.Fields(r.Formula), " "))
	case r.Table != nil:
		policy := "first match"
		if r.Table.HitPolicy != "first" {
			policy = "unique"
		}
		lines = append(lines, "table, "+policy+":")
		for _, rule := range r.Table.Rules {
			var parts []string
			for i, col := range r.Table.Columns {
				text := "any"
				if i < len(rule.Raw) {
					if s := fmt.Sprint(rule.Raw[i]); s != "-" {
						text = s
					}
				}
				parts = append(parts, col+" "+text)
			}
			lines = append(lines, "  "+strings.Join(parts, ", ")+" -> "+fmt.Sprint(rule.Result))
		}
	case r.Lookup != nil:
		l := r.Lookup
		line := "lookup: from " + l.From + ", key " + strings.Join(l.Key, "+")
		if l.ValuesFile != "" {
			line += ", values_file " + l.ValuesFile
		}
		lines = append(lines, line)
		if len(l.InlineOrder) > 0 {
			var vals []string
			for _, k := range l.InlineOrder {
				vals = append(vals, k+"="+fmt.Sprint(l.Inline[k]))
			}
			lines = append(lines, "values: "+strings.Join(vals, ", "))
		}
	case r.Text != "":
		lines = append(lines, "text: "+strings.Join(strings.Fields(r.Text), " "))
	}
	for _, rj := range r.Rejects {
		lines = append(lines, "reject when "+rj.When+" -> "+rj.Code)
	}
	return strings.Join(lines, "\n")
}
