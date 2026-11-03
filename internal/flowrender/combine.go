package flowrender

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/iface"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// A combined grid draws several flows that share interfaces as one table.
// A flow whose input is another's output continues it; a flow from a call's
// request to its response fills that call's callee step; a flow that starts
// at, or ends at, a boundary in the middle of another is drawn after it as
// a branch or a feed. Values line up across flows through the fields of the
// interfaces they share.

type gcol struct {
	flow     int
	boundary bool
	idx      int      // boundary or step index in that flow
	also     [][2]int // other (flow, boundary) pairs merged into this column
	eyebrow  string   // overrides the role label, to mark where a segment attaches
}

// BuildCombined draws flows as one grid. Each analysis must be free of
// errors. check adds the review layer, as for a single flow.
func BuildCombined(title string, as []*flow.Analysis, now time.Time, check bool) (*Page, error) {
	n := len(as)
	name := func(f, b int) string {
		if bd := as[f].Boundaries[b]; bd.Iface != nil {
			return bd.Iface.Name
		}
		return ""
	}
	last := func(f int) int { return len(as[f].Boundaries) - 1 }

	// Relations between flows.
	next := make([]int, n) // chain successor
	callee := map[[2]int]int{}
	placed := make([]bool, n)
	for i := range next {
		next[i] = -1
	}
	isCallee, hasPrev := make([]bool, n), make([]bool, n)
	for c := 0; c < n; c++ {
		for p := 0; p < n; p++ {
			if p == c {
				continue
			}
			for k, b := range as[p].Boundaries {
				if b.Role == flow.RoleRequest && name(p, k) == name(c, 0) && k+1 < len(as[p].Boundaries) && name(p, k+1) == name(c, last(c)) {
					callee[[2]int{p, k}] = c
					isCallee[c] = true
				}
			}
		}
	}
	for c := 0; c < n; c++ {
		if isCallee[c] {
			continue
		}
		for p := 0; p < n; p++ {
			if p != c && !isCallee[p] && next[p] < 0 && name(p, last(p)) != "" && name(p, last(p)) == name(c, 0) {
				next[p] = c
				hasPrev[c] = true
				break
			}
		}
	}

	var cols []gcol
	var walkChain func(f int, skipInput bool)
	walkChain = func(f int, skipInput bool) {
		placed[f] = true
		a := as[f]
		for k := range a.Boundaries {
			if k > 0 {
				step := k - 1
				if c, ok := callee[[2]int{f, step}]; ok && !placed[c] {
					// The callee's own steps take the place of the empty one.
					placed[c] = true
					cols[len(cols)-1].also = append(cols[len(cols)-1].also, [2]int{c, 0})
					for s := range as[c].Steps {
						cols = append(cols, gcol{flow: c, idx: s})
					}
				} else {
					cols = append(cols, gcol{flow: f, idx: step})
				}
			}
			if k == 0 && skipInput {
				continue
			}
			col := gcol{flow: f, boundary: true, idx: k}
			if a.Boundaries[k].Role == flow.RoleResponse {
				if c, ok := callee[[2]int{f, k - 1}]; ok {
					col.also = append(col.also, [2]int{c, last(c)})
				}
			}
			cols = append(cols, col)
		}
		if c := next[f]; c >= 0 && !placed[c] {
			cols[len(cols)-1].also = append(cols[len(cols)-1].also, [2]int{c, 0})
			walkChain(c, true)
		}
	}
	for f := 0; f < n; f++ {
		if !placed[f] && !hasPrev[f] && !isCallee[f] && attachPoint(as, f, name) == "" {
			walkChain(f, false)
		}
	}
	// Branches and feeds: drawn after, labelled with where they attach.
	for f := 0; f < n; f++ {
		if placed[f] {
			continue
		}
		start := len(cols)
		walkChain(f, false)
		if at := attachPoint(as, f, name); at != "" && len(cols) > start {
			cols[start].eyebrow = at
		}
	}

	// Values: link rows of different flows through shared interface fields.
	uf := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if uf[x] == "" || uf[x] == x {
			uf[x] = x
			return x
		}
		uf[x] = find(uf[x])
		return uf[x]
	}
	union := func(x, y string) { uf[find(x)] = find(y) }
	key := func(f int, id string) string { return fmt.Sprintf("%d\x00%s", f, id) }
	for f := 0; f < n; f++ {
		for g := 0; g < n; g++ {
			if f == g {
				continue
			}
			for _, bf := range as[f].Boundaries {
				for _, bg := range as[g].Boundaries {
					if bf.Iface == nil || bg.Iface == nil || bf.Iface.Name != bg.Iface.Name {
						continue
					}
					df, dg := declared(as[f], bf), declared(as[g], bg)
					for field, rf := range df {
						if rg, ok := dg[field]; ok {
							union(key(f, rf), key(g, rg))
						}
					}
				}
			}
		}
	}

	p := &Page{Product: brand.Product, Title: title, CSS: template.CSS(pageCSS), Check: check,
		Generated: now.Format("2 Jan 2006 15:04"), SheetPath: "one per flow"}
	var slugs []string
	for _, a := range as {
		slugs = append(slugs, a.Flow.Slug)
	}
	p.Slug = strings.Join(slugs, " + ")
	p.Flows = slugs
	p.Version = as[0].Flow.Version
	if p.Title == "" {
		p.Title = p.Slug
	}

	stepN := 0
	for _, c := range cols {
		a := as[c.flow]
		if c.boundary {
			b := a.Boundaries[c.idx]
			col := Column{Boundary: true, Eyebrow: b.RoleLabel()}
			if len(c.also) > 0 && (b.Role == flow.RoleOutput || b.Role == flow.RoleInput) {
				col.Eyebrow = "Handoff" // one flow's output is the next one's input
			}
			if c.eyebrow != "" {
				col.Eyebrow = c.eyebrow
			}
			if b.Iface != nil {
				col.Name, col.Status, col.Path = b.Iface.Name, b.Iface.Status, b.Iface.Ref
				col.Meta = iface.KindLabel(b.Iface.Kind)
				if b.Iface.Owner != "" {
					col.Meta += " · " + b.Iface.Owner
				}
				if alias, _ := sources.Split(b.Iface.Ref); alias != "" {
					col.Source = b.Iface.Ref
				}
				if check {
					for _, it := range b.Items {
						switch it.Status {
						case flow.Missing:
							col.Missing++
						case flow.Warn:
							col.Warnings++
						}
					}
				}
			}
			p.Columns = append(p.Columns, col)
			continue
		}
		stepN++
		st := a.Steps[c.idx]
		meta := st.RoleLabel() + " · " + a.Flow.Slug
		p.Columns = append(p.Columns, Column{Eyebrow: fmt.Sprintf("Step %d", stepN), Name: st.Team(), Meta: meta})
	}
	p.SpanAll = len(p.Columns) + 1

	// One grid row per linked value.
	type member struct {
		flow int
		row  *sheet.Row
	}
	classes := map[string][]member{}
	var order []string
	for f, a := range as {
		for _, r := range a.Engine.Graph.Order {
			if r.IsInput() && r.SourceKind() != "request" && r.SourceKind() != "call" {
				continue
			}
			k := find(key(f, r.ID))
			if classes[k] == nil {
				order = append(order, k)
			}
			classes[k] = append(classes[k], member{f, r})
		}
	}
	names := make([]map[string]string, n)
	for f, a := range as {
		names[f] = map[string]string{}
		for _, r := range a.Sheet.Rows {
			names[f][r.ID] = r.DisplayName()
		}
	}
	type built struct {
		row   Row
		first int
		input bool
	}
	var rows []built
	for _, k := range order {
		ms := classes[k]
		rowIn := func(f int) *sheet.Row {
			for _, m := range ms {
				if m.flow == f {
					return m.row
				}
			}
			return nil
		}
		b := built{row: Row{ID: ms[0].row.ID, Name: ms[0].row.DisplayName()}, first: len(cols)}
		var ids []string
		for _, m := range ms {
			if !contains(ids, m.row.ID) {
				ids = append(ids, m.row.ID)
			}
		}
		b.row.ID = strings.Join(ids, " · ")
		for ci, c := range cols {
			var cell Cell
			if c.boundary {
				cell = Cell{Boundary: true}
				pairs := append([][2]int{{c.flow, c.idx}}, c.also...)
				for _, pr := range pairs {
					if r := rowIn(pr[0]); r != nil {
						if bc := boundaryCell(as[pr[0]].Boundaries[pr[1]], r, check); bc.HTML != "" {
							cell = bc
							break
						}
					}
				}
			} else if r := rowIn(c.flow); r != nil {
				cell = stepCell(as[c.flow], c.idx, r, names[c.flow])
			}
			if cell.HTML != "" && ci < b.first {
				b.first = ci
				if c.boundary && ci == 0 {
					b.input = true
				}
			}
			b.row.Cells = append(b.row.Cells, cell)
		}
		rows = append(rows, b)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].first < rows[j].first })
	var req, der []Row
	for _, b := range rows {
		if b.input {
			req = append(req, b.row)
		} else {
			der = append(der, b.row)
		}
	}
	if len(req) > 0 {
		p.Groups = append(p.Groups, Group{Title: "From the request", Rows: req})
	}
	if len(der) > 0 {
		p.Groups = append(p.Groups, Group{Title: "Derived and received", Rows: der})
	}
	if check {
		seen := map[string]bool{}
		byTeam := map[string]*AgendaTeam{}
		var teams []string
		for _, a := range as {
			for _, t := range agenda(a) {
				for _, it := range t.Items {
					k := t.Team + it.Interface + string(it.Text)
					if seen[k] {
						continue
					}
					seen[k] = true
					if byTeam[t.Team] == nil {
						byTeam[t.Team] = &AgendaTeam{Team: t.Team}
						teams = append(teams, t.Team)
					}
					byTeam[t.Team].Items = append(byTeam[t.Team].Items, it)
				}
			}
		}
		sort.Strings(teams)
		for _, t := range teams {
			p.Agenda = append(p.Agenda, *byTeam[t])
		}
	}
	return p, nil
}

