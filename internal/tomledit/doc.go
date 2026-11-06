// Package tomledit edits sheet, flow and interface files without disturbing
// what an edit does not touch. go-toml drops comments, so it cannot write
// these files; this package reads positions with go-toml's parser and works
// on spans of lines instead.
//
// A file is split into spans: each top-level key, and each top-level table
// or [[array]] element together with its sub-tables. A comment block
// directly above a span belongs to it; everything else between spans
// (section headings, blank lines) is free text that stays where it is.
// Parse a file, change Top and Tables, and Bytes writes it back: unchanged
// spans byte for byte, changed spans in canonical form.
package tomledit

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Doc is a parsed TOML file.
type Doc struct {
	Top    []*Key
	Tables []*Table

	layout *Layout
	lines  []string            // original lines, each with its own terminator
	nl     string              // line ending for new and changed lines
	segs   []segment           // the original file, in order
	top    map[string]*keyOrig // original top-level keys by name
}

// Key is a key and its value: string, bool, int64, float64, []any, Inline
// or Raw. Dotted keys are joined with "." and keep their spelling while the
// name is unchanged. A new name with dots is written as a dotted key in a
// table, and as one quoted key in an inline table.
type Key struct {
	Name  string
	Value any

	// text is the key as written, quotes included, for the name textFor.
	text, textFor string
}

// written is the key as the file writes it: as parsed while the name is
// unchanged, otherwise each "."-separated part quoted as needed.
func (k *Key) written() string {
	if k.text != "" && k.textFor == k.Name {
		return k.text
	}
	return formatDotted(k.Name)
}

// writtenInline is a key of an inline table as the file writes it: as
// parsed while the name is unchanged, otherwise one key, quoted as needed.
// Inline keys made in code are single keys, such as "0.05" in a send map.
func (k *Key) writtenInline() string {
	if k.text != "" && k.textFor == k.Name {
		return k.text
	}
	return formatKey(k.Name)
}

// Inline is an inline table, in written order.
type Inline []*Key

// Raw is value text written as is: dates and times.
type Raw string

// Table is a [name] table or one [[name]] element with its sub-tables. A
// sub-table's Name is relative to its parent: "send" in [row.send].
type Table struct {
	Name  string
	Array bool
	Keys  []*Key
	Subs  []*Table

	// orig is the span this table was parsed from, or nil for a new table.
	orig *tableOrig
}

// segment is a run of original lines: free text, or the span of a
// top-level key or table.
type segment struct {
	start, end int
	free       bool
	key        string     // top-level key span: the key's name
	table      *tableOrig // table span
}

// keyOrig is a key as it was in the file.
type keyOrig struct {
	name       string
	value      any      // a deep copy of the parsed value
	text       string   // the value as written
	trailing   string   // what follows the value on its last line
	above      []string // comment lines above the key, trimmed on the left
	start, end int      // the key's own lines
	elems      []elemOrig
}

// elemOrig is an element of a multi-line array and its comments.
type elemOrig struct {
	value    any
	above    []string // comment lines above it, trimmed on the left
	trailing string   // a comment after it on its line, with the space before
}

// extent returns the bytes a value node covers, from the scalars inside
// it: array and inline table nodes have no range of their own.
func extent(n *unstable.Node) (from, to int, ok bool) {
	if n.Kind != unstable.Array && n.Kind != unstable.InlineTable && n.Raw.Length > 0 {
		return int(n.Raw.Offset), int(n.Raw.Offset + n.Raw.Length), true
	}
	for it := n.Children(); it.Next(); {
		f, t, k := extent(it.Node())
		if !k {
			continue
		}
		if !ok || f < from {
			from = f
		}
		if !ok || t > to {
			to = t
		}
		ok = true
	}
	return from, to, ok
}

// tableOrig is a table as it was in the file.
type tableOrig struct {
	start, end int // lines of a top-level table's span, leading comments included
	name       string
	array      bool
	header     string   // what follows the header's closing bracket
	text       string   // the table's own key parts as written, joined with "."
	above      []string // comment lines above the header, trimmed on the left
	keys       []*keyOrig
	subs       []*tableOrig
}

var heading = regexp.MustCompile(`^\s*#\s*-{3,}`)

func isComment(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "#") }

// splitLines splits text after each "\n", keeping the terminators.
func splitLines(text string) []string {
	var lines []string
	for text != "" {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:i+1])
		text = text[i+1:]
	}
	return lines
}

func chomp(line string) string { return strings.TrimRight(line, "\r\n") }

// comments returns the comment lines in lines[from:to], trimmed on the left.
func comments(lines []string, from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		if isComment(lines[i]) {
			out = append(out, strings.TrimLeft(chomp(lines[i]), " \t"))
		}
	}
	return out
}

