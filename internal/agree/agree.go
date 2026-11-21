// Package agree runs a product manager's generated cases through each team's
// own sheet and compares the values at the boundaries between teams.
package agree

import (
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
)

// Mode says what each team is fed.
type Mode int

const (
	Isolated Mode = iota // each team fed the PM's values at its incoming handoff
	EndToEnd             // each team fed the previous team's output
)

// Check statuses.
const (
	Agrees     = "agrees"
	Differs    = "differs"
	CannotTell = "cannot-tell"
)

// Check is one compared value: a boundary field, or a reject, in one case.
type Check struct {
	Case     string `json:"case"`
	Boundary int    `json:"-"`             // index in the PM analysis's Boundaries
	Role     string `json:"role"`          // flow.Role*
	Stage    string `json:"stage"`         // the PM's stage id
	Iface    string `json:"interface"`     // interface name
	Field    string `json:"field"`         // interface field, or "reject"
	Row      string `json:"row,omitempty"` // the PM row the field carries
	Team     string `json:"team"`          // team whose step produces the value
	Status   string `json:"status"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Report is the outcome of one agreement run.
type Report struct {
	Flow   string
	Mode   Mode
	Cases  int
	Checks []Check
	Notes  []string // what could not be read, once each
}

// TeamFlows returns the analysed own flows of a team, and notes on what
// could not be read.
type TeamFlows func(team string) (flows []*flow.Analysis, notes []string)

// Options tune a run.
type Options struct {
	Mode    Mode
	RefTime time.Time   // generated cases' reference time; zero means cases.DefaultReferenceTime
	Filled  *cases.File // the PM's committed cases.toml, for "?" values; may be nil
}
