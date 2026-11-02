package flowrender

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"derivationsheets/internal/flow"
	"derivationsheets/internal/sources"
)

func page(t *testing.T, check bool) string {
	t.Helper()
	f, err := flow.Load(filepath.Join("..", "..", "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	a := flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources))
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	var buf bytes.Buffer
	if err := Write(&buf, Build(a, time.Now(), check)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The review markup, which only check mode shows.
var review = []string{"To agree", "Fields this flow does not use", "add <code>shippingMethod</code>",
	`class="count missing"`, "2 missing", "⚠"}

func TestPlainView(t *testing.T) {
	out := page(t, false)
	for _, w := range review {
		if strings.Contains(out, w) {
			t.Errorf("plain view contains %q", w)
		}
	}
	// It still shows the flow: boundaries, teams, carried fields, rules.
	for _, w := range []string{"Handoff", "<code>order-placed</code>", "Caller (continued)", "<code>weightKg</code>",
		"<code>fee</code>", "↷ kept", "decision table on"} {
		if !strings.Contains(out, w) {
			t.Errorf("plain view lacks %q", w)
		}
	}
}

func TestCheckView(t *testing.T) {
	out := page(t, true)
	for _, w := range review {
		if !strings.Contains(out, w) {
			t.Errorf("check view lacks %q", w)
		}
	}
}

func TestCombinePieces(t *testing.T) {
	f, err := flow.Load(filepath.Join("..", "..", "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	main := flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources))
	pieces, err := main.Export(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var as []*flow.Analysis
	for _, p := range pieces {
		pf, err := flow.Load(p.Dir)
		if err != nil {
			t.Fatal(err)
		}
		as = append(as, flow.Analyze(pf, sources.NewResolver(pf.Dir, pf.Sources)))
	}
	p, err := BuildCombined("", as, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	var eyebrows []string
	for _, c := range p.Columns {
		eyebrows = append(eyebrows, c.Eyebrow)
	}
	want := "Input,Step 1,Handoff,Step 2,Call request,Step 3,Call response,Step 4,Output"
	if strings.Join(eyebrows, ",") != want {
		t.Errorf("columns = %v, want %s", eyebrows, want)
	}
	seen := map[string]bool{}
	for _, g := range p.Groups {
		for _, r := range g.Rows {
			if seen[r.Name] {
				t.Errorf("value %q appears twice", r.Name)
			}
			seen[r.Name] = true
		}
	}
	if len(seen) != 18 {
		t.Errorf("%d values, want 18 as in the end-to-end flow", len(seen))
	}
}
