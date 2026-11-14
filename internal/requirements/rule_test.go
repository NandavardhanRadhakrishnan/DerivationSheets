package requirements

import (
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// ruleOf parses a one-sheet fixture and returns RuleText of the named row.
func ruleOf(t *testing.T, text, id string) string {
	t.Helper()
	s, err := sheet.Parse(".", "sheet.toml", "sheet = \"x\"\nversion = 1\n"+text)
	if err != nil {
		t.Fatal(err)
	}
	r := s.ByID[id]
	if r == nil {
		t.Fatalf("no row %q", id)
	}
	return RuleText(r)
}

// ruleKeyOf is RuleKey of the named row of a one-sheet fixture.
func ruleKeyOf(t *testing.T, text, id string) string {
	t.Helper()
	s, err := sheet.Parse(".", "sheet.toml", "sheet = \"x\"\nversion = 1\n"+text)
	if err != nil {
		t.Fatal(err)
	}
	return RuleKey(s.ByID[id])
}

const inputs = `
[[row]]
id = "subtotal"
source = "request.subtotal"
type = "number"

[[row]]
id = "loyalty_tier"
source = "request.tier"
type = "string"
`

func TestRuleTextFormula(t *testing.T) {
	got := ruleOf(t, inputs+`
[[row]]
id = "fee"
uses = ["subtotal"]
formula = "subtotal   *  2"
`, "fee")
	if got != "formula: subtotal * 2" {
		t.Errorf("got %q", got)
	}
}

func TestRuleTextTable(t *testing.T) {
	body := inputs + `
[[row]]
id = "free_shipping"
uses = ["subtotal", "loyalty_tier"]

  [row.table]
  hit_policy = "%s"
  columns = ["subtotal", "loyalty_tier", "=>"]
  rows = [
    [">= 100", "-",    true],
    ["-",      "Gold", true],
    ["-",      "-",    false],
  ]
`
	want := "table, first match:\n" +
		"  subtotal >= 100, loyalty_tier any -> true\n" +
		"  subtotal any, loyalty_tier Gold -> true\n" +
		"  subtotal any, loyalty_tier any -> false"
	if got := ruleOf(t, strings.Replace(body, "%s", "first", 1), "free_shipping"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := ruleOf(t, strings.Replace(body, "%s", "unique", 1), "free_shipping"); !strings.HasPrefix(got, "table, unique:\n") {
		t.Errorf("got %q", got)
	}
}

func TestRuleTextLookupAndRejects(t *testing.T) {
	got := ruleOf(t, inputs+`
[[row]]
id = "zone"
uses = ["loyalty_tier"]
lookup = { from = "db.shipping_zones", key = ["loyalty_tier"] }
values_file = "data/shipping_zones.csv"

  [[row.reject]]
  when = "zone == \"NONE\""
  code = "NOT_SERVED"
  message = "no"
`, "zone")
	want := "lookup: from db.shipping_zones, key loyalty_tier, values_file data/shipping_zones.csv\n" +
		`reject when zone == "NONE" -> NOT_SERVED: no`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	inline := ruleOf(t, inputs+`
[[row]]
id = "rate"
uses = ["loyalty_tier"]
lookup = { from = "config.pricing.discount", key = ["loyalty_tier"] }
values = { Standard = 0, Silver = 0.05, Gold = 0.1 }
`, "rate")
	if !strings.Contains(inline, "\nvalues: Standard=0, Silver=0.05, Gold=0.1") {
		t.Errorf("got %q", inline)
	}
}

func TestRuleTextInputIsEmpty(t *testing.T) {
	if got := ruleOf(t, inputs, "subtotal"); got != "" {
		t.Errorf("got %q", got)
	}
}
