// Package evidence records how well rows and decision-table cells agree with
// test runs or production traces. Evidence is never stored in the sheet; it
// lives behind Store, which starts as JSON files in a git-ignored folder.
package evidence

import (
	"strconv"
	"time"
)

// Run kinds.
const (
	KindTest  = "test"  // QA results compared with cases.toml
	KindTrace = "trace" // production traces replayed through the evaluator (Phase 3)
)

// Run is one batch of evidence for one sheet version.
type Run struct {
	Sheet   string    `json:"sheet"`
	Version int       `json:"version"`
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Created time.Time `json:"created"`
	// Source describes where the observations came from, e.g. a results file.
	Source string `json:"source,omitempty"`
	// Period is the traffic window for trace runs.
	PeriodFrom *time.Time `json:"period_from,omitempty"`
	PeriodTo   *time.Time `json:"period_to,omitempty"`
	// Observations is the number of cases or traces compared.
	Observations int `json:"observations"`
	// Rows holds evidence per row ID.
	Rows map[string]*Tally `json:"rows"`
	// Branches holds evidence per branch: decision-table rules
	// ("loyalty_tier:[true,>= 2]"), rejects ("zone:reject:DESTINATION_NOT_SERVED"),
	// situational conditions ("customs_value:send.when:true") and inline lookup values
	// ("loading:High"). Keys match the covers in cases.toml.
	Branches map[string]*Tally `json:"branches"`
	// Notes are run-level problems, such as cases with no result.
	Notes []string `json:"notes,omitempty"`
}

// Tally counts observations of one row or branch.
type Tally struct {
	// Exercised counts observations that took this row or branch and could
	// confirm or refute it.
	Exercised int `json:"exercised"`
	// Matched counts exercised observations that agreed.
	Matched        int             `json:"matched"`
	Contradictions []Contradiction `json:"contradictions,omitempty"`
}

// Contradiction is one observation that disagreed.
type Contradiction struct {
	Case     string `json:"case"`
	Field    string `json:"field"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
	Note     string `json:"note,omitempty"`
}

// Store persists runs.
type Store interface {
	// Save stores a run and returns where it went.
	Save(run *Run) (string, error)
	// Latest returns the newest run for a sheet version, or nil.
	Latest(sheet string, version int) (*Run, error)
}

// State is the evidence state of a row or branch.
type State string

const (
	Asserted     State = "asserted"
	Verified     State = "verified"
	Partly       State = "partly"
	Unexercised  State = "unexercised"
	Contradicted State = "contradicted"
)

// Glyph returns the symbol for a state.
func (s State) Glyph() string {
	switch s {
	case Verified:
		return "●"
	case Partly:
		return "◐"
	case Unexercised:
		return "○"
	case Contradicted:
		return "✕"
	}
	return "◇"
}

// Status is a computed evidence state with its counts.
type Status struct {
	State State
	// Count is the number of matching observations (verified), or of
	// contradictions (contradicted).
	Count int
	// Covered and Branches are set for partly verified rows: n of m branches.
	Covered, Branches int
	Tally             *Tally
}

// Label renders the status compactly, e.g. "● 14", "◐ 2 of 3", "✕ 1".
func (s Status) Label() string {
	switch s.State {
	case Verified:
		return s.State.Glyph() + " " + itoa(s.Count)
	case Partly:
		return s.State.Glyph() + " " + itoa(s.Covered) + " of " + itoa(s.Branches)
	case Contradicted:
		return s.State.Glyph() + " " + itoa(s.Count)
	}
	return s.State.Glyph()
}

// RowStatus computes a row's state from a run. branches lists the row's
// branch keys; a row whose branches were not all exercised is partly
// verified.
func RowStatus(run *Run, row string, branches []string) Status {
	if run == nil {
		return Status{State: Asserted}
	}
	t := run.Rows[row]
	if t == nil || t.Exercised == 0 {
		return Status{State: Unexercised, Tally: t}
	}
	if n := len(t.Contradictions); n > 0 {
		return Status{State: Contradicted, Count: n, Tally: t}
	}
	covered := 0
	for _, b := range branches {
		if bt := run.Branches[b]; bt != nil && bt.Exercised > 0 {
			covered++
		}
	}
	if covered < len(branches) {
		return Status{State: Partly, Count: t.Matched, Covered: covered, Branches: len(branches), Tally: t}
	}
	return Status{State: Verified, Count: t.Matched, Tally: t}
}

// BranchStatus computes the state of one branch.
func BranchStatus(run *Run, key string) Status {
	if run == nil {
		return Status{State: Asserted}
	}
	t := run.Branches[key]
	switch {
	case t == nil || t.Exercised == 0:
		return Status{State: Unexercised, Tally: t}
	case len(t.Contradictions) > 0:
		return Status{State: Contradicted, Count: len(t.Contradictions), Tally: t}
	}
	return Status{State: Verified, Count: t.Matched, Tally: t}
}

func itoa(n int) string { return strconv.Itoa(n) }
