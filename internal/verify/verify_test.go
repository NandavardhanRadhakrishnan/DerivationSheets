package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/evidence"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

func setup(t *testing.T) (*eval.Engine, *cases.File) {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "specs", "order-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := time.Parse(time.RFC3339, cases.DefaultReferenceTime)
	gen := cases.Generate(e, ref)
	return e, &cases.File{Sheet: s.Slug, Version: s.Version, Cases: gen.Cases}
}

// perfect builds results.json content in which every case behaves as expected.
func perfect(cf *cases.File) map[string]any {
	out := map[string]any{}
	for _, c := range cf.Cases {
		if c.ExpectedReject != "" {
			out[c.ID] = map[string]any{"reject": c.ExpectedReject}
			continue
		}
		p := map[string]any{}
		for _, f := range c.Expected {
			p[f.Name] = f.Value
		}
		out[c.ID] = map[string]any{"payload": p}
	}
	return out
}

func write(t *testing.T, v any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.json")
	data, _ := json.Marshal(map[string]any{"cases": v})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAllMatch(t *testing.T) {
	e, cf := setup(t)
	results, err := ReadResults(write(t, perfect(cf)))
	if err != nil {
		t.Fatal(err)
	}
	rep := Verify(e, cf, results, "results.json", time.Now())
	if len(rep.Problems) > 0 || len(rep.Notes) > 0 {
		t.Fatalf("problems %v notes %v", rep.Problems, rep.Notes)
	}
	for _, r := range e.Sheet.Rows {
		st := evidence.RowStatus(rep.Run, r.ID, nil)
		if st.State != evidence.Verified {
			t.Errorf("row %s is %s", r.ID, st.State)
		}
	}
	if st := evidence.BranchStatus(rep.Run, "zone:reject:DESTINATION_NOT_SERVED"); st.State != evidence.Verified {
		t.Errorf("zone reject is %s", st.State)
	}
}

func TestOneValueChanged(t *testing.T) {
	e, cf := setup(t)
	res := perfect(cf)
	// Change loyaltyTier in the first case that sends GLD.
	var target string
	for _, c := range cf.Cases {
		for _, f := range c.Expected {
			if f.Name == "loyaltyTier" && f.Value == "GLD" && target == "" {
				target = c.ID
			}
		}
	}
	res[target].(map[string]any)["payload"].(map[string]any)["loyaltyTier"] = "SLV"
	results, _ := ReadResults(write(t, res))
	rep := Verify(e, cf, results, "results.json", time.Now())
	t.Log(rep.Problems)
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], target) || !strings.Contains(rep.Problems[0], "loyaltyTier") {
		t.Fatalf("problems = %v; want one naming case %q and field loyaltyTier", rep.Problems, target)
	}
	if st := evidence.RowStatus(rep.Run, "loyalty_tier", nil); st.State != evidence.Contradicted {
		t.Errorf("loyalty_tier is %s", st.State)
	}
	if st := evidence.BranchStatus(rep.Run, "loyalty_tier:[true,>= 2]"); st.State != evidence.Contradicted {
		t.Errorf("cell [true,>= 2] is %s", st.State)
	}
	if st := evidence.BranchStatus(rep.Run, "loyalty_tier:[false,< 2]"); st.State != evidence.Verified {
		t.Errorf("cell [false,< 2] is %s", st.State)
	}
	// Rows upstream, and rows whose own outputs matched, stay verified.
	for _, id := range []string{"is_member", "customer_years", "discount_rate", "discount", "free_shipping", "net_amount"} {
		if st := evidence.RowStatus(rep.Run, id, nil); st.State != evidence.Verified {
			t.Errorf("%s is %s", id, st.State)
		}
	}
}

func TestSentWhenItShouldNotBe(t *testing.T) {
	e, cf := setup(t)
	res := perfect(cf)
	var target string
	for _, c := range cf.Cases {
		if c.ExpectedReject != "" {
			continue
		}
		has := false
		for _, f := range c.Expected {
			has = has || f.Name == "customsValue"
		}
		if !has {
			target = c.ID
			break
		}
	}
	res[target].(map[string]any)["payload"].(map[string]any)["customsValue"] = 94
	results, _ := ReadResults(write(t, res))
	rep := Verify(e, cf, results, "results.json", time.Now())
	if st := evidence.BranchStatus(rep.Run, "customs_value:send.when:false"); st.State != evidence.Contradicted {
		t.Errorf("send.when:false is %s; problems %v", st.State, rep.Problems)
	}
}

// setupPiece generates cases for the orders team's piece, which sends
// method, weightKg and zone to the shipping-quote call.
func setupPiece(t *testing.T) (*eval.Engine, *cases.File) {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "examples", "pieces", "2-orders-team"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := time.Parse(time.RFC3339, cases.DefaultReferenceTime)
	gen := cases.Generate(e, ref)
	return e, &cases.File{Sheet: s.Slug, Version: s.Version, Cases: gen.Cases}
}

