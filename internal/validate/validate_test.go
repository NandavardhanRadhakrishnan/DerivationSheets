package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

var fixtureDir = filepath.Join("..", "..", "specs", "order-checkout")

func fixtureText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, sheet.FileName))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func run(t *testing.T, text string) *Report {
	t.Helper()
	s, err := sheet.Parse(fixtureDir, filepath.Join(fixtureDir, sheet.FileName), text)
	if err != nil {
		t.Fatal(err)
	}
	return Validate(s)
}

func TestFixtureIsValid(t *testing.T) {
	rep := run(t, fixtureText(t))
	for _, d := range rep.Diagnostics {
		t.Log(d)
	}
	if !rep.OK() {
		t.Fatalf("fixture has %d errors", rep.Count(Error))
	}
	if rep.Count(Warning) != 0 {
		t.Errorf("fixture has %d warnings", rep.Count(Warning))
	}
}

// Each mutation introduces one of the validator errors from the handoff, and
// must produce a diagnostic with the expected code on the expected line.
func TestMutations(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		code     string
		sev      Severity
		line     string // text on the reported line, when the line matters
	}{
		{"duplicate id", `id     = "country"`, `id     = "is_member"`, "id-duplicate", Error, `id     = "is_member"`},
		{"malformed id", `id     = "country"`, `id     = "Country-Code"`, "id-malformed", Error, `"Country-Code"`},
		{"undefined in uses", `uses   = ["country"]`, `uses   = ["contry"]`, "uses-undefined", Error, `uses   = ["contry"]`},
		{"undefined in expression", `formula = "net_amount + sales_tax"`, `formula = "net_amount + sales_taxx"`, "ref-undefined", Error, `sales_taxx`},
		{"undefined in lookup key", `key = ["country"] }`, `key = ["nation"] }`, "ref-undefined", Error, `key = ["nation"]`},
		{"undefined in table column", `columns = ["is_member", "customer_years", "=>"]`, `columns = ["is_membr", "customer_years", "=>"]`, "ref-undefined", Error, `is_membr`},
		{"expression identifier missing from uses", `uses    = ["net_amount", "sales_tax"]`, `uses    = ["net_amount"]`, "ref-not-in-uses", Error, `formula = "net_amount + sales_tax"`},
		{"circular dependency", `uses = ["is_member", "customer_years"]`, `uses = ["is_member", "customer_years", "free_shipping"]`, "cycle", Error, ""},
		{"no rule", `formula = "net_amount + sales_tax"`, ``, "rule-missing", Error, ""},
		{"several rules", `formula = "net_amount + sales_tax"`, "formula = \"net_amount + sales_tax\"\ntext = \"Add them\"", "rule-multiple", Error, `text = "Add them"`},
		{"table missing case", `    [true,  ">= 2", "Gold"],` + "\n", "", "table-missing-case", Error, ""},
		{"table overlap", `[true,  "< 2",  "Silver"],`, `[true,  "-",    "Silver"],`, "table-overlap", Error, ""},
		{"situational without when", `  when      = "zone != \"DOMESTIC\""` + "\n", "", "situational-no-when", Error, ""},
		{"situational without otherwise", `  otherwise = "do-not-send"` + "\n", "", "situational-no-otherwise", Error, ""},
		{"unused row", `formula = "net_amount + sales_tax"

  [row.send]
  field = "totalAmount"
  usage = "R"`, `formula = "net_amount + sales_tax"`, "unused", Error, `id      = "total"`},
		{"text rule", `formula = "net_amount + sales_tax"`, `text = "Net amount plus sales tax"`, "text-rule", Warning, `text = `},
		{"open question", `name   = "Shipping zone"`, "name   = \"Shipping zone\"\nopen_questions = [\"Which zone for new countries?\"]", "open-question", Warning, `open_questions`},
		{"schema: bad display", `title   = "Online order checkout"`, `title   = "Online order checkout"

[display]
evidence = "maybe"`, "schema", Error, `evidence = "maybe"`},
		{"schema: unknown display column", `title   = "Online order checkout"`, `title   = "Online order checkout"

[display.columns]
send = "Sent to billing as"
sent = "typo"`, "schema", Error, `[display.columns]`},
		{"schema: bad usage", `  usage = "R"
  map   = { Standard`, `  usage = "X"
  map   = { Standard`, "schema", Error, `usage = "X"`},
	}
	base := fixtureText(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(base, tc.old) {
				t.Fatalf("fixture does not contain %q", tc.old)
			}
			text := strings.Replace(base, tc.old, tc.new, 1)
			rep := run(t, text)
			lines := strings.Split(text, "\n")
			var found *Diagnostic
			for i, d := range rep.Diagnostics {
				if d.Code == tc.code && d.Severity == tc.sev {
					found = &rep.Diagnostics[i]
					break
				}
			}
			if found == nil {
				for _, d := range rep.Diagnostics {
					t.Log(d)
				}
				t.Fatalf("no %s %q diagnostic", tc.sev, tc.code)
			}
			t.Log(found)
			if found.Line == 0 {
				t.Errorf("diagnostic has no line")
			} else if tc.line != "" && !strings.Contains(lines[found.Line-1], tc.line) {
				t.Errorf("line %d is %q, want a line containing %q", found.Line, lines[found.Line-1], tc.line)
			}
			if tc.sev == Error && rep.OK() {
				t.Error("report is OK despite an error")
			}
		})
	}
}

