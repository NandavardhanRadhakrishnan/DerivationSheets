package agree

import (
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
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
	m, note := findFlow(pm, segments(pm)[1], fl)
	if m == nil || m.StageOf[segments(pm)[1].Calls[0]] != "quote" {
		t.Fatalf("match = %+v, note %q", m, note)
	}
}

func TestFindFlowNoneAndSeveral(t *testing.T) {
	// none: the Cart flow's output is not the interface the segment ends with
	patch := map[string]string{"cart/flows/cart/flow.toml": replaceIn(t, "cart/flows/cart/flow.toml", "order-placed.toml", "quote-request.toml")}
	pm, teams := shop(t, patch)
	fl, _ := teams("Cart team")
	if m, note := findFlow(pm, segments(pm)[0], fl); m != nil || !strings.Contains(note, "order-placed") {
		t.Errorf("none: m = %v, note %q", m, note)
	}
	// several: the same flow twice
	pm, teams = shop(t, nil)
	fl, _ = teams("Cart team")
	m, note := findFlow(pm, segments(pm)[0], []*flow.Analysis{fl[0], fl[0]})
	if m != nil || strings.Count(note, fl[0].Flow.Dir) != 2 {
		t.Errorf("several: m = %v, note %q", m, note)
	}
}
