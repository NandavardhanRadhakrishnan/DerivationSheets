package eval

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
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

// withInputRejects is the fixture with a reject on the weight input that
// checks only its own value, and one on the subtotal input that also names
// another input.
func withInputRejects(t *testing.T, edits ...string) *Engine {
	t.Helper()
	dir := filepath.Join("..", "..", "specs", "order-checkout")
	b, err := os.ReadFile(filepath.Join(dir, sheet.FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	edits = append([]string{
		"source = \"request.order.weightKg\"\ntype   = \"number\"\n",
		"source = \"request.order.weightKg\"\ntype   = \"number\"\n\n  [[row.reject]]\n  when = \"weight_kg > 30\"\n  code = \"OVERWEIGHT\"\n",
		"source = \"request.order.subtotal\"\ntype   = \"number\"\n",
		"source = \"request.order.subtotal\"\ntype   = \"number\"\n\n  [[row.reject]]\n  when = \"subtotal > 1000 && !is_member\"\n  code = \"NON_MEMBER_LIMIT\"\n",
	}, edits...)
	for i := 0; i < len(edits); i += 2 {
		if !strings.Contains(text, edits[i]) {
			t.Fatalf("fixture does not contain %q", edits[i])
		}
		text = strings.Replace(text, edits[i], edits[i+1], 1)
	}
	s, err := sheet.Parse(dir, filepath.Join(dir, sheet.FileName), text)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestInputReject(t *testing.T) {
	e := withInputRejects(t)
	if res := e.Evaluate(baseInput()); res.Err != nil || res.Reject != nil {
		t.Fatalf("base input: err=%v reject=%+v", res.Err, res.Reject)
	}

	in := baseInput()
	in["weight_kg"] = int64(31)
	res := e.Evaluate(in)
	if res.Err != nil || res.Reject == nil || res.Reject.Code != "OVERWEIGHT" || res.Reject.Row != "weight_kg" || res.Reject.Index != 0 {
		t.Fatalf("err=%v reject=%+v", res.Err, res.Reject)
	}
	if !res.ByRow["weight_kg"].Rejected {
		t.Error("weight_kg step is not marked rejected")
	}
	// The request stops at the input: the shipping fee, which has no rate
	// over 30 kg, is never looked up.
	if _, ok := res.ByRow["shipping_fee"]; ok {
		t.Error("rows after the rejected input were evaluated")
	}
}

func TestInputRejectNamingAnotherInput(t *testing.T) {
	e := withInputRejects(t)
	// subtotal is checked after is_member, which its reject names.
	if !slices.Contains(e.Graph.Deps["subtotal"], "is_member") {
		t.Errorf("subtotal deps = %v, want is_member", e.Graph.Deps["subtotal"])
	}
	for _, member := range []bool{true, false} {
		in := baseInput()
		in["subtotal"], in["is_member"] = int64(2000), member
		res := e.Evaluate(in)
		if res.Err != nil {
			t.Fatalf("member=%v: %v", member, res.Err)
		}
		if got := res.Reject != nil && res.Reject.Code == "NON_MEMBER_LIMIT"; got == member {
			t.Errorf("member=%v: reject=%+v", member, res.Reject)
		}
	}
}

func TestInputRejectSkippedWhenNotRequired(t *testing.T) {
	e := withInputRejects(t,
		"source = \"request.order.subtotal\"\ntype   = \"number\"\n",
		"source = \"request.order.subtotal\"\ntype   = \"number\"\nusage  = \"S\"\nwhen   = \"is_member\"\n")
	in := baseInput()
	in["is_member"] = false
	delete(in, "subtotal")
	res := e.Evaluate(in)
	if st := res.ByRow["subtotal"]; st == nil || st.Status != StatusNotRequired || st.Rejected {
		t.Fatalf("subtotal step = %+v", st)
	}
	if res.Err != nil && res.Err.Row == "subtotal" {
		t.Errorf("the missing input's reject was evaluated: %v", res.Err)
	}
}
