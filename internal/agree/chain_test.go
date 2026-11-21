package agree

import (
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

const (
	ordersSheet   = "orders/specs/orders/sheet.toml"
	shippingSheet = "shipping/specs/ship/sheet.toml"
	cartSheet     = "cart/specs/cart/sheet.toml"
)

var modes = []Mode{Isolated, EndToEnd}

// countBy counts the checks of a team with the field and status; an empty
// field matches any field.
func countBy(r *Report, team, field, status string) int {
	n := 0
	for _, c := range r.Checks {
		if c.Team == team && (field == "" || c.Field == field) && c.Status == status {
			n++
		}
	}
	return n
}

// ordersWith returns the Orders sheet with the shipping_cost formula
// replaced.
func ordersWith(t *testing.T, formula string) string {
	t.Helper()
	return replaceIn(t, ordersSheet, `formula = "tier == \"Silver\" ? fee / 2 : fee"`,
		`formula = "`+strings.ReplaceAll(formula, `"`, `\"`)+`"`)
}

// tierOf maps each PM case id to its tier.
func tierOf(pm *flow.Analysis) map[string]any {
	m := map[string]any{}
	for _, c := range pmCases(pm, Options{}) {
		m[c.ID] = c.Res.Values["tier"]
	}
	return m
}

// rejecting returns the ids of the PM cases that reject.
func rejecting(pm *flow.Analysis) map[string]bool {
	m := map[string]bool{}
	for _, c := range pmCases(pm, Options{}) {
		if c.Res.Reject != nil {
			m[c.ID] = true
		}
	}
	return m
}

func TestShopAgrees(t *testing.T) {
	for _, mode := range modes {
		pm, teams := shop(t, nil)
		r := Run(pm, teams, Options{Mode: mode})
		for _, c := range r.Checks {
			if c.Status != Agrees {
				t.Errorf("mode %v: %+v", mode, c)
			}
		}
		if countBy(r, "Orders team", "shippingCost", Agrees) == 0 || countBy(r, "Shipping team", "fee", Agrees) == 0 {
			t.Errorf("mode %v: missing checks: %+v", mode, r.Checks)
		}
		if countBy(r, "Cart team", "tier", Agrees) == 0 || countBy(r, "Shipping team", "reject", Agrees) == 0 {
			t.Errorf("mode %v: no Cart tier or Shipping reject check: %+v", mode, r.Checks)
		}
		if r.Cases == 0 || r.Mode != mode || r.Flow != "shop" {
			t.Errorf("mode %v: report header = %q %v %d", mode, r.Flow, r.Mode, r.Cases)
		}
	}
}

func TestLowercaseSilverDiffersOnOrders(t *testing.T) {
	pm, teams := shop(t, map[string]string{ordersSheet: ordersWith(t, `tier == "silver" ? fee / 2 : fee`)})
	r := Run(pm, teams, Options{Mode: Isolated})
	if countBy(r, "Orders team", "shippingCost", Differs) == 0 {
		t.Fatalf("want a differing shippingCost: %+v", r.Checks)
	}
	if countBy(r, "Cart team", "", Differs)+countBy(r, "Shipping team", "", Differs) != 0 {
		t.Error("only Orders is wrong")
	}
	tiers := tierOf(pm)
	for _, c := range r.Checks {
		if c.Team == "Orders team" && c.Field == "shippingCost" && (c.Status == Differs) != (tiers[c.Case] == "Silver") {
			t.Errorf("only Silver cases differ: %+v", c)
		}
	}
}

func TestSplitAgainstABindingRow(t *testing.T) {
	// Shipping halves the fee for Silver and Orders passes it on: the
	// output agrees, but the PM's shipping_fee, carried on quote-response,
	// is binding (D-23), so Shipping's fee differs on Silver cases.
	quote := replaceIn(t, "interfaces/quote-request.toml", "[[field]]", `[[field]]
name     = "tier"
type     = "string"
required = true

[[field]]`)
	orders := replaceIn(t, ordersSheet, `id     = "tier"
name   = "Tier"
source = "request.tier"
type   = "string"
`, `id     = "tier"
name   = "Tier"
source = "request.tier"
type   = "string"

  [[row.send]]
  target = "quote"
  field  = "tier"
  usage  = "R"
`)
	orders = strings.Replace(orders, `uses    = ["tier", "fee"]
formula = "tier == \"Silver\" ? fee / 2 : fee"`, `uses    = ["fee"]
formula = "fee"`, 1)
	ship := replaceIn(t, shippingSheet, `[[row]]
id      = "fee"
name    = "Fee"
uses    = ["weight"]
formula = "weight * 2"`, `[[row]]
id     = "tier"
name   = "Tier"
source = "request.tier"
type   = "string"

[[row]]
id      = "fee"
name    = "Fee"
uses    = ["tier", "weight"]
formula = "tier == \"Silver\" ? weight : weight * 2"`)
	for _, mode := range modes {
		pm, teams := shop(t, map[string]string{"interfaces/quote-request.toml": quote, ordersSheet: orders, shippingSheet: ship})
		r := Run(pm, teams, Options{Mode: mode})
		tiers := tierOf(pm)
		if countBy(r, "Shipping team", "fee", Differs) == 0 {
			t.Errorf("mode %v: want Shipping's fee to differ: %+v", mode, r.Checks)
		}
		for _, c := range r.Checks {
			if c.Team == "Shipping team" && c.Field == "fee" && (c.Status == Differs) != (tiers[c.Case] == "Silver") {
				t.Errorf("mode %v: Shipping's fee differs on Silver cases only: %+v", mode, c)
			}
		}
		if countBy(r, "Orders team", "shippingCost", Agrees) == 0 || countBy(r, "Orders team", "", Differs)+countBy(r, "Orders team", "", CannotTell) != 0 {
			t.Errorf("mode %v: Orders agrees everywhere: %+v", mode, r.Checks)
		}
		if countBy(r, "Orders team", "tier", Agrees) == 0 {
			t.Errorf("mode %v: Orders' tier on the quote request is compared", mode)
		}
	}
}

func TestTeamStageIdDiffers(t *testing.T) {
	for _, mode := range modes {
		pm, teams := shop(t, nil)
		r := Run(pm, teams, Options{Mode: mode})
		n := 0
		for _, c := range r.Checks {
			if c.Role == flow.RoleRequest && c.Field == "weight" {
				n++
				if c.Team != "Orders team" || c.Stage != "shipping-quote" || c.Iface != "quote-request" || c.Status != Agrees || c.Row != "weight" {
					t.Errorf("mode %v: %+v", mode, c)
				}
			}
		}
		if n != r.Cases {
			t.Errorf("mode %v: %d request checks for %d cases", mode, n, r.Cases)
		}
	}
}

func TestRejectMustMatch(t *testing.T) {
	ship := replaceIn(t, shippingSheet, `code    = "TOO_HEAVY"`, `code    = "HEAVY"`)
	for _, mode := range modes {
		pm, teams := shop(t, map[string]string{shippingSheet: ship})
		rej := rejecting(pm)
		if len(rej) == 0 {
			t.Fatal("the fixture has no rejecting case")
		}
		r := Run(pm, teams, Options{Mode: mode})
		for id := range rej {
			var rejects []Check
			for _, c := range r.Checks {
				if c.Case != id {
					continue
				}
				if c.Field == "reject" {
					rejects = append(rejects, c)
				}
				if c.Field == "shippingCost" || c.Field == "fee" {
					t.Errorf("mode %v: no field after the reject is compared: %+v", mode, c)
				}
			}
			if len(rejects) != 1 {
				t.Fatalf("mode %v case %s: want one reject check, got %+v", mode, id, rejects)
			}
			c := rejects[0]
			if c.Team != "Shipping team" || c.Status != Differs || c.Expected != "TOO_HEAVY" || c.Actual != "HEAVY" || c.Row != "shipping_fee" {
				t.Errorf("mode %v: %+v", mode, c)
			}
		}
		for _, c := range r.Checks {
			if c.Field != "reject" && c.Status != Agrees {
				t.Errorf("mode %v: only the reject differs: %+v", mode, c)
			}
		}
	}
}

func TestTeamRejectThePMDoesNotMake(t *testing.T) {
	// Orders rejects heavy orders itself, before calling Shipping. In
	// isolated mode the reject belongs to Shipping, the team the PM places
	// it with; end to end, the chain rejects with the same code and agrees.
	orders := replaceIn(t, ordersSheet, `source = "request.weight"
type   = "number"
`, `source = "request.weight"
type   = "number"

  [[row.reject]]
  when    = "weight > 30"
  code    = "TOO_HEAVY"
  message = "Too heavy to ship"
`)
	pm, teams := shop(t, map[string]string{ordersSheet: orders})
	rej := rejecting(pm)
	iso := Run(pm, teams, Options{Mode: Isolated})
	if n := countBy(iso, "Orders team", "reject", Differs); n != len(rej) {
		t.Errorf("isolated: want %d Orders reject differs, got %d: %+v", len(rej), n, iso.Checks)
	}
	for _, c := range iso.Checks {
		if c.Team == "Orders team" && c.Field == "reject" && (c.Expected != "(none)" || c.Actual != "TOO_HEAVY") {
			t.Errorf("isolated: %+v", c)
		}
	}
	if countBy(iso, "Shipping team", "reject", Agrees) != len(rej) {
		t.Errorf("isolated: Shipping still rejects: %+v", iso.Checks)
	}
	e2e := Run(pm, teams, Options{Mode: EndToEnd})
	if countBy(e2e, "Orders team", "reject", Agrees) != len(rej) || countBy(e2e, "Shipping team", "reject", Agrees) != 0 {
		t.Errorf("end to end: the chain's reject agrees once, at Orders: %+v", e2e.Checks)
	}
	for _, c := range e2e.Checks {
		if c.Status != Agrees {
			t.Errorf("end to end: %+v", c)
		}
	}
}

func TestNumbersCompareByValue(t *testing.T) {
	// Orders writes fee * 1.0, a double. The PM's shipping_cost is a text
	// rule whose values come from cases.toml as TOML integers: 4 and 4.0
	// agree by value.
	pmText := replaceIn(t, pmSheet, `formula = "tier == \"Silver\" ? shipping_fee / 2 : shipping_fee"`,
		`text    = "Silver members pay half the fee; others pay all of it"`)
	pm, teams := shop(t, map[string]string{pmSheet: pmText, ordersSheet: ordersWith(t, `tier == "Silver" ? fee / 2 : fee * 1.0`)})
	filled := &cases.File{}
	for _, c := range pmCases(pm, Options{}) {
		cost := int64(c.Res.Values["weight"].(float64)) * 2
		if c.Res.Values["tier"] == "Silver" {
			cost /= 2
		}
		filled.Cases = append(filled.Cases, cases.Case{ID: c.ID, Expected: []cases.Field{{Name: "shippingCost", Value: cost}}})
	}
	for _, mode := range modes {
		r := Run(pm, teams, Options{Mode: mode, Filled: filled})
		for _, c := range r.Checks {
			if c.Status != Agrees {
				t.Errorf("mode %v: %+v", mode, c)
			}
			if _, isInt := c.Expected.(int64); c.Field == "shippingCost" && !isInt {
				t.Errorf("mode %v: the expected value is the filled integer: %+v", mode, c)
			}
		}
		if countBy(r, "Orders team", "shippingCost", Agrees) == 0 {
			t.Errorf("mode %v: missing checks: %+v", mode, r.Checks)
		}
	}
}

func TestDottedFieldIsFedAndCompared(t *testing.T) {
	patch := map[string]string{
		"interfaces/order-placed.toml": replaceIn(t, "interfaces/order-placed.toml", `name     = "tier"`, `name     = "order.tier"`),
		cartSheet:                      replaceIn(t, cartSheet, `field = "tier"`, `field = "order.tier"`),
		ordersSheet:                    replaceIn(t, ordersSheet, `source = "request.tier"`, `source = "request.order.tier"`),
	}
	for _, mode := range modes {
		pm, teams := shop(t, patch)
		r := Run(pm, teams, Options{Mode: mode})
		for _, c := range r.Checks {
			if c.Status != Agrees {
				t.Errorf("mode %v: %+v", mode, c)
			}
		}
		if countBy(r, "Cart team", "order.tier", Agrees) == 0 || countBy(r, "Orders team", "shippingCost", Agrees) == 0 {
			t.Errorf("mode %v: missing checks: %+v", mode, r.Checks)
		}
	}
}

func TestNoTeamFlowIsCannotTell(t *testing.T) {
	for _, mode := range modes {
		pm, all := shop(t, nil)
		calls := map[string]int{}
		teams := func(team string) ([]*flow.Analysis, []string) {
			calls[team]++
			if team == "Cart team" {
				return nil, []string{"Cart team: no repository"}
			}
			fl, notes := all(team)
			return fl, append(notes, "read every flow")
		}
		r := Run(pm, teams, Options{Mode: mode})
		if countBy(r, "Cart team", "", CannotTell) == 0 || countBy(r, "Cart team", "", Agrees)+countBy(r, "Cart team", "", Differs) != 0 {
			t.Errorf("mode %v: Cart's checks are cannot tell: %+v", mode, r.Checks)
		}
		for _, c := range r.Checks {
			if c.Team == "Cart team" && !strings.Contains(c.Note, "order-placed") {
				t.Errorf("mode %v: note names the boundary: %+v", mode, c)
			}
			// Only Cart's own checks are cannot tell for want of a flow;
			// end to end, Orders cannot tell because Cart sent nothing.
			if c.NoFlow != (c.Team == "Cart team") {
				t.Errorf("mode %v: NoFlow marks the team without a flow only: %+v", mode, c)
			}
		}
		if mode == Isolated && countBy(r, "Orders team", "shippingCost", Agrees) == 0 {
			t.Errorf("isolated: Orders is judged on its own: %+v", r.Checks)
		}
		if mode == EndToEnd && (countBy(r, "Orders team", "shippingCost", CannotTell) == 0 || countBy(r, "Orders team", "", Agrees) != 0) {
			t.Errorf("end to end: nothing reaches Orders: %+v", r.Checks)
		}
		if len(r.Notes) != 2 || r.Notes[0] != "Cart team: no repository" || r.Notes[1] != "read every flow" {
			t.Errorf("mode %v: notes once each: %q", mode, r.Notes)
		}
		if calls["Cart team"] != 1 || calls["Orders team"] != 1 || calls["Shipping team"] != 1 {
			t.Errorf("mode %v: one call per segment: %v", mode, calls)
		}
	}
}

func TestTextRuleIsCannotTell(t *testing.T) {
	orders := replaceIn(t, ordersSheet, `formula = "tier == \"Silver\" ? fee / 2 : fee"`,
		`text    = "Silver members pay half the fee; others pay all of it"`)
	for _, mode := range modes {
		pm, teams := shop(t, map[string]string{ordersSheet: orders})
		r := Run(pm, teams, Options{Mode: mode})
		if countBy(r, "Orders team", "shippingCost", CannotTell) == 0 {
			t.Fatalf("mode %v: want cannot tell: %+v", mode, r.Checks)
		}
		for _, c := range r.Checks {
			if c.Field == "shippingCost" && (c.Status != CannotTell || !strings.Contains(c.Note, "text rule")) {
				t.Errorf("mode %v: %+v", mode, c)
			}
		}
	}
}

func TestEndToEndPassesTheChainAlong(t *testing.T) {
	cart := replaceIn(t, cartSheet, `formula = "member ? \"Silver\" : \"Standard\""`, `formula = "member ? \"Standard\" : \"Standard\""`)
	pm, teams := shop(t, map[string]string{cartSheet: cart})
	e2e := Run(pm, teams, Options{Mode: EndToEnd})
	if countBy(e2e, "Cart team", "tier", Differs) == 0 || countBy(e2e, "Orders team", "shippingCost", Differs) == 0 {
		t.Errorf("end to end: Cart's tier and Orders' cost differ: %+v", e2e.Checks)
	}
	iso := Run(pm, teams, Options{Mode: Isolated})
	if countBy(iso, "Cart team", "tier", Differs) == 0 || countBy(iso, "Orders team", "", Differs)+countBy(iso, "Shipping team", "", Differs) != 0 {
		t.Errorf("isolated: only Cart differs: %+v", iso.Checks)
	}
}

func TestMissingInputNamesTheField(t *testing.T) {
	orders := replaceIn(t, ordersSheet, `[[row]]
id     = "fee"`, `[[row]]
id     = "discount"
name   = "Discount"
source = "request.discount"
type   = "number"

[[row]]
id     = "fee"`)
	orders = strings.Replace(orders, `uses    = ["tier", "fee"]
formula = "tier == \"Silver\" ? fee / 2 : fee"`, `uses    = ["tier", "fee", "discount"]
formula = "(tier == \"Silver\" ? fee / 2 : fee) - discount"`, 1)
	pm, teams := shop(t, map[string]string{ordersSheet: orders})
	r := Run(pm, teams, Options{Mode: Isolated})
	if countBy(r, "Orders team", "reject", Differs) != r.Cases {
		t.Fatalf("want an Orders reject in every case: %+v", r.Checks)
	}
	for _, c := range r.Checks {
		if c.Team == "Orders team" && c.Field == "reject" &&
			(c.Actual != "MISSING_INPUT" || c.Note != "reads request.discount, which order-placed does not carry") {
			t.Errorf("%+v", c)
		}
	}
}

func TestCallValuesFollowTheResponse(t *testing.T) {
	// A response value the callee cannot compute leaves the caller's
	// fields that use it cannot tell, naming the reason.
	ship := replaceIn(t, shippingSheet, `formula = "weight * 2"`, `text    = "twice the weight"`)
	pm, teams := shop(t, map[string]string{shippingSheet: ship})
	for _, mode := range modes {
		r := Run(pm, teams, Options{Mode: mode})
		if countBy(r, "Orders team", "shippingCost", CannotTell) == 0 || countBy(r, "Shipping team", "fee", CannotTell) == 0 {
			t.Fatalf("mode %v: %+v", mode, r.Checks)
		}
		for _, c := range r.Checks {
			if c.Field == "shippingCost" && !strings.Contains(c.Note, "text rule") {
				t.Errorf("mode %v: the note follows the response: %+v", mode, c)
			}
			if c.Status == Differs {
				t.Errorf("mode %v: %+v", mode, c)
			}
		}
	}
}

func TestExpectedValuesAreWireValues(t *testing.T) {
	pm, teams := shop(t, nil)
	r := Run(pm, teams, Options{Mode: Isolated})
	for _, c := range r.Checks {
		if c.Team == "Shipping team" && c.Field == "fee" {
			if !sheet.Equal(c.Expected, c.Actual) {
				t.Errorf("%+v", c)
			}
			if c.Role != flow.RoleResponse || c.Row != "shipping_fee" {
				t.Errorf("%+v", c)
			}
		}
	}
}

// rejectChecks maps each case to its reject checks.
func rejectChecks(r *Report) map[string][]Check {
	m := map[string][]Check{}
	for _, c := range r.Checks {
		if c.Field == "reject" {
			m[c.Case] = append(m[c.Case], c)
		}
	}
	return m
}

// pmReject is the TOO_HEAVY reject block of the PM sheet and of
// Shipping's sheet, for moving or removing it.
const pmReject = `

  [[row.reject]]
  when    = "weight > 30"
  code    = "TOO_HEAVY"
  message = "Too heavy to ship"
`

func TestEndToEndRejectPlacedEarlierAgrees(t *testing.T) {
	// Probe A: the PM rejects on the input row weight (step 0, Cart); the
	// teams' chain still rejects at Shipping with the same code.
	pmText := replaceIn(t, pmSheet, pmReject, "\n")
	pmText = strings.Replace(pmText, `source = "request.weight"
type   = "number"
`, `source = "request.weight"
type   = "number"
`+strings.TrimPrefix(pmReject, "\n"), 1)
	pm, teams := shop(t, map[string]string{pmSheet: pmText})
	rej := rejecting(pm)
	if len(rej) == 0 {
		t.Fatal("no rejecting case")
	}
	for _, c := range pmCases(pm, Options{}) {
		if c.Res.Reject != nil && (c.Res.Reject.Row != "weight" || pm.Place[c.Res.Reject.Row] != 0) {
			t.Fatalf("the PM's reject is at %+v", c.Res.Reject)
		}
	}
	e2e := Run(pm, teams, Options{Mode: EndToEnd})
	byCase := rejectChecks(e2e)
	for id := range rej {
		got := byCase[id]
		if len(got) != 1 || got[0].Team != "Shipping team" || got[0].Status != Agrees || got[0].Expected != "TOO_HEAVY" || got[0].Row != "weight" ||
			!strings.Contains(got[0].Note, "Cart team") {
			t.Errorf("end to end, case %s: %+v", id, got)
		}
	}
	for _, c := range e2e.Checks {
		if c.Status != Agrees {
			t.Errorf("end to end: %+v", c)
		}
		if rej[c.Case] && c.Field != "reject" && c.Boundary > 0 {
			t.Errorf("end to end: no field after the PM's reject: %+v", c)
		}
	}
	// Isolated, the reject belongs to Cart, which does not make it.
	iso := Run(pm, teams, Options{Mode: Isolated})
	for id := range rej {
		got := rejectChecks(iso)[id]
		if len(got) != 1 || got[0].Team != "Cart team" || got[0].Status != Differs || got[0].Actual != none {
			t.Errorf("isolated, case %s: %+v", id, got)
		}
	}
}

func TestEndToEndCalleeRejectIsJudged(t *testing.T) {
	// Probe B: the PM rejects on parcel_weight, which Orders computes
	// before the call (step 1); the teams' chain rejects inside the call,
	// at Shipping, with the same code.
	pmText := replaceIn(t, pmSheet, `[[row]]
id      = "shipping_fee"
name    = "Shipping fee"
uses    = ["weight"]
formula = "weight * 2"
`+strings.TrimPrefix(pmReject, "\n"), `[[row]]
id      = "parcel_weight"
name    = "Parcel weight"
uses    = ["weight"]
formula = "weight"
`+strings.TrimPrefix(pmReject, "\n")+`
[[row]]
id      = "shipping_fee"
name    = "Shipping fee"
uses    = ["parcel_weight"]
formula = "parcel_weight * 2"
`)
	flowText := replaceIn(t, "product/flows/shop/flow.toml", `callee_computes = ["shipping_fee"]`, `computes = ["parcel_weight"]
callee_computes = ["shipping_fee"]`)
	flowText = strings.Replace(flowText, `interface = "../../../interfaces/quote-request.toml"`, `interface = "../../../interfaces/quote-request.toml"
  carries   = { weight = "parcel_weight" }`, 1)
	pm, teams := shop(t, map[string]string{pmSheet: pmText, "product/flows/shop/flow.toml": flowText})
	rej := rejecting(pm)
	if len(rej) == 0 {
		t.Fatal("no rejecting case")
	}
	for _, c := range pmCases(pm, Options{}) {
		if c.Res.Reject != nil && pm.Place[c.Res.Reject.Row] != 1 {
			t.Fatalf("the PM's reject %+v is placed at %d", c.Res.Reject, pm.Place[c.Res.Reject.Row])
		}
	}
	e2e := Run(pm, teams, Options{Mode: EndToEnd})
	byCase := rejectChecks(e2e)
	for id := range rej {
		got := byCase[id]
		if len(got) != 1 || got[0].Team != "Shipping team" || got[0].Status != Agrees || got[0].Row != "parcel_weight" {
			t.Errorf("end to end, case %s: %+v", id, got)
		}
	}
	for _, c := range e2e.Checks {
		if c.Status != Agrees {
			t.Errorf("end to end: %+v", c)
		}
	}
}

func TestEndToEndRejectWithAnotherCodeExpectsThePMs(t *testing.T) {
	// Orders rejects heavy orders itself with its own code; the PM rejects
	// later, at Shipping, with TOO_HEAVY.
	orders := replaceIn(t, ordersSheet, `source = "request.weight"
type   = "number"
`, `source = "request.weight"
type   = "number"

  [[row.reject]]
  when    = "weight > 30"
  code    = "HEAVY_ORDER"
  message = "Too heavy"
`)
	pm, teams := shop(t, map[string]string{ordersSheet: orders})
	rej := rejecting(pm)
	byCase := rejectChecks(Run(pm, teams, Options{Mode: EndToEnd}))
	for id := range rej {
		got := byCase[id]
		if len(got) != 1 || got[0].Team != "Orders team" || got[0].Status != Differs || got[0].Expected != "TOO_HEAVY" || got[0].Actual != "HEAVY_ORDER" {
			t.Errorf("case %s: %+v", id, got)
		}
	}
}

func TestEndToEndUnrejectedCaseIsOneCheck(t *testing.T) {
	// Shipping no longer rejects: the chain never does, so the PM's reject
	// differs once, at the team it is placed with.
	ship := replaceIn(t, shippingSheet, pmReject, "\n")
	pm, teams := shop(t, map[string]string{shippingSheet: ship})
	rej := rejecting(pm)
	byCase := rejectChecks(Run(pm, teams, Options{Mode: EndToEnd}))
	for id := range rej {
		got := byCase[id]
		if len(got) != 1 || got[0].Team != "Shipping team" || got[0].Status != Differs || got[0].Expected != "TOO_HEAVY" || got[0].Actual != none {
			t.Errorf("case %s: %+v", id, got)
		}
	}
}