func TestTargetSchema(t *testing.T) {
	dir := t.TempDir()
	schema := `{
	  "type": "object",
	  "required": ["shippingMethod", "netAmount", "currency"],
	  "properties": {
	    "shippingMethod": {"type": "string"}, "subtotal": {"type": "number"},
	    "loyaltyTier": {"type": "string"}, "discountAmount": {"type": "number"},
	    "freeShipping": {"type": "boolean"}, "shippingCost": {"type": "number"},
	    "customsValue": {"type": "number"}, "netAmount": {"type": "number"},
	    "salesTax": {"type": "number"}, "currency": {"type": "string"}
	  }
	}`
	if err := os.WriteFile(filepath.Join(dir, "target.json"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(fixtureText(t), `title   = "Online order checkout"`,
		`title   = "Online order checkout"`+"\ntarget_schema = "+quote(filepath.ToSlash(filepath.Join(dir, "target.json"))), 1)
	rep := run(t, text)
	var unknown, unsent bool
	for _, d := range rep.Diagnostics {
		t.Log(d)
		if d.Code == "send-field-unknown" && strings.Contains(d.Message, "totalAmount") {
			unknown = true
		}
		if d.Code == "target-required-unsent" && strings.Contains(d.Message, "currency") {
			unsent = true
		}
	}
	if !unknown || !unsent {
		t.Errorf("unknown field reported: %v; required unsent field reported: %v", unknown, unsent)
	}
}

func quote(s string) string { return `"` + s + `"` }

// inputRejects adds a reject on the weight input that checks only its own
// value, and one on the subtotal input that also names another input.
func inputRejects(text string) string {
	text = strings.Replace(text, `source = "request.order.weightKg"
type   = "number"
`, `source = "request.order.weightKg"
type   = "number"

  [[row.reject]]
  when    = "weight_kg > 30"
  code    = "OVERWEIGHT"
  message = "Parcels over 30 kg are not shipped"
`, 1)
	return strings.Replace(text, `source = "request.order.subtotal"
type   = "number"
`, `source = "request.order.subtotal"
type   = "number"

  [[row.reject]]
  when    = "subtotal > 1000 && !is_member"
  code    = "NON_MEMBER_LIMIT"
  message = "Orders over 1000 need a membership"
`, 1)
}

func TestInputRejects(t *testing.T) {
	base := fixtureText(t)
	text := inputRejects(base)
	if text == base || strings.Count(text, "[[row.reject]]") != strings.Count(base, "[[row.reject]]")+2 {
		t.Fatal("fixture inputs changed; update inputRejects")
	}
	rep := run(t, text)
	for _, d := range rep.Diagnostics {
		t.Errorf("unexpected diagnostic: %v", d)
	}

	t.Run("undefined name", func(t *testing.T) {
		rep := run(t, strings.Replace(text, `!is_member"`, `!is_membr"`, 1))
		var found bool
		for _, d := range rep.Diagnostics {
			t.Log(d)
			if d.Code == "ref-undefined" && strings.Contains(d.Message, "reject.0.when") && strings.Contains(d.Message, "is_membr") {
				found = true
			}
		}
		if !found {
			t.Error("no ref-undefined diagnostic for the input's reject")
		}
	})
	t.Run("derived row", func(t *testing.T) {
		// zone is derived, so the check belongs on a derived row's reject.
		rep := run(t, strings.Replace(text, `"weight_kg > 30"`, `"weight_kg > 30 && zone != 'DOMESTIC'"`, 1))
		var found bool
		for _, d := range rep.Diagnostics {
			t.Log(d)
			if d.Code == "input-reject-derived" && d.Severity == Error && d.Row == "weight_kg" &&
				strings.Contains(d.Message, `"zone"`) && strings.Contains(d.Message, "reject on a derived row") {
				found = true
			}
		}
		if !found {
			t.Error("no input-reject-derived diagnostic for the weight reject naming zone")
		}
	})
	t.Run("when may name a derived row", func(t *testing.T) {
		// An input's when keeps allowing any row; only its rejects are limited.
		rep := run(t, strings.Replace(text, `source = "request.order.weightKg"
type   = "number"
`, `source = "request.order.weightKg"
type   = "number"
usage  = "S"
when   = "zone != 'DOMESTIC'"
`, 1))
		for _, d := range rep.Diagnostics {
			t.Errorf("unexpected diagnostic: %v", d)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		// net_amount is derived from subtotal, so subtotal's when cannot
		// wait for it.
		rep := run(t, strings.Replace(text, `source = "request.order.subtotal"
type   = "number"
`, `source = "request.order.subtotal"
type   = "number"
usage  = "S"
when   = "net_amount > 900"
`, 1))
		var found bool
		for _, d := range rep.Diagnostics {
			t.Log(d)
			found = found || d.Code == "cycle"
		}
		if !found {
			t.Error("no cycle diagnostic")
		}
	})
}
