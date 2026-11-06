package cases

import (
	"fmt"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Summary is what a regeneration reports.
type Summary struct {
	Cases, Covered, Targets, Evaluated, Kept int
	Uncovered                                []Target
	FillIn                                   []string // one line per expected value to fill in by hand
}

// Regenerate builds the content of cases.toml for e's sheet, keeping
// hand-written expected values from old (nil: none).
func Regenerate(e *eval.Engine, old *File, ref time.Time) ([]byte, Summary) {
	res := Generate(e, ref)
	sum := Summary{
		Cases:     len(res.Cases),
		Covered:   len(res.Targets) - len(res.Uncovered),
		Targets:   len(res.Targets),
		Evaluated: res.Evaluated,
		Kept:      Preserve(res.Cases, old),
		Uncovered: res.Uncovered,
	}
	for _, c := range res.Cases {
		for _, f := range c.Expected {
			if f.Value == Unknown {
				sum.FillIn = append(sum.FillIn, fmt.Sprintf("  case %q: fill in expected %s by hand (depends on a text rule)", c.ID, f.Name))
			}
		}
		for _, call := range c.Calls {
			for _, f := range call.Fields {
				if f.Value == Unknown {
					sum.FillIn = append(sum.FillIn, fmt.Sprintf("  case %q: fill in expected %s in the call to %s by hand (depends on a text rule)", c.ID, f.Name, call.Target))
				}
			}
		}
	}
	var inputs []*sheet.Row
	for _, r := range e.Graph.Order {
		if r.IsInput() {
			inputs = append(inputs, r)
		}
	}
	return Format(e.Sheet, inputs, ref, res.Cases), sum
}

// Lines is the report `dsheets cases` prints after writing path.
func (s Summary) Lines(path string) []string {
	var out []string
	if s.Kept > 0 {
		out = append(out, fmt.Sprintf("kept %d hand-written expected values", s.Kept))
	}
	out = append(out, fmt.Sprintf("wrote %s: %d cases covering %d of %d targets (%d inputs evaluated)",
		path, s.Cases, s.Covered, s.Targets, s.Evaluated))
	for _, t := range s.Uncovered {
		out = append(out, fmt.Sprintf("  not covered: %s (%s)", t.Key, t.Kind))
	}
	return append(out, s.FillIn...)
}

// RefTime is old's reference time, or DefaultReferenceTime when old is nil
// or has none.
func RefTime(old *File) (time.Time, error) {
	if old == nil || old.ReferenceTime == "" {
		return time.Parse(time.RFC3339, DefaultReferenceTime)
	}
	t, err := time.Parse(time.RFC3339, old.ReferenceTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("reference_time: %v", err)
	}
	return t, nil
}
