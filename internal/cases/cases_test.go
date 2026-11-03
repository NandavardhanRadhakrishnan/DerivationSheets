package cases

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

func fixture(t *testing.T) *eval.Engine {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "specs", "order-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestGenerateCoversEverything(t *testing.T) {
	e := fixture(t)
	ref, _ := time.Parse(time.RFC3339, DefaultReferenceTime)
	res := Generate(e, ref)
	for _, c := range res.Cases {
		t.Logf("%-28s covers=%v reject=%q expected=%v", c.ID, c.Covers, c.ExpectedReject, c.Expected)
	}
	t.Logf("%d cases from %d evaluated inputs", len(res.Cases), res.Evaluated)
	if len(res.Uncovered) > 0 {
		t.Fatalf("uncovered targets: %+v", res.Uncovered)
	}
	covered := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range res.Cases {
		if ids[c.ID] {
			t.Errorf("duplicate case id %q", c.ID)
		}
		ids[c.ID] = true
		for _, k := range c.Covers {
			covered[k] = true
		}
	}
	// The acceptance criteria name these explicitly.
	for _, k := range []string{
		"loyalty_tier:[false,< 2]", "loyalty_tier:[false,>= 2]", "loyalty_tier:[true,< 2]", "loyalty_tier:[true,>= 2]",
		"free_shipping:[>= 100,-]", "free_shipping:[-,Gold]", "free_shipping:[-,-]",
		"zone:reject:DESTINATION_NOT_SERVED",
		"customs_value:send.when:true", "customs_value:send.when:false",
		"customer_years:isolation", "zone:isolation", "discount_rate:isolation", "shipping_fee:isolation",
	} {
		if !covered[k] {
			t.Errorf("%s not covered", k)
		}
	}
	// Every case re-evaluates to its expectation.
	for _, c := range res.Cases {
		r := e.Evaluate(eval.Input(c.Input))
		if c.ExpectedReject != "" {
			if r.Reject == nil || r.Reject.Code != c.ExpectedReject {
				t.Errorf("%s: expected reject %s", c.ID, c.ExpectedReject)
			}
			continue
		}
		flat := Flatten(r.Payload)
		for _, f := range c.Expected {
			if !sheet.Equal(flat[f.Name], f.Value) {
				t.Errorf("%s: %s = %v, expected %v", c.ID, f.Name, flat[f.Name], f.Value)
			}
		}
	}
}

func TestWriteRead(t *testing.T) {
	e := fixture(t)
	ref, _ := time.Parse(time.RFC3339, DefaultReferenceTime)
	res := Generate(e, ref)
	path := filepath.Join(t.TempDir(), FileName)
	var inputs []*sheet.Row
	for _, r := range e.Graph.Order {
		if r.IsInput() {
			inputs = append(inputs, r)
		}
	}
	if err := Write(path, e.Sheet, inputs, ref, res.Cases); err != nil {
		t.Fatal(err)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) != len(res.Cases) || f.Version != 1 || f.Sheet != "order-checkout" {
		t.Fatalf("read back %d cases, version %d, sheet %q", len(f.Cases), f.Version, f.Sheet)
	}
	for i, c := range f.Cases {
		if c.ID != res.Cases[i].ID || !slices.Equal(c.Covers, res.Cases[i].Covers) {
			t.Errorf("case %d differs after round trip", i)
		}
	}
}

