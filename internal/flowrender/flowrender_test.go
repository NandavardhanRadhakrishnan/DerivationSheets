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
