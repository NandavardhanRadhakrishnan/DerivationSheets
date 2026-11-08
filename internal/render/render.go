// Package render produces the static calculation sheet: one self-contained
// HTML file that reads fully on paper, with hover highlighting as an extra.
package render

import (
	_ "embed"
	"fmt"
	"html"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/analysis"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/evidence"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

//go:embed sheet.html.tmpl
var pageTemplate string

//go:embed sheet.css
var pageCSS string

//go:embed sheet.js
var pageJS string

//go:embed editor.css
var editorCSS string

//go:embed editor.js
var editorJS string

// Options control rendering.
type Options struct {
	// Run is the evidence to merge in, or nil.
	Run *evidence.Run
	// SnapshotDate overrides the date shown for design-time data. When zero,
	// the newest modification time of the files in data/ is used.
	SnapshotDate time.Time
	// Now is the render time, shown in the footer.
	Now time.Time
	// Evidence is the evidence mode: "shown" or "hidden" set the initial
	// state of the page's evidence switch, "off" leaves evidence out of the
	// page entirely, and "" uses the sheet's display setting.
	Evidence string
}

// EvidenceOff leaves evidence out of the page.
const EvidenceOff = "off"

// Page is the view model for the template.
type Page struct {
	Product, Document string
	Title, Slug       string
	Version           int
	EvidenceLine      string
	SnapshotLine      string
	Generated         string
	Sections          []Section
	GridCols          []GridCol
	Contradictions    []NoteRow
	OpenItems         []NoteRow
	Unexercised       []NoteRow
	// EvidenceUI is set when the page carries evidence behind a switch;
	// EvidenceShown is the switch's initial state.
	EvidenceUI    bool
	EvidenceShown bool
	// Columns holds header labels by key: number, value, rule, send, evidence.
	Columns map[string]string
	// SpanAll is the number of table columns, for full-width rows.
	SpanAll int
	CSS     template.CSS
	JS      template.JS
	// Edit is set when the page is served with --edit.
	Edit *EditView
}

// EditView adds the editor to a page: its starting state as JSON, and the
// editor's own style and script.
type EditView struct {
	State template.JS
	CSS   template.CSS
	JS    template.JS
}

// EditorCSS is the sheet editor's style: its bar, panel and controls,
// which the flow editor shares.
func EditorCSS() string { return editorCSS }

// NewEditView wraps the editor's starting state, which must be JSON with
// "<" escaped, as encoding/json writes it.
func NewEditView(state []byte) *EditView {
	return &EditView{State: template.JS(state), CSS: template.CSS(editorCSS), JS: template.JS(editorJS)}
}

// Section is a group of rows.
type Section struct {
	Title string
	Rows  []*RowView
}

// GridCol is one column of the dependency grid.
type GridCol struct {
	Num int
	ID  string
}

// RowView is one rendered row.
type RowView struct {
	Num         int
	ID          string
	Name        string
	Sub         string // source path, or "" for derived rows
	Description string
	Grid        []GridCell
	Rule        template.HTML
	Rejects     []template.HTML
	Send        *SendView
	Evidence    evidence.Status
	EvClass     string
	Deps        string // displayed dependency IDs, space separated, for hover
	Pseudo      bool   // a db/config source shown as its own line
}

// GridCell is one cell of the dependency grid.
type GridCell struct {
	Dot    bool
	Shaded bool
	Title  string
}

// SendView describes the output column.
type SendView struct {
	Field string
	Usage string
	Rule  template.HTML
	Map   template.HTML
}

// NoteRow is one line in the notes under the sheet.
type NoteRow struct {
	Num  int
	Name string
	Text template.HTML
}

type builder struct {
	e       *eval.Engine
	s       *sheet.Sheet
	run     *evidence.Run
	num     map[string]int // display number per displayed row or pseudo-source key
	names   map[string]string
	hidden  map[string]bool   // config/db inputs folded into their single user
	pseudo  map[string]string // lookup source -> pseudo key, for sources shared by rows
	domains map[string][]any
}

// Build computes the page model.
func Build(e *eval.Engine, opt Options) *Page {
	s := e.Sheet
	b := &builder{
		e: e, s: s, run: opt.Run,
		num: map[string]int{}, names: map[string]string{}, hidden: map[string]bool{}, pseudo: map[string]string{},
		domains: analysis.Domains(s),
	}
	for _, r := range s.Rows {
		b.names[r.ID] = r.DisplayName()
	}
	p := &Page{
		Product: brand.Product, Document: brand.Document,
		Title: s.Title, Slug: s.Slug, Version: s.Version,
		Columns: s.Display.Columns,
		CSS:     template.CSS(pageCSS),
		JS:      template.JS(pageJS),
	}
	if p.Title == "" {
		p.Title = s.Slug
	}
	mode := opt.Evidence
	if mode == "" {
		mode = s.Display.Evidence
	}
	p.EvidenceUI = mode != EvidenceOff
	p.EvidenceShown = mode == sheet.EvidenceShown
	if !p.EvidenceUI {
		b.run = nil
	}

	// Decide which rows are shown, and in which section.
	var request, context, dbcfg, derived []*sheet.Row
	for _, r := range e.Graph.Order {
		switch {
		case !r.IsInput():
			derived = append(derived, r)
		case r.SourceKind() == "request":
			request = append(request, r)
		case r.SourceKind() == "context":
			context = append(context, r)
		case len(e.Graph.Users[r.ID]) == 1:
			b.hidden[r.ID] = true
		default:
			dbcfg = append(dbcfg, r)
		}
	}
	sourceUsers := map[string][]string{}
	var sources []string
	for _, r := range derived {
		if r.Lookup != nil && r.Lookup.From != "" {
			if len(sourceUsers[r.Lookup.From]) == 0 {
				sources = append(sources, r.Lookup.From)
			}
			sourceUsers[r.Lookup.From] = append(sourceUsers[r.Lookup.From], r.ID)
		}
	}
	for _, src := range sources {
		if len(sourceUsers[src]) > 1 {
			b.pseudo[src] = "@" + src
		}
	}

	n := 0
	number := func(id string) { n++; b.num[id] = n }
	for _, r := range request {
		number(r.ID)
	}
	for _, r := range context {
		number(r.ID)
	}
	for _, r := range dbcfg {
		number(r.ID)
	}
	for _, src := range sources {
		if key, ok := b.pseudo[src]; ok {
			number(key)
		}
	}
	for _, r := range derived {
		number(r.ID)
	}

	// Grid columns: shown lines that feed at least one other shown line.
	feeds := map[string]bool{}
	for _, r := range s.Rows {
		for _, d := range b.deps(r) {
			feeds[d] = true
		}
	}
	var cols []GridCol
	for id, k := range b.num {
		if feeds[id] {
			cols = append(cols, GridCol{Num: k, ID: id})
		}
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Num < cols[j].Num })
	p.GridCols = cols

	add := func(title string, views []*RowView) {
		if len(views) > 0 {
			p.Sections = append(p.Sections, Section{Title: title, Rows: views})
		}
	}
	titles := s.Display.Sections
	add(titles["request"], b.views(request, cols))
	add(titles["context"], b.views(context, cols))
	var dbViews []*RowView
	dbViews = append(dbViews, b.views(dbcfg, cols)...)
	for _, src := range sources {
		if key, ok := b.pseudo[src]; ok {
			dbViews = append(dbViews, b.pseudoView(src, key, sourceUsers[src], cols))
		}
	}
	add(titles["data"], dbViews)
	add(titles["derived"], b.views(derived, cols))
	p.SpanAll = len(cols) + 4
	if p.EvidenceUI {
		p.SpanAll++
	}

	p.EvidenceLine = evidenceLine(b.run)
	p.SnapshotLine = snapshotLine(s, opt.SnapshotDate)
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	p.Generated = now.Format("2 Jan 2006 15:04")
	b.notes(p)
	return p
}

