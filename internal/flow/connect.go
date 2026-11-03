package flow

import (
	"fmt"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// Connect checks a flow against other flows it shares interfaces with: the
// reverse of slicing, for adding a new consumer or producer to an existing
// interface.
//
//   - Branch off: when this flow's input is produced by another flow, every
//     value it lacks there is traced upstream through that flow, to where
//     it could come from.
//   - Feed in: when this flow produces an interface another flow consumes,
//     it must produce every field the other flow actually reads there, not
//     only the fields the schema marks required.
//
// Names differ between services, so values are only identified exactly by
// the interface fields where flows meet. Upstream origins found by name are
// labelled as guesses for the owning team to confirm.
func Connect(main *Analysis, others []*Analysis) {
	if main.Engine == nil {
		return
	}
	in := main.Boundaries[0]
	out := main.Boundaries[len(main.Boundaries)-1]
	defer sortItems(in)
	defer sortItems(out)
	for _, o := range others {
		if o.Engine == nil || o == main {
			continue
		}
		for _, b := range o.Boundaries {
			if b.Iface == nil {
				continue
			}
			if in.Iface != nil && b.Iface.Name == in.Iface.Name && b.Role != RoleInput {
				traceUpstream(main, in, o, b)
			}
			if out.Iface != nil && b.Iface.Name == out.Iface.Name && b.Role != RoleOutput {
				requireDownstream(main, out, o, b)
			}
		}
	}
}

// traceUpstream annotates each value main needs from its input that the
// interface lacks, with where the producing flow could get it.
func traceUpstream(main *Analysis, in *Boundary, up *Analysis, at *Boundary) {
	producer := up.Steps[at.Index-1].Team()
	for i := range in.Items {
		it := &in.Items[i]
		if it.Status != Missing || it.Row == "" {
			continue
		}
		// Every producer of the interface must supply the field, so each
		// one's answer is listed.
		trace := fmt.Sprintf("%s produces %s in flow %s: %s", producer, at.Iface.Name, up.Flow.Slug,
			origin(it.Field, up, at, main.Sheet.ByID[it.Row]))
		it.Note += "; " + trace
	}
}

// origin looks for a value upstream of a boundary: a row of the producing
// flow, or else a field of an earlier interface. Matches are by name, so
// they are reported as guesses.
func origin(field string, up *Analysis, at *Boundary, want *sheet.Row) string {
	guess := func(r *sheet.Row) bool {
		return nameMatch(field, r) || (want != nil && (norm(r.ID) == norm(want.ID) || norm(r.DisplayName()) == norm(want.DisplayName())))
	}
	for _, r := range up.Engine.Graph.Order {
		if isLocal(r) || !guess(r) {
			continue
		}
		p, placed := up.Place[r.ID]
		if placed && p > at.Index-1 {
			continue // computed only after this boundary
		}
		where := "received as " + r.SourcePath()
		if placed {
			where = "computed by " + up.Steps[p].Team()
		}
		return fmt.Sprintf("probable origin: %s (%s, %s; matched by name, please confirm); add %s to %s",
			r.DisplayName(), r.ID, where, field, at.Iface.Name)
	}
	for _, b := range up.Boundaries[:at.Index] {
		if b.Iface == nil {
			continue
		}
		for _, fd := range b.Iface.Fields {
			if fd.Type == "object" {
				continue
			}
			n := norm(field)
			if norm(fd.Name) == n || norm(last(fd.Name)) == n {
				return fmt.Sprintf("probable origin: %s field %s (matched by name, please confirm); pass it through to %s as %s",
					b.Iface.Name, fd.Name, at.Iface.Name, field)
			}
		}
	}
	return "not found upstream; name its origin"
}

// requireDownstream adds to main's output what the consuming flow reads
// from that interface.
func requireDownstream(main *Analysis, out *Boundary, down *Analysis, at *Boundary) {
	reads := map[string][]string{} // field -> consumers
	var order []string
	for _, it := range at.Items {
		if (it.Status == Carried || it.Status == Warn) && len(it.Consumers) > 0 {
			if reads[it.Field] == nil {
				order = append(order, it.Field)
			}
			reads[it.Field] = append(reads[it.Field], it.Consumers...)
		}
	}
	produced := map[string]bool{}
	for _, it := range out.Items {
		if it.Status == Carried || it.Status == Warn {
			produced[it.Field] = true
		}
	}
	for _, f := range order {
		note := fmt.Sprintf("flow %s reads it: needed by %s", down.Flow.Slug, strings.Join(reads[f], ", "))
		if produced[f] {
			continue
		}
		replaced := false
		for i := range out.Items {
			if out.Items[i].Field == f {
				out.Items[i].Status, out.Items[i].Note = Missing, note
				replaced = true
			}
		}
		if !replaced {
			out.Items = append([]Item{{Status: Missing, Field: f, Note: note}}, out.Items...)
		}
	}
}
