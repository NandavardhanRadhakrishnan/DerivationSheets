package requirements

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// copyTree copies the folders the order-checkout fixture needs.
func copyTree(t *testing.T, dst string) {
	t.Helper()
	root := filepath.Join("..", "..")
	for _, d := range []string{"specs/order-checkout", "flows/checkout-to-payment", "interfaces", "schema"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(d)), func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, p)
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
	}
}

// edit replaces old by new in a file of the fixture, which must contain old.
func edit(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.Contains(s, old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(s, old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func analyse(t *testing.T, dir string) *flow.Analysis {
	t.Helper()
	f, err := flow.Load(filepath.Join(dir, "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	return flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))
}

// pair returns the fixture's analysis before and after mutate changes a copy.
func pair(t *testing.T, mutate func(dir string)) (base, head *flow.Analysis) {
	t.Helper()
	b, h := t.TempDir(), t.TempDir()
	copyTree(t, b)
	copyTree(t, h)
	mutate(h)
	return analyse(t, b), analyse(t, h)
}

// find returns the requirement of a kind whose subject contains subject, on
// the team when team is not empty.
func find(rs []Requirement, kind Kind, team, subject string) *Requirement {
	for i := range rs {
		if rs[i].Kind == kind && (team == "" || rs[i].Team == team) && strings.Contains(rs[i].Subject, subject) {
			return &rs[i]
		}
	}
	return nil
}

const handlingRow = `
[[row]]
id      = "handling_fee"
name    = "Handling fee"
uses    = ["shipping_fee"]
formula = "shipping_fee * 0.1"

  [row.send]
  field = "handlingFee"
  usage = "R"
`

func addHandling(t *testing.T, dir string, pinned bool) {
	t.Helper()
	p := filepath.Join(dir, "specs", "order-checkout", "sheet.toml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append([]byte(strings.ReplaceAll(string(data), "\r\n", "\n")), handlingRow...), 0o644); err != nil {
		t.Fatal(err)
	}
	if pinned {
		edit(t, dir, "flows/checkout-to-payment/flow.toml", `"sales_tax", "total"]`, `"sales_tax", "total", "handling_fee"]`)
	}
}

func TestCompareFormulaChange(t *testing.T) {
	base, head := pair(t, func(d string) {
		edit(t, d, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	})
	rs, err := Compare(base, head, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d requirements, want 1: %+v", len(rs), rs)
	}
	r := rs[0]
	if r.Kind != Compute || r.Team != "Orders team" || r.Subject != "shipping_cost" {
		t.Errorf("got %+v", r)
	}
	if !strings.Contains(r.Before, "shipping_fee") || !strings.Contains(r.After, "shipping_fee * 2") {
		t.Errorf("before/after = %q / %q", r.Before, r.After)
	}
}

func TestCompareNewFieldOnBoundary(t *testing.T) {
	base, head := pair(t, func(d string) { addHandling(t, d, true) })
	rs, err := Compare(base, head, false)
	if err != nil {
		t.Fatal(err)
	}
	schema := find(rs, Schema, "Payments team", "handlingFee")
	comp := find(rs, Compute, "Orders team", "handling_fee")
	send := find(rs, Send, "Orders team", "handlingFee")
	if schema == nil || comp == nil || send == nil {
		t.Fatalf("want Schema, Compute and Send, got %+v", rs)
	}
	if comp.Placed != "" {
		t.Errorf("a row the flow names is not placed by default, got %q", comp.Placed)
	}
	if !strings.HasSuffix(schema.File, "payment-request.toml") || schema.Edit == nil || schema.Edit.Field != "handlingFee" {
		t.Errorf("schema = %+v", schema)
	}
	if len(comp.Needs) != 1 || comp.Needs[0] != schema.ID {
		t.Errorf("compute needs %v, want [%s]", comp.Needs, schema.ID)
	}
	if len(send.Needs) != 1 || send.Needs[0] != comp.ID {
		t.Errorf("send needs %v, want [%s]", send.Needs, comp.ID)
	}
	if rs[0].Kind != Schema {
		t.Errorf("first requirement is %s, want Schema", rs[0].Kind)
	}
}

func TestCompareLabelOnlyExcluded(t *testing.T) {
	base, head := pair(t, func(d string) {
		edit(t, d, "specs/order-checkout/sheet.toml", `name    = "Shipping cost"`, `name    = "Shipping charge"`)
	})
	rs, err := Compare(base, head, false)
	if err != nil || len(rs) != 0 {
		t.Fatalf("got %+v, %v; want none", rs, err)
	}
	rs, err = Compare(base, head, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(rs, Compute, "Orders team", "shipping_cost"); c == nil || !strings.Contains(c.After, "Shipping charge") {
		t.Errorf("with all, want a Compute carrying the label change, got %+v", rs)
	}
}

func TestCompareTeamMoved(t *testing.T) {
	base, head := pair(t, func(d string) {
		edit(t, d, "flows/checkout-to-payment/flow.toml", `computes  = ["customer_years", "loyalty_tier"]`, `computes  = ["customer_years", "loyalty_tier", "zone"]`)
		edit(t, d, "flows/checkout-to-payment/flow.toml", `computes = ["zone", "discount_rate"`, `computes = ["discount_rate"`)
	})
	rs, err := Compare(base, head, false)
	if err != nil {
		t.Fatal(err)
	}
	if find(rs, Compute, "Storefront team", "zone") == nil {
		t.Errorf("want a Compute for the new team, got %+v", rs)
	}
	gone := find(rs, Compute, "Orders team", "zone")
	if gone == nil || !strings.Contains(gone.What, "stop") {
		t.Errorf("want a removal for the old team, got %+v", gone)
	}
}

func TestCompareDefaultPlacement(t *testing.T) {
	base, head := pair(t, func(d string) { addHandling(t, d, false) })
	rs, err := Compare(base, head, false)
	if err != nil {
		t.Fatal(err)
	}
	c := find(rs, Compute, "", "handling_fee")
	if c == nil || c.Placed != "default" {
		t.Fatalf("want a Compute placed by default, got %+v", rs)
	}
}

func TestCompareNilBase(t *testing.T) {
	_, head := pair(t, func(string) {})
	rs, err := Compare(nil, head, false)
	if err != nil {
		t.Fatal(err)
	}
	if find(rs, Compute, "Shipping team", "shipping_fee") == nil {
		t.Errorf("a new flow should list every computed row, got %+v", rs)
	}
}

func TestCompareErrors(t *testing.T) {
	base, head := pair(t, func(d string) {
		edit(t, d, "flows/checkout-to-payment/flow.toml", `"../../interfaces/payments/payment-request.toml"`, `"../../interfaces/payments/missing.toml"`)
	})
	if len(head.Errors) == 0 {
		t.Fatal("fixture should break the flow")
	}
	if _, err := Compare(base, head, false); err == nil || !strings.Contains(err.Error(), "head") {
		t.Errorf("want an error naming the head, got %v", err)
	}
}

func TestCompareNoChange(t *testing.T) {
	base, head := pair(t, func(string) {})
	rs, err := Compare(base, head, false)
	if err != nil || len(rs) != 0 {
		t.Fatalf("got %+v, %v; want none", rs, err)
	}
}
