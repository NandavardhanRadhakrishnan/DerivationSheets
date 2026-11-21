package requirements

import (
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/agree"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// outCheck is a hand-built check of a payment-request field produced by the
// Orders team.
func outCheck(id, row, field, status string, expected, actual any) agree.Check {
	return agree.Check{Case: id, Role: flow.RoleOutput, Stage: "output", Iface: "payment-request",
		Field: field, Row: row, Team: "Orders team", Status: status, Expected: expected, Actual: actual}
}

func costItem(status, note string) Requirement {
	return Requirement{ID: "R-91c2e4", Kind: Compute, Team: "Orders team", Subject: "shipping_cost", Row: "shipping_cost",
		Status: status, Note: note}
}

func TestComputeClosesWhenCasesAgreeDespiteRuleText(t *testing.T) {
	rs := []Requirement{costItem(StatusOpen, "orders's sheet has `shipping_cost` but its rule differs from the flow's")}
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Agrees, 0.0, 0.0),
		outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.Agrees, 18.0, 18.0),
		outCheck("silver-domestic", "net_amount", "netAmount", agree.Differs, 112.0, 100.0), // another row
	}}
	applyCases(rs, nil, rep)
	if q := rs[0]; q.Status != StatusDone || q.Note != "" || len(q.Cases) != 0 {
		t.Fatalf("got %+v, want done with no note", q)
	}
}

func TestComputeOpenListsDifferingCases(t *testing.T) {
	rs := []Requirement{costItem(StatusDone, "")}
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Agrees, 0.0, 0.0),
		outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.Differs, 4.0, 8.0),
		outCheck("standard-intl", "shipping_cost", "shippingCost", agree.Agrees, 31.0, 31.0),
	}}
	applyCases(rs, nil, rep)
	q := rs[0]
	if q.Status != StatusOpen || q.Note != "1 of 3 cases differ" {
		t.Fatalf("got status %q, note %q", q.Status, q.Note)
	}
	if len(q.Cases) != 1 || q.Cases[0].Case != "silver-domestic" {
		t.Errorf("cases = %+v, want the differing one", q.Cases)
	}
}

func TestComputeCannotTellIsUndocumented(t *testing.T) {
	rs := []Requirement{costItem(StatusDone, "")}
	unsure := outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.CannotTell, 4.0, nil)
	unsure.Note = "the sheet orders fails at row shipping_cost: division by zero"
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Agrees, 0.0, 0.0),
		unsure,
	}}
	applyCases(rs, nil, rep)
	q := rs[0]
	if q.Status != StatusUndocumented || q.Note != unsure.Note {
		t.Fatalf("got status %q, note %q; cannot tell is never done", q.Status, q.Note)
	}
	if len(q.Cases) != 1 || q.Cases[0].Status != agree.CannotTell {
		t.Errorf("cases = %+v, want the cannot-tell one", q.Cases)
	}
}

func TestComputeWithoutTeamFlowKeepsRuleText(t *testing.T) {
	note := "no flow of Orders team takes order-placed in and gives payment-request out"
	noFlow := func(id string) agree.Check {
		c := outCheck(id, "shipping_cost", "shippingCost", agree.CannotTell, 4.0, nil)
		c.Note, c.NoFlow = note, true
		return c
	}
	rep := &agree.Report{Checks: []agree.Check{noFlow("gold-domestic"), noFlow("silver-domestic")}}

	rs := []Requirement{
		costItem(StatusOpen, "orders's sheet has `shipping_cost` but its rule differs from the flow's"),
		costItem(StatusDone, ""),
	}
	applyCases(rs, nil, rep)
	if q := rs[0]; q.Status != StatusOpen || !strings.HasPrefix(q.Note, "orders's sheet has") ||
		!strings.Contains(q.Note, "checked by rule text: "+note) || len(q.Cases) != 0 {
		t.Errorf("open by rule text: got %+v", q)
	}
	if q := rs[1]; q.Status != StatusDone || q.Note != "checked by rule text: "+note {
		t.Errorf("done by rule text: got %+v", q)
	}

	// No check carries the row at all: the rule text decides too.
	rs = []Requirement{costItem(StatusDone, "")}
	applyCases(rs, nil, &agree.Report{})
	if q := rs[0]; q.Status != StatusDone || !strings.HasPrefix(q.Note, "checked by rule text: ") {
		t.Errorf("no checks: got %+v", q)
	}
}