// deps returns the displayed lines a row depends on: shown rows and shared
// sources. Folded config rows are left out; they appear as tags.
func (b *builder) deps(r *sheet.Row) []string {
	var out []string
	for _, d := range b.e.Graph.Deps[r.ID] {
		if !b.hidden[d] {
			out = append(out, d)
		}
	}
	if r.Lookup != nil {
		if key, ok := b.pseudo[r.Lookup.From]; ok {
			out = append(out, key)
		}
	}
	return out
}

func (b *builder) views(rows []*sheet.Row, cols []GridCol) []*RowView {
	var out []*RowView
	for _, r := range rows {
		out = append(out, b.view(r, cols))
	}
	return out
}

func (b *builder) grid(num int, deps []string, cols []GridCol) []GridCell {
	cells := make([]GridCell, len(cols))
	for i, c := range cols {
		cells[i].Shaded = c.Num >= num
		for _, d := range deps {
			if d == c.ID {
				cells[i].Dot = true
				cells[i].Title = fmt.Sprintf("line %d uses line %d", num, c.Num)
			}
		}
	}
	return cells
}

func (b *builder) view(r *sheet.Row, cols []GridCol) *RowView {
	v := &RowView{
		Num:         b.num[r.ID],
		ID:          r.ID,
		Name:        r.DisplayName(),
		Description: r.Description,
	}
	if r.IsInput() {
		v.Sub = r.Source
	}
	deps := b.deps(r)
	v.Deps = strings.Join(deps, " ")
	v.Grid = b.grid(v.Num, deps, cols)
	v.Rule = b.rule(r)
	for _, rj := range r.Rejects {
		v.Rejects = append(v.Rejects, template.HTML(fmt.Sprintf(
			`%s → reject <code>%s</code>%s`, exprHTML(rj.When, b.ref), html.EscapeString(rj.Code), b.branchMark(r.ID+":reject:"+rj.Code))))
	}
	v.Send = b.send(r)
	v.Evidence = evidence.RowStatus(b.run, r.ID, b.branches(r))
	v.EvClass = string(v.Evidence.State)
	return v
}

