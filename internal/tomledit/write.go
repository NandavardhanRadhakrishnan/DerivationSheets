package tomledit

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Bytes writes the document back: unchanged spans as they were, changed
// and new spans in canonical form, free text where it was. A table keeps
// its identity wherever it moves in Tables; a table that is gone is deleted
// together with the blank line before it.
func (d *Doc) Bytes() ([]byte, error) { return d.write(false) }

// Format writes every span in canonical form. Values keep their text as
// written; comments and free text are kept.
func (d *Doc) Format() ([]byte, error) { return d.write(true) }

// item is a top-level key or table in the new order, with the index of its
// original segment, or -1 when it is new.
type item struct {
	key   *Key
	table *Table
	seg   int
}

func (d *Doc) write(all bool) ([]byte, error) {
	keySeg, tableSeg := map[string]int{}, map[*tableOrig]int{}
	for i, sg := range d.segs {
		switch {
		case sg.free:
		case sg.table != nil:
			tableSeg[sg.table] = i
		default:
			keySeg[sg.key] = i
		}
	}
	used := map[int]bool{}
	var items []item
	for _, k := range d.Top {
		seg := -1
		if i, ok := keySeg[k.Name]; ok && !used[i] {
			seg, used[i] = i, true
		}
		items = append(items, item{key: k, seg: seg})
	}
	for _, t := range d.Tables {
		seg := -1
		if i, ok := tableSeg[t.orig]; ok && t.orig != nil && !used[i] {
			seg, used[i] = i, true
		}
		items = append(items, item{table: t, seg: seg})
	}

	// Items whose original positions still increase stay where they are;
	// the others are new or moved, and go after the item before them.
	anchor := anchors(items)
	keep := map[int]bool{}
	after := map[int][]item{}
	var head []item
	last := -1
	for i, it := range items {
		if anchor[i] {
			keep[it.seg] = true
			last = it.seg
		} else if last < 0 {
			head = append(head, it)
		} else {
			after[last] = append(after[last], it)
		}
	}
	dropped := func(si int) bool { return si < len(d.segs) && !d.segs[si].free && !keep[si] }
	blankAt := func(si int) bool { return si >= len(d.segs) || d.segs[si].free && blank(d.lines[d.segs[si].start]) }

	widths := d.topWidths()
	w := &writer{nl: d.nl}
	emit := func(it item) error {
		lines, err := d.renderItem(w, it, all, widths)
		if err == nil {
			w.rendered(lines)
		}
		return err
	}
	for si, sg := range d.segs {
		if sg.free {
			lines := d.lines[sg.start:sg.end]
			if dropped(si+1) && blankAt(si+2) {
				for len(lines) > 0 && blank(lines[len(lines)-1]) {
					lines = lines[:len(lines)-1]
				}
			}
			w.verbatim(lines)
			continue
		}
		if !keep[si] {
			continue
		}
		for _, it := range head {
			if err := emit(it); err != nil {
				return nil, err
			}
			if it.table != nil {
				w.rendered([]string{""})
			}
		}
		head = nil
		if err := emit(items[indexOfSeg(items, si)]); err != nil {
			return nil, err
		}
		for _, it := range after[si] {
			if it.table != nil {
				w.rendered([]string{""})
			}
			if err := emit(it); err != nil {
				return nil, err
			}
		}
	}
	// Nothing was kept: everything is new.
	for i, it := range head {
		if it.table != nil && (i > 0 || w.b.Len() > 0) {
			w.rendered([]string{""})
		}
		if err := emit(it); err != nil {
			return nil, err
		}
	}
	return w.bytes(d.endsWithNewline()), nil
}

func indexOfSeg(items []item, seg int) int {
	for i, it := range items {
		if it.seg == seg {
			return i
		}
	}
	return -1
}

func blank(line string) bool { return strings.TrimSpace(line) == "" }

// anchors marks the longest run of items whose original segments appear in
// increasing order.
func anchors(items []item) []bool {
	var tails []int // index into items of the smallest tail of each length
	prev := make([]int, len(items))
	for i, it := range items {
		prev[i] = -1
		if it.seg < 0 {
			continue
		}
		n := sort.Search(len(tails), func(j int) bool { return items[tails[j]].seg >= it.seg })
		if n > 0 {
			prev[i] = tails[n-1]
		}
		if n == len(tails) {
			tails = append(tails, i)
		} else {
			tails[n] = i
		}
	}
	out := make([]bool, len(items))
	if len(tails) > 0 {
		for i := tails[len(tails)-1]; i >= 0; i = prev[i] {
			out[i] = true
		}
	}
	return out
}

