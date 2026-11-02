package eval

import (
	"path/filepath"
	"testing"

	"derivationsheets/internal/sheet"
)

func fixture(t *testing.T) *Engine {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "specs", "order-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.LoadIssues) > 0 {
		t.Fatalf("load issues: %+v", s.LoadIssues)
	}
	e, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// baseInput is a member since 2020 shipping 3 kg express to Canada.
func baseInput() Input {
	return Input{
		"signup_date": "2020-03-15", "is_member": true, "country": "CA", "shipping_method": "EXPRESS",
		"weight_kg": int64(3), "subtotal": int64(80), "request_time": "2026-11-02T10:00:00+05:30",
	}
}

func TestGoldMemberAbroad(t *testing.T) {
	res := fixture(t).Evaluate(baseInput())
	if res.Err != nil || res.Reject != nil {
		t.Fatalf("err=%v reject=%v", res.Err, res.Reject)
	}
	// 6 years and a member: Gold, 10% off 80 = 8, free shipping for Gold,
	// net 72, tax round(72 * 0.08) = 6, total 78. Canada is not domestic, so
	// the customs value is sent.
	want := map[string]any{
		"shippingMethod": "EXPRESS", "subtotal": 80.0, "loyaltyTier": "GLD", "discountAmount": 8.0,
		"freeShipping": true, "shippingCost": 0.0, "customsValue": 72.0,
		"netAmount": 72.0, "salesTax": 6.0, "totalAmount": 78.0,
	}
	for k, v := range want {
		if !sheet.Equal(res.Payload[k], v) {
			t.Errorf("%s = %v, want %v", k, res.Payload[k], v)
		}
	}
	if len(res.Payload) != len(want) {
		t.Errorf("payload has %d fields, want %d: %v", len(res.Payload), len(want), res.Payload)
	}
	// The trace lists every row, and the table rule that fired.
	if len(res.Steps) != len(fixture(t).Sheet.Rows) {
		t.Errorf("trace has %d steps, want one per row", len(res.Steps))
	}
	if st := res.ByRow["loyalty_tier"]; st.TableRule != 4 || st.TableKey != "[true,>= 2]" {
		t.Errorf("loyalty_tier fired rule %d %s", st.TableRule, st.TableKey)
	}
	if st := res.ByRow["free_shipping"]; st.TableRule != 2 {
		t.Errorf("free_shipping fired rule %d, want 2", st.TableRule)
	}
	if st := res.ByRow["shipping_fee"]; st.LookupAt != "data/shipping_rates.csv:12" || !sheet.Equal(st.Value, 36.0) {
		t.Errorf("shipping_fee looked up %v at %s", st.Value, st.LookupAt)
	}
}

func TestDestinationReject(t *testing.T) {
	in := baseInput()
	in["country"] = "AQ"
	res := fixture(t).Evaluate(in)
	if res.Reject == nil || res.Reject.Code != "DESTINATION_NOT_SERVED" {
		t.Fatalf("reject = %+v, err = %v", res.Reject, res.Err)
	}
}

func TestSituationalSend(t *testing.T) {
	in := baseInput()
	in["country"] = "US"
	res := fixture(t).Evaluate(in)
	if _, ok := res.Payload["customsValue"]; ok {
		t.Error("customsValue sent for a domestic order")
	}
	if st := res.ByRow["customs_value"].Send; st == nil || st.When == nil || *st.When {
		t.Errorf("send trace = %+v", st)
	}
}

func TestMissingInput(t *testing.T) {
	in := baseInput()
	delete(in, "country")
	res := fixture(t).Evaluate(in)
	if res.Reject == nil || res.Reject.Code != MissingInputCode || res.Reject.Row != "country" {
		t.Fatalf("reject = %+v", res.Reject)
	}
}
