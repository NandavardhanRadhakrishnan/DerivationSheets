package tomledit

import (
	"regexp"
	"strings"
)

// Layout is the canonical form of one kind of file, by table path: "" for
// the top level, "row" for [[row]], "row.send" for its [row.send].
type Layout struct {
	Tables map[string]TableLayout
}

// TableLayout is the canonical form of one table.
type TableLayout struct {
	// Order lists keys in canonical order; other keys follow in their
	// current order.
	Order []string
	// Unaligned keys are written "key = value"; every other key lines up
	// its "=" with the longest of them.
	Unaligned []string
	// Multiline arrays are written one element per line, cells lined up.
	Multiline []string
	// Subs orders sub-tables; others follow in their current order.
	Subs []string
}

// Sheet is the layout of specs/order-checkout/sheet.toml.
var Sheet = &Layout{Tables: map[string]TableLayout{
	"": {Order: []string{"sheet", "version", "title", "request_schema", "target_schema"},
		Unaligned: []string{"request_schema", "target_schema"}},
	"row": {Order: []string{"id", "name", "description", "source", "type", "usage", "when", "default", "uses", "formula", "lookup", "values", "values_file", "text", "open_questions"},
		Unaligned: []string{"description", "values_file", "open_questions"}, Subs: []string{"table", "reject", "send"}},
	"row.table": {Order: []string{"hit_policy", "columns", "rows"},
		Unaligned: []string{"hit_policy", "columns", "rows"}, Multiline: []string{"rows"}},
	"row.reject":       {Order: []string{"when", "code", "message"}},
	"row.send":         {Order: []string{"target", "field", "usage", "when", "otherwise", "map"}},
	"display":          {Order: []string{"evidence"}, Subs: []string{"columns", "sections"}},
	"display.columns":  {Order: []string{"number", "value", "rule", "send", "evidence"}},
	"display.sections": {Order: []string{"request", "context", "data", "derived"}},
}}

// Flow is the layout of flows/checkout-to-payment/flow.toml.
var Flow = &Layout{Tables: map[string]TableLayout{
	"":      {Order: []string{"flow", "version", "title", "sheet"}},
	"input": {Order: []string{"interface", "carries"}},
	"stage": {Order: []string{"id", "kind", "interface", "carries", "producer", "caller", "computes", "callee", "callee_computes"},
		Unaligned: []string{"callee_computes"}, Subs: []string{"request", "response"}},
	"stage.request":  {Order: []string{"interface", "carries"}},
	"stage.response": {Order: []string{"interface", "carries"}},
	"output":         {Order: []string{"interface", "carries", "producer", "computes"}},
}}

// Interface is the layout `dsheets interface import` writes.
var Interface = &Layout{Tables: map[string]TableLayout{
	"":      {Order: []string{"interface", "title", "kind", "owner", "status", "version", "imported", "description"}},
	"field": {Order: []string{"name", "type", "required", "enum", "local", "description"}, Unaligned: []string{"description"}},
}}

func (l *Layout) table(path string) TableLayout {
	if l == nil {
		return TableLayout{}
	}
	return l.Tables[path]
}

// rank is the position of name in order, or len(order) when absent.
func rank(order []string, name string) int {
	for i, x := range order {
		if x == name {
			return i
		}
	}
	return len(order)
}

func has(xs []string, x string) bool { return rank(xs, x) < len(xs) }

var firstKey = regexp.MustCompile(`^([A-Za-z0-9_\-]+)\s*=`)

// LayoutFor picks a layout by the file's first top-level key: sheet, flow
// or interface. It returns nil for anything else.
func LayoutFor(data []byte) *Layout {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := firstKey.FindStringSubmatch(t)
		if m == nil {
			return nil
		}
		switch m[1] {
		case "sheet":
			return Sheet
		case "flow":
			return Flow
		case "interface":
			return Interface
		}
		return nil
	}
	return nil
}