// Parse splits TOML text into spans. A nil layout writes changed spans with
// no canonical key order.
func Parse(data []byte, l *Layout) (*Doc, error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, _ := de.Position()
			return nil, fmt.Errorf("line %d: %v", line, err)
		}
		return nil, err
	}
	d := &Doc{layout: l, lines: splitLines(string(data)), nl: "\n", top: map[string]*keyOrig{}}
	if len(d.lines) > 0 && strings.HasSuffix(d.lines[0], "\r\n") {
		d.nl = "\r\n"
	}
	starts := make([]int, len(d.lines))
	off := 0
	for i, line := range d.lines {
		starts[i] = off
		off += len(line)
	}
	lineOf := func(o int) int { return sort.Search(len(starts), func(i int) bool { return starts[i] > o }) - 1 }
	// rest is what follows offset o on its line, without the terminator.
	rest := func(o int) string {
		n := lineOf(o)
		return chomp(d.lines[n])[o-starts[n]:]
	}

	type item struct {
		first, last int
		key         string
		table       *tableOrig
	}
	var items []*item
	var topTable, cur *Table
	var topParts []string
	prev := -1 // last line of the previous expression

	p := &unstable.Parser{}
	p.Reset(data)
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.KeyValue:
			v, err := decode(e.Value(), data)
			if err != nil {
				return nil, err
			}
			start, end := int(e.Raw.Offset), int(e.Raw.Offset+e.Raw.Length)
			it := e.Key()
			valStart := 0
			for it.Next() {
				valStart = int(it.Node().Raw.Offset + it.Node().Raw.Length)
			}
			for valStart < end && data[valStart] != '=' {
				valStart++
			}
			valStart++
			for valStart < end && (data[valStart] == ' ' || data[valStart] == '\t') {
				valStart++
			}
			name := keyName(e)
			k := &Key{Name: name, Value: v, text: keyText(e, data), textFor: name}
			ko := &keyOrig{
				name: k.Name, value: copyValue(v), text: string(data[valStart:end]), trailing: rest(end),
				start: lineOf(start), end: lineOf(end-1) + 1,
			}
			ko.above = comments(d.lines, prev+1, ko.start)
			prev = ko.end - 1
			if vn := e.Value(); vn.Kind == unstable.Array && ko.end-ko.start > 1 {
				// Comments on the elements of a multi-line array, such as
				// the rules of a decision table.
				var elems []*unstable.Node
				for it := vn.Children(); it.Next(); {
					elems = append(elems, it.Node())
				}
				above := ko.start
				for i, n := range elems {
					el := elemOrig{value: copyValue(v.([]any)[i])}
					if from, to, ok := extent(n); ok {
						first, last := lineOf(from), lineOf(to-1)
						el.above = comments(d.lines, above+1, first)
						shared := false
						if i+1 < len(elems) {
							if next, _, ok := extent(elems[i+1]); ok && lineOf(next) == last {
								shared = true
							}
						}
						if r := rest(to); !shared && last != ko.start && last != ko.end-1 {
							if j := strings.IndexByte(r, '#'); j >= 0 {
								for j > 0 && (r[j-1] == ' ' || r[j-1] == '\t') {
									j--
								}
								el.trailing = r[j:]
							}
						}
						above = last
					}
					ko.elems = append(ko.elems, el)
				}
			}
			if cur == nil {
				d.Top = append(d.Top, k)
				d.top[k.Name] = ko
				items = append(items, &item{first: ko.start, last: prev, key: k.Name})
				continue
			}
			cur.Keys = append(cur.Keys, k)
			cur.orig.keys = append(cur.orig.keys, ko)
			items[len(items)-1].last = prev
		case unstable.Table, unstable.ArrayTable:
			var parts, texts []string
			first, last := -1, 0
			it := e.Key()
			for it.Next() {
				n := it.Node()
				parts = append(parts, string(n.Data))
				texts = append(texts, string(data[n.Raw.Offset:n.Raw.Offset+n.Raw.Length]))
				if first < 0 {
					first = int(n.Raw.Offset)
				}
				last = int(n.Raw.Offset + n.Raw.Length)
			}
			array := e.Kind == unstable.ArrayTable
			header := rest(last)
			header = strings.TrimLeft(header, " \t")
			header = strings.TrimPrefix(header, "]")
			if array {
				header = strings.TrimPrefix(header, "]")
			}
			line := lineOf(first)
			o := &tableOrig{array: array, header: header, above: comments(d.lines, prev+1, line)}
			t := &Table{Array: array, orig: o}
			if topTable != nil && len(parts) > len(topParts) && strings.Join(parts[:len(topParts)], ".") == strings.Join(topParts, ".") {
				o.name = strings.Join(parts[len(topParts):], ".")
				o.text = strings.Join(texts[len(topParts):], ".")
				topTable.Subs = append(topTable.Subs, t)
				topTable.orig.subs = append(topTable.orig.subs, o)
				items[len(items)-1].last = line
			} else {
				o.name = strings.Join(parts, ".")
				o.text = strings.Join(texts, ".")
				d.Tables = append(d.Tables, t)
				topTable, topParts = t, parts
				items = append(items, &item{first: line, last: line, table: o})
			}
			t.Name = o.name
			cur = t
			prev = line
		}
	}
	if err := p.Error(); err != nil {
		return nil, err
	}

	prevLast := -1
	for _, it := range items {
		start := it.first
		for start-1 > prevLast && isComment(d.lines[start-1]) && !heading.MatchString(d.lines[start-1]) {
			start--
		}
		if start > prevLast+1 {
			d.segs = append(d.segs, segment{start: prevLast + 1, end: start, free: true})
		}
		d.segs = append(d.segs, segment{start: start, end: it.last + 1, key: it.key, table: it.table})
		lead := comments(d.lines, start, it.first)
		if it.table != nil {
			it.table.start, it.table.end, it.table.above = start, it.last+1, lead
		} else {
			d.top[it.key].above = lead
		}
		prevLast = it.last
	}
	if prevLast+1 < len(d.lines) {
		d.segs = append(d.segs, segment{start: prevLast + 1, end: len(d.lines), free: true})
	}
	return d, nil
}

