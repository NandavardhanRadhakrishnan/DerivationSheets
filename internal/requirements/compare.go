package requirements

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/diff"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Compare lists what each team has to do for the flow to change from base to
// head: the cells of the head analysis that the base analysis does not have.
// base is nil for a flow that is new. Label and formatting changes are left
// out unless all is set. Both analyses must be free of errors.
func Compare(base, head *flow.Analysis, all bool) ([]Requirement, error) {
	if base != nil && len(base.Errors) > 0 {
		return nil, fmt.Errorf("the flow has errors at the base: %s", base.Errors[0])
	}
	if len(head.Errors) > 0 {
		return nil, fmt.Errorf("the flow has errors at head: %s", head.Errors[0])
	}
	c := &comparer{base: base, head: head, all: all, flowName: head.Flow.Slug,
		compute: map[string]string{}, carry: map[string]string{}, send: map[string]string{},
		schema: map[string]string{}, schemaByRow: map[string][]string{}}
	if err := c.computes(); err != nil {
		return nil, err
	}
	c.boundaries()
	c.carries()
	c.link()
	Sort(c.out)
	return c.out, nil
}

type comparer struct {
	base, head *flow.Analysis
	all        bool
	flowName   string
	out        []Requirement
	// Ids of what has been made, to link `needs`: compute by team|row, carry
	// by team|row, send by boundary index|row, schema by boundary index|field.
	compute, carry, send, schema map[string]string
	schemaByRow                  map[string][]string
	needs                        []need
}

// need is a link to resolve once every requirement exists.
type need struct {
	from string   // requirement id
	keys []string // fallbacks, tried in order
}

func (c *comparer) add(r Requirement) *Requirement {
	r.Flow = c.flowName
	r.ID = MakeID(r.Kind, r.Team, r.Flow, r.Subject)
	c.out = append(c.out, r)
	return &c.out[len(c.out)-1]
}

func teamOf(a *flow.Analysis, row string) (string, bool) {
	i, ok := a.Place[row]
	if !ok {
		return "", false
	}
	return a.Steps[i].Team(), true
}

// rowChanges is what changed in each row of the end-to-end sheet.
func (c *comparer) rowChanges() (map[string]diff.RowChange, error) {
	var baseData []byte
	if c.base != nil {
		var err error
		if baseData, err = os.ReadFile(c.base.Sheet.File); err != nil {
			return nil, err
		}
	}
	headData, err := os.ReadFile(c.head.Sheet.File)
	if err != nil {
		return nil, err
	}
	sc, err := diff.DiffSheet(baseData, headData)
	if err != nil {
		return nil, err
	}
	out := map[string]diff.RowChange{}
	for _, r := range sc.Rows {
		out[r.ID] = r
	}
	return out, nil
}

// describe turns changed parts into the before and after text of a
// requirement; one part reads as its values, several as path=value lists.
func describe(fs []diff.FieldChange) (before, after string) {
	if len(fs) == 1 {
		return fs[0].Before, fs[0].After
	}
	var b, a []string
	for _, f := range fs {
		if f.Before != "" {
			b = append(b, f.Path+"="+f.Before)
		}
		if f.After != "" {
			a = append(a, f.Path+"="+f.After)
		}
	}
	return strings.Join(b, "; "), strings.Join(a, "; ")
}

func (c *comparer) visible(fs []diff.FieldChange) []diff.FieldChange {
	var out []diff.FieldChange
	for _, f := range fs {
		if c.all || !f.Label {
			out = append(out, f)
		}
	}
	return out
}

func (c *comparer) computes() error {
	changes, err := c.rowChanges()
	if err != nil {
		return err
	}
	for _, r := range c.head.Sheet.Rows {
		ht, placed := teamOf(c.head, r.ID)
		if !placed {
			continue
		}
		rc := changes[r.ID]
		fields := c.visible(rc.Fields)
		var bt string
		var was bool
		if c.base != nil {
			bt, was = teamOf(c.base, r.ID)
		}
		req := Requirement{Kind: Compute, Team: ht, Subject: r.ID, Why: fmt.Sprintf("row `%s` of the end-to-end sheet", r.ID)}
		if !c.head.Pinned(r.ID) {
			req.Placed = "default"
		}
		switch {
		case !was:
			req.What = "compute `" + r.ID + "`"
			req.Before, req.After = describe(fields)
			if rc.Status != diff.Added {
				req.Before = ""
			}
		case bt != ht:
			req.What = fmt.Sprintf("compute `%s` (moved from %s)", r.ID, bt)
			req.Before, req.After = describe(fields)
		case len(fields) > 0:
			req.What = "change how `" + r.ID + "` is computed"
			req.Before, req.After = describe(fields)
		default:
			continue
		}
		added := c.add(req)
		c.compute[ht+"|"+r.ID] = added.ID
		if was && bt != ht {
			c.add(Requirement{Kind: Compute, Team: bt, Subject: r.ID + " (remove)", What: "stop computing `" + r.ID + "` (now " + ht + ")",
				Why: fmt.Sprintf("row `%s` moved to %s", r.ID, ht)})
		}
	}
	if c.base == nil {
		return nil
	}
	for _, r := range c.base.Sheet.Rows {
		bt, was := teamOf(c.base, r.ID)
		if _, still := c.head.Place[r.ID]; was && !still {
			c.add(Requirement{Kind: Compute, Team: bt, Subject: r.ID + " (remove)", What: "stop computing `" + r.ID + "`",
				Why: fmt.Sprintf("row `%s` is no longer computed in this flow", r.ID)})
		}
	}
	return nil
}

