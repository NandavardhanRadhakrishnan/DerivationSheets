package editor

import (
	"path/filepath"
	"sort"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/expr"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/graph"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Refs is what the formula editor needs to know about a draft.
type Refs struct {
	// Uses lists, for each row in the draft with a formula, the rows its
	// expressions name (the formula, then any reject or send condition), in
	// the order they name them; the row itself is left out.
	Uses map[string][]string `json:"uses"`
	// Unknown lists, for each row in the draft with a formula, the names
	// in its expressions that are not rows.
	Unknown map[string][]string `json:"unknown"`
	// Cycle lists, for every row, the rows that would make a cycle if it
	// used them: itself and every row that depends on it.
	Cycle map[string][]string `json:"cycle"`
}

// SheetRefs reads a draft's formulas against the rows the draft leaves.
// A draft whose result does not parse has no refs.
func (ed *Editor) SheetRefs(d SheetDraft) (*Refs, error) {
	cur, err := ed.Store.Load(d.File)
	if err != nil {
		return nil, err
	}
	refs := &Refs{Uses: map[string][]string{}, Unknown: map[string][]string{}, Cycle: map[string][]string{}}
	content, err := d.Apply(cur.Data)
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(ed.Root, filepath.FromSlash(cur.Path))
	s, err := sheet.Parse(filepath.Dir(abs), abs, string(content))
	if err != nil {
		return refs, nil
	}
	var ids []string
	for _, r := range s.Rows {
		ids = append(ids, r.ID)
	}
	x, err := expr.NewCEL(ids)
	if err != nil {
		return nil, err
	}
	for id := range d.Rows {
		r := s.ByID[id]
		if r == nil || r.Formula == "" {
			continue
		}
		uses, unknown := []string{}, []string{}
		seen := map[string]bool{id: true}
		for _, le := range graph.Expressions(r) {
			names, err := x.Identifiers(le.Expr)
			if err != nil {
				continue // does not parse yet; validation says why
			}
			for _, n := range names {
				switch {
				case seen[n]:
				case s.ByID[n] != nil:
					uses = append(uses, n)
				default:
					unknown = append(unknown, n)
				}
				seen[n] = true
			}
		}
		refs.Uses[id], refs.Unknown[id] = uses, unknown
	}
	g := graph.Build(s, x)
	for _, r := range s.Rows {
		c := []string{r.ID}
		for u := range g.Descendants(r.ID) {
			c = append(c, u)
		}
		sort.Strings(c[1:])
		refs.Cycle[r.ID] = c
	}
	return refs, nil
}