// renderItem writes an unchanged original item as it was, and returns the
// rendered lines of anything else, after writing its original leading
// comments.
func (d *Doc) renderItem(w *writer, it item, all bool, widths map[string]int) ([]string, error) {
	if it.key != nil {
		var ko *keyOrig
		if it.seg >= 0 {
			sg := d.segs[it.seg]
			ko = d.top[it.key.Name]
			if !all && equal(ko.value, it.key.Value) {
				w.verbatim(d.lines[sg.start:sg.end])
				return nil, nil
			}
			w.verbatim(d.lines[sg.start:ko.start])
		}
		return d.renderKey(it.key, ko, "", widths[it.key.Name], d.layout.table(""))
	}
	var o *tableOrig
	if it.seg >= 0 {
		sg := d.segs[it.seg]
		o = it.table.orig
		if !all && sameTable(it.table, o) {
			w.verbatim(d.lines[sg.start:sg.end])
			return nil, nil
		}
		w.verbatim(d.lines[sg.start : sg.start+len(o.above)])
	}
	return d.renderTable(it.table, o, "", "", "")
}

// sameTable reports whether a table still says what it said in the file:
// the same keys and values in any order, and the same sub-tables in order.
func sameTable(t *Table, o *tableOrig) bool {
	if o == nil || t.Name != o.name || t.Array != o.array || len(t.Keys) != len(o.keys) || len(t.Subs) != len(o.subs) {
		return false
	}
	for _, k := range t.Keys {
		if ko := o.key(k.Name); ko == nil || !equal(ko.value, k.Value) {
			return false
		}
	}
	for i, s := range t.Subs {
		if !sameTable(s, o.subs[i]) {
			return false
		}
	}
	return true
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
		if !strings.HasSuffix(l, "\n") {
			w.b.WriteString(w.nl)
		}
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
			if n := len(k.written()); !has(tl.Unaligned, k.Name) && n > width {
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
	name := k.written()
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
		var elems []elemOrig
		if ko != nil {
			elems = ko.elems
		}
		var err error
		// A multi-line array with comments on its elements stays multi-line,
		// so the comments are kept.
		multiline := has(tl.Multiline, k.Name)
		for _, el := range elems {
			if len(el.above) > 0 || el.trailing != "" {
				multiline = true
			}
		}
		value, err = d.formatValueLines(k.Value, indent, multiline, elems)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k.Name, err)
		}
	}
	value[0] = indent + name + pad + " = " + value[0]
	value[len(value)-1] += trailing
	return append(lines, value...), nil
}

// formatValueLines writes a value as lines. A multiline array is written
// one element per line, cells lined up; an element equal to one of the
// original elements keeps that element's comments.
func (d *Doc) formatValueLines(v any, indent string, multiline bool, elems []elemOrig) ([]string, error) {
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
	used := make([]bool, len(elems))
	comments := func(e any) ([]string, string) {
		for j, el := range elems {
			if !used[j] && equal(el.value, e) {
				used[j] = true
				return el.above, el.trailing
			}
		}
		return nil, ""
	}
	lines := []string{"["}
	for i, e := range arr {
		above, trailing := comments(e)
		for _, c := range above {
			lines = append(lines, indent+"  "+c)
		}
		if cells[i] == nil {
			lines = append(lines, indent+"  "+texts[i]+","+trailing)
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
		lines = append(lines, indent+"  "+b.String()+trailing)
	}
	return append(lines, indent+"]"), nil
}

// renderTable writes a table and its sub-tables in canonical form. o is the
// table as it was, or nil; the leading comments of a top-level table are
// written by the caller. parent is the parent's name for the layout, and
// parentText its header as written.
func (d *Doc) renderTable(t *Table, o *tableOrig, parent, parentText, indent string) ([]string, error) {
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
	text := formatDotted(t.Name)
	if o != nil && o.name == t.Name && o.text != "" {
		text = o.text
	}
	if parentText != "" {
		text = parentText + "." + text
	}
	open, close := "[", "]"
	if t.Array {
		open, close = "[[", "]]"
	}
	header := indent + open + text + close
	if o != nil {
		header += o.header
	}
	lines = append(lines, header)

	keys := append([]*Key(nil), t.Keys...)
	sort.SliceStable(keys, func(i, j int) bool { return rank(tl.Order, keys[i].Name) < rank(tl.Order, keys[j].Name) })
	width := 0
	for _, k := range keys {
		if n := len(k.written()); !has(tl.Unaligned, k.Name) && n > width {
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
		sl, err := d.renderTable(s.t, s.o, path, text, indent+"  ")
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