func (b *builder) pseudoView(src, key string, users []string, cols []GridCol) *RowView {
	kind, path, _ := strings.Cut(src, ".")
	v := &RowView{
		Num:    b.num[key],
		ID:     key,
		Name:   path,
		Sub:    src,
		Pseudo: true,
		Grid:   b.grid(b.num[key], nil, cols),
	}
	var used []string
	for _, u := range users {
		used = append(used, b.refText(u))
	}
	v.Rule = template.HTML(sourceTag(kind, path, "") + " shared by " + strings.Join(used, ", "))
	v.EvClass = "none"
	return v
}

// ref renders a reference to a row inside an expression.
func (b *builder) ref(id string) string {
	if _, ok := b.names[id]; !ok {
		return ""
	}
	return b.refText(id)
}

func (b *builder) refText(id string) string {
	name := html.EscapeString(b.names[id])
	if k, ok := b.num[id]; ok {
		return fmt.Sprintf(`<span class="ref" title="%s">%s<sup>%d</sup></span>`, html.EscapeString(id), name, k)
	}
	return fmt.Sprintf(`<span class="ref" title="%s">%s</span>`, html.EscapeString(id), name)
}

// branches lists the evidence branches of a row.
func (b *builder) branches(r *sheet.Row) []string {
	var out []string
	if r.IsInput() && r.Usage == sheet.UsageSituational && r.When != "" {
		out = append(out, r.ID+":when:true", r.ID+":when:false")
	}
	if r.Rule() == sheet.RuleTable {
		for _, rule := range r.Table.Rules {
			out = append(out, r.ID+":"+rule.Key())
		}
	}
	if r.Rule() == sheet.RuleLookup && r.Lookup.Inline != nil {
		for _, k := range r.Lookup.InlineOrder {
			out = append(out, r.ID+":"+k)
		}
	}
	for _, rj := range r.Rejects {
		out = append(out, r.ID+":reject:"+rj.Code)
	}
	if r.Send != nil && r.Send.Usage == sheet.UsageSituational && r.Send.When != "" {
		out = append(out, r.ID+":send.when:true", r.ID+":send.when:false")
	}
	for _, sd := range r.CallSends {
		if sd.Usage == sheet.UsageSituational && sd.When != "" {
			out = append(out, cases.CallWhenKey(r.ID, sd.Target, true), cases.CallWhenKey(r.ID, sd.Target, false))
		}
	}
	return out
}

// branchMark is the evidence mark shown inside a rule cell: nothing when
// verified or when there is no evidence yet.
func (b *builder) branchMark(key string) string {
	if b.run == nil {
		return ""
	}
	st := evidence.BranchStatus(b.run, key)
	switch st.State {
	case evidence.Unexercised:
		return ` <span class="mark evd unexercised" title="never exercised">○</span>`
	case evidence.Contradicted:
		return fmt.Sprintf(` <span class="mark evd contradicted" title="contradicted %d×">✕</span>`, st.Count)
	}
	return ""
}

func (b *builder) branchClass(key string) string {
	if b.run == nil {
		return ""
	}
	return string(evidence.BranchStatus(b.run, key).State)
}

