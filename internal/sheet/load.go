package sheet

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// FileName is the authored specification inside a workflow folder.
const FileName = "sheet.toml"

// ConfigFile holds design-time config values, relative to the workflow folder.
const ConfigFile = "data/config.toml"

// ParseError is a TOML syntax error with its position.
type ParseError struct {
	File    string
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message)
}

// Load reads a workflow folder, or a sheet.toml path.
func Load(path string) (*Sheet, error) {
	dir, file := path, filepath.Join(path, FileName)
	if strings.HasSuffix(strings.ToLower(path), ".toml") {
		dir, file = filepath.Dir(path), path
	}
	text, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return Parse(dir, file, string(text))
}

// Parse builds a sheet from sheet.toml text. dir is the workflow folder used
// to resolve data files.
func Parse(dir, file, text string) (*Sheet, error) {
	var raw map[string]any
	if err := toml.Unmarshal([]byte(text), &raw); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, _ := de.Position()
			return nil, &ParseError{File: file, Line: line, Message: de.Error()}
		}
		return nil, &ParseError{File: file, Message: err.Error()}
	}
	s := &Sheet{
		Dir:  dir,
		File: file,
		Raw:  raw,
		Pos:  ScanPositions(file, text),
		ByID: map[string]*Row{},
	}
	lines := strings.Split(text, "\n")
	s.Slug, _ = raw["sheet"].(string)
	if v, ok := raw["version"].(int64); ok {
		s.Version = int(v)
	}
	s.Title, _ = raw["title"].(string)
	s.RequestSchema, _ = raw["request_schema"].(string)
	s.TargetSchema, _ = raw["target_schema"].(string)
	s.loadConfig()
	s.loadDisplay()

	rows, _ := raw["row"].([]any)
	for i, rr := range rows {
		m, ok := rr.(map[string]any)
		if !ok {
			continue
		}
		r := parseRow(i, m)
		r.Index = i
		s.Rows = append(s.Rows, r)
		if _, dup := s.ByID[r.ID]; !dup && r.ID != "" {
			s.ByID[r.ID] = r
		}
		// Inline tables lose key order when decoded; recover it from the text.
		if r.Send != nil && r.Send.Map != nil {
			r.Send.MapOrder = inlineKeyOrder(lines, s.Pos.RowLine(i, "send.map"), r.Send.Map)
		}
		if r.Lookup != nil && r.Lookup.Inline != nil {
			r.Lookup.InlineOrder = inlineKeyOrder(lines, s.Pos.RowLine(i, "values"), r.Lookup.Inline)
		}
		if r.Lookup != nil {
			s.loadLookupData(r)
		}
	}
	return s, nil
}

func parseRow(i int, m map[string]any) *Row {
	r := &Row{}
	r.ID, _ = m["id"].(string)
	r.Name, _ = m["name"].(string)
	r.Description, _ = m["description"].(string)
	r.Source, _ = m["source"].(string)
	r.Type, _ = m["type"].(string)
	r.Usage, _ = m["usage"].(string)
	r.When, _ = m["when"].(string)
	if d, ok := m["default"]; ok {
		r.Default, r.HasDefault = Normalize(d), true
	}
	r.Uses = stringList(m["uses"])
	r.OpenQuestions = stringList(m["open_questions"])
	for _, k := range []string{RuleFormula, RuleTable, RuleLookup, RuleText} {
		if _, ok := m[k]; ok {
			r.RuleKeys = append(r.RuleKeys, k)
		}
	}
	r.Formula, _ = m["formula"].(string)
	r.Text, _ = m["text"].(string)
	if t, ok := m["table"].(map[string]any); ok {
		r.Table = parseTable(t)
	}
	if l, ok := m["lookup"].(map[string]any); ok {
		r.Lookup = &Lookup{Key: stringList(l["key"])}
		r.Lookup.From, _ = l["from"].(string)
		r.Lookup.ValuesFile, _ = m["values_file"].(string)
		if v, ok := m["values"].(map[string]any); ok {
			r.Lookup.Inline = Normalize(v).(map[string]any)
		}
	}
	if rs, ok := m["reject"].([]any); ok {
		for _, x := range rs {
			if rm, ok := x.(map[string]any); ok {
				var rj Reject
				rj.When, _ = rm["when"].(string)
				rj.Code, _ = rm["code"].(string)
				rj.Message, _ = rm["message"].(string)
				r.Rejects = append(r.Rejects, rj)
			}
		}
	}
	// send is one table, or an array of tables when a row is sent to more
	// than one target: the sheet's own target and calls made on the way.
	var sends []map[string]any
	switch sv := m["send"].(type) {
	case map[string]any:
		sends = append(sends, sv)
	case []any:
		for _, x := range sv {
			if sm, ok := x.(map[string]any); ok {
				sends = append(sends, sm)
			}
		}
	}
	for _, sm := range sends {
		sd := &Send{}
		sd.Field, _ = sm["field"].(string)
		sd.Usage, _ = sm["usage"].(string)
		sd.When, _ = sm["when"].(string)
		sd.Otherwise, _ = sm["otherwise"].(string)
		sd.Target, _ = sm["target"].(string)
		if mp, ok := sm["map"].(map[string]any); ok {
			sd.Map = Normalize(mp).(map[string]any)
		}
		switch {
		case sd.Target != "":
			r.CallSends = append(r.CallSends, sd)
		case r.Send == nil:
			r.Send = sd
		default:
			r.ExtraSends++
		}
	}
	return r
}