// piece is the orders team's piece of the example flow: it sends three
// fields to the shipping-quote call and reads the fee back.
func piece(t *testing.T) *eval.Engine {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "examples", "pieces", "2-orders-team"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// callWhen has a field sent to a call only when the order is express.
const callWhen = `
sheet   = "call-when"
version = 1

[[row]]
id     = "amount"
source = "request.amount"
type   = "number"

[[row]]
id     = "express"
source = "request.express"
type   = "boolean"

  [[row.send]]
  target = "quote"
  field  = "express"
  usage  = "S"
  when   = "express"

[[row]]
id     = "fee"
source = "call.quote.fee"
type   = "number"

[[row]]
id      = "total"
uses    = ["amount", "fee"]
formula = "amount + fee"

  [row.send]
  field = "total"
  usage = "R"
`

func parsed(t *testing.T, text string) *eval.Engine {
	t.Helper()
	dir := t.TempDir()
	s, err := sheet.Parse(dir, filepath.Join(dir, sheet.FileName), text)
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCallsInCases(t *testing.T) {
	e := piece(t)
	ref, _ := time.Parse(time.RFC3339, DefaultReferenceTime)
	res := Generate(e, ref)
	if len(res.Uncovered) > 0 {
		t.Fatalf("uncovered targets: %+v", res.Uncovered)
	}
	// zone is sent to the call, so it is checked directly, not through an
	// isolation pair.
	for _, r := range IsolationRows(e) {
		if r.ID == "zone" {
			t.Error("zone is an isolation row although it is sent to shipping-quote")
		}
	}
	for _, c := range res.Cases {
		if c.ExpectedReject != "" {
			if len(c.Calls) > 0 {
				t.Errorf("%s: rejected case expects calls %v", c.ID, c.Calls)
			}
			continue
		}
		if len(c.Calls) != 1 || c.Calls[0].Target != "shipping-quote" {
			t.Fatalf("%s: calls = %+v; want one to shipping-quote", c.ID, c.Calls)
		}
		got := Flatten(e.Evaluate(eval.Input(c.Input)).CallPayloads["shipping-quote"])
		var names []string
		for _, f := range c.Calls[0].Fields {
			names = append(names, f.Name)
			if !sheet.Equal(got[f.Name], f.Value) {
				t.Errorf("%s: %s = %v, expected %v", c.ID, f.Name, got[f.Name], f.Value)
			}
		}
		if !slices.Equal(names, []string{"method", "weightKg", "zone"}) {
			t.Errorf("%s: call fields %v", c.ID, names)
		}
	}

	path := filepath.Join(t.TempDir(), FileName)
	var inputs []*sheet.Row
	for _, r := range e.Graph.Order {
		if r.IsInput() {
			inputs = append(inputs, r)
		}
	}
	if err := Write(path, e.Sheet, inputs, ref, res.Cases); err != nil {
		t.Fatal(err)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range f.Cases {
		want := res.Cases[i].Calls
		if len(c.Calls) != len(want) {
			t.Fatalf("%s: read back %d calls, want %d", c.ID, len(c.Calls), len(want))
		}
		for j := range want {
			if c.Calls[j].Target != want[j].Target || len(c.Calls[j].Fields) != len(want[j].Fields) {
				t.Errorf("%s: call %d differs after round trip: %+v", c.ID, j, c.Calls[j])
				continue
			}
			for k, fl := range want[j].Fields {
				if g := c.Calls[j].Fields[k]; g.Name != fl.Name || !sheet.Equal(g.Value, fl.Value) {
					t.Errorf("%s: field %s = %v after round trip, want %v", c.ID, g.Name, g.Value, fl.Value)
				}
			}
		}
	}
}

func TestSituationalCallSend(t *testing.T) {
	e := parsed(t, callWhen)
	ref, _ := time.Parse(time.RFC3339, DefaultReferenceTime)
	res := Generate(e, ref)
	var keys []string
	for _, tg := range res.Targets {
		keys = append(keys, tg.Key)
	}
	for _, k := range []string{"express:send.quote.when:true", "express:send.quote.when:false"} {
		if !slices.Contains(keys, k) {
			t.Errorf("target %s missing from %v", k, keys)
		}
	}
	if len(res.Uncovered) > 0 {
		t.Fatalf("uncovered targets: %+v", res.Uncovered)
	}
	ids := map[string]Case{}
	for _, c := range res.Cases {
		ids[c.ID] = c
	}
	sent, ok := ids["express-sent-to-quote"]
	if !ok {
		t.Fatalf("no case express-sent-to-quote among %v", ids)
	}
	if len(sent.Calls) != 1 || len(sent.Calls[0].Fields) != 1 || sent.Calls[0].Fields[0] != (Field{"express", true}) {
		t.Errorf("express-sent-to-quote calls = %+v", sent.Calls)
	}
	if c, ok := ids["express-not-sent-to-quote"]; !ok || len(c.Calls) != 0 {
		t.Errorf("express-not-sent-to-quote = %+v (found %t)", c, ok)
	}
}
