package editor

import (
	"path/filepath"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// LookupFile is design-time data a lookup reads from a file: its
// values_file, or data/config.toml for a config lookup with no values.
// The editor shows it read-only.
type LookupFile struct {
	Path    string           `json:"path"`    // slash-separated, relative to the sheet's folder
	Columns []string         `json:"columns"` // the key columns
	Entries []LookupFileItem `json:"entries"`
}

// LookupFileItem is one entry: its key cells as written, and its value as
// the page shows it.
type LookupFileItem struct {
	Cells []string `json:"cells"`
	Value string   `json:"value"`
}

// LookupFiles lists, by row, the data of every lookup that reads a file.
func LookupFiles(s *sheet.Sheet) map[string]LookupFile {
	out := map[string]LookupFile{}
	for _, r := range s.Rows {
		if r.Lookup == nil || r.Lookup.Data == nil {
			continue
		}
		d := r.Lookup.Data
		p := d.File
		if rel, err := filepath.Rel(s.Dir, d.File); err == nil {
			p = rel
		}
		lf := LookupFile{Path: filepath.ToSlash(p), Columns: d.Columns, Entries: []LookupFileItem{}}
		for _, e := range d.Entries {
			it := LookupFileItem{Value: display(e.Value)}
			for _, c := range e.Cells {
				it.Cells = append(it.Cells, c.Raw)
			}
			lf.Entries = append(lf.Entries, it)
		}
		out[r.ID] = lf
	}
	return out
}
