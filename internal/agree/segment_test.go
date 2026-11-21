package agree

import (
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/iface"
)

func TestSegmentsOfShop(t *testing.T) {
	pm, _ := shop(t, nil)
	got := segments(pm)
	if len(got) != 3 || got[0].Team != "Cart team" || got[1].Team != "Orders team" || len(got[1].Calls) != 1 || !got[2].Callee {
		t.Fatalf("segments = %+v", got)
	}
	last := len(pm.Boundaries) - 1
	if got[0].In != 0 || got[0].Out != 1 {
		t.Errorf("Cart = %+v", got[0])
	}
	if got[1].In != 1 || got[1].Out != last || pm.Boundaries[got[1].Calls[0]].Role != flow.RoleRequest {
		t.Errorf("Orders = %+v", got[1])
	}
	if pm.Boundaries[got[2].In].Role != flow.RoleRequest || pm.Boundaries[got[2].Out].Role != flow.RoleResponse {
		t.Errorf("Shipping = %+v", got[2])
	}
}

func TestFindFlowByInterfaceName(t *testing.T) {
	pm, teams := shop(t, nil)
	fl, _ := teams("Orders team")
	m, note, _ := findFlow(pm, segments(pm)[1], fl)
	if m == nil || m.StageOf[segments(pm)[1].Calls[0]] != "quote" {
		t.Fatalf("match = %+v, note %q", m, note)
	}
}

func TestFindFlowNoneAndSeveral(t *testing.T) {
	// none: the Cart flow's output is not the interface the segment ends with
	patch := map[string]string{"cart/flows/cart/flow.toml": replaceIn(t, "cart/flows/cart/flow.toml", "order-placed.toml", "quote-request.toml")}
	pm, teams := shop(t, patch)
	fl, _ := teams("Cart team")
	if m, note, none := findFlow(pm, segments(pm)[0], fl); m != nil || !none || !strings.Contains(note, "order-placed") {
		t.Errorf("none: m = %v, note %q, none %v", m, note, none)
	}
	// several: the same flow twice
	pm, teams = shop(t, nil)
	fl, _ = teams("Cart team")
	m, note, none := findFlow(pm, segments(pm)[0], []*flow.Analysis{fl[0], fl[0]})
	if m != nil || none || strings.Count(note, fl[0].Flow.Dir) != 2 {
		t.Errorf("several: m = %v, note %q, none %v", m, note, none)
	}
}

func TestFindFlowForACallee(t *testing.T) {
	pm, teams := shop(t, nil)
	sg := segments(pm)[2]
	fl, _ := teams("Shipping team")
	if m, note, _ := findFlow(pm, sg, fl); m == nil || m.A != fl[0] || len(m.StageOf) != 0 {
		t.Errorf("Shipping: m = %+v, note %q", m, note)
	}
	// The caller's flow takes order-placed in, not the request.
	other, _ := teams("Orders team")
	if m, note, none := findFlow(pm, sg, other); m != nil || !none || !strings.Contains(note, "quote-request in and gives quote-response out") {
		t.Errorf("Orders for the callee: m = %+v, note %q", m, note)
	}
}

// bounds builds boundaries with the roles, stage ids and interface names,
// three strings each; an empty name leaves the interface out.
func bounds(spec ...string) []*flow.Boundary {
	var out []*flow.Boundary
	for i := 0; i+2 < len(spec); i += 3 {
		b := &flow.Boundary{Index: len(out), Role: spec[i], StageID: spec[i+1]}
		if spec[i+2] != "" {
			b.Iface = &iface.Interface{Name: spec[i+2]}
		}
		out = append(out, b)
	}
	return out
}

func TestFindFlowGivesEachCallItsOwnStage(t *testing.T) {
	// Two calls in one segment ask for the same interface: each takes its
	// own request boundary of the team flow, in order.
	pm := &flow.Analysis{Boundaries: bounds(
		flow.RoleInput, "input", "in",
		flow.RoleRequest, "first", "quote",
		flow.RoleResponse, "first", "fee",
		flow.RoleRequest, "second", "quote",
		flow.RoleResponse, "second", "fee",
		flow.RoleOutput, "output", "out")}
	team := &flow.Analysis{Flow: &flow.Flow{Dir: "team"}, Boundaries: bounds(
		flow.RoleInput, "input", "in",
		flow.RoleRequest, "a", "quote",
		flow.RoleResponse, "a", "fee",
		flow.RoleRequest, "b", "quote",
		flow.RoleResponse, "b", "fee",
		flow.RoleOutput, "output", "out")}
	sg := segment{Team: "T", In: 0, Out: 5, Calls: []int{1, 3}}
	m, note, _ := findFlow(pm, sg, []*flow.Analysis{team})
	if m == nil || m.StageOf[1] != "a" || m.StageOf[3] != "b" {
		t.Fatalf("m = %+v, note %q", m, note)
	}
	// A team flow with one such request cannot serve both calls.
	one := &flow.Analysis{Flow: &flow.Flow{Dir: "one"}, Boundaries: bounds(
		flow.RoleInput, "input", "in",
		flow.RoleRequest, "a", "quote",
		flow.RoleResponse, "a", "fee",
		flow.RoleOutput, "output", "out")}
	if m, note, none := findFlow(pm, sg, []*flow.Analysis{one}); m != nil || !none || !strings.Contains(note, "with a call request for quote, quote") {
		t.Errorf("one request: m = %+v, note %q", m, note)
	}
}

func TestFindFlowNeedsBothInterfaces(t *testing.T) {
	// A segment whose outgoing boundary has no interface matches nothing,
	// even a team flow whose last boundary has none either.
	pm := &flow.Analysis{Boundaries: bounds(flow.RoleInput, "input", "in", flow.RoleOutput, "output", "")}
	team := &flow.Analysis{Flow: &flow.Flow{Dir: "team"}, Boundaries: bounds(flow.RoleInput, "input", "in", flow.RoleOutput, "output", "")}
	if m, _, none := findFlow(pm, segment{Team: "T", In: 0, Out: 1}, []*flow.Analysis{team}); m != nil || !none {
		t.Errorf("m = %+v", m)
	}
}