// attachPoint describes where a flow joins another in the middle: a branch
// starting at an interface another flow produces midway, or a feed ending at
// one. Empty when it joins nothing midway.
func attachPoint(as []*flow.Analysis, f int, name func(int, int) string) string {
	in, out := name(f, 0), name(f, len(as[f].Boundaries)-1)
	for g := range as {
		if g == f {
			continue
		}
		for k := 1; k < len(as[g].Boundaries)-1; k++ {
			switch name(g, k) {
			case in:
				return "Branch from " + in
			case out:
				return "Feeds " + out
			}
		}
	}
	return ""
}

// declared maps field names at a boundary to the rows that take them: the
// fields the interface carries, and also the names each side declares for
// a field the interface does not have yet (a send field, a request path, a
// call field). So a value still links across a boundary while the field is
// being agreed.
func declared(a *flow.Analysis, b *flow.Boundary) map[string]string {
	out := map[string]string{}
	for _, r := range a.Sheet.Rows {
		if c := b.CarryOf(r.ID); c != nil {
			out[c.Field.Name] = r.ID
		}
	}
	for _, r := range a.Sheet.Rows {
		var name string
		switch b.Role {
		case flow.RoleInput:
			if r.IsInput() && r.SourceKind() == "request" {
				name = r.SourcePath()
			}
		case flow.RoleOutput:
			if r.Send != nil {
				name = r.Send.Field
			}
		case flow.RoleRequest:
			for _, sd := range r.CallSends {
				if sd.Target == b.StageID {
					name = sd.Field
				}
			}
		case flow.RoleResponse:
			if r.IsInput() && r.SourceKind() == "call" {
				if stage, f, ok := strings.Cut(r.SourcePath(), "."); ok && stage == b.StageID {
					name = f
				}
			}
		}
		if name != "" && out[name] == "" {
			out[name] = r.ID
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
