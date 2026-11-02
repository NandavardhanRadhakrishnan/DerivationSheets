package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"derivationsheets/internal/sheet"
)

var fixtureDir = filepath.Join("..", "..", "specs", "premium-calculation")

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
		{"duplicate id", `id     = "city"`, `id     = "dob"`, "id-duplicate", Error, `id     = "dob"`},
		{"malformed id", `id     = "city"`, `id     = "City-Name"`, "id-malformed", Error, `"City-Name"`},
		{"undefined in uses", `uses   = ["city"]`, `uses   = ["cty"]`, "uses-undefined", Error, `uses   = ["cty"]`},
		{"undefined in expression", `formula = "net_premium + gst"`, `formula = "net_premium + gsst"`, "ref-undefined", Error, `gsst`},
		{"undefined in lookup key", `key = ["city"] }`, `key = ["town"] }`, "ref-undefined", Error, `key = ["town"]`},
		{"undefined in table column", `columns = ["is_smoker", "has_ped", "=>"]`, `columns = ["is_smoker", "has_pd", "=>"]`, "ref-undefined", Error, `has_pd`},
		{"expression identifier missing from uses", `uses    = ["net_premium", "gst"]`, `uses    = ["net_premium"]`, "ref-not-in-uses", Error, `formula = "net_premium + gst"`},
		{"circular dependency", `uses = ["is_smoker", "has_ped"]`, `uses = ["is_smoker", "has_ped", "medical_check"]`, "cycle", Error, ""},
		{"no rule", `formula = "net_premium + gst"`, ``, "rule-missing", Error, ""},
		{"several rules", `formula = "net_premium + gst"`, "formula = \"net_premium + gst\"\ntext = \"Add them\"", "rule-multiple", Error, `text = "Add them"`},
		{"table missing case", `    [true,  true,  "High"],` + "\n", "", "table-missing-case", Error, ""},
		{"table overlap", `[false, true,  "Elevated"],`, `["-",   true,  "Elevated"],`, "table-overlap", Error, ""},
		{"situational without when", `  when      = "has_ped == true"` + "\n", "", "situational-no-when", Error, ""},
		{"situational without otherwise", `  otherwise = "do-not-send"` + "\n", "", "situational-no-otherwise", Error, ""},
		{"unused row", `formula = "net_premium + gst"

  [row.send]
  field = "totalPayable"
  usage = "R"`, `formula = "net_premium + gst"`, "unused", Error, `id      = "total_payable"`},
		{"text rule", `formula = "net_premium + gst"`, `text = "Net premium plus GST"`, "text-rule", Warning, `text = `},
		{"open question", `name   = "Zone"`, "name   = \"Zone\"\nopen_questions = [\"Which zone for new cities?\"]", "open-question", Warning, `open_questions`},
		{"schema: bad display", `title   = "Health policy premium"`, `title   = "Health policy premium"

[display]
evidence = "maybe"`, "schema", Error, `evidence = "maybe"`},
		{"schema: unknown display column", `title   = "Health policy premium"`, `title   = "Health policy premium"

[display.columns]
send = "Sent to Acme as"
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
	  "required": ["planCode", "netPremium", "policyTerm"],
	  "properties": {
	    "planCode": {"type": "string"}, "sumInsured": {"type": "number"},
	    "riskClassCode": {"type": "string"}, "waitingPeriodMonths": {"type": "number"},
	    "underwritingType": {"type": "string"}, "netPremium": {"type": "number"},
	    "gstAmount": {"type": "number"}, "policyTerm": {"type": "number"}
	  }
	}`
	if err := os.WriteFile(filepath.Join(dir, "partner.json"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(fixtureText(t), `title   = "Health policy premium"`,
		`title   = "Health policy premium"`+"\ntarget_schema = "+quote(filepath.ToSlash(filepath.Join(dir, "partner.json"))), 1)
	rep := run(t, text)
	var unknown, unsent bool
	for _, d := range rep.Diagnostics {
		t.Log(d)
		if d.Code == "send-field-unknown" && strings.Contains(d.Message, "totalPayable") {
			unknown = true
		}
		if d.Code == "target-required-unsent" && strings.Contains(d.Message, "policyTerm") {
			unsent = true
		}
	}
	if !unknown || !unsent {
		t.Errorf("unknown field reported: %v; required unsent field reported: %v", unknown, unsent)
	}
}

func quote(s string) string { return `"` + s + `"` }
