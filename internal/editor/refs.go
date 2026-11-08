package editor

import (
	"path/filepath"
	"sort"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/expr"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/graph"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Refs is what the panel needs to know about a draft's references.
type Refs struct {
	// Uses lists, for each row in the draft with a formula, a decision
	// table or a lookup, the rows it names in the order it names them: a
	// table's columns or a lookup's key, then the formula, then any reject
	// or send condition. The row itself is left out.
	Uses map[string][]string `json:"uses"`
	// Unknown lists, for the same rows, the names that are not rows.
	Unknown map[string][]string `json:"unknown"`
	// Cycle lists, for every row, the rows that would make a cycle if it
	// used them: itself and every row that depends on it.
	Cycle map[string][]string `json:"cycle"`
}

// SheetRefs reads a draft's rules against the rows the draft leaves.
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
		var named []string
		switch {
		case r == nil:
			continue
		case r.Table != nil:
			named = r.Table.Columns
		case r.Lookup != nil:
			named = r.Lookup.Key
		case r.Formula == "":
			continue // an input, or a text rule whose uses are written by hand
		}
		uses, unknown := []string{}, []string{}
		seen := map[string]bool{id: true}
		name := func(n string) {
			switch {
			case seen[n]:
			case s.ByID[n] != nil:
				uses = append(uses, n)
			default:
				unknown = append(unknown, n)
			}
			seen[n] = true
		}
		for _, n := range named {
			name(n)
		}
		for _, le := range graph.Expressions(r) {
			names, err := x.Identifiers(le.Expr)
			if err != nil {
				continue // does not parse yet; validation says why
			}
			for _, n := range names {
				name(n)
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
