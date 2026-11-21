package agree

import (
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

const pmSheet = "product/specs/shop/sheet.toml"

func TestPMCasesCoverBothTiers(t *testing.T) {
	pm, _ := shop(t, nil)
	cs := pmCases(pm, Options{})
	tiers := map[any]bool{}
	for _, c := range cs {
		tiers[c.Res.Values["tier"]] = true
	}
	if !tiers["Silver"] || !tiers["Standard"] {
		t.Errorf("generated cases miss a tier: %v", tiers)
	}
}

func TestWireValueAtTheCallResponse(t *testing.T) {
	pm, _ := shop(t, nil)
	c := pmCaseWith(t, pm, "member", false, "weight", 2) // fee = weight * 2
	if v, ok := wire(pm, c, boundary(pm, flow.RoleResponse), "fee"); !ok || !sheet.Equal(v, 4) {
		t.Errorf("fee = %v, %v", v, ok)
	}
	if _, ok := wire(pm, c, boundary(pm, flow.RoleOutput), "notAField"); ok {
		t.Error("a field carrying no PM row is not compared")
	}
}

func TestWireValueUsesTheSendMap(t *testing.T) {
	// The PM sends tier to the output as a mapped code, and payment-request
	// gains a tier field: the wire value is the code, not the row's value.
	sheetText := replaceIn(t, pmSheet, `uses    = ["member"]
formula = "member ? \"Silver\" : \"Standard\""
`, `uses    = ["member"]
formula = "member ? \"Silver\" : \"Standard\""

  [row.send]
  field = "tier"
  usage = "R"
  map   = { Silver = "SLV", Standard = "STD" }
`)
	ifaceText := replaceIn(t, "interfaces/payment-request.toml", "[[field]]", `[[field]]
name     = "tier"
type     = "string"
required = true

[[field]]`)
	pm, _ := shop(t, map[string]string{pmSheet: sheetText, "interfaces/payment-request.toml": ifaceText})
	out := boundary(pm, flow.RoleOutput)
	// The generator no longer needs a member case once tier is only a mapped
	// send, so the Silver case is evaluated directly.
	for tier, want := range map[bool]string{true: "SLV", false: "STD"} {
		c := pmCase{ID: "tier", Res: pm.Engine.Evaluate(eval.Input{"member": tier, "weight": float64(2)})}
		if v, ok := wire(pm, c, out, "tier"); !ok || !sheet.Equal(v, want) {
			t.Errorf("member=%v: tier = %v, %v; want %s", tier, v, ok, want)
		}
	}
}

func TestFilledValueStandsInForATextRow(t *testing.T) {
	// shipping_cost becomes a text rule: the engine cannot compute it, so
	// the committed cases.toml has to supply the value.
	sheetText := replaceIn(t, pmSheet, `formula = "tier == \"Silver\" ? shipping_fee / 2 : shipping_fee"`,
		`text    = "Silver members pay half the fee; others pay all of it"`)
	pm, _ := shop(t, map[string]string{pmSheet: sheetText})
	out := boundary(pm, flow.RoleOutput)
	all := pmCases(pm, Options{})
	if len(all) < 2 {
		t.Fatalf("want at least two cases, got %d", len(all))
	}
	filled := &cases.File{Cases: []cases.Case{
		{ID: all[0].ID, Expected: []cases.Field{{Name: "shippingCost", Value: float64(4)}}},
		{ID: all[1].ID, Expected: []cases.Field{{Name: "shippingCost", Value: cases.Unknown}}},
	}}
	cs := pmCases(pm, Options{Filled: filled})
	if v, ok := wire(pm, cs[0], out, "shippingCost"); !ok || !sheet.Equal(v, 4) {
		t.Errorf("filled case: shippingCost = %v, %v; want 4", v, ok)
	}
	if v, ok := wire(pm, cs[1], out, "shippingCost"); ok {
		t.Errorf("a \"?\" value stands in for nothing, got %v", v)
	}
	if v, ok := wire(pm, cs[len(cs)-1], out, "shippingCost"); ok {
		t.Errorf("case with no filled value: got %v", v)
	}
}

func TestWireNeverFallsBackToTheUnmappedRowValue(t *testing.T) {
	// tier is sent to the output only for members, mapped to a code. For a
	// non-member nothing is sent, so there is no wire value to compare: the
	// row's own value "Standard" is not what would travel.
	sheetText := replaceIn(t, pmSheet, `uses    = ["member"]
formula = "member ? \"Silver\" : \"Standard\""
`, `uses    = ["member"]
formula = "member ? \"Silver\" : \"Standard\""

  [row.send]
  field = "tier"
  usage = "S"
  when  = "member"
  otherwise = "do-not-send"
  map   = { Silver = "SLV", Standard = "STD" }
`)
	ifaceText := replaceIn(t, "interfaces/payment-request.toml", "[[field]]", `[[field]]
name     = "tier"
type     = "string"
required = true

[[field]]`)
	pm, _ := shop(t, map[string]string{pmSheet: sheetText, "interfaces/payment-request.toml": ifaceText})
	out := boundary(pm, flow.RoleOutput)
	c := pmCase{ID: "std", Res: pm.Engine.Evaluate(eval.Input{"member": false, "weight": float64(2)})}
	if c.Res.Values["tier"] != "Standard" {
		t.Fatalf("tier = %v", c.Res.Values["tier"])
	}
	if v, ok := wire(pm, c, out, "tier"); ok {
		t.Errorf("unsent tier: got %v, want no value", v)
	}
	c = pmCase{ID: "silver", Res: pm.Engine.Evaluate(eval.Input{"member": true, "weight": float64(2)})}
	if v, ok := wire(pm, c, out, "tier"); !ok || !sheet.Equal(v, "SLV") {
		t.Errorf("sent tier = %v, %v; want SLV", v, ok)
	}
}

func TestWireValueAtTheCallRequest(t *testing.T) {
	// The PM sends weight to the shipping-quote call, mapped so the sent
	// value differs from the row value.
	sheetText := replaceIn(t, pmSheet, `type   = "number"

[[row]]
id      = "tier"`, `type   = "number"

  [[row.send]]
  target = "shipping-quote"
  field  = "weight"
  usage  = "R"
  map    = { "2" = "TWO" }

[[row]]
id      = "tier"`)
	pm, _ := shop(t, map[string]string{pmSheet: sheetText})
	req := boundary(pm, flow.RoleRequest)
	c := pmCaseWith(t, pm, "member", false, "weight", 2)
	if v, ok := wire(pm, c, req, "weight"); !ok || !sheet.Equal(v, c.Res.CallPayloads["shipping-quote"]["weight"]) {
		t.Errorf("weight at the request = %v, %v; call payload %v", v, ok, c.Res.CallPayloads)
	}
}
