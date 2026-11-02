package eval

import (
	"path/filepath"
	"testing"

	"derivationsheets/internal/sheet"
)

func fixture(t *testing.T) *Engine {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "specs", "premium-calculation"))
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

func baseInput() Input {
	return Input{
		"dob": "1980-04-12", "city": "Pune", "is_smoker": true, "has_ped": true,
		"plan": "GOLD", "sum_insured": int64(500000), "request_time": "2026-11-02T10:00:00+05:30",
	}
}

func TestSmokerWithPED(t *testing.T) {
	res := fixture(t).Evaluate(baseInput())
	if res.Err != nil || res.Reject != nil {
		t.Fatalf("err=%v reject=%v", res.Err, res.Reject)
	}
	want := map[string]any{
		"planCode": "GOLD", "sumInsured": 500000.0, "riskClassCode": "HIGH",
		"waitingPeriodMonths": 24.0, "underwritingType": "MANUAL",
		"netPremium": 21330.0, "gstAmount": 3839.0, "totalPayable": 25169.0,
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
	if st := res.ByRow["risk_class"]; st.TableRule != 4 || st.TableKey != "[true,true]" {
		t.Errorf("risk_class fired rule %d %s", st.TableRule, st.TableKey)
	}
	if st := res.ByRow["medical_check"]; st.TableRule != 1 {
		t.Errorf("medical_check fired rule %d, want 1", st.TableRule)
	}
}

func TestAgeReject(t *testing.T) {
	in := baseInput()
	in["dob"] = "1950-01-01"
	res := fixture(t).Evaluate(in)
	if res.Reject == nil || res.Reject.Code != "AGE_LIMIT" {
		t.Fatalf("reject = %+v, err = %v", res.Reject, res.Err)
	}
}

func TestSituationalSend(t *testing.T) {
	in := baseInput()
	in["has_ped"] = false
	res := fixture(t).Evaluate(in)
	if _, ok := res.Payload["waitingPeriodMonths"]; ok {
		t.Error("waitingPeriodMonths sent when has_ped is false")
	}
	if st := res.ByRow["waiting_period"].Send; st == nil || st.When == nil || *st.When {
		t.Errorf("send trace = %+v", st)
	}
}

func TestMissingInput(t *testing.T) {
	in := baseInput()
	delete(in, "city")
	res := fixture(t).Evaluate(in)
	if res.Reject == nil || res.Reject.Code != MissingInputCode || res.Reject.Row != "city" {
		t.Fatalf("reject = %+v", res.Reject)
	}
}
