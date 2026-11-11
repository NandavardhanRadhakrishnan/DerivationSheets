// Package diff compares two versions of the sheets, flows and interfaces in
// a git repository and reports what changed as rows, boundaries and who
// computes what, rather than as lines of TOML.
package diff

// Status says whether a sheet, row, flow or field was added, removed or
// changed.
type Status string

const (
	Added   Status = "added"
	Removed Status = "removed"
	Changed Status = "changed"
)

// FieldChange is one exact part that differs. Path names it, for example
// `formula`, `send.field` or `table.rows[true,>= 2]`. Before is empty for an
// addition and After is empty for a removal. Label marks fields that only
// describe (names, descriptions) and do not change behaviour.
type FieldChange struct {
	Path   string `json:"path,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	Label  bool   `json:"label,omitempty"`
}

// RowChange is one row of a sheet that was added, removed or changed. For an
// added or removed row, Fields lists everything it had.
type RowChange struct {
	ID     string        `json:"id,omitempty"`
	Status Status        `json:"status,omitempty"`
	Fields []FieldChange `json:"fields,omitempty"`
}

// SheetChange is the difference between two versions of one sheet.
type SheetChange struct {
	Name    string        `json:"name,omitempty"`     // the sheet's own `sheet` name
	Path    string        `json:"path,omitempty"`     // sheet.toml, relative to the repository
	OldPath string        `json:"old_path,omitempty"` // set only when the folder moved
	Status  Status        `json:"status,omitempty"`
	Meta    []FieldChange `json:"meta,omitempty"` // top-level keys (title, version, ...)
	Rows    []RowChange   `json:"rows,omitempty"`
	// TextOnly means the decoded content is the same and only formatting or
	// comments differ.
	TextOnly bool `json:"text_only,omitempty"`
	// Unparseable is "base" or "head" when that side is not valid TOML.
	Unparseable string `json:"unparseable,omitempty"`
}