func bkey(b *flow.Boundary) string { return b.Role + "@" + b.StageID }

type cell struct {
	row    string
	status string
	fix    string
}

// cells indexes the boundary items that say a field is wanted.
func cells(a *flow.Analysis) map[string]cell {
	out := map[string]cell{}
	if a == nil {
		return out
	}
	for _, b := range a.Boundaries {
		for _, it := range b.Items {
			if it.Row == "" || it.Status == flow.Unused {
				continue
			}
			out[bkey(b)+"|"+it.Field] = cell{row: it.Row, status: it.Status, fix: it.Fix}
		}
	}
	return out
}

func (c *comparer) boundaries() {
	was := cells(c.base)
	for _, b := range c.head.Boundaries {
		if b.Iface == nil {
			continue
		}
		for _, it := range b.Items {
			if it.Row == "" || it.Status == flow.Unused {
				continue
			}
			prev, had := was[bkey(b)+"|"+it.Field]
			row := c.head.Sheet.ByID[it.Row]
			if it.Status == flow.Missing && !(had && prev.status == flow.Missing) {
				c.schemaAdd(b, it, row)
			}
			if it.Status == flow.Warn && it.Fix != "" && !(had && prev.status == flow.Warn && prev.fix == it.Fix) {
				c.schemaFix(b, it)
			}
			if had && prev.row == it.Row {
				continue
			}
			c.sendReceive(b, it)
		}
	}
}

func ownerOf(b *flow.Boundary) string {
	if b.Iface.Owner == "" {
		return "No team named"
	}
	return b.Iface.Owner
}

func (c *comparer) schemaAdd(b *flow.Boundary, it flow.Item, row *sheet.Row) {
	typ, known := c.typeOf(row, 0), true
	if typ == "" {
		known = false
	}
	required := row.Usage != sheet.UsageSituational && !(row.Send != nil && row.Send.Usage == sheet.UsageSituational && b.Role == flow.RoleOutput)
	text := typ
	if !known {
		text = "type unknown, set it by hand"
	}
	if required {
		text += ", required"
	} else {
		text += ", optional"
	}
	r := c.add(Requirement{Kind: Schema, Team: ownerOf(b), Subject: b.Iface.Name + ":" + it.Field,
		What: fmt.Sprintf("add `%s` (%s) to %s", it.Field, text, filepath.Base(b.Iface.File)),
		Why:  fmt.Sprintf("%s needs `%s` for row `%s`", b.RoleLabel(), it.Field, it.Row),
		File: b.Iface.File, Edit: &SchemaEdit{Op: "add", Field: it.Field, Type: typ, Required: required}})
	c.schema[fmt.Sprint(b.Index)+"|"+it.Field] = r.ID
	c.schemaByRow[it.Row] = append(c.schemaByRow[it.Row], r.ID)
}

// typeOf is the row's type where it is known: what the sheet says, or
// number for a formula made of numbers and arithmetic. "" when unknown.
func (c *comparer) typeOf(r *sheet.Row, depth int) string {
	if t := flow.RowType(r); t != "" {
		return t
	}
	if r.IsInput() || r.Formula == "" || depth > 8 || strings.ContainsAny(r.Formula, "?:\"'<>=!&|") {
		return ""
	}
	for _, u := range r.Uses {
		d := c.head.Sheet.ByID[u]
		if d == nil || c.typeOf(d, depth+1) != "number" {
			return ""
		}
	}
	return "number"
}

func (c *comparer) schemaFix(b *flow.Boundary, it flow.Item) {
	edit := &SchemaEdit{Field: it.Field}
	var what string
	switch {
	case it.Fix == "required":
		edit.Op, edit.Required = "require", true
		what = fmt.Sprintf("make `%s` required in %s", it.Field, filepath.Base(b.Iface.File))
	case strings.HasPrefix(it.Fix, "type:"):
		edit.Op, edit.Type = "type", strings.TrimPrefix(it.Fix, "type:")
		what = fmt.Sprintf("change the type of `%s` to %s in %s", it.Field, edit.Type, filepath.Base(b.Iface.File))
	default:
		return
	}
	r := c.add(Requirement{Kind: Schema, Team: ownerOf(b), Subject: b.Iface.Name + ":" + it.Field, What: what, Why: it.Note,
		File: b.Iface.File, Edit: edit})
	c.schema[fmt.Sprint(b.Index)+"|"+it.Field] = r.ID
	c.schemaByRow[it.Row] = append(c.schemaByRow[it.Row], r.ID)
}

