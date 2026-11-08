// Package validate checks a sheet and reports problems with file and line.
package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/analysis"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/expr"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/graph"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/schema"
)

// Severity of a diagnostic.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Diagnostic is one reported problem.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"`
	Row      string   `json:"row,omitempty"`
	Message  string   `json:"message"`
	// Rules are the decision-table rules the diagnostic is about, from 1.
	Rules []int `json:"rules,omitempty"`
}

func (d Diagnostic) String() string {
	loc := d.File
	if d.Line > 0 {
		loc += ":" + strconv.Itoa(d.Line)
	}
	row := ""
	if d.Row != "" {
		row = fmt.Sprintf("row %q: ", d.Row)
	}
	return fmt.Sprintf("%s: %s[%s]: %s%s", loc, d.Severity, d.Code, row, d.Message)
}

// Report is the outcome of validation.
type Report struct {
	Diagnostics []Diagnostic
}

// Count returns the number of diagnostics with the given severity.
func (r *Report) Count(s Severity) int {
	n := 0
	for _, d := range r.Diagnostics {
		if d.Severity == s {
			n++
		}
	}
	return n
}

// OK reports whether there are no errors.
func (r *Report) OK() bool { return r.Count(Error) == 0 }

type checker struct {
	s     *sheet.Sheet
	x     expr.Engine
	g     *graph.Graph
	rep   *Report
	index map[*sheet.Row]int
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate runs every check on a loaded sheet.
func Validate(s *sheet.Sheet) *Report {
	c := &checker{s: s, rep: &Report{}, index: map[*sheet.Row]int{}}
	ids := make([]string, 0, len(s.Rows))
	for i, r := range s.Rows {
		c.index[r] = i
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	x, err := expr.NewCEL(uniq(ids))
	if err != nil {
		c.add(Error, "internal", nil, 0, "cannot build expression environment: %v", err)
		return c.rep
	}
	c.x = x
	c.g = graph.Build(s, x)

	c.checkSchema()
	for _, is := range s.LoadIssues {
		code := is.Code
		if code == "" {
			code = "data"
		}
		c.rep.Diagnostics = append(c.rep.Diagnostics, Diagnostic{Severity: Error, Code: code, File: is.File, Line: is.Line, Row: is.Row, Message: is.Message})
	}
	c.checkIDs()
	for _, r := range s.Rows {
		c.checkRow(r)
	}
	c.checkCycle()
	c.checkSends()
	c.checkUnused()
	c.checkTarget()
	sort.SliceStable(c.rep.Diagnostics, func(i, j int) bool {
		a, b := c.rep.Diagnostics[i], c.rep.Diagnostics[j]
		if a.File != b.File {
			return a.File == s.File
		}
		return a.Line < b.Line
	})
	return c.rep
}

func (c *checker) add(sev Severity, code string, r *sheet.Row, line int, format string, args ...any) {
	d := Diagnostic{Severity: sev, Code: code, File: c.s.File, Line: line, Message: fmt.Sprintf(format, args...)}
	if r != nil {
		d.Row = r.ID
		if d.Line == 0 {
			d.Line = c.s.Pos.RowLine(c.index[r])
		}
	}
	c.rep.Diagnostics = append(c.rep.Diagnostics, d)
}

// about records the decision-table rules, counted from 0, that the last
// diagnostic is about.
func (c *checker) about(rules ...int) {
	d := &c.rep.Diagnostics[len(c.rep.Diagnostics)-1]
	for _, j := range rules {
		d.Rules = append(d.Rules, j+1)
	}
}

func (c *checker) line(r *sheet.Row, keys ...string) int {
	return c.s.Pos.RowLine(c.index[r], keys...)
}

// checkSchema validates the decoded TOML against the embedded JSON Schema.
func (c *checker) checkSchema() {
	problems, err := SchemaProblems(schema.Sheet, c.s.Raw)
	if err != nil {
		c.add(Error, "internal", nil, 0, "schema check: %v", err)
		return
	}
	seen := map[string]bool{}
	for _, u := range problems {
		msg := u.Message
		loc := u.Location
		key := strings.Join(loc, "/") + msg
		// IDs get a dedicated, clearer check.
		if len(loc) == 3 && loc[0] == "row" && loc[2] == "id" {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		line, row := c.schemaLocation(loc)
		where := strings.Join(loc, ".")
		if row != nil {
			where = strings.Join(loc[2:], ".")
		}
		if where != "" {
			msg = where + ": " + msg
		}
		c.add(Error, "schema", row, line, "%s", msg)
	}
}

func (c *checker) schemaLocation(loc []string) (int, *sheet.Row) {
	if len(loc) >= 2 && loc[0] == "row" {
		i, err := strconv.Atoi(loc[1])
		if err == nil && i < len(c.s.Rows) {
			keys := loc[2:]
			var cand []string
			for n := len(keys); n > 0; n-- {
				cand = append(cand, strings.Join(keys[:n], "."))
			}
			return c.s.Pos.RowLine(i, cand...), c.s.Rows[i]
		}
	}
	for n := len(loc); n > 0; n-- {
		if line, ok := c.s.Pos.Top[strings.Join(loc[:n], ".")]; ok {
			return line, nil
		}
	}
	return 0, nil
}

func (c *checker) checkIDs() {
	first := map[string]*sheet.Row{}
	for _, r := range c.s.Rows {
		switch {
		case r.ID == "":
			c.add(Error, "id-missing", r, c.line(r), "row has no id")
			continue
		case !idPattern.MatchString(r.ID):
			c.add(Error, "id-malformed", r, c.line(r, "id"), "id %q must be lowercase snake_case: a letter, then letters, digits or underscores", r.ID)
		}
		if f, dup := first[r.ID]; dup {
			c.add(Error, "id-duplicate", r, c.line(r, "id"), "id %q is already used by the row at line %d", r.ID, c.line(f, "id"))
		} else {
			first[r.ID] = r
		}
	}
}

func (c *checker) checkRow(r *sheet.Row) {
	if r.ID == "" {
		return
	}
	if r.IsInput() {
		c.checkInput(r)
	} else {
		c.checkDerived(r)
	}
	for _, le := range graph.Expressions(r) {
		c.checkExpr(r, le)
	}
	c.checkUsesReferenced(r)
	for _, q := range r.OpenQuestions {
		c.add(Warning, "open-question", r, c.line(r, "open_questions"), "open question: %s", q)
	}
}

func (c *checker) checkInput(r *sheet.Row) {
	for _, k := range r.RuleKeys {
		c.add(Error, "input-has-rule", r, c.line(r, k), "input rows (with source) cannot have a %s rule; remove source or the rule", k)
	}
	if len(r.Uses) > 0 {
		c.add(Error, "input-has-uses", r, c.line(r, "uses"), "input rows (with source) do not use other rows; remove uses")
	}
	if r.Usage == sheet.UsageSituational && r.When == "" {
		c.add(Error, "situational-no-when", r, c.line(r, "usage"), "usage S needs `when`, stating when this input is required")
	}
	if r.Usage != sheet.UsageSituational && r.When != "" {
		c.add(Warning, "when-ignored", r, c.line(r, "when"), "`when` only applies with usage = \"S\"")
	}
	if r.HasDefault && r.Type != "" {
		if _, err := coerceDefault(r.Type, r.Default); err != nil {
			c.add(Error, "default-type", r, c.line(r, "default"), "default %v", err)
		}
	}
}

func (c *checker) checkDerived(r *sheet.Row) {
	for _, k := range []string{"type", "usage", "default"} {
		if _, ok := c.s.Pos.Rows[c.index[r]].Keys[k]; ok {
			c.add(Error, "derived-input-key", r, c.line(r, k), "%s applies to input rows only", k)
		}
	}
	switch len(r.RuleKeys) {
	case 0:
		c.add(Error, "rule-missing", r, c.line(r), "derived row needs exactly one of formula, table, lookup or text (or a source, if it is an input)")
	case 1:
	default:
		c.add(Error, "rule-multiple", r, c.line(r, r.RuleKeys[1]), "derived row has %s; it needs exactly one rule", strings.Join(r.RuleKeys, " and "))
	}
	for _, u := range r.Uses {
		switch {
		case u == r.ID:
			c.add(Error, "uses-self", r, c.line(r, "uses"), "row uses itself")
		case c.s.ByID[u] == nil:
			c.add(Error, "uses-undefined", r, c.line(r, "uses"), "uses %q, which is not a row%s", u, suggest(u, c.s))
		}
	}
	switch r.Rule() {
	case sheet.RuleTable:
		c.checkTable(r)
	case sheet.RuleLookup:
		c.checkLookup(r)
	case sheet.RuleText:
		c.add(Warning, "text-rule", r, c.line(r, "text"), "text rule is not computable; it is an open item: %s", r.Text)
	}
	if r.Lookup == nil && (r.Rule() != "" && (has(c.s, r, "values") || has(c.s, r, "values_file"))) {
		c.add(Error, "values-without-lookup", r, c.line(r, "values", "values_file"), "values and values_file only apply to lookup rules")
	}
}

func has(s *sheet.Sheet, r *sheet.Row, key string) bool {
	for i, x := range s.Rows {
		if x == r {
			_, ok := s.Pos.Rows[i].Keys[key]
			return ok
		}
	}
	return false
}

// checkRef reports an ID used by a rule that is undefined or missing from
// uses.
func (c *checker) checkRef(r *sheet.Row, id, where string, line int) {
	switch {
	case c.s.ByID[id] == nil:
		c.add(Error, "ref-undefined", r, line, "%s refers to %q, which is not a row%s", where, id, suggest(id, c.s))
	case id == r.ID:
		c.add(Error, "uses-self", r, line, "%s refers to the row itself", where)
	case !slices.Contains(r.Uses, id):
		c.add(Error, "ref-not-in-uses", r, line, "%s refers to %q; add it to uses", where, id)
	}
}

func (c *checker) checkExpr(r *sheet.Row, le graph.LabeledExpr) {
	line := c.line(r, le.Key)
	ids, err := c.x.Identifiers(le.Expr)
	if err != nil {
		c.add(Error, "expr-syntax", r, line, "%s: %v", le.Key, err)
		return
	}
	selfOK := le.Key != "formula" && le.Key != "when"
	for _, id := range ids {
		if r.IsInput() && (le.Key == "when" || graph.IsRejectWhen(le.Key)) {
			switch ref := c.s.ByID[id]; {
			case ref == nil:
				c.add(Error, "ref-undefined", r, line, "%s refers to %q, which is not a row%s", le.Key, id, suggest(id, c.s))
			case graph.IsRejectWhen(le.Key) && !ref.IsInput():
				// An input's reject checks the request itself; a check that
				// needs a derived value goes on that derived row, whose
				// reject can name anything in its uses. The input's when may
				// still name any row: it decides whether the input is needed.
				c.add(Error, "input-reject-derived", r, line, "%s names derived row %q; an input's reject may name only the input and other inputs, so move this check to a reject on a derived row", le.Key, id)
			}
			continue
		}
		if selfOK && id == r.ID {
			continue
		}
		c.checkRef(r, id, le.Key, line)
	}
	if fns, _ := c.x.Functions(le.Expr); slices.Contains(fns, "today") && graph.RequestTimeRow(c.s) == nil {
		c.add(Error, "today-no-request-time", r, line, "%s calls today(), which needs a row with source %q", le.Key, graph.RequestTimeSource)
	}
	if _, err := c.x.Compile(le.Expr); err != nil && c.allDefined(ids) {
		c.add(Error, "expr-type", r, line, "%s: %v", le.Key, err)
	}
}

func (c *checker) allDefined(ids []string) bool {
	for _, id := range ids {
		if c.s.ByID[id] == nil {
			return false
		}
	}
	return true
}

// checkUsesReferenced warns about uses entries that no rule, reject or send
// condition refers to. Text rules are skipped, since their references are
// not machine-readable.
func (c *checker) checkUsesReferenced(r *sheet.Row) {
	if r.IsInput() || r.Rule() == "" || r.Rule() == sheet.RuleText {
		return
	}
	refs := map[string]bool{}
	for _, le := range graph.Expressions(r) {
		ids, _ := c.x.Identifiers(le.Expr)
		for _, id := range ids {
			refs[id] = true
		}
	}
	if r.Table != nil {
		for _, col := range r.Table.Columns {
			refs[col] = true
		}
	}
	if r.Lookup != nil {
		for _, k := range r.Lookup.Key {
			refs[k] = true
		}
	}
	for _, u := range r.Uses {
		if !refs[u] && c.s.ByID[u] != nil {
			c.add(Warning, "uses-unreferenced", r, c.line(r, "uses"), "uses %q, but no rule or condition on this row refers to it", u)
		}
	}
}

func (c *checker) checkTable(r *sheet.Row) {
	t := r.Table
	idx := c.index[r]
	cols := c.line(r, "table.columns", "table")
	raw, _ := c.s.Raw["row"].([]any)[idx].(map[string]any)["table"].(map[string]any)["columns"].([]any)
	if n := len(raw); n == 0 || raw[n-1] != "=>" {
		c.add(Error, "table-result-column", r, cols, "the last column must be \"=>\", the result")
	}
	for _, col := range t.Columns {
		c.checkRef(r, col, "table column", cols)
	}
	broken := false
	for j, rule := range t.Rules {
		line := c.s.Pos.TableRuleLine(idx, j)
		if len(rule.Raw) != len(t.Columns)+1 {
			c.add(Error, "table-rule-width", r, line, "rule %d has %d cells; expected %d inputs and a result", j+1, len(rule.Raw), len(t.Columns))
			c.about(j)
			broken = true
			continue
		}
		for k, cell := range rule.Raw[:len(rule.Raw)-1] {
			if _, err := sheet.ParseCell(cell, false, false); err != nil {
				c.add(Error, "table-cell", r, line, "rule %d, column %q: %v", j+1, t.Columns[k], err)
				c.about(j)
				broken = true
			}
		}
	}
	if len(t.Rules) == 0 {
		c.add(Error, "table-empty", r, c.line(r, "table.rows", "table"), "decision table has no rules")
		return
	}
	if broken {
		return
	}
	res := analysis.CheckTable(t, analysis.Domains(c.s))
	if res.Skipped {
		c.add(Warning, "table-too-large", r, cols, "too many input combinations to check for gaps and overlaps")
		return
	}
	if n := len(res.Missing); n > 0 {
		var ex []string
		for _, m := range res.Missing[:min(n, 4)] {
			ex = append(ex, analysis.Describe(t.Columns, m))
		}
		more := ""
		if n > 4 {
			more = fmt.Sprintf(" (and %d more)", n-4)
		}
		c.add(Error, "table-missing-case", r, c.line(r, "table.rows", "table"),
			"no rule covers: %s%s. Add rules, or a catch-all rule of \"-\" cells", strings.Join(ex, "; "), more)
	}
	for _, o := range res.Overlaps {
		c.add(Error, "table-overlap", r, c.s.Pos.TableRuleLine(idx, o.B),
			"rules %d and %d both match %s; with hit_policy \"unique\" exactly one rule may match (use \"first\" if order should decide)",
			o.A+1, o.B+1, analysis.Describe(t.Columns, o.Example))
		c.about(o.A, o.B)
	}
}

func (c *checker) checkLookup(r *sheet.Row) {
	l := r.Lookup
	line := c.line(r, "lookup")
	for _, k := range l.Key {
		c.checkRef(r, k, "lookup.key", line)
	}
	if l.Inline != nil && len(l.Key) != 1 {
		c.add(Error, "lookup-inline-keys", r, c.line(r, "values"), "inline values need exactly one key; use values_file for %d keys", len(l.Key))
	}
	if l.Inline != nil && l.ValuesFile != "" {
		c.add(Error, "lookup-values-both", r, c.line(r, "values_file"), "give values or values_file, not both")
	}
	if !l.Available() && l.ValuesFile == "" {
		c.add(Warning, "lookup-no-values", r, line, "no design-time values for %s; rows that depend on it cannot be computed in tests", l.From)
	}
	if l.Data != nil && len(l.Data.Entries) > 0 {
		res := analysis.CheckLookupData(l.Data)
		file := l.Data.File
		for _, o := range res.Overlaps {
			c.rep.Diagnostics = append(c.rep.Diagnostics, Diagnostic{Severity: Error, Code: "lookup-overlap", File: file,
				Line: l.Data.Entries[o.B].Line, Row: r.ID,
				Message: fmt.Sprintf("entries at lines %d and %d both match %s", l.Data.Entries[o.A].Line, l.Data.Entries[o.B].Line, analysis.Describe(l.Key, o.Example))})
		}
		if n := len(res.Missing); n > 0 {
			var ex []string
			for _, m := range res.Missing[:min(n, 3)] {
				ex = append(ex, analysis.Describe(l.Key, m))
			}
			more := ""
			if n > 3 {
				more = fmt.Sprintf(" (and %d more)", n-3)
			}
			c.rep.Diagnostics = append(c.rep.Diagnostics, Diagnostic{Severity: Warning, Code: "lookup-gap", File: file, Row: r.ID,
				Message: fmt.Sprintf("no entry for %s%s", strings.Join(ex, "; "), more)})
		}
	}
}

func (c *checker) checkCycle() {
	if c.g.Cycle == nil {
		return
	}
	r := c.s.ByID[c.g.Cycle[0]]
	c.add(Error, "cycle", r, c.line(r, "uses"), "circular dependency: %s", strings.Join(c.g.Cycle, " -> "))
}

func (c *checker) checkSends() {
	fields := map[string]*sheet.Row{} // target + field -> row
	domains := analysis.Domains(c.s)
	for _, r := range c.s.Rows {
		if r.ID == "" {
			continue
		}
		if r.ExtraSends > 0 {
			c.add(Error, "send-twice", r, c.line(r, "send"), "row is sent to the sheet's own target more than once; give each extra send a target (a call stage)")
		}
		sends := r.CallSends
		if r.Send != nil {
			sends = append([]*sheet.Send{r.Send}, sends...)
		}
		for _, sd := range sends {
			c.checkSend(r, sd, fields, domains)
		}
	}
}

func (c *checker) checkSend(r *sheet.Row, sd *sheet.Send, fields map[string]*sheet.Row, domains map[string][]any) {
	{
		where := sd.Field
		if sd.Target != "" {
			where = sd.Target + ": " + sd.Field
		}
		key := sd.Target + "\x00" + sd.Field
		if prev, dup := fields[key]; dup && sd.Field != "" {
			c.add(Error, "send-duplicate", r, c.line(r, "send.field"), "field %q is already sent by row %q", where, prev.ID)
		}
		fields[key] = r
		if sd.Usage == sheet.UsageSituational {
			if sd.When == "" {
				c.add(Error, "situational-no-when", r, c.line(r, "send.usage", "send"), "usage S needs `when`, stating when the field is required")
			}
			if sd.Otherwise == "" {
				c.add(Error, "situational-no-otherwise", r, c.line(r, "send.usage", "send"), "usage S needs `otherwise`: \"do-not-send\" or \"sender-discretion\"")
			}
		} else if sd.When != "" || sd.Otherwise != "" {
			c.add(Warning, "when-ignored", r, c.line(r, "send.when", "send.otherwise"), "`when` and `otherwise` only apply with usage = \"S\"")
		}
		if sd.Map != nil {
			for _, v := range domains[r.ID] {
				if _, ok := sd.Map[sheet.FormatValue(v)]; !ok {
					c.add(Error, "send-map-missing", r, c.line(r, "send.map"), "send.map has no entry for %q, which this row can produce", sheet.FormatValue(v))
				}
			}
		}
	}
}

func (c *checker) checkUnused() {
	for _, r := range c.s.Rows {
		if r.ID == "" || r.Send != nil || len(r.CallSends) > 0 || len(r.Rejects) > 0 || len(c.g.Users[r.ID]) > 0 {
			continue
		}
		if c.s.ByID[r.ID] != r {
			continue
		}
		c.add(Error, "unused", r, c.line(r, "id"), "nothing uses this row and it is not sent; use it, send it, or remove it")
	}
}

func (c *checker) checkTarget() {
	if c.s.TargetSchema == "" {
		return
	}
	line := c.s.Pos.Top["target_schema"]
	spec := c.s.TargetSchema
	if !filepath.IsAbs(spec) {
		spec = filepath.Join(c.s.Dir, spec)
	}
	ts, err := LoadTarget(spec)
	if err != nil {
		c.add(Error, "target-schema", nil, line, "cannot read target_schema: %v", err)
		return
	}
	var sent []string
	for _, r := range c.s.Rows {
		if r.Send == nil || r.Send.Field == "" {
			continue
		}
		sent = append(sent, r.Send.Field)
		if !ts.HasField(r.Send.Field) {
			c.add(Error, "send-field-unknown", r, c.line(r, "send.field"), "field %q is not in target_schema", r.Send.Field)
		}
	}
	for _, f := range ts.RequiredFields(sent) {
		c.add(Error, "target-required-unsent", nil, line, "target_schema requires %q, but no row sends it", f)
	}
}

func coerceDefault(typ string, v any) (any, error) {
	switch typ {
	case "number":
		if _, ok := v.(float64); !ok {
			return nil, fmt.Errorf("must be a number")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return nil, fmt.Errorf("must be true or false")
		}
	case "string":
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("must be a string")
		}
	}
	return v, nil
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// suggest returns " (did you mean ...?)" for a near-miss ID.
func suggest(id string, s *sheet.Sheet) string {
	best, bestD := "", 3
	for _, r := range s.Rows {
		if d := distance(id, r.ID); d < bestD {
			best, bestD = r.ID, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// SchemaProblem is one JSON Schema violation, located by its path in the
// decoded document.
type SchemaProblem struct {
	Location []string
	Message  string
}

// SchemaProblems validates a decoded TOML document against an embedded JSON
// Schema, returning the most specific problems.
func SchemaProblems(schemaJSON []byte, raw map[string]any) ([]SchemaProblem, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	comp := jsonschema.NewCompiler()
	const url = "embedded.schema.json"
	if err := comp.AddResource(url, doc); err != nil {
		return nil, err
	}
	sch, err := comp.Compile(url)
	if err != nil {
		return nil, err
	}
	// Round-trip through JSON so TOML dates and integers become JSON types.
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	verr, ok := sch.Validate(inst).(*jsonschema.ValidationError)
	if !ok || verr == nil {
		return nil, nil
	}
	var out []SchemaProblem
	printer := message.NewPrinter(language.English)
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, SchemaProblem{Location: e.InstanceLocation, Message: e.ErrorKind.LocalizedString(printer)})
		}
		for _, ce := range e.Causes {
			walk(ce)
		}
	}
	walk(verr)
	// With anyOf, every alternative reports its own failure. Keep the most
	// specific: drop a problem when another one sits deeper inside it.
	var kept []SchemaProblem
	for i, p := range out {
		deeper := false
		for j, q := range out {
			if i != j && len(q.Location) > len(p.Location) && slices.Equal(q.Location[:len(p.Location)], p.Location) {
				deeper = true
				break
			}
		}
		if !deeper {
			kept = append(kept, p)
		}
	}
	return kept, nil
}
