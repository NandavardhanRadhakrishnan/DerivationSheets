package flow

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"derivationsheets/internal/sources"
)

func example(t *testing.T, mutate func(f *Flow)) *Analysis {
	t.Helper()
	f, err := Load(filepath.Join("..", "..", "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(f)
	}
	return Analyze(f, sources.NewResolver(f.Dir, f.Sources))
}

func item(t *testing.T, a *Analysis, iface, field string) Item {
	t.Helper()
	for _, b := range a.Boundaries {
		if b.Iface != nil && b.Iface.Name == iface {
			for _, it := range b.Items {
				if it.Field == field {
					return it
				}
			}
		}
	}
	t.Fatalf("no item %s.%s", iface, field)
	return Item{}
}

func TestExample(t *testing.T) {
	a := example(t, nil)
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	// Layout: input, topic, request, response, output; four team steps.
	var roles []string
	for _, b := range a.Boundaries {
		roles = append(roles, b.Role)
	}
	if strings.Join(roles, ",") != "input,handoff,request,response,output" || len(a.Steps) != 4 {
		t.Fatalf("roles %v, %d steps", roles, len(a.Steps))
	}
	if a.Steps[3].KeepsFrom != 1 || a.Steps[3].Owner != "Orders team" {
		t.Errorf("continuation step = %+v", a.Steps[3])
	}
	cases := []struct {
		iface, field, status string
	}{
		{"order-placed", "shippingMethod", Missing}, // needed by the shipping call and the output
		{"order-placed", "weightKg", Warn},          // optional, but always needed
		{"order-placed", "loyaltyTier", Warn},       // enum is upper case
		{"order-placed", "country", Carried},
		{"order-placed", "orderId", Unused},
		{"quote-request", "zone", Carried}, // NONE is rejected before the call
		{"quote-request", "method", Carried},
		{"quote-response", "fee", Carried},
		{"payment-request", "currency", Missing}, // required, nothing sends it
		{"payment-request", "totalAmount", Carried},
		{"checkout-request", "order.giftMessage", Unused},
	}
	for _, c := range cases {
		if it := item(t, a, c.iface, c.field); it.Status != c.status {
			t.Errorf("%s.%s = %s (%s), want %s", c.iface, c.field, it.Status, it.Note, c.status)
		}
	}
	it := item(t, a, "order-placed", "shippingMethod")
	if !slices.Contains(it.Consumers, "Shipping fee (Shipping team)") || !strings.Contains(it.Note, "ask Orders team") {
		t.Errorf("missing item = %+v", it)
	}
	// Values the Orders team holds are kept across the call, not sent.
	if a.Transit[3]["free_shipping"] != "kept" || a.Transit[3]["shipping_fee"] != "in" {
		t.Errorf("transit at continuation = %v", a.Transit[3])
	}
	for _, b := range a.Boundaries {
		if b.Role == RoleRequest && b.Needs("free_shipping") {
			t.Error("free_shipping should not cross the call")
		}
	}
}

func TestCalleeSeesOnlyTheRequest(t *testing.T) {
	// If the Shipping team computed the discount, it would need the
	// subtotal in the quote request.
	a := example(t, func(f *Flow) {
		f.Stages[1].Computes = []string{"zone", "discount_rate", "free_shipping"}
		f.Stages[1].CalleeComputes = []string{"shipping_fee", "discount"}
	})
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	if it := item(t, a, "quote-request", "subtotal"); it.Status != Missing {
		t.Errorf("subtotal at the request = %+v", it)
	}
	if it := item(t, a, "quote-response", "discountAmount"); it.Status != Missing {
		t.Errorf("discount at the response = %+v", it)
	}
}

func TestComputedTooLate(t *testing.T) {
	a := example(t, func(f *Flow) {
		f.Stages[0].Computes = []string{"customer_years"}
		f.Output.Computes = append(f.Output.Computes, "loyalty_tier")
	})
	found := false
	for _, e := range a.Errors {
		if strings.Contains(e.Message, "Loyalty tier is computed by Orders team, but") && e.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %v", a.Errors)
	}
}

func TestStructureErrors(t *testing.T) {
	a := example(t, func(f *Flow) {
		f.Output.Producer = "Payments team" // the caller must continue after a call
		f.Stages[0].Computes = append(f.Stages[0].Computes, "no_such_row", "subtotal")
		f.Stages[1].Response.Carries["nope"] = "shipping_fee"
	})
	want := []string{"the caller (Orders team) continues", `"no_such_row"`, `"subtotal", but it is an input`, `carries "nope"`}
	for _, w := range want {
		ok := false
		for _, e := range a.Errors {
			ok = ok || strings.Contains(e.Message, w)
		}
		if !ok {
			t.Errorf("no error containing %q in %v", w, a.Errors)
		}
	}
}
