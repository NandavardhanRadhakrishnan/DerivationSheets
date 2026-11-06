// Package editor turns drafts sent by the browser into new file content,
// checks them against the files as they were, and saves them through the
// store.
package editor

import (
	"encoding/json"
	"fmt"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// Pair is one entry of an inline table, kept in written order. In JSON it
// is ["key", value].
type Pair struct {
	Key   string
	Value any
}

func (p Pair) MarshalJSON() ([]byte, error) { return json.Marshal([]any{p.Key, p.Value}) }

func (p *Pair) UnmarshalJSON(b []byte) error {
	var kv []json.RawMessage
	if err := json.Unmarshal(b, &kv); err != nil {
		return err
	}
	if len(kv) != 2 {
		return fmt.Errorf("a pair has 2 elements, not %d", len(kv))
	}
	if err := json.Unmarshal(kv[0], &p.Key); err != nil {
		return fmt.Errorf("pair key: %v", err)
	}
	return json.Unmarshal(kv[1], &p.Value)
}

// TableData is a decision table. Columns are written as in the file,
// "=>" included.
type TableData struct {
	HitPolicy string   `json:"hit_policy,omitempty"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
}

// LookupData is a lookup with its inline values or values file.
type LookupData struct {
	From       string   `json:"from"`
	Key        []string `json:"key"`
	Values     []Pair   `json:"values,omitempty"`
	ValuesFile string   `json:"values_file,omitempty"`
}

type RejectData struct {
	When    string `json:"when"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SendData struct {
	Target    string `json:"target,omitempty"`
	Field     string `json:"field"`
	Usage     string `json:"usage"`
	When      string `json:"when,omitempty"`
	Otherwise string `json:"otherwise,omitempty"`
	Map       []Pair `json:"map,omitempty"`
}

// RowData is one [[row]] as the editor sees it.
type RowData struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	Type        string `json:"type,omitempty"`
	Usage       string `json:"usage,omitempty"`
	When        string `json:"when,omitempty"`
	HasDefault  bool   `json:"has_default,omitempty"`
	Default     any    `json:"default,omitempty"`

	Uses          []string     `json:"uses,omitempty"`
	Formula       string       `json:"formula,omitempty"`
	Table         *TableData   `json:"table,omitempty"`
	Lookup        *LookupData  `json:"lookup,omitempty"`
	Text          string       `json:"text,omitempty"`
	Rejects       []RejectData `json:"rejects,omitempty"`
	Sends         []SendData   `json:"sends,omitempty"`
	OpenQuestions []string     `json:"open_questions,omitempty"`
}

// RowFromTable reads a [[row]] table.
func RowFromTable(t *tomledit.Table) RowData {
	r := RowData{
		ID: str(t, "id"), Name: str(t, "name"), Description: str(t, "description"),
		Source: str(t, "source"), Type: str(t, "type"), Usage: str(t, "usage"), When: str(t, "when"),
		Uses: strs(t.Get("uses")), Formula: str(t, "formula"), Text: str(t, "text"),
		OpenQuestions: strs(t.Get("open_questions")),
	}
	if k := t.Get("default"); k != nil {
		r.HasDefault, r.Default = true, plain(k.Value)
	}
	if subs := t.Sub("table"); len(subs) > 0 {
		tb := subs[0]
		td := &TableData{HitPolicy: str(tb, "hit_policy"), Columns: strs(tb.Get("columns")), Rows: [][]any{}}
		if k := tb.Get("rows"); k != nil {
			rows, _ := k.Value.([]any)
			for _, row := range rows {
				cells, _ := plain(row).([]any)
				td.Rows = append(td.Rows, cells)
			}
		}
		r.Table = td
	}
	if k := t.Get("lookup"); k != nil {
		in, _ := k.Value.(tomledit.Inline)
		l := &LookupData{ValuesFile: str(t, "values_file")}
		for _, e := range in {
			switch e.Name {
			case "from":
				l.From, _ = e.Value.(string)
			case "key":
				l.Key = strs(e)
			}
		}
		if v := t.Get("values"); v != nil {
			l.Values = pairs(v.Value)
		}
		r.Lookup = l
	}
	for _, s := range t.Sub("reject") {
		r.Rejects = append(r.Rejects, RejectData{When: str(s, "when"), Code: str(s, "code"), Message: str(s, "message")})
	}
	for _, s := range t.Sub("send") {
		sd := SendData{Target: str(s, "target"), Field: str(s, "field"), Usage: str(s, "usage"),
			When: str(s, "when"), Otherwise: str(s, "otherwise")}
		if m := s.Get("map"); m != nil {
			sd.Map = pairs(m.Value)
		}
		r.Sends = append(r.Sends, sd)
	}
	return r
}

// ApplyRow changes t so it says r. Keys the editor does not know are left
// alone, and so is every value r does not change, so an unchanged row is
// written back byte for byte. Several sends, or a call send, are written
// [[row.send]]; a new single send without a target is written [row.send],
// and an existing one keeps its form.
func ApplyRow(t *tomledit.Table, r RowData) {
	for _, f := range []struct{ key, v string }{
		{"id", r.ID}, {"name", r.Name}, {"description", r.Description}, {"source", r.Source},
		{"type", r.Type}, {"usage", r.Usage}, {"when", r.When}, {"formula", r.Formula}, {"text", r.Text},
	} {
		setStr(t, f.key, f.v)
	}
	if r.HasDefault {
		set(t, "default", value(r.Default))
	} else {
		t.Delete("default")
	}
	setList(t, "uses", r.Uses)
	setList(t, "open_questions", r.OpenQuestions)

	if r.Table == nil {
		removeSubs(t, "table", 0)
	} else {
		tb := subAt(t, "table", 0, false)
		removeSubs(t, "table", 1)
		setStr(tb, "hit_policy", r.Table.HitPolicy)
		set(tb, "columns", list(r.Table.Columns))
		rows := make([]any, len(r.Table.Rows))
		for i, row := range r.Table.Rows {
			rows[i] = value(row)
		}
		set(tb, "rows", rows)
	}

	if r.Lookup == nil {
		for _, k := range []string{"lookup", "values", "values_file"} {
			t.Delete(k)
		}
	} else {
		set(t, "lookup", lookupInline(t.Get("lookup"), r.Lookup))
		if r.Lookup.Values != nil {
			set(t, "values", inline(r.Lookup.Values))
		} else {
			t.Delete("values")
		}
		setStr(t, "values_file", r.Lookup.ValuesFile)
	}

	for i, rj := range r.Rejects {
		s := subAt(t, "reject", i, true)
		setStr(s, "when", rj.When)
		setStr(s, "code", rj.Code)
		setStr(s, "message", rj.Message)
	}
	removeSubs(t, "reject", len(r.Rejects))

	// Several sends, or a call send, need [[row.send]]. A single send keeps
	// the form the file gives it.
	array := len(r.Sends) > 1 || len(r.Sends) == 1 && r.Sends[0].Target != ""
	for i, sd := range r.Sends {
		s := subAt(t, "send", i, array)
		if array {
			s.Array = true
		}
		for _, f := range []struct{ key, v string }{
			{"target", sd.Target}, {"field", sd.Field}, {"usage", sd.Usage}, {"when", sd.When}, {"otherwise", sd.Otherwise},
		} {
			setStr(s, f.key, f.v)
		}
		if sd.Map != nil {
			set(s, "map", inline(sd.Map))
		} else {
			s.Delete("map")
		}
	}
	removeSubs(t, "send", len(r.Sends))
}

// lookupInline is the lookup's inline table, in the order the file writes
// its keys.
func lookupInline(old *tomledit.Key, l *LookupData) tomledit.Inline {
	want := map[string]any{"from": l.From, "key": list(l.Key)}
	var out tomledit.Inline
	if old != nil {
		in, _ := old.Value.(tomledit.Inline)
		for _, k := range in {
			if v, ok := want[k.Name]; ok {
				out = append(out, &tomledit.Key{Name: k.Name, Value: v})
				delete(want, k.Name)
			}
		}
	}
	for _, name := range []string{"from", "key"} {
		if v, ok := want[name]; ok {
			out = append(out, &tomledit.Key{Name: name, Value: v})
		}
	}
	return out
}

func str(t *tomledit.Table, key string) string {
	if k := t.Get(key); k != nil {
		s, _ := k.Value.(string)
		return s
	}
	return ""
}

func strs(k *tomledit.Key) []string {
	if k == nil {
		return nil
	}
	xs, _ := k.Value.([]any)
	out := []string{}
	for _, x := range xs {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// plain turns a file value into what JSON carries: dates become strings,
// inline tables become pairs.
func plain(v any) any {
	switch x := v.(type) {
	case tomledit.Raw:
		return string(x)
	case int64:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	case tomledit.Inline:
		return pairs(x)
	}
	return v
}

func pairs(v any) []Pair {
	in, _ := v.(tomledit.Inline)
	out := []Pair{}
	for _, k := range in {
		out = append(out, Pair{k.Name, plain(k.Value)})
	}
	return out
}

// value turns a value from JSON into a file value.
func value(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = value(e)
		}
		return out
	case []Pair:
		return inline(x)
	}
	return v
}

func inline(ps []Pair) tomledit.Inline {
	out := tomledit.Inline{}
	for _, p := range ps {
		out = append(out, &tomledit.Key{Name: p.Key, Value: value(p.Value)})
	}
	return out
}

func list(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// set changes a key, keeping what the file says where v says the same:
// a date written bare stays bare, and an inline table keeps its key order.
func set(t *tomledit.Table, key string, v any) {
	if k := t.Get(key); k != nil {
		v = conform(k.Value, v)
	}
	t.Set(key, v)
}

// conform returns v in old's form where they say the same thing.
func conform(old, v any) any {
	switch o := old.(type) {
	case tomledit.Raw:
		if s, ok := v.(string); ok && s == string(o) {
			return o
		}
	case []any:
		if n, ok := v.([]any); ok && len(n) == len(o) {
			out := make([]any, len(n))
			for i := range n {
				out[i] = conform(o[i], n[i])
			}
			return out
		}
	case tomledit.Inline:
		n, ok := v.(tomledit.Inline)
		if !ok || len(n) != len(o) {
			return v
		}
		out := make(tomledit.Inline, len(n))
		for i, k := range n {
			if k.Name != o[i].Name {
				return v
			}
			out[i] = &tomledit.Key{Name: k.Name, Value: conform(o[i].Value, k.Value)}
		}
		return out
	}
	return v
}

// setStr sets a string key, or deletes it when v is empty. A key the file
// writes as "" stays.
func setStr(t *tomledit.Table, key, v string) {
	if v != "" {
		set(t, key, v)
		return
	}
	if k := t.Get(key); k != nil && k.Value != "" {
		t.Delete(key)
	}
}

// setList sets a list of strings, or deletes it when empty. A key the file
// writes as [] stays.
func setList(t *tomledit.Table, key string, xs []string) {
	if len(xs) > 0 {
		set(t, key, list(xs))
		return
	}
	if k := t.Get(key); k != nil {
		if a, ok := k.Value.([]any); !ok || len(a) > 0 {
			t.Delete(key)
		}
	}
}

// subAt returns the i-th sub-table with that name, adding it when there are
// not that many.
func subAt(t *tomledit.Table, name string, i int, array bool) *tomledit.Table {
	if subs := t.Sub(name); i < len(subs) {
		return subs[i]
	}
	s := &tomledit.Table{Name: name, Array: array}
	t.Subs = append(t.Subs, s)
	return s
}

// removeSubs keeps the first n sub-tables with that name.
func removeSubs(t *tomledit.Table, name string, n int) {
	out := t.Subs[:0]
	for _, s := range t.Subs {
		if s.Name == name {
			if n == 0 {
				continue
			}
			n--
		}
		out = append(out, s)
	}
	t.Subs = out
}
