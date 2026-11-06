package tomledit

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Bytes writes the document back.
func (d *Doc) Bytes() ([]byte, error) {
	var b strings.Builder
	for _, sg := range d.segs {
		for _, line := range d.lines[sg.start:sg.end] {
			b.WriteString(line)
		}
	}
	return []byte(b.String()), nil
}

// Format writes every span in canonical form. Values keep their text as
// written; comments and free text are kept.
func (d *Doc) Format() ([]byte, error) {
	tables := map[*tableOrig]*Table{}
	for _, t := range d.Tables {
		if t.orig != nil {
			tables[t.orig] = t
		}
	}
	widths := d.topWidths()
	w := &writer{nl: d.nl}
	for _, sg := range d.segs {
		switch {
		case sg.free:
			w.verbatim(d.lines[sg.start:sg.end])
		case sg.table != nil:
			w.verbatim(d.lines[sg.start : sg.start+len(sg.table.above)])
			lines, err := d.renderTable(tables[sg.table], sg.table, "", "")
			if err != nil {
				return nil, err
			}
			w.rendered(lines)
		default:
			ko := d.top[sg.key]
			w.verbatim(d.lines[sg.start:ko.start])
			lines, err := d.renderKey(d.Get(sg.key), ko, "", widths[sg.key], d.layout.table(""))
			if err != nil {
				return nil, err
			}
			w.rendered(lines)
		}
	}
	return w.bytes(d.endsWithNewline()), nil
}

// writer assembles output from original lines, which carry their own
// terminators, and rendered lines, which get the file's line ending.
type writer struct {
	b  strings.Builder
	nl string
}

func (w *writer) verbatim(lines []string) {
	for _, l := range lines {
		w.b.WriteString(l)
	}
}

func (w *writer) rendered(lines []string) {
	for _, l := range lines {
		w.b.WriteString(l)
		w.b.WriteString(w.nl)
	}
}

// bytes returns the output, without a final newline when the original had
// none.
func (w *writer) bytes(final bool) []byte {
	s := w.b.String()
	if !final {
		s = strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
	}
	return []byte(s)
}

func (d *Doc) endsWithNewline() bool {
	return len(d.lines) == 0 || strings.HasSuffix(d.lines[len(d.lines)-1], "\n")
}

// topWidths is the alignment width of each top-level key: the longest
// aligned key of its group, the keys whose original lines are adjacent plus
// keys inserted next to them.
func (d *Doc) topWidths() map[string]int {
	tl := d.layout.table("")
	widths := map[string]int{}
	var group []*Key
	flush := func() {
		width := 0
		for _, k := range group {
			if n := len(formatKey(k.Name)); !has(tl.Unaligned, k.Name) && n > width {
				width = n
			}
		}
		for _, k := range group {
			widths[k.Name] = width
		}
		group = nil
	}
	for i, k := range d.Top {
		if i > 0 {
			a, b := d.top[d.Top[i-1].Name], d.top[k.Name]
			if a != nil && b != nil && b.start-len(b.above) != a.end {
				flush()
			}
		}
		group = append(group, k)
	}
	flush()
	return widths
}

// renderKey writes one key in canonical form: the comments that were above
// it, then the key, its value and its original trailing comment. A value
// equal to the original keeps its original text.
func (d *Doc) renderKey(k *Key, ko *keyOrig, indent string, width int, tl TableLayout) ([]string, error) {
	var lines []string
	name := formatKey(k.Name)
	if strings.Contains(k.Name, ".") && ko != nil {
		name = k.Name // a dotted key, written as it was parsed
	}
	pad := ""
	if !has(tl.Unaligned, k.Name) && width > len(name) {
		pad = strings.Repeat(" ", width-len(name))
	}
	var value []string
	trailing := ""
	if ko != nil {
		trailing = ko.trailing
		if equal(ko.value, k.Value) {
			for _, l := range strings.Split(ko.text, "\n") {
				value = append(value, strings.TrimSuffix(l, "\r"))
			}
		}
	}
	if value == nil {
		var err error
		value, err = d.formatValueLines(k.Value, indent, has(tl.Multiline, k.Name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k.Name, err)
		}
	}
	value[0] = indent + name + pad + " = " + value[0]
	value[len(value)-1] += trailing
	return append(lines, value...), nil
}

