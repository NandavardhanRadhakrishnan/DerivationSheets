package requirements

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// fakeFetcher serves an alias from a local folder, or fails.
type fakeFetcher map[string]string

func (f fakeFetcher) Fetch(src sources.Source) (string, string, error) {
	dir, ok := f[src.Alias]
	if !ok {
		return "", "", errors.New("cannot fetch " + src.Alias)
	}
	return dir, "abc1234", nil
}

// owner makes a folder holding the given sheet texts, one folder each.
func owner(t *testing.T, sheets ...string) string {
	t.Helper()
	root := t.TempDir()
	for i, text := range sheets {
		dir := filepath.Join(root, "specs", string(rune('a'+i)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sheet.toml"), []byte("sheet = \"s\"\nversion = 1\n"+text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const freeShippingTable = `
[[row]]
id = "free_shipping"
uses = ["subtotal", "loyalty_tier"]

  [row.table]
  hit_policy = "first"
  columns = ["subtotal", "loyalty_tier", "=>"]
  rows = [
    [">= 100", "-",    true],
    ["-",      "Gold", true],
    ["-",      "-",    false],
  ]
`

var registry = map[string]sources.Source{
	"cart":   {Alias: "cart", Git: "x"},
	"orders": {Alias: "orders", Git: "x", Team: "The Order Desk"},
}

func check(t *testing.T, q Requirement, dirs fakeFetcher) Requirement {
	t.Helper()
	rs := []Requirement{q}
	Check(rs, registry, dirs, "", nil)
	return rs[0]
}

func TestCheckComputeAdd(t *testing.T) {
	rule := ruleOf(t, inputs+freeShippingTable, "free_shipping")
	q := Requirement{Kind: Compute, Team: "Cart team", Row: "free_shipping", Rule: rule, Status: StatusOpen}

	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs+freeShippingTable)}); got.Status != StatusDone {
		t.Errorf("same rule: %+v", got)
	}
	different := strings.Replace(freeShippingTable, ">= 100", ">= 50", 1)
	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs+different)}); got.Status != StatusOpen || !strings.Contains(got.Note, "differs") {
		t.Errorf("different rule: %+v", got)
	}
	asInput := `
[[row]]
id = "free_shipping"
source = "request.freeShipping"
type = "boolean"
`
	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs+asInput)}); got.Status != StatusOpen || !strings.Contains(got.Note, "input") {
		t.Errorf("only an input: %+v", got)
	}
	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs)}); got.Status != StatusOpen || !strings.Contains(got.Note, "free_shipping") {
		t.Errorf("absent: %+v", got)
	}
}

func TestCheckWhitespaceOnlyDifferenceIsTheSameRule(t *testing.T) {
	rule := ruleOf(t, inputs+"\n[[row]]\nid = \"fee\"\nuses = [\"subtotal\"]\nformula = \"subtotal * 2\"\n", "fee")
	q := Requirement{Kind: Compute, Team: "Cart team", Row: "fee", Rule: rule}
	spaced := inputs + "\n[[row]]\nid = \"fee\"\nuses = [\"subtotal\"]\nformula = \"subtotal   *   2\"\n"
	if got := check(t, q, fakeFetcher{"cart": owner(t, spaced)}); got.Status != StatusDone {
		t.Errorf("got %+v", got)
	}
}

func TestCheckComputeRemove(t *testing.T) {
	q := Requirement{Kind: Compute, Team: "The Order Desk", Row: "free_shipping", Remove: true}
	asInput := inputs + "\n[[row]]\nid = \"free_shipping\"\nsource = \"request.freeShipping\"\ntype = \"boolean\"\n"
	if got := check(t, q, fakeFetcher{"orders": owner(t, asInput)}); got.Status != StatusDone {
		t.Errorf("no longer derived: %+v", got)
	}
	if got := check(t, q, fakeFetcher{"orders": owner(t, inputs+freeShippingTable)}); got.Status != StatusOpen || !strings.Contains(got.Note, "still") {
		t.Errorf("still derived: %+v", got)
	}
}

func TestCheckSendMatchesTheBoundary(t *testing.T) {
	own := inputs + freeShippingTable + "\n  [row.send]\n  field = \"freeShipping\"\n  usage = \"R\"\n"
	call := inputs + `
[[row]]
id = "subtotal_copy"
uses = ["subtotal"]
formula = "subtotal"

  [[row.send]]
  target = "shipping-quote"
  field = "freeShipping"
  usage = "R"
`
	handoff := Requirement{Kind: Send, Team: "Cart team", Row: "free_shipping", Field: "freeShipping", Role: "handoff", Stage: "0"}
	request := Requirement{Kind: Send, Team: "Cart team", Row: "free_shipping", Field: "freeShipping", Role: "request", Stage: "shipping-quote"}
	if got := check(t, handoff, fakeFetcher{"cart": owner(t, own)}); got.Status != StatusDone {
		t.Errorf("handoff, own target: %+v", got)
	}
	if got := check(t, handoff, fakeFetcher{"cart": owner(t, call)}); got.Status != StatusOpen {
		t.Errorf("a send to a call request does not fill a handoff: %+v", got)
	}
	if got := check(t, request, fakeFetcher{"cart": owner(t, call)}); got.Status != StatusDone {
		t.Errorf("request, send to that stage: %+v", got)
	}
	if got := check(t, request, fakeFetcher{"cart": owner(t, own)}); got.Status != StatusOpen {
		t.Errorf("a send to the sheet's own target does not fill a call request: %+v", got)
	}
	if got := check(t, handoff, fakeFetcher{"cart": owner(t, inputs+freeShippingTable)}); got.Status != StatusOpen || !strings.Contains(got.Note, "freeShipping") {
		t.Errorf("no send: %+v", got)
	}
}

