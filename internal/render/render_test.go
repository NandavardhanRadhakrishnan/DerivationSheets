package render

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/evidence"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/verify"
)

func engine(t *testing.T) *eval.Engine {
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

func renderPage(t *testing.T, e *eval.Engine, run *evidence.Run) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, Build(e, Options{Run: run, Now: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)})); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// rowHTML returns one row, up to and including its evidence cell. Rule
// cells may hold nested tables, so it cannot stop at the first </tr>.
func rowHTML(page, id string) string {
	re := regexp.MustCompile(`(?s)<tr class="row[^"]*" id="row-` + regexp.QuoteMeta(id) + `".*?<td class="ev[^>]*>.*?</td>`)
	return re.FindString(page)
}

func TestLayout(t *testing.T) {
	e := engine(t)
	page := renderPage(t, e, nil)
	// Sections in order; the config row feeding one line is folded into it.
	order := []string{"From the request", "From context", "Derived"}
	last := -1
	for _, s := range order {
		i := strings.Index(page, ">"+s+"<")
		if i < last {
			t.Errorf("section %q out of order", s)
		}
		last = i
	}
	if strings.Contains(page, "From database and config") {
		t.Error("db/config section shown although no source feeds more than one line")
	}
	if rowHTML(page, "sales_tax_rate") != "" {
		t.Error("sales_tax_rate shown as its own line; it feeds only sales tax")
	}
	if !strings.Contains(rowHTML(page, "sales_tax"), "tax.sales_tax_rate = 0.08") {
		t.Error("sales tax rule lacks the config source tag")
	}
	// Every dot is left of the staircase: no dot in a shaded cell.
	if strings.Contains(page, `class="g shaded" title=`) {
		t.Error("a dependency dot falls in the shaded staircase")
	}
	// Row IDs are visible, rule forms match the spec.
	for _, want := range []string{
		`<code>loyalty_tier</code>`, `class="g2"`, "first match", "years_between", "reject <code>DESTINATION_NOT_SERVED</code>",
		"customsValue", "required when", "otherwise do not send", "18 entries in data/shipping_rates.csv",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(page, "◇") {
		t.Error("no asserted glyph without evidence")
	}
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") || strings.Contains(page, "<link rel=\"stylesheet\"") {
		t.Error("page references the network")
	}
}

func TestEvidenceMerged(t *testing.T) {
	e := engine(t)
	ref, _ := time.Parse(time.RFC3339, cases.DefaultReferenceTime)
	gen := cases.Generate(e, ref)
	cf := &cases.File{Sheet: e.Sheet.Slug, Version: e.Sheet.Version, Cases: gen.Cases}
	results := map[string]verify.Actual{}
	for _, c := range cf.Cases {
		if c.ExpectedReject != "" {
			results[c.ID] = verify.Actual{Reject: c.ExpectedReject}
			continue
		}
		p := map[string]any{}
		for _, f := range c.Expected {
			p[f.Name] = f.Value
		}
		if c.ID == "loyalty-tier-gold" {
			p["loyaltyTier"] = "SLV"
		}
		results[c.ID] = verify.Actual{Payload: p}
	}
	// Drop the reject case, so the reject branch is never exercised.
	delete(results, "reject-destination-not-served")
	rep := verify.Verify(e, cf, results, "results.json", time.Now())
	page := renderPage(t, e, rep.Run)

	tier := rowHTML(page, "loyalty_tier")
	if !strings.Contains(tier, `class="ev evd contradicted"`) || !strings.Contains(tier, "✕") {
		t.Error("loyalty_tier row not shown as contradicted")
	}
	if !regexp.MustCompile(`<td class="contradicted" title="rule 4">Gold <span class="mark evd contradicted"`).MatchString(tier) {
		t.Error("contradicted cell [true,>= 2] not marked inside the grid")
	}
	if zone := rowHTML(page, "zone"); !strings.Contains(zone, `class="ev evd partly"`) || !strings.Contains(zone, "◐") || !strings.Contains(zone, "mark evd unexercised") {
		t.Error("zone row should be partly verified with its reject marked unexercised")
	}
	if !strings.Contains(page, "case <code>loyalty-tier-gold</code>, field <code>loyaltyTier</code>") {
		t.Error("contradiction note does not name case and field")
	}
	if !strings.Contains(page, "Tests: 6 cases from results.json") {
		t.Error("header lacks the evidence line")
	}
}

func TestEvidenceSwitch(t *testing.T) {
	e := engine(t)
	page := func(mode string) string {
		var buf bytes.Buffer
		if err := Write(&buf, Build(e, Options{Evidence: mode, Run: &evidence.Run{}})); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	shown, hidden, off := page("shown"), page("hidden"), page(EvidenceOff)
	if !strings.Contains(shown, `class="ev-toggle" checked>`) {
		t.Error("shown: switch not checked")
	}
	if !strings.Contains(hidden, `class="ev-toggle">`) || strings.Contains(hidden, `class="ev-toggle" checked`) {
		t.Error("hidden: switch should be present and unchecked")
	}
	// Hidden evidence is still in the page, behind the switch.
	if !strings.Contains(hidden, `<th class="ev evd">Evidence</th>`) {
		t.Error("hidden: evidence column missing")
	}
	if strings.Contains(off, `class="ev-toggle"`) || strings.Contains(off, `class="ev evd`) || strings.Contains(off, "Asserted</dt>") {
		t.Error("off: evidence should be absent")
	}
	// The sheet's own setting applies when no mode is given.
	e.Sheet.Display.Evidence = "hidden"
	if strings.Contains(page(""), `class="ev-toggle" checked`) {
		t.Error("display.evidence = hidden not applied")
	}
}

func TestConfiguredLabels(t *testing.T) {
	e := engine(t)
	e.Sheet.Display = sheet.DefaultDisplay.Merge(sheet.Display{
		Columns:  map[string]string{"send": "Sent to billing as", "value": "Field"},
		Sections: map[string]string{"derived": "Calculated"},
	})
	page := renderPage(t, e, nil)
	for _, want := range []string{`<th class="send">Sent to billing as</th>`, `<th class="value">Field</th>`, `<th class="rule">Rule</th>`, `>Calculated</th>`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, "<th class=\"send\">Sent as</th>") {
		t.Error("default send label still present")
	}
}