// formatValueLines writes a value as lines. A multiline array is written
// one element per line, cells lined up.
func (d *Doc) formatValueLines(v any, indent string, multiline bool) ([]string, error) {
	arr, ok := v.([]any)
	if !multiline || !ok || len(arr) == 0 {
		s, err := FormatValue(v, d.nl)
		if err != nil {
			return nil, err
		}
		return strings.Split(s, d.nl), nil
	}
	cells := make([][]string, len(arr)) // cells of array elements
	texts := make([]string, len(arr))   // other elements
	var widths []int
	for i, e := range arr {
		inner, isArr := e.([]any)
		if !isArr {
			s, err := FormatValue(e, d.nl)
			if err != nil {
				return nil, err
			}
			texts[i] = s
			continue
		}
		cells[i] = []string{}
		for j, c := range inner {
			s, err := FormatValue(c, d.nl)
			if err != nil {
				return nil, err
			}
			cells[i] = append(cells[i], s)
			if j < len(inner)-1 {
				for len(widths) <= j {
					widths = append(widths, 0)
				}
				widths[j] = max(widths[j], utf8.RuneCountInString(s)+1)
			}
		}
	}
	lines := []string{"["}
	for i := range arr {
		if cells[i] == nil {
			lines = append(lines, indent+"  "+texts[i]+",")
			continue
		}
		var b strings.Builder
		b.WriteString("[")
		for j, c := range cells[i] {
			if j == len(cells[i])-1 {
				b.WriteString(c)
				break
			}
			b.WriteString(c + ",")
			b.WriteString(strings.Repeat(" ", widths[j]-utf8.RuneCountInString(c)))
		}
		b.WriteString("],")
		lines = append(lines, indent+"  "+b.String())
	}
	return append(lines, indent+"]"), nil
}

// renderTable writes a table and its sub-tables in canonical form. o is the
// table as it was, or nil; the leading comments of a top-level table are
// written by the caller.
func (d *Doc) renderTable(t *Table, o *tableOrig, parent, indent string) ([]string, error) {
	path := t.Name
	if parent != "" {
		path = parent + "." + t.Name
	}
	tl := d.layout.table(path)
	var lines []string
	if o != nil && parent != "" {
		for _, c := range o.above {
			lines = append(lines, indent+c)
		}
	}
	var hdr []string
	for _, part := range strings.Split(path, ".") {
		hdr = append(hdr, formatKey(part))
	}
	open, close := "[", "]"
	if t.Array {
		open, close = "[[", "]]"
	}
	header := indent + open + strings.Join(hdr, ".") + close
	if o != nil {
		header += o.header
	}
	lines = append(lines, header)

	keys := append([]*Key(nil), t.Keys...)
	sort.SliceStable(keys, func(i, j int) bool { return rank(tl.Order, keys[i].Name) < rank(tl.Order, keys[j].Name) })
	width := 0
	for _, k := range keys {
		if n := len(formatKey(k.Name)); !has(tl.Unaligned, k.Name) && n > width {
			width = n
		}
	}
	for _, k := range keys {
		ko := o.key(k.Name)
		if ko != nil {
			for _, c := range ko.above {
				lines = append(lines, indent+c)
			}
		}
		kl, err := d.renderKey(k, ko, indent, width, tl)
		if err != nil {
			return nil, err
		}
		lines = append(lines, kl...)
	}

	type sub struct {
		t *Table
		o *tableOrig
	}
	seen := map[string]int{}
	subs := make([]sub, len(t.Subs))
	for i, s := range t.Subs {
		subs[i] = sub{s, o.sub(s.Name, seen[s.Name])}
		seen[s.Name]++
	}
	sort.SliceStable(subs, func(i, j int) bool { return rank(tl.Subs, subs[i].t.Name) < rank(tl.Subs, subs[j].t.Name) })
	for _, s := range subs {
		sl, err := d.renderTable(s.t, s.o, path, indent+"  ")
		if err != nil {
			return nil, err
		}
		lines = append(lines, "")
		lines = append(lines, sl...)
	}
	return lines, nil
}

// key returns the original key with that name, or nil.
func (o *tableOrig) key(name string) *keyOrig {
	if o == nil {
		return nil
	}
	for _, k := range o.keys {
		if k.name == name {
			return k
		}
	}
	return nil
}

// sub returns the n-th original sub-table with that name, or nil.
func (o *tableOrig) sub(name string, n int) *tableOrig {
	if o == nil {
		return nil
	}
	for _, s := range o.subs {
		if s.name == name {
			if n == 0 {
				return s
			}
			n--
		}
	}
	return nil
}