func TestCheckReceiveMatchesTheSource(t *testing.T) {
	row := func(id, source string) string {
		return inputs + "\n[[row]]\nid = \"" + id + "\"\nsource = \"" + source + "\"\ntype = \"number\"\n"
	}
	handoff := Requirement{Kind: Receive, Team: "The Order Desk", Row: "free_shipping", Field: "freeShipping", Role: "handoff", Stage: "0"}
	if got := check(t, handoff, fakeFetcher{"orders": owner(t, row("free_shipping", "request.freeShipping"))}); got.Status != StatusDone {
		t.Errorf("reads it from the request: %+v", got)
	}
	tax := Requirement{Kind: Receive, Team: "The Order Desk", Row: "sales_tax_rate", Field: "salesTaxRate", Role: "handoff", Stage: "0"}
	if got := check(t, tax, fakeFetcher{"orders": owner(t, row("sales_tax_rate", "config.tax.sales_tax_rate"))}); got.Status != StatusOpen {
		t.Errorf("reading the value from config is not reading the field: %+v", got)
	}
	if got := check(t, handoff, fakeFetcher{"orders": owner(t, inputs+freeShippingTable)}); got.Status != StatusOpen || !strings.Contains(got.Note, "reads") {
		t.Errorf("derives it instead: %+v", got)
	}
	billing := Requirement{Kind: Receive, Team: "Cart team", Row: "billing_country", Field: "billing.country", Role: "input"}
	if got := check(t, billing, fakeFetcher{"cart": owner(t, row("country", "request.shipping.country"))}); got.Status != StatusOpen {
		t.Errorf("billing.country is not shipping.country: %+v", got)
	}
	plain := Requirement{Kind: Receive, Team: "Cart team", Row: "country", Field: "country", Role: "input"}
	if got := check(t, plain, fakeFetcher{"cart": owner(t, row("country", "request.shipping.country"))}); got.Status != StatusDone {
		t.Errorf("a field with no dots matches the last part of a path: %+v", got)
	}
	resp := Requirement{Kind: Receive, Team: "The Order Desk", Row: "shipping_fee", Field: "fee", Role: "response", Stage: "shipping-quote"}
	if got := check(t, resp, fakeFetcher{"orders": owner(t, row("shipping_fee", "call.shipping-quote.fee"))}); got.Status != StatusDone {
		t.Errorf("reads the response: %+v", got)
	}
	if got := check(t, resp, fakeFetcher{"orders": owner(t, row("shipping_fee", "request.fee"))}); got.Status != StatusOpen {
		t.Errorf("a request field is not the response: %+v", got)
	}
}

func TestCheckCarry(t *testing.T) {
	q := Requirement{Kind: Carry, Team: "Cart team", Row: "subtotal"}
	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs)}); got.Status != StatusDone {
		t.Errorf("holds it: %+v", got)
	}
	if got := check(t, q, fakeFetcher{"cart": owner(t, "\n[[row]]\nid = \"other\"\nsource = \"request.other\"\ntype = \"string\"\n")}); got.Status != StatusOpen {
		t.Errorf("does not hold it: %+v", got)
	}
}

func TestCheckUndocumented(t *testing.T) {
	q := Requirement{Kind: Send, Team: "Payments team", Row: "x", Field: "x"}
	if got := check(t, q, fakeFetcher{}); got.Status != StatusUndocumented || !strings.Contains(got.Note, "registry") {
		t.Errorf("no owner: %+v", got)
	}
	q.Team = "Cart team"
	empty := t.TempDir()
	if got := check(t, q, fakeFetcher{"cart": empty}); got.Status != StatusUndocumented || !strings.Contains(got.Note, "no sheets") {
		t.Errorf("no sheets: %+v", got)
	}
	if got := check(t, q, fakeFetcher{}); got.Status != StatusUndocumented || !strings.Contains(got.Note, "cannot fetch cart") {
		t.Errorf("fetch error: %+v", got)
	}
	remove := Requirement{Kind: Compute, Team: "Cart team", Row: "x", Remove: true}
	if got := check(t, remove, fakeFetcher{"cart": empty}); got.Status != StatusUndocumented {
		t.Errorf("a stop in a team with no sheets must not read as done: %+v", got)
	}
}

func TestCheckLeavesSchemaOpen(t *testing.T) {
	q := Requirement{Kind: Schema, Team: "Cart team", Status: StatusOpen}
	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs)}); got.Status != StatusOpen {
		t.Errorf("got %+v", got)
	}
}

