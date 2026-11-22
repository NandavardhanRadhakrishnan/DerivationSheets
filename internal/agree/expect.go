package agree

import (
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
)

// pmCase is one of the PM's generated cases, evaluated on the PM's sheet.
type pmCase struct {
	ID    string
	Input map[string]any
	Res   *eval.Result
	// Filled holds field -> value pairs the PM committed for this case, for
	// rows the engine cannot compute. "?" placeholders are left out.
	Filled map[string]any
}

// pmCases generates the PM sheet's cases and evaluates each one.
func pmCases(pm *flow.Analysis, o Options) []pmCase {
	ref := o.RefTime
	if ref.IsZero() {
		var err error
		if ref, err = cases.RefTime(nil); err != nil {
			panic("cases.DefaultReferenceTime does not parse: " + err.Error())
		}
	}
	filled := map[string]map[string]any{}
	if o.Filled != nil {
		for _, c := range o.Filled.Cases {
			m := map[string]any{}
			// Own fields win over call fields of the same name.
			for _, f := range c.Expected {
				if f.Value != cases.Unknown {
					m[f.Name] = f.Value
				}
			}
			for _, call := range c.Calls {
				for _, f := range call.Fields {
					if _, has := m[f.Name]; !has && f.Value != cases.Unknown {
						m[f.Name] = f.Value
					}
				}
			}
			filled[c.ID] = m
		}
	}
	var out []pmCase
	for _, c := range cases.Generate(pm.Engine, ref).Cases {
		out = append(out, pmCase{ID: c.ID, Input: c.Input, Res: pm.Engine.Evaluate(eval.Input(c.Input)), Filled: filled[c.ID]})
	}
	return out
}

// wire is the value the PM expects on the wire for a boundary field in a
// case. ok is false when the field carries no PM row or the value is not
// known.
func wire(pm *flow.Analysis, c pmCase, b *flow.Boundary, field string) (v any, ok bool) {
	row := b.RowOf(field)
	if row == "" {
		return nil, false
	}
	sent := false
	if c.Res != nil {
		if r := pm.Sheet.ByID[row]; r != nil {
			// A sent field is read from the payload so the send map applies. The
			// row value is never a stand-in: it is not what goes on the wire.
			switch b.Role {
			case flow.RoleOutput:
				if r.Send != nil && flow.Norm(r.Send.Field) == flow.Norm(field) {
					sent = true
					if v, ok := eval.GetPath(c.Res.Payload, r.Send.Field); ok {
						return v, true
					}
				}
			case flow.RoleRequest:
				for _, sd := range r.CallSends {
					if sd.Target == b.StageID && flow.Norm(sd.Field) == flow.Norm(field) {
						sent = true
						if v, ok := eval.GetPath(c.Res.CallPayloads[sd.Target], sd.Field); ok {
							return v, true
						}
					}
				}
			}
		}
	}
	// A send exists but put nothing on the wire (not sent, or the row is unknown):
	// only a filled value may stand in, never the unmapped row value.
	if !sent && c.Res != nil && c.Res.Status[row] != eval.StatusUnknown {
		if v, ok := c.Res.Values[row]; ok {
			return v, true
		}
	}
	if v, ok := c.Filled[field]; ok {
		return v, true
	}
	return nil, false
}