// withCalls adds every expected call request to perfect results.
func withCalls(cf *cases.File, res map[string]any) map[string]any {
	for _, c := range cf.Cases {
		if len(c.Calls) == 0 {
			continue
		}
		calls := map[string]any{}
		for _, call := range c.Calls {
			p := map[string]any{}
			for _, f := range call.Fields {
				p[f.Name] = f.Value
			}
			calls[call.Target] = p
		}
		res[c.ID].(map[string]any)["calls"] = calls
	}
	return res
}

func TestCallsAllMatch(t *testing.T) {
	e, cf := setupPiece(t)
	results, err := ReadResults(write(t, withCalls(cf, perfect(cf))))
	if err != nil {
		t.Fatal(err)
	}
	rep := Verify(e, cf, results, "results.json", time.Now())
	if len(rep.Problems) > 0 || len(rep.Notes) > 0 {
		t.Fatalf("problems %v notes %v", rep.Problems, rep.Notes)
	}
	if st := evidence.RowStatus(rep.Run, "zone", nil); st.State != evidence.Verified {
		t.Errorf("zone is %s", st.State)
	}
}

func TestCallFieldChanged(t *testing.T) {
	e, cf := setupPiece(t)
	res := withCalls(cf, perfect(cf))
	var target string
	for _, c := range cf.Cases {
		if len(c.Calls) > 0 {
			target = c.ID
			break
		}
	}
	res[target].(map[string]any)["calls"].(map[string]any)["shipping-quote"].(map[string]any)["zone"] = "NOWHERE"
	results, _ := ReadResults(write(t, res))
	rep := Verify(e, cf, results, "results.json", time.Now())
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], target) || !strings.Contains(rep.Problems[0], "zone in the call to shipping-quote") {
		t.Fatalf("problems = %v; want one naming case %q and zone in the call to shipping-quote", rep.Problems, target)
	}
	st := evidence.RowStatus(rep.Run, "zone", nil)
	if st.State != evidence.Contradicted {
		t.Fatalf("zone is %s", st.State)
	}
	if c := st.Tally.Contradictions[0]; c.Call != "shipping-quote" || c.Field != "zone" {
		t.Errorf("contradiction = %+v", c)
	}
	// country feeds zone, but also customs_value, which matched.
	for _, id := range []string{"country", "weight_kg", "shipping_method", "total"} {
		if st := evidence.RowStatus(rep.Run, id, nil); st.State != evidence.Verified {
			t.Errorf("%s is %s", id, st.State)
		}
	}
}

func TestCallsNotCaptured(t *testing.T) {
	e, cf := setupPiece(t)
	results, _ := ReadResults(write(t, perfect(cf)))
	rep := Verify(e, cf, results, "results.json", time.Now())
	if len(rep.Problems) > 0 {
		t.Fatalf("problems %v; uncaptured calls must not contradict", rep.Problems)
	}
	if len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "no captured request for call shipping-quote in 4 of 4 cases") {
		t.Errorf("notes = %v", rep.Notes)
	}
}

func TestCallFieldSentWhenItShouldNotBe(t *testing.T) {
	text := `
sheet   = "call-when"
version = 1

[[row]]
id     = "express"
source = "request.express"
type   = "boolean"

  [[row.send]]
  target = "quote"
  field  = "express"
  usage  = "S"
  when   = "express"

  [[row.send]]
  field = "express"
  usage = "R"
`
	dir := t.TempDir()
	s, err := sheet.Parse(dir, filepath.Join(dir, sheet.FileName), text)
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := time.Parse(time.RFC3339, cases.DefaultReferenceTime)
	cf := &cases.File{Sheet: s.Slug, Version: s.Version, Cases: cases.Generate(e, ref).Cases}
	res := withCalls(cf, perfect(cf))
	for _, c := range cf.Cases {
		if len(c.Calls) == 0 {
			res[c.ID].(map[string]any)["calls"] = map[string]any{"quote": map[string]any{"express": false}}
		}
	}
	results, _ := ReadResults(write(t, res))
	rep := Verify(e, cf, results, "results.json", time.Now())
	if st := evidence.BranchStatus(rep.Run, "express:send.quote.when:false"); st.State != evidence.Contradicted {
		t.Errorf("send.quote.when:false is %s; problems %v", st.State, rep.Problems)
	}
	if st := evidence.BranchStatus(rep.Run, "express:send.quote.when:true"); st.State != evidence.Verified {
		t.Errorf("send.quote.when:true is %s", st.State)
	}
}

func TestReadResultsWithCalls(t *testing.T) {
	path := write(t, map[string]any{
		"a": map[string]any{"payload": map[string]any{"x": 1}, "calls": map[string]any{"quote": map[string]any{"y": "Z"}}},
		"b": map[string]any{"calls": 3, "other": 1},
	})
	got, err := ReadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if a := got["a"]; a.Payload["x"] != 1.0 || a.Calls["quote"]["y"] != "Z" {
		t.Errorf("a = %+v", a)
	}
	// An entry with keys beyond payload, calls and reject is a bare payload.
	if b := got["b"]; b.Calls != nil || b.Payload["calls"] != 3.0 {
		t.Errorf("b = %+v", b)
	}
}