func TestCheckRuleCoversEveryNonLabelField(t *testing.T) {
	base := inputs + `
[[row]]
id = "zone"
uses = ["loyalty_tier"]
lookup = { from = "db.zones", key = ["loyalty_tier"] }
values_file = "data/z.csv"

  [[row.reject]]
  when = "zone == \"NONE\""
  code = "NOT_SERVED"
  message = "We do not ship there"
`
	rule := ruleOf(t, base, "zone")
	key := ruleKeyOf(t, base, "zone")
	q := Requirement{Kind: Compute, Team: "Cart team", Row: "zone", Rule: rule, RuleKey: key}
	if got := check(t, q, fakeFetcher{"cart": owner(t, base)}); got.Status != StatusDone {
		t.Errorf("same row: %+v", got)
	}
	message := strings.Replace(base, "We do not ship there", "Sorry, not served", 1)
	if got := check(t, q, fakeFetcher{"cart": owner(t, message)}); got.Status != StatusOpen {
		t.Errorf("a changed reject message changes the row: %+v", got)
	}
	uses := strings.Replace(base, `uses = ["loyalty_tier"]`, `uses = ["loyalty_tier", "subtotal"]`, 1)
	if got := check(t, q, fakeFetcher{"cart": owner(t, uses)}); got.Status != StatusOpen {
		t.Errorf("changed uses change the row: %+v", got)
	}
}

func TestCheckOwnRepositoryIsUndocumented(t *testing.T) {
	rs := []Requirement{{Kind: Carry, Team: "Cart team", Row: "subtotal"}}
	Check(rs, registry, fakeFetcher{"cart": owner(t, inputs)}, "cart", nil)
	if rs[0].Status != StatusUndocumented || !strings.Contains(rs[0].Note, "this repository") {
		t.Errorf("got %+v", rs[0])
	}
}

func TestCheckUnreadableSheetBlocksAnAbsence(t *testing.T) {
	root := owner(t, inputs+"\n[[row]]\nid = \"free_shipping\"\nsource = \"request.freeShipping\"\ntype = \"boolean\"\n")
	broken := filepath.Join(root, "specs", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "sheet.toml"), []byte("this is [not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := Requirement{Kind: Compute, Team: "Cart team", Row: "free_shipping", Remove: true}
	got := check(t, stop, fakeFetcher{"cart": root})
	if got.Status != StatusUndocumented || !strings.Contains(got.Note, "broken") {
		t.Errorf("a stop must not close while a sheet cannot be read: %+v", got)
	}
	carry := Requirement{Kind: Carry, Team: "Cart team", Row: "subtotal"}
	if got := check(t, carry, fakeFetcher{"cart": root}); got.Status != StatusDone {
		t.Errorf("evidence that was found still counts: %+v", got)
	}
	allBroken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(allBroken, "specs", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(allBroken, "specs", "x", "sheet.toml"), []byte("[[[["), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := check(t, carry, fakeFetcher{"cart": allBroken}); got.Status != StatusUndocumented || strings.Contains(got.Note, "no sheets") || !strings.Contains(got.Note, "could not read") {
		t.Errorf("all unreadable is not 'no sheets': %+v", got)
	}
}

func TestCheckSharedTeamKeyReadsEveryRepository(t *testing.T) {
	two := map[string]sources.Source{"cart": {Alias: "cart", Git: "x", Team: "Cart team"}, "cart-web": {Alias: "cart-web", Git: "x", Team: "Cart team"}}
	rs := []Requirement{{Kind: Carry, Team: "Cart team", Row: "subtotal"}}
	other := "\n[[row]]\nid = \"other\"\nsource = \"request.other\"\ntype = \"string\"\n"
	Check(rs, two, fakeFetcher{"cart": owner(t, other), "cart-web": owner(t, inputs)}, "", nil)
	if rs[0].Status != StatusDone {
		t.Errorf("evidence in either repository of the team counts: %+v", rs[0])
	}
}

func TestCheckReadsOverlay(t *testing.T) {
	fee := func(formula string) string {
		return inputs + "\n[[row]]\nid = \"fee\"\nuses = [\"subtotal\"]\nformula = \"" + formula + "\"\n"
	}
	wanted := ruleOf(t, fee("subtotal * 2"), "fee")
	dir := owner(t, fee("subtotal * 3"))
	q := Requirement{Kind: Compute, Team: "Cart team", Row: "fee", Rule: wanted}
	file := filepath.Join(dir, "specs", "a", "sheet.toml")
	draft := "sheet = \"s\"\nversion = 1\n" + fee("subtotal * 2")

	rs := []Requirement{q}
	Check(rs, registry, fakeFetcher{"cart": dir}, "", nil)
	if rs[0].Status != StatusOpen {
		t.Fatalf("without the draft: %+v", rs[0])
	}
	ov := flow.Overlay{flow.OverlayKey(file): []byte(draft)}
	rs = []Requirement{q}
	Check(rs, registry, fakeFetcher{"cart": dir}, "", ov)
	if rs[0].Status != StatusDone {
		t.Fatalf("with the draft: %+v", rs[0])
	}
}
