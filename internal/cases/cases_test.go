package cases

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"derivationsheets/internal/eval"
	"derivationsheets/internal/sheet"
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
