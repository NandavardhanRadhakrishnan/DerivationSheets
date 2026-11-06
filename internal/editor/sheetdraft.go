package editor

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// SheetDraft is what the browser sends for a sheet: whole rows that
// changed or are new, deletions, and the row order.
type SheetDraft struct {
	File    string             `json:"file"` // relative to the store root
	Token   string             `json:"token"`
	Title   *string            `json:"title,omitempty"`
	Order   []string           `json:"order,omitempty"` // every row ID in the new order; nil keeps the order
	Rows    map[string]RowData `json:"rows,omitempty"`  // changed and new rows, by ID
	Deleted []string           `json:"deleted,omitempty"`
}

// ErrDraft marks a draft that cannot be applied, as opposed to a file that
// cannot be read.
var ErrDraft = errors.New("draft")

func draftErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDraft, fmt.Sprintf(format, args...))
}

// Apply returns the content the draft describes.
func (d SheetDraft) Apply(orig []byte) ([]byte, error) {
	doc, err := tomledit.Parse(orig, tomledit.Sheet)
	if err != nil {
		return nil, err
	}
	if d.Title != nil {
		doc.Set("title", *d.Title)
	}
	byID := map[string]*tomledit.Table{}
	for _, t := range rows(doc) {
		id := str(t, "id")
		if _, dup := byID[id]; dup {
			return nil, draftErr("the file has two rows with ID %q; fix it by hand first", id)
		}
		byID[id] = t
	}

	deleted := map[string]bool{}
	for _, id := range d.Deleted {
		t := byID[id]
		if t == nil {
			return nil, draftErr("cannot delete row %q: there is no such row", id)
		}
		if _, ok := d.Rows[id]; ok {
			return nil, draftErr("row %q is both changed and deleted", id)
		}
		doc.Remove(t)
		delete(byID, id)
		deleted[id] = true
	}

	var added []string
	for id, r := range d.Rows {
		if id == "" {
			return nil, draftErr("a row has no ID")
		}
		if r.ID == "" {
			r.ID = id
		}
		if r.ID != id {
			return nil, draftErr("row %q: row IDs cannot be renamed (got %q)", id, r.ID)
		}
		if deleted[id] {
			continue
		}
		if t := byID[id]; t != nil {
			ApplyRow(t, r)
		} else {
			added = append(added, id)
		}
	}
	for _, id := range insertionOrder(added, d.Rows) {
		r := d.Rows[id]
		r.ID = id
		t := &tomledit.Table{Name: "row", Array: true}
		ApplyRow(t, r)
		doc.Insert(DefaultPosition(doc, r), t)
		byID[id] = t
	}

	if d.Order != nil {
		if err := reorder(doc, byID, d.Order); err != nil {
			return nil, err
		}
	}
	return doc.Bytes()
}

// insertionOrder sorts new rows so a row is placed after the new rows it
// uses; otherwise by ID, so the result does not depend on map order.
func insertionOrder(ids []string, rs map[string]RowData) []string {
	sort.Strings(ids)
	pending := map[string]bool{}
	for _, id := range ids {
		pending[id] = true
	}
	var out []string
	for len(out) < len(ids) {
		pick := ""
		for _, id := range ids {
			if !pending[id] {
				continue
			}
			ready := true
			for _, u := range rs[id].Uses {
				if pending[u] && u != id {
					ready = false
				}
			}
			if ready {
				pick = id
				break
			}
		}
		if pick == "" { // a cycle among new rows: take the first
			for _, id := range ids {
				if pending[id] {
					pick = id
					break
				}
			}
		}
		delete(pending, pick)
		out = append(out, pick)
	}
	return out
}

// reorder puts the rows in order, in the slots rows held before. Every row
// must be named exactly once: a row left out is an error, never a deletion.
func reorder(doc *tomledit.Doc, byID map[string]*tomledit.Table, order []string) error {
	seen := map[string]bool{}
	var unknown, twice, missing []string
	for _, id := range order {
		switch {
		case byID[id] == nil:
			unknown = append(unknown, id)
		case seen[id]:
			twice = append(twice, id)
		}
		seen[id] = true
	}
	for _, t := range rows(doc) {
		if id := str(t, "id"); !seen[id] {
			missing = append(missing, id)
		}
	}
	switch {
	case len(missing) > 0:
		return draftErr("the row order leaves out %s; delete rows explicitly", strings.Join(missing, ", "))
	case len(unknown) > 0:
		return draftErr("the row order names rows that do not exist: %s", strings.Join(unknown, ", "))
	case len(twice) > 0:
		return draftErr("the row order names rows more than once: %s", strings.Join(twice, ", "))
	}
	i := 0
	for j, t := range doc.Tables {
		if t.Name == "row" {
			doc.Tables[j] = byID[order[i]]
			i++
		}
	}
	return nil
}

func rows(doc *tomledit.Doc) []*tomledit.Table {
	var out []*tomledit.Table
	for _, t := range doc.Tables {
		if t.Name == "row" {
			out = append(out, t)
		}
	}
	return out
}

// DefaultPosition is the index in Tables for a new row with no position:
// inputs after the last input of the same source kind (else after the last
// input, else first); derived rows after the lowest row they use (else
// after the last row).
func DefaultPosition(doc *tomledit.Doc, r RowData) int {
	first, last, lastInput, lastKind, lowestUse := -1, -1, -1, -1, -1
	kind, _, _ := strings.Cut(r.Source, ".")
	uses := map[string]bool{}
	for _, u := range r.Uses {
		uses[u] = true
	}
	for i, t := range doc.Tables {
		if t.Name != "row" {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		if src := str(t, "source"); src != "" {
			lastInput = i
			if k, _, _ := strings.Cut(src, "."); k == kind {
				lastKind = i
			}
		}
		if uses[str(t, "id")] {
			lowestUse = i
		}
	}
	after := func(i int) int { return i + 1 }
	if r.Source != "" {
		switch {
		case lastKind >= 0:
			return after(lastKind)
		case lastInput >= 0:
			return after(lastInput)
		case first >= 0:
			return first
		}
		return len(doc.Tables)
	}
	switch {
	case lowestUse >= 0:
		return after(lowestUse)
	case last >= 0:
		return after(last)
	}
	return len(doc.Tables)
}