func TestUnsentRowJudgedOnDescendants(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, dir)
	pm := analyse(t, dir)
	if pm.Engine == nil {
		t.Fatalf("fixture does not evaluate: %v", pm.Errors)
	}
	item := Requirement{ID: "R-1", Kind: Compute, Team: "Orders team", Subject: "discount_rate", Row: "discount_rate", Status: StatusDone}
	other := outCheck("silver-domestic", "discount", "discountAmount", agree.Differs, 5.0, 9.0)
	other.Team = "Payments team" // another team's check is not evidence
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "discount", "discountAmount", agree.Agrees, 10.0, 10.0),
		outCheck("silver-domestic", "discount", "discountAmount", agree.Differs, 5.0, 9.0),
		other,
	}}
	rs := []Requirement{item}
	applyCases(rs, pm, rep)
	q := rs[0]
	if q.Status != StatusOpen || q.Note != "1 of 2 cases differ; judged on discountAmount, which other rows also feed" {
		t.Fatalf("got status %q, note %q", q.Status, q.Note)
	}
	if len(q.Cases) != 1 || q.Cases[0].Team != "Orders team" {
		t.Errorf("cases = %+v", q.Cases)
	}

	// The nearest fields agree: done, still saying what it was judged on. A
	// row further down (net_amount, fed by discount) is not looked at, so a
	// difference there, say from shipping_cost, does not hold it open.
	rs = []Requirement{item}
	applyCases(rs, pm, &agree.Report{Checks: []agree.Check{
		rep.Checks[0],
		outCheck("silver-domestic", "discount", "discountAmount", agree.Agrees, 5.0, 5.0),
		outCheck("silver-domestic", "net_amount", "netAmount", agree.Differs, 112.0, 100.0),
		outCheck("silver-domestic", "total", "totalAmount", agree.Differs, 121.0, 108.0),
	}})
	if q := rs[0]; q.Status != StatusDone || q.Note != "judged on discountAmount, which other rows also feed" {
		t.Errorf("got status %q, note %q", q.Status, q.Note)
	}

	// With no check on discount, the next rows down are the nearest.
	rs = []Requirement{item}
	applyCases(rs, pm, &agree.Report{Checks: []agree.Check{
		outCheck("silver-domestic", "net_amount", "netAmount", agree.Differs, 112.0, 100.0),
		outCheck("silver-domestic", "customs_value", "customsValue", agree.Agrees, 94.0, 94.0),
		outCheck("silver-domestic", "total", "totalAmount", agree.Differs, 121.0, 108.0),
	}})
	if q := rs[0]; q.Status != StatusOpen || !strings.Contains(q.Note, "judged on netAmount, customsValue,") ||
		strings.Contains(q.Note, "totalAmount") {
		t.Errorf("got status %q, note %q", q.Status, q.Note)
	}
}

// Done needs every check to agree: a status the runner did not name is
// not evidence.
func TestComputeDoneOnlyWhenEveryCaseAgrees(t *testing.T) {
	rs := []Requirement{costItem(StatusDone, "")}
	odd := outCheck("silver-domestic", "shipping_cost", "shippingCost", "skipped", 4.0, nil)
	applyCases(rs, nil, &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Agrees, 0.0, 0.0),
		odd,
	}})
	if q := rs[0]; q.Status != StatusUndocumented || len(q.Cases) != 1 || q.Note == "" {
		t.Errorf("got %+v", q)
	}
}

