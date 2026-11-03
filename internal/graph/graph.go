// Package graph computes row dependencies and evaluation order.
package graph

import (
	"slices"
	"strconv"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/expr"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// RequestTimeSource is the source that resolves today().
const RequestTimeSource = "context.request_time"

// Graph holds dependencies between rows.
type Graph struct {
	// Deps maps a row ID to the defined rows it depends on, in declaration
	// order: its uses, the inputs named in an input row's `when`, and the
	// request-time row when an expression calls today().
	Deps map[string][]string
	// Users is the reverse of Deps.
	Users map[string][]string
	// Order is a stable topological order of all rows: among rows that are
	// ready, the one written first comes first. Nil when there is a cycle.
	Order []*sheet.Row
	// Cycle lists the IDs on one cycle, first ID repeated at the end.
	Cycle []string
}

// RequestTimeRow returns the row whose source is context.request_time.
func RequestTimeRow(s *sheet.Sheet) *sheet.Row {
	for _, r := range s.Rows {
		if r.Source == RequestTimeSource {
			return r
		}
	}
	return nil
}

// Expressions returns every expression on a row, keyed by a label used in
// messages: "formula", "when", "reject.0.when", "send.when".
func Expressions(r *sheet.Row) []LabeledExpr {
	var out []LabeledExpr
	if r.Formula != "" {
		out = append(out, LabeledExpr{"formula", r.Formula})
	}
	if r.When != "" {
		out = append(out, LabeledExpr{"when", r.When})
	}
	for i, rj := range r.Rejects {
		if rj.When != "" {
			out = append(out, LabeledExpr{"reject." + itoa(i) + ".when", rj.When})
		}
	}
	if r.Send != nil && r.Send.When != "" {
		out = append(out, LabeledExpr{"send.when", r.Send.When})
	}
	for _, sd := range r.CallSends {
		if sd.When != "" {
			out = append(out, LabeledExpr{"send.when", sd.When})
		}
	}
	return out
}

// LabeledExpr is an expression and where it sits on its row.
type LabeledExpr struct {
	Key  string
	Expr string
}

// Build computes the graph. Expressions that fail to parse are ignored here;
// the validator reports them.
func Build(s *sheet.Sheet, eng expr.Engine) *Graph {
	g := &Graph{Deps: map[string][]string{}, Users: map[string][]string{}}
	rt := RequestTimeRow(s)
	for _, r := range s.Rows {
		var deps []string
		add := func(id string) {
			if id != r.ID && s.ByID[id] != nil && !slices.Contains(deps, id) {
				deps = append(deps, id)
			}
		}
		for _, u := range r.Uses {
			add(u)
		}
		for _, le := range Expressions(r) {
			if r.IsInput() && le.Key == "when" {
				ids, _ := eng.Identifiers(le.Expr)
				for _, id := range ids {
					add(id)
				}
			}
			if fns, _ := eng.Functions(le.Expr); rt != nil && slices.Contains(fns, "today") {
				add(rt.ID)
			}
		}
		g.Deps[r.ID] = deps
		for _, d := range deps {
			g.Users[d] = append(g.Users[d], r.ID)
		}
	}
	g.Cycle = findCycle(s, g.Deps)
	if g.Cycle == nil {
		g.Order = topo(s, g.Deps)
	}
	return g
}

func topo(s *sheet.Sheet, deps map[string][]string) []*sheet.Row {
	indeg := map[string]int{}
	for _, r := range s.Rows {
		indeg[r.ID] = len(deps[r.ID])
	}
	done := map[string]bool{}
	var order []*sheet.Row
	for len(order) < len(s.ByID) {
		progressed := false
		for _, r := range s.Rows {
			if done[r.ID] || s.ByID[r.ID] != r {
				continue
			}
			ready := true
			for _, d := range deps[r.ID] {
				if !done[d] {
					ready = false
					break
				}
			}
			if ready {
				done[r.ID] = true
				order = append(order, r)
				progressed = true
				break // restart so the earliest ready row always goes next
			}
		}
		if !progressed {
			return nil
		}
	}
	return order
}

func findCycle(s *sheet.Sheet, deps map[string][]string) []string {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = grey
		stack = append(stack, id)
		for _, d := range deps[id] {
			switch color[d] {
			case grey:
				start := slices.Index(stack, d)
				cycle = append(append([]string{}, stack[start:]...), d)
				return true
			case white:
				if visit(d) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	for _, r := range s.Rows {
		if color[r.ID] == white && visit(r.ID) {
			return cycle
		}
	}
	return nil
}

// Ancestors returns every row id reaches through Deps, transitively.
func (g *Graph) Ancestors(id string) map[string]bool {
	return walk(id, g.Deps)
}

// Descendants returns every row that reaches id, transitively.
func (g *Graph) Descendants(id string) map[string]bool {
	return walk(id, g.Users)
}

func walk(id string, edges map[string][]string) map[string]bool {
	seen := map[string]bool{}
	var rec func(string)
	rec = func(x string) {
		for _, y := range edges[x] {
			if !seen[y] {
				seen[y] = true
				rec(y)
			}
		}
	}
	rec(id)
	return seen
}

func itoa(i int) string { return strconv.Itoa(i) }