// Get returns a top-level key, or nil.
func (d *Doc) Get(name string) *Key { return getKey(d.Top, name) }

// Find returns the first top-level table named table whose key equals v,
// such as Find("row", "id", "zone").
func (d *Doc) Find(table, key string, v any) *Table {
	for _, t := range d.Tables {
		if t.Name != table {
			continue
		}
		if k := t.Get(key); k != nil && equal(k.Value, v) {
			return t
		}
	}
	return nil
}

// Get returns a key of the table, or nil.
func (t *Table) Get(name string) *Key { return getKey(t.Keys, name) }

// Sub returns the sub-tables with that name, in order.
func (t *Table) Sub(name string) []*Table {
	var out []*Table
	for _, s := range t.Subs {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// Set changes a top-level key, or inserts it after the last key that comes
// before it in the layout's order.
func (d *Doc) Set(name string, v any) {
	if k := d.Get(name); k != nil {
		k.Value = v
		return
	}
	order := d.layout.table("").Order
	r := rank(order, name)
	at := len(d.Top)
	if r < len(order) {
		at = 0
		for i, k := range d.Top {
			if rank(order, k.Name) < r {
				at = i + 1
			}
		}
	}
	d.Top = append(d.Top[:at], append([]*Key{{Name: name, Value: v}}, d.Top[at:]...)...)
}

// Delete removes a top-level key.
func (d *Doc) Delete(name string) { d.Top = deleteKey(d.Top, name) }

// Insert puts a table into Tables at index i.
func (d *Doc) Insert(i int, t *Table) {
	i = max(0, min(i, len(d.Tables)))
	d.Tables = append(d.Tables[:i], append([]*Table{t}, d.Tables[i:]...)...)
}

// Remove takes a table out of Tables.
func (d *Doc) Remove(t *Table) bool {
	for i, x := range d.Tables {
		if x == t {
			d.Tables = append(d.Tables[:i], d.Tables[i+1:]...)
			return true
		}
	}
	return false
}

// Set changes a key, or adds it.
func (t *Table) Set(name string, v any) {
	if k := t.Get(name); k != nil {
		k.Value = v
		return
	}
	t.Keys = append(t.Keys, &Key{Name: name, Value: v})
}

// Delete removes a key.
func (t *Table) Delete(name string) { t.Keys = deleteKey(t.Keys, name) }

// Clone returns a deep copy that is written as a new span.
func (t *Table) Clone() *Table {
	c := &Table{Name: t.Name, Array: t.Array}
	for _, k := range t.Keys {
		c.Keys = append(c.Keys, &Key{Name: k.Name, Value: copyValue(k.Value)})
	}
	for _, s := range t.Subs {
		c.Subs = append(c.Subs, s.Clone())
	}
	return c
}

func deleteKey(keys []*Key, name string) []*Key {
	out := keys[:0]
	for _, k := range keys {
		if k.Name != name {
			out = append(out, k)
		}
	}
	return out
}

func getKey(keys []*Key, name string) *Key {
	for _, k := range keys {
		if k.Name == name {
			return k
		}
	}
	return nil
}
