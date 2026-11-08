// Package flowrender draws a flow horizontally: one row per value, and
// columns alternating between interfaces (boundaries) and the team steps
// between them, from the input on the left to the output on the right.
//
// By default the page is a plain visualization: who computes what, and
// which field carries each value. Check mode adds the review: missing and
// unfit fields marked in the table, counts, the "To agree" list grouped by
// owning team, and the fields nobody uses.
package flowrender

import (
	_ "embed"
	"fmt"
	"html"
	"html/template"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/iface"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/render"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

//go:embed flow.html.tmpl
var pageTemplate string

//go:embed flow.css
var pageCSS string

// Page is the template model.
type Page struct {
	Product   string
	Title     string
	Slug      string
	Version   int
	SheetPath string
	Summary   string
	Generated string
	Columns   []Column
	SpanAll   int
	Groups    []Group
	Agenda    []AgendaTeam
	Unused    []UnusedLine
	CSS       template.CSS
	// Check is set in check mode.
	Check bool
	// Flows lists the flows of a combined grid; empty for a single flow.
	Flows []string
	// Edit is set when the page is served with --edit.
	Edit *EditView
}

// EditView adds the flow editor to a page: its starting state as JSON, and
// the editor's style and script. A view with no script only marks the page
// up for the editor, as a check's preview needs.
type EditView struct {
	State template.JS
	CSS   template.CSS
	JS    template.JS
}

//go:embed flowedit.css
var editCSS string

//go:embed flowedit.js
var editJS string

// NewEditView wraps the editor's starting state, which must be JSON with
// "<" escaped, as encoding/json writes it.
func NewEditView(state []byte) *EditView {
	return &EditView{State: template.JS(state), CSS: template.CSS(render.EditorCSS() + editCSS), JS: template.JS(editJS)}
}

// Column is a boundary or a step. Both have the same three header lines:
// Eyebrow (the boundary's role, or "Step n"), Name (the interface, or the
// team), and Meta (kind and owner, or the team's role).
type Column struct {
	Boundary bool
	// Index is the boundary's or the step's position in the analysis.
	Index    int
	Eyebrow  string
	Name     string
	Meta     string
	Status   string // existing or proposed, for boundaries
	Source   string
	Path     string
	Missing  int
	Warnings int
}

// Group is a block of rows under a heading.
type Group struct {
	Title string
	Rows  []Row
}

// Row is one value across the flow.
type Row struct {
	ID    string
	Name  string
	Cells []Cell
}

// Cell is one value at one column.
type Cell struct {
	Boundary bool
	Class    string
	HTML     template.HTML
	Title    string
}

// AgendaTeam lists what one team is asked to change.
type AgendaTeam struct {
	Team  string
	Items []AgendaItem
}

// AgendaItem is one change request or decision.
type AgendaItem struct {
	Class     string
	Mark      string
	Interface string
	Text      template.HTML
}

// UnusedLine lists fields of a boundary the flow does not use.
type UnusedLine struct {
	Interface string
	Fields    string
}

// Build computes the page for an analysis without errors. check adds the
// review layer to the plain visualization.
func Build(a *flow.Analysis, now time.Time, check bool) *Page {
	f, s := a.Flow, a.Sheet
	p := &Page{
		Product: brand.Product, Title: f.Title, Slug: f.Slug, Version: f.Version,
		Generated: now.Format("2 Jan 2006 15:04"), CSS: template.CSS(pageCSS), Check: check,
	}
	if p.Title == "" {
		p.Title = f.Slug
	}
	if alias, _ := sources.Split(f.SheetRef); alias != "" {
		// From another repository: name it as written, and its commit.
		p.SheetPath = f.SheetRef
		if a.SheetRev != "" {
			p.SheetPath += " @ " + a.SheetRev
		}
	} else if rel, err := filepath.Rel(f.Dir, s.File); err == nil {
		p.SheetPath = filepath.ToSlash(rel)
	}
	c := a.Counts()
	p.Summary = fmt.Sprintf("%d interfaces, %d team steps · %d missing · %d warnings · %d carried",
		len(a.Boundaries), len(a.Steps), c[flow.Missing], c[flow.Warn], c[flow.Carried])

	names := map[string]string{}
	for _, r := range s.Rows {
		names[r.ID] = r.DisplayName()
	}
	// Columns alternate: boundary 0, step 0, boundary 1, ..., boundary m.
	for i, b := range a.Boundaries {
		if i > 0 {
			st := a.Steps[i-1]
			p.Columns = append(p.Columns, Column{Index: st.Index, Eyebrow: st.Label(), Name: st.Team(), Meta: st.RoleLabel()})
		}
		col := Column{Boundary: true, Index: i, Eyebrow: b.RoleLabel()}
		if b.Iface != nil {
			col.Name, col.Status = b.Iface.Name, b.Iface.Status
			col.Meta = iface.KindLabel(b.Iface.Kind)
			if b.Iface.Owner != "" {
				col.Meta += " · " + b.Iface.Owner
			}
			// Plain paths are noise in a header; source aliases say which
			// team repository the interface comes from.
			if alias, _ := sources.Split(b.Iface.Ref); alias != "" {
				col.Source = b.Iface.Ref
				if b.Iface.Revision != "" {
					col.Source += " @ " + b.Iface.Revision
				}
			}
			col.Path = b.Iface.Ref
			for _, it := range b.Items {
				if !check {
					break
				}
				switch it.Status {
				case flow.Missing:
					col.Missing++
				case flow.Warn:
					col.Warnings++
				}
			}
		}
		p.Columns = append(p.Columns, col)
	}

	p.SpanAll = len(p.Columns) + 1
	var request, derived []Row
	for _, r := range a.Engine.Graph.Order {
		if r.IsInput() && r.SourceKind() != "request" {
			continue // context, database and config values belong inside a step
		}
		row := Row{ID: r.ID, Name: r.DisplayName()}
		for i, b := range a.Boundaries {
			if i > 0 {
				row.Cells = append(row.Cells, stepCell(a, i-1, r, names))
			}
			row.Cells = append(row.Cells, boundaryCell(b, r, check))
		}
		if r.IsInput() {
			request = append(request, row)
		} else {
			derived = append(derived, row)
		}
	}
	if len(request) > 0 {
		p.Groups = append(p.Groups, Group{Title: "From the request", Rows: request})
	}
	if len(derived) > 0 {
		p.Groups = append(p.Groups, Group{Title: "Derived", Rows: derived})
	}
	if !check {
		return p
	}
	p.Agenda = agenda(a)
	for _, b := range a.Boundaries {
		if b.Iface == nil {
			continue
		}
		var fields []string
		for _, it := range b.Items {
			if it.Status == flow.Unused && it.Row == "" {
				fields = append(fields, it.Field)
			}
		}
		if len(fields) > 0 {
			p.Unused = append(p.Unused, UnusedLine{Interface: b.Iface.Name, Fields: strings.Join(fields, ", ")})
		}
	}
	return p
}

func itemFor(b *flow.Boundary, row string) *flow.Item {
	for i := range b.Items {
		if b.Items[i].Row == row {
			return &b.Items[i]
		}
	}
	return nil
}

func boundaryCell(b *flow.Boundary, r *sheet.Row, check bool) Cell {
	c := Cell{Boundary: true}
	it := itemFor(b, r.ID)
	if it == nil {
		return c
	}
	field := "<code>" + html.EscapeString(it.Field) + "</code>"
	if !check {
		// The plain view shows what the interface carries, and nothing else.
		if it.Status == flow.Missing {
			return c
		}
		c.Class = "carried"
		c.HTML = template.HTML(field)
		if b.Role == flow.RoleOutput && r.Send != nil {
			c.HTML += template.HTML(` <span class="usage">` + html.EscapeString(r.Send.Usage) + `</span>`)
		}
		return c
	}
	auto := ""
	if it.Auto {
		auto = ` <span class="auto" title="matched by name; list it under carries to make it explicit">≈</span>`
	}
	switch it.Status {
	case flow.Carried:
		c.Class = "carried"
		c.HTML = template.HTML(field + auto)
		if b.Role == flow.RoleOutput && r.Send != nil {
			c.HTML += template.HTML(` <span class="usage">` + html.EscapeString(r.Send.Usage) + `</span>`)
		}
	case flow.Warn:
		c.Class = "warning"
		c.HTML = template.HTML(field + auto + ` <span class="mark">⚠</span>`)
		c.Title = it.Note
	case flow.Missing:
		c.Class = "missing"
		c.HTML = template.HTML(`<span class="mark">✕</span> add ` + field)
		c.Title = it.Reason()
	case flow.Unused:
		c.Class = "unused"
		c.HTML = template.HTML(field)
		c.Title = it.Note
	}
	return c
}

func stepCell(a *flow.Analysis, step int, r *sheet.Row, names map[string]string) Cell {
	if p, ok := a.Place[r.ID]; ok && p == step {
		return Cell{Class: "computed", HTML: ruleHTML(r, names)}
	}
	switch a.Transit[step][r.ID] {
	case "in":
		return Cell{Class: "transit", HTML: "→", Title: "passes through"}
	case "kept":
		return Cell{Class: "transit", HTML: "↷ kept", Title: "kept by the caller across the call"}
	}
	return Cell{}
}

// ruleHTML is a one-line form of a rule; the derivation sheet has the detail.
func ruleHTML(r *sheet.Row, names map[string]string) template.HTML {
	var out string
	switch r.Rule() {
	case sheet.RuleFormula:
		out = `<span class="formula">` + string(render.ExprHTML(r.Formula, names)) + `</span>`
	case sheet.RuleTable:
		cols := make([]string, len(r.Table.Columns))
		for i, c := range r.Table.Columns {
			cols[i] = html.EscapeString(names[c])
		}
		out = fmt.Sprintf(`decision table on %s <span class="muted">(%d rules)</span>`, strings.Join(cols, ", "), len(r.Table.Rules))
	case sheet.RuleLookup:
		out = fmt.Sprintf(`look up in <code>%s</code>`, html.EscapeString(r.Lookup.From))
	case sheet.RuleText:
		out = `<i>` + html.EscapeString(r.Text) + `</i>`
	}
	for _, rj := range r.Rejects {
		out += fmt.Sprintf(` <span class="reject" title="%s">rejects %s</span>`, html.EscapeString(rj.When), html.EscapeString(rj.Code))
	}
	return template.HTML(out)
}

// agenda groups missing fields and warnings by the team that owns each
// interface: what each team is asked to change or decide.
func agenda(a *flow.Analysis) []AgendaTeam {
	byTeam := map[string][]AgendaItem{}
	for _, b := range a.Boundaries {
		if b.Iface == nil {
			continue
		}
		team := b.Iface.Owner
		if team == "" {
			team = "No owner named"
		}
		for _, it := range b.Items {
			var item AgendaItem
			switch it.Status {
			case flow.Missing:
				item = AgendaItem{Class: "missing", Mark: "✕"}
				reason := it.Reason()
				item.Text = template.HTML(fmt.Sprintf("<code>%s</code>: %s", html.EscapeString(it.Field), html.EscapeString(reason)))
			case flow.Warn:
				item = AgendaItem{Class: "warning", Mark: "⚠", Text: template.HTML(fmt.Sprintf("<code>%s</code>: %s",
					html.EscapeString(it.Field), html.EscapeString(it.Note)))}
			default:
				continue
			}
			item.Interface = b.Iface.Name
			byTeam[team] = append(byTeam[team], item)
		}
	}
	teams := make([]string, 0, len(byTeam))
	for t := range byTeam {
		teams = append(teams, t)
	}
	sort.Strings(teams)
	out := make([]AgendaTeam, len(teams))
	for i, t := range teams {
		out[i] = AgendaTeam{Team: t, Items: byTeam[t]}
	}
	return out
}

// Write renders the page.
func Write(w io.Writer, p *Page) error {
	t, err := template.New("flow").Parse(pageTemplate)
	if err != nil {
		return err
	}
	return t.ExecuteTemplate(w, "page", p)
}
