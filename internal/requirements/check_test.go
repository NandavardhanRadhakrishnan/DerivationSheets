package requirements

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	Check(rs, registry, dirs)
	return rs[0]
}

func TestOwnerAlias(t *testing.T) {
	if a, ok := ownerAlias("The Order Desk", registry); !ok || a != "orders" {
		t.Errorf("team key: %q, %v", a, ok)
	}
	if a, ok := ownerAlias("Cart team", registry); !ok || a != "cart" {
		t.Errorf("first word: %q, %v", a, ok)
	}
	if a, ok := ownerAlias("Payments team", registry); ok {
		t.Errorf("unknown team resolved to %q", a)
	}
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

func TestCheckSend(t *testing.T) {
	q := Requirement{Kind: Send, Team: "Cart team", Row: "free_shipping", Field: "freeShipping"}
	sends := inputs + freeShippingTable + "\n  [row.send]\n  field = \"freeShipping\"\n  usage = \"R\"\n"
	if got := check(t, q, fakeFetcher{"cart": owner(t, sends)}); got.Status != StatusDone {
		t.Errorf("own target: %+v", got)
	}
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
	if got := check(t, q, fakeFetcher{"cart": owner(t, call)}); got.Status != StatusDone {
		t.Errorf("call send: %+v", got)
	}
	if got := check(t, q, fakeFetcher{"cart": owner(t, inputs+freeShippingTable)}); got.Status != StatusOpen || !strings.Contains(got.Note, "freeShipping") {
		t.Errorf("no send: %+v", got)
	}
}

func TestCheckReceive(t *testing.T) {
	q := Requirement{Kind: Receive, Team: "The Order Desk", Row: "free_shipping", Field: "freeShipping"}
	reads := inputs + "\n[[row]]\nid = \"free_shipping\"\nsource = \"request.freeShipping\"\ntype = \"boolean\"\n"
	if got := check(t, q, fakeFetcher{"orders": owner(t, reads)}); got.Status != StatusDone {
		t.Errorf("reads it: %+v", got)
	}
	if got := check(t, q, fakeFetcher{"orders": owner(t, inputs+freeShippingTable)}); got.Status != StatusOpen || !strings.Contains(got.Note, "reads") {
		t.Errorf("derives it instead: %+v", got)
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
