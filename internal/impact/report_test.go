package impact

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

func sample() *Report {
	return &Report{Self: "shipping", Checked: 2, New: []Finding{{Repo: "orders", Flow: "orders:flows/checkout-to-payment",
		Interface: "quote-response", Owner: "Shipping team", Where: "stage shipping-quote", Field: "fee",
		Status: flow.Missing, Message: "missing; needed by Shipping cost (Orders team)"}}}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	if err := sample().WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	want := []string{
		"quote-response (Shipping team) no longer carries fee\n",
		"  orders:flows/checkout-to-payment, stage shipping-quote: needed by Shipping cost (Orders team)\n",
		"1 new problem in 2 checked flows\n",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(out[at:], w)
		if i < 0 {
			t.Fatalf("output lacks %q after position %d:\n%s", w, at, out)
		}
		at += i + len(w)
	}
}

func TestWriteTextSections(t *testing.T) {
	r := &Report{Self: "shipping", Checked: 1,
		Resolved:   []Finding{{Flow: "orders:flows/a", Where: "stage q", Interface: "quote-request", Field: "zone", Status: flow.Missing}},
		Pinned:     []Pinned{{Repo: "orders", Flow: "orders:flows/b", Ref: "v1"}},
		NotChecked: []Skipped{{Repo: "billing", Reason: "git fetch: not found"}},
		Failing:    []Skipped{{Repo: "orders", Flow: "orders:flows/c", Reason: "computes \"x\""}},
	}
	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, w := range []string{"Resolved by this change", "orders:flows/a", "Pinned, not affected yet", "orders:flows/b pins shipping to v1",
		"Not checked", "billing: git fetch: not found", "Already failing before this change", "orders:flows/c: computes", "No new problems in 1 checked flow"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := sample().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Report
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.ExitCode != 1 || len(back.New) != 1 || back.New[0].Field != "fee" || back.Resolved == nil {
		t.Errorf("round trip = %+v\n%s", back, buf.String())
	}
}

func TestWriteHTML(t *testing.T) {
	f, err := flow.Load(filepath.Join(repoRoot, "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	r := sample()
	r.Views = map[string]*flow.Analysis{r.New[0].Flow: flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))}
	var buf bytes.Buffer
	if err := r.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, w := range []string{"<!doctype html>", "orders", "no longer carries fee", "srcdoc=", "Checkout to payment"} {
		if !strings.Contains(out, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}

// A value the flow newly needs, which the interface never carried, is not
// reported as a field the interface lost.
func TestWriteTextNewNeed(t *testing.T) {
	r := &Report{Self: "shipping", Checked: 1, New: []Finding{{Repo: "orders", Flow: "orders:flows/checkout-to-payment",
		Interface: "order-placed", Owner: "Orders team", Where: "stage order-placed", Field: "giftWrap",
		Status: flow.Missing, Cause: CauseNeeded, Message: "missing; needed by Gift wrap (Orders team); ask Orders team to add it"}}}
	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "orders:flows/checkout-to-payment now needs giftWrap, which order-placed (Orders team) does not carry\n") ||
		strings.Contains(out, "no longer carries") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCauseOf(t *testing.T) {
	f, err := flow.Load(filepath.Join(repoRoot, "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	before := flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))
	cases := map[string]string{"weightKg": CauseRemoved, "giftWrap": CauseNeeded}
	for field, want := range cases {
		got := causeOf(before, Finding{Interface: "order-placed", Where: "stage order-placed", Field: field, Status: flow.Missing})
		if got != want {
			t.Errorf("causeOf(%s) = %q; want %q", field, got, want)
		}
	}
}