func (b *builder) send(r *sheet.Row) *SendView {
	sd := r.Send
	if sd == nil {
		return nil
	}
	v := &SendView{Field: sd.Field, Usage: sd.Usage}
	if sd.Usage == sheet.UsageSituational {
		other := "otherwise do not send"
		if sd.Otherwise == sheet.OtherwiseSenderDiscretion {
			other = "otherwise at sender's discretion"
		}
		v.Rule = template.HTML(fmt.Sprintf("required when %s%s; %s%s",
			exprHTML(sd.When, b.ref), b.branchMark(r.ID+":send.when:true"), other, b.branchMark(r.ID+":send.when:false")))
	}
	if sd.Map != nil {
		var parts []string
		for _, k := range sd.MapOrder {
			parts = append(parts, fmt.Sprintf(`%s→<code>%s</code>`, html.EscapeString(displayValue(sheet.ParseText(k))), html.EscapeString(sheet.FormatValue(sd.Map[k]))))
		}
		v.Map = template.HTML(strings.Join(parts, " · "))
	}
	return v
}

func evidenceLine(run *evidence.Run) string {
	if run == nil {
		return "Evidence: none recorded for this version. Every rule is asserted (◇)."
	}
	switch run.Kind {
	case evidence.KindTrace:
		period := ""
		if run.PeriodFrom != nil && run.PeriodTo != nil {
			period = ", " + dateRange(*run.PeriodFrom, *run.PeriodTo)
		}
		return fmt.Sprintf("Traces: %s production requests%s", thousands(run.Observations), period)
	default:
		src := ""
		if run.Source != "" {
			src = " from " + filepath.Base(run.Source)
		}
		return fmt.Sprintf("Tests: %d cases%s, run %s", run.Observations, src, run.Created.Format("2 Jan 2006"))
	}
}

func snapshotLine(s *sheet.Sheet, override time.Time) string {
	if !override.IsZero() {
		return "Database and config snapshot: " + override.Format("2 Jan 2006")
	}
	var newest time.Time
	_ = filepath.WalkDir(filepath.Join(s.Dir, "data"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		return nil
	})
	if newest.IsZero() {
		return "Database and config: no design-time data"
	}
	return "Database and config snapshot: design-time data in data/, updated " + newest.Format("2 Jan 2006")
}

func dateRange(a, b time.Time) string {
	if a.Year() == b.Year() && a.Month() == b.Month() {
		return fmt.Sprintf("%d–%s", a.Day(), b.Format("2 Jan 2006"))
	}
	return a.Format("2 Jan 2006") + " – " + b.Format("2 Jan 2006")
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (b *builder) notes(p *Page) {
	for _, r := range b.e.Graph.Order {
		num := b.num[r.ID]
		if b.run != nil {
			if t := b.run.Rows[r.ID]; t != nil {
				for _, c := range t.Contradictions {
					field := fmt.Sprintf("<code>%s</code>", html.EscapeString(c.Field))
					if c.Call != "" {
						field += fmt.Sprintf(" in the call to <code>%s</code>", html.EscapeString(c.Call))
					}
					text := fmt.Sprintf(`case <code>%s</code>, field %s: expected <b>%s</b>, got <b>%s</b>`,
						html.EscapeString(c.Case), field,
						html.EscapeString(sheet.FormatValue(c.Expected)), html.EscapeString(sheet.FormatValue(c.Actual)))
					if c.Note != "" {
						text += " — " + html.EscapeString(c.Note)
					}
					p.Contradictions = append(p.Contradictions, NoteRow{num, r.DisplayName(), template.HTML(text)})
				}
			}
			var never []string
			for _, k := range b.branches(r) {
				if evidence.BranchStatus(b.run, k).State == evidence.Unexercised {
					never = append(never, "<code>"+html.EscapeString(strings.TrimPrefix(k, r.ID+":"))+"</code>")
				}
			}
			if len(never) > 0 {
				p.Unexercised = append(p.Unexercised, NoteRow{num, r.DisplayName(), template.HTML("never exercised: " + strings.Join(never, ", "))})
			}
		}
		if r.Rule() == sheet.RuleText {
			p.OpenItems = append(p.OpenItems, NoteRow{num, r.DisplayName(), template.HTML("rule is text only: " + html.EscapeString(r.Text))})
		}
		for _, q := range r.OpenQuestions {
			p.OpenItems = append(p.OpenItems, NoteRow{num, r.DisplayName(), template.HTML("open question: " + html.EscapeString(q))})
		}
	}
}

// Write renders the page to w.
func Write(w io.Writer, p *Page) error {
	t, err := template.New("sheet").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		// breakDots lets long source paths wrap after dots instead of mid-word.
		"breakDots": func(s string) template.HTML {
			return template.HTML(strings.ReplaceAll(html.EscapeString(s), ".", ".<wbr>"))
		},
	}).Parse(pageTemplate)
	if err != nil {
		return err
	}
	return t.Execute(w, p)
}
