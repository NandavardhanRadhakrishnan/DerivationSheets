package editor

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// DraftEngine builds an engine for the sheet a draft describes, without
// writing anything, so the page can show the draft before it is saved.
// A draft with errors is still shown, as an unfinished row is part of the
// work being reviewed; the check reports the errors. When the draft cannot
// be shown, e is nil and reason says why: its result does not parse or
// makes a cycle. err is for a file that cannot be read or a draft that
// cannot be applied.
func (ed *Editor) DraftEngine(d SheetDraft) (e *eval.Engine, reason string, err error) {
	cur, err := ed.Store.Load(d.File)
	if err != nil {
		return nil, "", err
	}
	content, err := d.Apply(cur.Data)
	if err != nil {
		return nil, "", err
	}
	abs := filepath.Join(ed.Root, filepath.FromSlash(cur.Path))
	s, err := sheet.Parse(filepath.Dir(abs), abs, string(content))
	var pe *sheet.ParseError
	switch {
	case errors.As(err, &pe):
		return nil, fmt.Sprintf("line %d: %s", pe.Line, pe.Message), nil
	case err != nil:
		return nil, "", err
	}
	if e, err = eval.New(s); err != nil {
		return nil, err.Error(), nil
	}
	return e, "", nil
}

// CaseView is one case the draft would generate, with what every row
// computes in it.
type CaseView struct {
	ID string `json:"id"`
	// Key identifies the case by its input. Case IDs are named after
	// results, so an edit can rename a case; its key stays.
	Key  string             `json:"key"`
	Rows map[string]CaseRow `json:"rows"`
}

// Unreached is a coverage target no generated case reaches, such as a
// table rule whose result a later row cannot use.
type Unreached struct {
	Row    string `json:"row"`
	Target string `json:"target"` // the target's key without the row, such as "[true,>= 2]"
	Kind   string `json:"kind"`   // as cases.Target.Kind
}

// DraftCaseSet is what saving would write to cases.toml.
type DraftCaseSet struct {
	Cases     []CaseView  `json:"cases"`
	Unreached []Unreached `json:"unreached,omitempty"`
}

// CaseRow is one row's result in one case.
type CaseRow struct {
	Value string `json:"value,omitempty"` // as the page shows it
	None  bool   `json:"none,omitempty"`  // there is no value; Note says why
	Rule  int    `json:"rule,omitempty"`  // the decision-table rule that gave the value
	Note  string `json:"note,omitempty"`  // why there is no value, or how the value came about
}

// DraftCases generates the cases the draft would write on save, with the
// reference time of the cases file now beside the sheet, and evaluates
// every row in each. When the draft cannot be evaluated, reason says why,
// as for DraftEngine. A draft may have validation errors the case
// generator was never written for: a panic becomes a reason.
func (ed *Editor) DraftCases(d SheetDraft) (set *DraftCaseSet, reason string, err error) {
	e, reason, err := ed.DraftEngine(d)
	if e == nil {
		return nil, reason, err
	}
	defer func() {
		if r := recover(); r != nil {
			set, reason, err = nil, fmt.Sprintf("the cases cannot be worked out yet: %v", r), nil
		}
	}()
	gen := cases.Generate(e, ed.refTime(path.Join(path.Dir(d.File), cases.FileName)))
	set = &DraftCaseSet{Cases: make([]CaseView, 0, len(gen.Cases))}
	for _, c := range gen.Cases {
		set.Cases = append(set.Cases, CaseView{ID: c.ID, Key: inputKey(c.Input), Rows: caseRows(e, e.Evaluate(eval.Input(c.Input)))})
	}
	for _, t := range gen.Uncovered {
		if t.Kind != "isolation" {
			set.Unreached = append(set.Unreached, Unreached{Row: t.Row, Target: strings.TrimPrefix(t.Key, t.Row+":"), Kind: t.Kind})
		}
	}
	return set, "", nil
}

// refTime is the reference time saving would regenerate cases with: the
// one in the cases file, or the default when there is none or it cannot be
// read (saving then reports why it did not regenerate).
func (ed *Editor) refTime(file string) time.Time {
	var old *cases.File
	if f, err := ed.Store.Load(file); err == nil {
		old, _ = cases.ParseFile(f.Data)
	}
	t, err := cases.RefTime(old)
	if err != nil {
		t, _ = cases.RefTime(nil)
	}
	return t
}

// inputKey writes an input as sorted name=value pairs.
func inputKey(in map[string]any) string {
	names := make([]string, 0, len(in))
	for k := range in {
		names = append(names, k)
	}
	sort.Strings(names)
	for i, k := range names {
		names[i] = k + "=" + sheet.FormatValue(in[k])
	}
	return strings.Join(names, ";")
}

// caseRows reads every row's result out of one evaluation.
func caseRows(e *eval.Engine, res *eval.Result) map[string]CaseRow {
	out := map[string]CaseRow{}
	for _, r := range e.Sheet.Rows {
		st := res.ByRow[r.ID]
		var cr CaseRow
		switch {
		case st == nil && res.Reject != nil:
			cr = CaseRow{None: true, Note: fmt.Sprintf("rejected earlier (%s)", res.Reject.Code)}
		case st == nil && res.Err != nil:
			cr = CaseRow{None: true, Note: fmt.Sprintf("not computed: %s failed", res.Err.Row)}
		case st == nil:
			cr = CaseRow{None: true, Note: "not computed"}
		case res.Err != nil && res.Err.Row == r.ID:
			cr = CaseRow{None: true, Note: res.Err.Message}
		case st.Status == eval.StatusUnknown:
			cr = CaseRow{None: true, Note: st.Detail}
		case st.Status == eval.StatusNotRequired:
			cr = CaseRow{None: true, Note: "not given; not required"}
		case st.Rejected && res.Reject != nil && res.Reject.Index < 0:
			cr = CaseRow{None: true, Note: fmt.Sprintf("rejected here (%s)", res.Reject.Code)}
		default:
			cr = CaseRow{Value: display(st.Value), Rule: st.TableRule}
			switch {
			case st.Rejected && res.Reject != nil:
				cr.Note = fmt.Sprintf("rejected here (%s)", res.Reject.Code)
			case st.Status == eval.StatusDefault:
				cr.Note = "missing; default used"
			}
		}
		out[r.ID] = cr
	}
	return out
}

// display writes a value as the sheet page does: booleans as Yes and No.
func display(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return "Yes"
		}
		return "No"
	}
	return sheet.FormatValue(v)
}
