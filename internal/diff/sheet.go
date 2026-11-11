package diff

import (
	"bytes"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// Fields that only describe a row; every other field is logic.
var rowLabels = map[string]bool{"name": true, "description": true, "open_questions": true}

// DiffSheet compares two versions of a sheet.toml. A nil side means the
// sheet did not exist there (it was added or removed whole). A side that is
// not valid TOML is reported in Unparseable, not as an error.
func DiffSheet(base, head []byte) (SheetChange, error) {
	var sc SheetChange
	bm, bad := decodeSide(base)
	if bad {
		sc.Unparseable = "base"
	}
	hm, bad := decodeSide(head)
	if bad && sc.Unparseable == "" {
		sc.Unparseable = "head"
	}
	sc.Name, _ = hm["sheet"].(string)
	if sc.Name == "" {
		sc.Name, _ = bm["sheet"].(string)
	}
	switch {
	case base == nil:
		sc.Status = Added
	case head == nil:
		sc.Status = Removed
	default:
		sc.Status = Changed
	}
	if sc.Unparseable != "" {
		return sc, nil
	}

	for _, f := range diffMaps("", without(bm, "row", "sheet"), without(hm, "row", "sheet"), sheetListKey) {
		f.Label = f.Path == "title"
		sc.Meta = append(sc.Meta, f)
	}
	sc.Rows = diffRows(rowsOf(bm), rowsOf(hm))
	sc.TextOnly = base != nil && head != nil && !bytes.Equal(base, head) && equalValues(bm, hm)
	return sc, nil
}

func decodeSide(data []byte) (m map[string]any, bad bool) {
	if data == nil {
		return nil, false
	}
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, true
	}
	return m, false
}

func without(m map[string]any, names ...string) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, n := range names {
		delete(out, n)
	}
	return out
}

type rowEntry struct {
	id  string
	row map[string]any
}

// rowsOf returns a sheet's rows in file order. A row without an id is named
// by its position.
func rowsOf(m map[string]any) []rowEntry {
	list, _ := m["row"].([]any)
	var out []rowEntry
	for i, r := range list {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" {
			id = fmt.Sprintf("#%d", i+1)
		}
		out = append(out, rowEntry{id, row})
	}
	return out
}

func diffRows(before, after []rowEntry) []RowChange {
	byID := map[string]map[string]any{}
	for _, r := range before {
		byID[r.id] = r.row
	}
	inAfter := map[string]bool{}
	var out []RowChange
	for _, r := range after {
		inAfter[r.id] = true
		b, ok := byID[r.id]
		if !ok {
			out = append(out, RowChange{ID: r.id, Status: Added, Fields: rowFields(nil, r.row)})
			continue
		}
		if fs := rowFields(b, r.row); len(fs) > 0 {
			out = append(out, RowChange{ID: r.id, Status: Changed, Fields: fs})
		}
	}
	for _, r := range before {
		if !inAfter[r.id] {
			out = append(out, RowChange{ID: r.id, Status: Removed, Fields: rowFields(r.row, nil)})
		}
	}
	return out
}

func rowFields(before, after map[string]any) []FieldChange {
	fs := diffMaps("", without(before, "id"), without(after, "id"), sheetListKey)
	for i := range fs {
		fs[i].Label = rowLabels[firstSegment(fs[i].Path)]
	}
	return fs
}

func firstSegment(path string) string {
	for i, c := range path {
		if c == '.' || c == '[' {
			return path[:i]
		}
	}
	return path
}
