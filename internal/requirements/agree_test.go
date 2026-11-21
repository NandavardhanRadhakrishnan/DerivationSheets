package requirements

import (
	"os"
	"path/filepath"
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
// on an item the cases could not tell. The notes are agree's own, from the
// registry: Orders' repository cannot be read offline, and Shipping has
// none.
func TestRunNotesReachTheItems(t *testing.T) {
	pm, _ := shopFlows(t)
	reg := map[string]sources.Source{"orders": {Alias: "orders", Git: "https://example.invalid/orders.git", Team: "Orders team"}}
	fe := sources.NewFetcher(t.TempDir())
	fe.Offline = true
	rep := agree.Run(pm, agree.RegistryTeams(reg, fe, ""), agree.Options{Mode: agree.Isolated})
	ordersNotes := rep.TeamNotes["Orders team"]
	if len(ordersNotes) != 1 || !strings.HasPrefix(ordersNotes[0], "could not read orders: ") {
		t.Fatalf("Orders' notes: %q", ordersNotes)
	}
	rs := []Requirement{
		{Kind: Compute, Team: "Orders team", Status: StatusOpen, Note: "rule differs; checked by rule text: no flow of Orders team takes a in and gives b out"},
		{Kind: Compute, Team: "Orders team", Status: StatusUndocumented, Note: "the PM's case has no value for gift", Cases: []agree.Check{{Case: "c"}}},
		{Kind: Compute, Team: "Orders team", Status: StatusDone},
	}
	explainCases(rs, rep, "cases.toml could not be read: bad")
	if n := rs[0].Note; !strings.Contains(n, ordersNotes[0]) || strings.Contains(n, "Shipping team") || strings.Contains(n, "Cart team") {
		t.Errorf("fallback note: %q", n)
	}
	ship := []Requirement{{Kind: Compute, Team: "Shipping team", Status: StatusDone, Note: "checked by rule text: no flow"}}
	explainCases(ship, rep, "")
	if n := ship[0].Note; !strings.Contains(n, "no repository for Shipping team in the registry") || strings.Contains(n, "orders") {
		t.Errorf("Shipping's fallback note: %q", n)
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

// shopEdit replaces old by new in a file of the agree package's shop
// fixture: path, old, new.
type shopEdit [3]string

// shopCases runs the PM's cases of the shop fixture, with the edits,
// through the teams' sheets in isolated mode, as Run does.
func shopCases(t *testing.T, edits ...shopEdit) (*flow.Analysis, *agree.Report) {
	t.Helper()
	pm, teams := shopFlows(t, edits...)
	return pm, agree.Run(pm, teams, agree.Options{Mode: agree.Isolated})
}

// shopFlows copies the agree package's shop fixture (a PM flow over Cart,
// Orders and a Shipping callee, each team with its own flow), applies the
// edits, and returns the PM's flow and the teams' own flows.
func shopFlows(t *testing.T, edits ...shopEdit) (*flow.Analysis, agree.TeamFlows) {
	t.Helper()
	src := filepath.Join("..", "agree", "testdata", "shop")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edits {
		edit(t, dst, e[0], e[1], e[2])
	}
	load := func(dir string) *flow.Analysis {
		f, err := flow.Load(filepath.Join(dst, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatal(err)
		}
		return flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))
	}
	pm := load("product/flows/shop")
	if len(pm.Errors) > 0 || pm.Engine == nil {
		t.Fatalf("PM flow: %v", pm.Errors)
	}
	dirs := map[string]string{"Cart team": "cart/flows/cart", "Orders team": "orders/flows/orders", "Shipping team": "shipping/flows/ship"}
	teams := func(team string) ([]*flow.Analysis, []string) {
		a := load(dirs[team])
		if len(a.Errors) > 0 { // as agree.RegistryTeams leaves it out
			return nil, []string{a.Errors[0].Message}
		}
		return []*flow.Analysis{a}, nil
	}
	return pm, teams
}

// shopItems are Compute items of the shop flow, as Check closed them on
// the rule text.
func shopItems() []Requirement {
	return []Requirement{
		{ID: "R-fee", Kind: Compute, Team: "Shipping team", Subject: "shipping_fee", Row: "shipping_fee", Status: StatusDone},
		{ID: "R-cost", Kind: Compute, Team: "Orders team", Subject: "shipping_cost", Row: "shipping_cost", Status: StatusDone},
	}
}

func itemOf(rs []Requirement, id string) Requirement {
	for _, q := range rs {
		if q.ID == id {
			return q
		}
	}
	return Requirement{}
}

// A team, or a callee, that rejects where the PM's case does not never
// leaves an item done: the fields it did not send are judged.
func TestUnexpectedRejectIsNeverDone(t *testing.T) {
	const (
		ordersSheet = "orders/specs/orders/sheet.toml"
		shipSheet   = "shipping/specs/ship/sheet.toml"
	)
	t.Run("the callee rejects earlier than the PM", func(t *testing.T) {
		pm, rep := shopCases(t, shopEdit{shipSheet, `when    = "weight > 30"`, `when    = "weight > 3"`})
		rs := shopItems()
		applyCases(rs, pm, rep)
		fee := itemOf(rs, "R-fee")
		if fee.Status != StatusOpen || len(fee.Cases) == 0 {
			t.Errorf("Shipping's fee: got %+v", fee)
		}
		for _, c := range fee.Cases {
			if c.Status != agree.Differs || c.Row != "shipping_fee" || !strings.Contains(c.Note, "TOO_HEAVY") {
				t.Errorf("Shipping's fee case: %+v", c)
			}
		}
		if cost := itemOf(rs, "R-cost"); cost.Status == StatusDone {
			t.Errorf("Orders' cost rests on the rejecting call: got %+v", cost)
		}
	})
	t.Run("the team reads a field its boundary does not carry", func(t *testing.T) {
		pm, rep := shopCases(t, shopEdit{ordersSheet, `source = "request.tier"`, `source = "request.tierCode"`})
		rs := shopItems()
		applyCases(rs, pm, rep)
		cost := itemOf(rs, "R-cost")
		if cost.Status != StatusOpen || len(cost.Cases) == 0 {
			t.Fatalf("Orders' cost: got %+v", cost)
		}
		for _, c := range cost.Cases {
			if c.Status != agree.Differs || c.Note != "reads request.tierCode, which order-placed does not carry" {
				t.Errorf("Orders' cost case: %+v", c)
			}
		}
	})
	t.Run("the callee rejects inside the caller's run", func(t *testing.T) {
		pm, rep := shopCases(t, shopEdit{ordersSheet, `  [[row.send]]
  target = "quote"
  field  = "weight"
  usage  = "R"`, `[[row]]
id      = "quote_weight"
name    = "Quote weight"
uses    = ["tier", "weight"]
formula = "weight"

  [[row.send]]
  target    = "quote"
  field     = "weight"
  usage     = "S"
  when      = "tier == \"Silver\""
  otherwise = "do-not-send"`})
		rs := shopItems()
		applyCases(rs, pm, rep)
		if cost := itemOf(rs, "R-cost"); cost.Status == StatusDone {
			t.Errorf("Orders' cost: got %+v", cost)
		}
	})
}

// A team with no own flow for some cases, and checks that agree on others,
// is not done: the cases its sheet was not run for are not evidence.
func TestNoFlowBesideAgreeingChecksIsUndocumented(t *testing.T) {
	noFlow := outCheck("silver-domestic", "shipping_cost", "shippingCost", agree.CannotTell, 4.0, nil)
	noFlow.Note, noFlow.NoFlow = "no flow of Orders team takes order-placed in and gives payment-request out", true
	rs := []Requirement{costItem(StatusDone, "")}
	applyCases(rs, nil, &agree.Report{Checks: []agree.Check{
		outCheck("gold-domestic", "shipping_cost", "shippingCost", agree.Agrees, 0.0, 0.0),
		noFlow,
	}})
	if q := rs[0]; q.Status != StatusUndocumented || q.Note != noFlow.Note || len(q.Cases) != 1 || !q.Cases[0].NoFlow {
		t.Errorf("got %+v", q)
	}
}

// A reject check carries the PM's reject row when the reject is placed in
// the team's segment, so it judges that row's Compute item directly: a
// reject with another code opens it.
func TestDifferingRejectOpensTheRowsItem(t *testing.T) {
	pm, rep := shopCases(t, shopEdit{"shipping/specs/ship/sheet.toml", `code    = "TOO_HEAVY"`, `code    = "HEAVY"`})
	rs := shopItems()
	applyCases(rs, pm, rep)
	fee := itemOf(rs, "R-fee")
	if fee.Status != StatusOpen || len(fee.Cases) == 0 {
		t.Fatalf("got %+v", fee)
	}
	for _, c := range fee.Cases {
		if c.Field != "reject" || c.Row != "shipping_fee" || c.Expected != "TOO_HEAVY" || c.Actual != "HEAVY" {
			t.Errorf("case %+v", c)
		}
	}
	if cost := itemOf(rs, "R-cost"); cost.Status != StatusDone {
		t.Errorf("Orders' cost is not past the reject: %+v", cost)
	}
}