// sendReceive makes the requirements for a field a boundary now carries, or
// now fills from a different row: the producer sends it and the consumer
// reads it. The input has no producer in the flow, and the output no consumer.
func (c *comparer) sendReceive(b *flow.Boundary, it flow.Item) {
	j := b.Index
	label := fmt.Sprintf("`%s` in %s", it.Field, b.Iface.Name)
	why := fmt.Sprintf("%s now carries `%s` from row `%s`", strings.ToLower(b.RoleLabel()), it.Field, it.Row)
	subject := bkey(b) + ":" + it.Field
	if j >= 1 {
		team := c.head.Steps[j-1].Team()
		r := c.add(Requirement{Kind: Send, Team: team, Subject: subject, What: "send " + label + ", from `" + it.Row + "`", Why: why})
		c.send[fmt.Sprint(j)+"|"+it.Row] = r.ID
		keys := []string{}
		if p, ok := c.head.Place[it.Row]; ok && p == j-1 {
			keys = append(keys, "compute:"+team+"|"+it.Row)
		} else if c.head.Transit[j-1][it.Row] != "" {
			keys = append(keys, "carry:"+team+"|"+it.Row)
		}
		if it.Status == flow.Missing {
			keys = append(keys, "schema:"+fmt.Sprint(j)+"|"+it.Field)
		}
		c.needs = append(c.needs, need{from: r.ID, keys: keys})
	}
	if j < len(c.head.Steps) {
		team := c.head.Steps[j].Team()
		r := c.add(Requirement{Kind: Receive, Team: team, Subject: subject, What: "read " + label, Why: why})
		keys := []string{"send:" + fmt.Sprint(j) + "|" + it.Row}
		if it.Status == flow.Missing {
			keys = append(keys, "schema:"+fmt.Sprint(j)+"|"+it.Field)
		}
		c.needs = append(c.needs, need{from: r.ID, keys: keys})
	}
}

// carries lists the values a team now holds without computing them.
func (c *comparer) carries() {
	held := map[string]bool{}
	if c.base != nil {
		for step, rows := range c.base.Transit {
			for row := range rows {
				held[c.base.Steps[step].Team()+"|"+row] = true
			}
		}
	}
	var steps []int
	for s := range c.head.Transit {
		steps = append(steps, s)
	}
	sort.Ints(steps)
	for _, s := range steps {
		team := c.head.Steps[s].Team()
		var rows []string
		for row := range c.head.Transit[s] {
			rows = append(rows, row)
		}
		sort.Strings(rows)
		for _, row := range rows {
			if held[team+"|"+row] {
				continue
			}
			how := c.head.Transit[s][row]
			// A step that only consumes a value has nothing to carry: the
			// Receive covers it. It carries a value that crosses the next
			// boundary, or one it keeps across a call.
			if how == "in" && !(s+1 < len(c.head.Boundaries) && c.head.Boundaries[s+1].Needs(row)) {
				continue
			}
			what, why := "pass `"+row+"` on to the next boundary", "the value arrives here and is needed later"
			keys := []string{"send:" + fmt.Sprint(s) + "|" + row}
			if how == "kept" {
				what, why = "keep `"+row+"` across the call", "the caller already holds the value and uses it after the response"
				keys = []string{"compute:" + team + "|" + row}
			}
			r := c.add(Requirement{Kind: Carry, Team: team, Subject: fmt.Sprintf("step%d:%s", s+1, row), What: what, Why: why})
			c.carry[team+"|"+row] = r.ID
			c.needs = append(c.needs, need{from: r.ID, keys: keys})
		}
	}
}

// link resolves needs, and makes each Compute need the Schema items that add
// the fields its row is sent in.
func (c *comparer) link() {
	for _, r := range c.out {
		if r.Kind == Compute && !strings.HasSuffix(r.Subject, "(remove)") {
			c.needs = append(c.needs, need{from: r.ID, keys: []string{"rowschema:" + r.Subject}})
		}
	}
	lookup := func(key string) []string {
		kind, rest, _ := strings.Cut(key, ":")
		switch kind {
		case "compute":
			if id := c.compute[rest]; id != "" {
				return []string{id}
			}
		case "carry":
			if id := c.carry[rest]; id != "" {
				return []string{id}
			}
		case "send":
			if id := c.send[rest]; id != "" {
				return []string{id}
			}
		case "schema":
			if id := c.schema[rest]; id != "" {
				return []string{id}
			}
		case "rowschema":
			return c.schemaByRow[rest]
		}
		return nil
	}
	for _, n := range c.needs {
		// Keys are fallbacks, nearest source first: the first that exists wins.
		var ids []string
		for _, k := range n.keys {
			if ids = lookup(k); len(ids) > 0 {
				break
			}
		}
		for i := range c.out {
			if c.out[i].ID == n.from {
				c.out[i].Needs = appendUnique(c.out[i].Needs, ids...)
			}
		}
	}
}

func appendUnique(xs []string, more ...string) []string {
	for _, m := range more {
		dup := false
		for _, x := range xs {
			if x == m {
				dup = true
			}
		}
		if !dup {
			xs = append(xs, m)
		}
	}
	return xs
}