// The notes of the run reach the items they explain: why a team's flow was
// left out on a rule-text fallback, and why the filled values were not used
// on an item the cases could not tell.
func TestRunNotesReachTheItems(t *testing.T) {
	reg := map[string]sources.Source{"orders": {Alias: "orders"}, "ship": {Alias: "ship", Team: "Shipping team"}}
	rep := &agree.Report{Notes: []string{
		"orders: flow orders left out: end-to-end sheet x has 9 errors; run validate on it",
		"ship: flow ship left out: something else",
		"could not read orders: no network",
	}}
	rs := []Requirement{
		{Kind: Compute, Team: "Orders team", Status: StatusOpen, Note: "rule differs; checked by rule text: no flow of Orders team takes a in and gives b out"},
		{Kind: Compute, Team: "Orders team", Status: StatusUndocumented, Note: "the PM's case has no value for gift", Cases: []agree.Check{{Case: "c"}}},
		{Kind: Compute, Team: "Orders team", Status: StatusDone},
	}
	explainCases(rs, rep, reg, "cases.toml could not be read: bad")
	if n := rs[0].Note; !strings.Contains(n, "orders: flow orders left out") || !strings.Contains(n, "could not read orders: no network") || strings.Contains(n, "ship:") {
		t.Errorf("fallback note: %q", n)
	}
	if n := rs[1].Note; !strings.HasSuffix(n, "; cases.toml could not be read: bad, so the cases ran without its filled values") {
		t.Errorf("undocumented note: %q", n)
	}
	if rs[2].Note != "" {
		t.Errorf("done item: %q", rs[2].Note)
	}
}

func TestSendOpensOnDifferingField(t *testing.T) {
	send := Requirement{ID: "R-2", Kind: Send, Team: "Orders team", Subject: "output@output:shippingCost", Row: "shipping_cost",
		Field: "shippingCost", Role: flow.RoleOutput, Stage: "output", Status: StatusDone}
	elsewhere := outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Differs, 0.0, 1.0)
	elsewhere.Role, elsewhere.Stage = flow.RoleRequest, "shipping-quote" // another boundary
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Agrees, 0.0, 0.0),
		outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.Differs, 4.0, 8.0),
		outCheck("silver-domestic", "net_amount", "netAmount", agree.Differs, 112.0, 100.0),
		elsewhere,
	}}
	rs := []Requirement{send}
	applyCases(rs, nil, rep)
	q := rs[0]
	if q.Status != StatusOpen || q.Note != "1 of 2 cases differ" || len(q.Cases) != 1 || q.Cases[0].Case != "silver-domestic" {
		t.Fatalf("got %+v", q)
	}

	// Agreeing values leave a done Send done; an open one stays as Check left it.
	rs = []Requirement{send, send}
	rs[1].Status, rs[1].Note = StatusOpen, "no row in orders's sheets sends `shippingCost`"
	applyCases(rs, nil, &agree.Report{Checks: rep.Checks[:1]})
	if rs[0].Status != StatusDone || rs[0].Note != "" {
		t.Errorf("agreeing send: %+v", rs[0])
	}
	if rs[1].Status != StatusOpen || rs[1].Note != "no row in orders's sheets sends `shippingCost`" {
		t.Errorf("open send: %+v", rs[1])
	}
}

func TestUndocumentedIsLeftAlone(t *testing.T) {
	q := costItem(StatusUndocumented, "no sheets in orders")
	send := Requirement{ID: "R-2", Kind: Send, Team: "Orders team", Row: "shipping_cost", Field: "shippingCost",
		Role: flow.RoleOutput, Stage: "output", Status: StatusUndocumented, Note: "orders is this repository"}
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.Differs, 4.0, 8.0),
	}}
	rs := []Requirement{q, send}
	applyCases(rs, nil, rep)
	if rs[0].Status != StatusUndocumented || rs[0].Note != "no sheets in orders" || rs[0].Cases != nil {
		t.Errorf("compute: %+v", rs[0])
	}
	if rs[1].Status != StatusUndocumented || rs[1].Note != "orders is this repository" || rs[1].Cases != nil {
		t.Errorf("send: %+v", rs[1])
	}
}

// Receive, Carry and "stop computing" keep what Check decided.
func TestStructuralKindsAreLeftAlone(t *testing.T) {
	rep := &agree.Report{Checks: []agree.Check{
		outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.Differs, 4.0, 8.0),
	}}
	stop := costItem(StatusDone, "")
	stop.Remove = true
	rs := []Requirement{
		stop,
		{Kind: Carry, Team: "Orders team", Row: "shipping_cost", Status: StatusDone},
		{Kind: Receive, Team: "Orders team", Row: "shipping_cost", Field: "shippingCost", Role: flow.RoleOutput, Stage: "output", Status: StatusDone},
	}
	applyCases(rs, nil, rep)
	for _, q := range rs {
		if q.Status != StatusDone || q.Note != "" || q.Cases != nil {
			t.Errorf("%s: %+v", q.Kind, q)
		}
	}
}
