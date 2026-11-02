// Package sheet loads a workflow folder (sheet.toml plus data/) into a model.
//
// Loading is lenient: fields with the wrong shape are skipped, and the
// validator reports them through the JSON Schema check. Callers that need a
// correct sheet must run validate first.
package sheet

import "strings"

// Rule kinds for derived rows.
const (
	RuleFormula = "formula"
	RuleTable   = "table"
	RuleLookup  = "lookup"
	RuleText    = "text"
)

// Usage codes, borrowed from X12 implementation guides.
const (
	UsageRequired    = "R"
	UsageSituational = "S"
)

// Values for send.otherwise.
const (
	OtherwiseDoNotSend        = "do-not-send"
	OtherwiseSenderDiscretion = "sender-discretion"
)

// Sheet is one workflow's specification.
type Sheet struct {
	Dir  string // workflow folder
	File string // path to sheet.toml

	Slug          string
	Version       int
	Title         string
	RequestSchema string
	TargetSchema  string

	Rows []*Row
	ByID map[string]*Row

	// Raw is the decoded TOML, used for JSON Schema validation.
	Raw map[string]any
	// Pos maps rows and keys to source lines.
	Pos *Positions
	// Config holds design-time config values from data/config.toml, keyed by
	// dotted path without the "config." prefix.
	Config map[string]any
	// LoadIssues are problems found while reading data files and the
	// project file.
	LoadIssues []Issue
	// Display holds rendering settings: built-in defaults, overridden by the
	// project file (dsheets.toml), overridden by the sheet's [display].
	Display Display
	// ProjectFile is the dsheets.toml that was applied, or "".
	ProjectFile string
}

// Issue is a problem found while loading.
type Issue struct {
	Code    string // diagnostic code; "data" when empty
	File    string
	Line    int
	Row     string
	Message string
}

// Row is one value on the sheet.
type Row struct {
	Index       int // position in sheet.toml, from 0
	ID          string
	Name        string
	Description string

	// Input rows.
	Source     string
	Type       string
	Usage      string
	When       string
	Default    any
	HasDefault bool

	// Derived rows.
	Uses    []string
	Formula string
	Table   *Table
	Lookup  *Lookup
	Text    string
	Rejects []Reject

	Send *Send
	// CallSends go to calls made on the way, by stage ID (send.target).
	CallSends []*Send
	// ExtraSends counts sends to the sheet's own target beyond the first.
	ExtraSends    int
	OpenQuestions []string

	// rules counts how many rule keys were present, for the validator.
	RuleKeys []string
}

// IsInput reports whether the row comes from a source rather than a rule.
func (r *Row) IsInput() bool { return r.Source != "" }

// SourceKind returns "request", "context", "db" or "config" for input rows.
func (r *Row) SourceKind() string {
	kind, _, _ := strings.Cut(r.Source, ".")
	return kind
}

// SourcePath returns the source without its kind prefix.
func (r *Row) SourcePath() string {
	_, path, _ := strings.Cut(r.Source, ".")
	return path
}

// Rule returns the row's rule kind, or "" for input rows and rows with no or
// several rules.
func (r *Row) Rule() string {
	if len(r.RuleKeys) != 1 {
		return ""
	}
	return r.RuleKeys[0]
}

// DisplayName is the name, or the ID when no name is given.
func (r *Row) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

// Table is a decision table.
type Table struct {
	HitPolicy string   // "unique" or "first"
	Columns   []string // input row IDs, without the trailing "=>"
	Rules     []TableRule
	// ResultColumn reports whether the last column was "=>".
	ResultColumn bool
}

// TableRule is one row of a decision table.
type TableRule struct {
	Cells  []Cell
	Result any
	Raw    []any
}

// Key identifies the rule by its input cells, e.g. `[true,true]`. Keys are
// stable when rules are reordered, unlike indexes.
func (t TableRule) Key() string {
	parts := make([]string, len(t.Cells))
	for i, c := range t.Cells {
		parts[i] = c.String()
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Lookup is a keyed lookup into a db or config source.
type Lookup struct {
	From       string
	Key        []string
	ValuesFile string
	// Inline values from `values`, for single-key lookups. Keys are matched
	// against the string form of the key value.
	Inline map[string]any
	// InlineOrder keeps the order of inline keys as written.
	InlineOrder []string
	// Data holds entries from values_file, or from data/config.toml for a
	// config source with no values.
	Data *LookupData
}

// Available reports whether design-time values exist.
func (l *Lookup) Available() bool {
	return l.Inline != nil || (l.Data != nil && len(l.Data.Entries) > 0)
}

// SourceKind returns "db" or "config".
func (l *Lookup) SourceKind() string {
	kind, _, _ := strings.Cut(l.From, ".")
	return kind
}

// SourcePath returns the source without its kind prefix.
func (l *Lookup) SourcePath() string {
	_, path, _ := strings.Cut(l.From, ".")
	return path
}

// LookupData is a table of lookup entries, usually from a CSV file.
type LookupData struct {
	File    string
	Columns []string // key columns
	Entries []LookupEntry
}

// LookupEntry is one line of lookup data.
type LookupEntry struct {
	Cells []Cell
	Value any
	Line  int
}

// Key identifies the entry by its key cells.
func (e LookupEntry) Key() string {
	parts := make([]string, len(e.Cells))
	for i, c := range e.Cells {
		parts[i] = c.String()
	}
	return strings.Join(parts, ",")
}

// Reject rejects the whole request when its condition is true.
type Reject struct {
	When    string
	Code    string
	Message string
}

// Send describes how a row is sent to the target.
type Send struct {
	// Target names a call stage of the flow the sheet belongs to; empty for
	// the sheet's own target.
	Target    string
	Field     string
	Usage     string
	When      string
	Otherwise string
	Map       map[string]any
	MapOrder  []string
}