func parseTable(t map[string]any) *Table {
	tb := &Table{HitPolicy: "unique"}
	if hp, ok := t["hit_policy"].(string); ok {
		tb.HitPolicy = hp
	}
	cols := stringList(t["columns"])
	if n := len(cols); n > 0 && cols[n-1] == "=>" {
		tb.ResultColumn = true
		cols = cols[:n-1]
	}
	tb.Columns = cols
	rows, _ := t["rows"].([]any)
	for _, rr := range rows {
		cells, _ := rr.([]any)
		rule := TableRule{Raw: cells}
		for j, c := range cells {
			if j == len(cells)-1 {
				rule.Result = Normalize(c)
				break
			}
			cell, err := ParseCell(c, false, false)
			if err != nil {
				// Kept as a literal; the validator re-parses and reports it.
				cell = Cell{Kind: CellLiteral, Value: c, Raw: fmt.Sprint(c)}
			}
			rule.Cells = append(rule.Cells, cell)
		}
		tb.Rules = append(tb.Rules, rule)
	}
	return tb
}

func stringList(v any) []string {
	xs, _ := v.([]any)
	var out []string
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

var inlineKey = regexp.MustCompile(`(?:^|[{,])\s*("(?:[^"\\]|\\.)*"|'[^']*'|[A-Za-z0-9_\-]+)\s*=`)

// inlineKeyOrder returns the keys of an inline table in written order, read
// from the source line. Keys it cannot find are appended in sorted order.
func inlineKeyOrder(lines []string, line int, m map[string]any) []string {
	var order []string
	seen := map[string]bool{}
	if line > 0 && line <= len(lines) {
		text := lines[line-1]
		if open := strings.Index(text, "{"); open >= 0 {
			for _, mm := range inlineKey.FindAllStringSubmatch(text[open:], -1) {
				k := strings.Trim(mm[1], `"'`)
				if _, ok := m[k]; ok && !seen[k] {
					seen[k] = true
					order = append(order, k)
				}
			}
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

func (s *Sheet) loadConfig() {
	path := filepath.Join(s.Dir, filepath.FromSlash(ConfigFile))
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		line := 0
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, _ = de.Position()
		}
		s.LoadIssues = append(s.LoadIssues, Issue{File: path, Line: line, Message: err.Error()})
		return
	}
	s.Config = map[string]any{}
	flatten("", Normalize(raw).(map[string]any), s.Config)
}

// flatten stores every key path, so both "pricing.loading" (a table) and
// "pricing.loading.High" resolve.
func flatten(prefix string, m map[string]any, out map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		out[key] = v
		if sub, ok := v.(map[string]any); ok {
			flatten(key, sub, out)
		}
	}
}

func (s *Sheet) loadLookupData(r *Row) {
	l := r.Lookup
	if l.ValuesFile != "" {
		path := filepath.Join(s.Dir, filepath.FromSlash(l.ValuesFile))
		data, issues := ReadLookupCSV(path, l.Key)
		for i := range issues {
			issues[i].Row = r.ID
		}
		s.LoadIssues = append(s.LoadIssues, issues...)
		l.Data = data
		return
	}
	if l.Inline == nil && l.SourceKind() == "config" && s.Config != nil {
		if m, ok := s.Config[l.SourcePath()].(map[string]any); ok {
			l.Inline = m
			for k := range m {
				l.InlineOrder = append(l.InlineOrder, k)
			}
			sort.Strings(l.InlineOrder)
		}
	}
}

// ReadLookupCSV reads lookup data. The header must name the key columns, in
// the order of lookup.key, followed by one result column (conventionally
// "value"). Key cells use the decision-table grammar plus ranges.
func ReadLookupCSV(path string, keys []string) (*LookupData, []Issue) {
	f, err := os.Open(path)
	if err != nil {
		return nil, []Issue{{File: path, Message: err.Error()}}
	}
	defer f.Close()
	rd := csv.NewReader(f)
	rd.TrimLeadingSpace = true
	rd.Comment = '#'
	header, err := rd.Read()
	if err != nil {
		return nil, []Issue{{File: path, Line: 1, Message: "cannot read header: " + err.Error()}}
	}
	for i := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(header[i], "\uFEFF"))
	}
	var issues []Issue
	if len(header) != len(keys)+1 {
		issues = append(issues, Issue{File: path, Line: 1, Message: fmt.Sprintf(
			"header has %d columns; expected the %d key columns %v followed by one result column", len(header), len(keys), keys)})
		return &LookupData{File: path, Columns: keys}, issues
	}
	for i, k := range keys {
		if header[i] != k {
			issues = append(issues, Issue{File: path, Line: 1, Message: fmt.Sprintf(
				"column %d is %q; expected key %q (columns must follow lookup.key)", i+1, header[i], k)})
		}
	}
	d := &LookupData{File: path, Columns: keys}
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		line, _ := rd.FieldPos(0)
		if err != nil {
			issues = append(issues, Issue{File: path, Line: line, Message: err.Error()})
			break
		}
		e := LookupEntry{Line: line}
		for i, raw := range rec[:len(rec)-1] {
			c, err := ParseCell(strings.TrimSpace(raw), true, true)
			if err != nil {
				issues = append(issues, Issue{File: path, Line: line, Message: fmt.Sprintf("column %q: %v", header[i], err)})
			}
			e.Cells = append(e.Cells, c)
		}
		e.Value = ParseText(strings.TrimSpace(rec[len(rec)-1]))
		d.Entries = append(d.Entries, e)
	}
	return d, issues
}
