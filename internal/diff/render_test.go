package diff

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sampleReport() *Report {
	return &Report{
		Repo: "app", Base: "abc1234", Head: "def5678",
		Sheets: []SheetChange{{
			Name: "demo", Path: "specs/demo/sheet.toml", Status: Changed,
			Rows: []RowChange{{ID: "net", Status: Changed, Fields: []FieldChange{
				{Path: "formula", Before: "a - b", After: "a - c"},
				{Path: "send.field", After: "netAmount"},
				{Path: "name", Before: "Net", After: "Net amount", Label: true},
			}}},
		}},
	}
}

func render(t *testing.T, r *Report, all bool) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.WriteMarkdown(&buf, all); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func renderText(t *testing.T, r *Report, all, color bool) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.WriteText(&buf, all, color); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestWriteMarkdownRowChange(t *testing.T) {
	want := "## dsheets diff: app abc1234..def5678\n\n" +
		"### Sheet `demo` - specs/demo/sheet.toml\n\n" +
		"```diff\n" +
		"  net (changed)\n" +
		"- formula: a - b\n" +
		"+ formula: a - c\n" +
		"+ send.field: netAmount\n" +
		"```\n\n"
	if got := render(t, sampleReport(), false); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestWriteMarkdownLabelsOnlyWithAll(t *testing.T) {
	got := render(t, sampleReport(), true)
	if !strings.Contains(got, "- name: Net\n+ name: Net amount\n") {
		t.Fatalf("label change missing with all:\n%s", got)
	}
	if strings.Contains(render(t, sampleReport(), false), "Net amount") {
		t.Fatal("label change shown without all")
	}
}

func TestWriteMarkdownHidesSheetWithOnlyLabelChanges(t *testing.T) {
	r := &Report{Repo: "app", Base: "a", Head: "b", Sheets: []SheetChange{{
		Name: "demo", Path: "p", Status: Changed,
		Rows: []RowChange{{ID: "net", Status: Changed, Fields: []FieldChange{{Path: "name", Before: "x", After: "y", Label: true}}}},
	}}}
	if got := render(t, r, false); !strings.Contains(got, "No sheet or flow differences.") || strings.Contains(got, "demo") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestWriteMarkdownTextOnlyNeedsAll(t *testing.T) {
	r := &Report{Repo: "app", Base: "a", Head: "b", Sheets: []SheetChange{{Name: "demo", Path: "p", Status: Changed, TextOnly: true}}}
	if got := render(t, r, false); !strings.Contains(got, "No sheet or flow differences.") {
		t.Fatalf("got:\n%s", got)
	}
	if got := render(t, r, true); !strings.Contains(got, "Formatting or comments only.") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestWriteMarkdownAddedRemovedAndUnparseable(t *testing.T) {
	r := &Report{Repo: "app", Base: "a", Head: "b", Sheets: []SheetChange{
		{Name: "new", Path: "n/sheet.toml", Status: Added, Rows: []RowChange{{ID: "r", Status: Added, Fields: []FieldChange{{Path: "formula", After: "1"}}}}},
		{Name: "gone", Path: "g/sheet.toml", Status: Removed, Rows: []RowChange{{ID: "s", Status: Removed, Fields: []FieldChange{{Path: "formula", Before: "2"}}}}},
		{Name: "bad", Path: "b/sheet.toml", Status: Changed, Unparseable: "head"},
		{Name: "moved", Path: "z/sheet.toml", OldPath: "y/sheet.toml", Status: Changed},
	}}
	got := render(t, r, false)
	for _, want := range []string{
		"### Sheet `new` - n/sheet.toml (added)", "  r (added)\n+ formula: 1\n",
		"  s (removed)\n- formula: 2\n",
		"not valid TOML at head", "moved from y/sheet.toml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWriteMarkdownFlow(t *testing.T) {
	r := &Report{Repo: "app", Base: "a", Head: "b", Flows: []FlowChange{{
		Name: "f", Path: "flows/f/flow.toml", Status: Changed,
		Boundaries: []BoundaryChange{{Where: "stage ship response", Added: []string{"tax"}, Dropped: []string{"zip"},
			Rewired: []FieldChange{{Path: "fee", Before: "rate", After: "rate2"}}}},
		Computes: []ComputeChange{{Row: "fee", Before: "Orders", After: "Shipping"}, {Row: "audit", After: "Orders"}},
	}}}
	got := render(t, r, false)
	for _, want := range []string{
		"### Flow `f` - flows/f/flow.toml",
		"  stage ship response\n",
		"+ carries: tax\n",
		"- carries: zip\n",
		"- carries fee: rate\n+ carries fee: rate2\n",
		"  who computes what\n",
		"- fee: Orders\n+ fee: Shipping\n",
		"+ audit: Orders\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- audit") {
		t.Errorf("a row nobody computed before has no removed line:\n%s", got)
	}
}

func TestWriteMarkdownFlowMeta(t *testing.T) {
	r := &Report{Repo: "app", Base: "a", Head: "b", Flows: []FlowChange{{
		Name: "f", Path: "flows/f/flow.toml", Status: Changed,
		Meta: []FieldChange{
			{Path: "sheet", Before: ".", After: "../moved"},
			{Path: "title", Before: "Old", After: "New", Label: true},
		},
	}}}
	got := render(t, r, false)
	if !strings.Contains(got, "- sheet: .\n+ sheet: ../moved\n") || strings.Contains(got, "New") {
		t.Fatalf("without --all:\n%s", got)
	}
	if got := render(t, r, true); !strings.Contains(got, "+ title: New") {
		t.Fatalf("with --all:\n%s", got)
	}
}

func TestWriteMarkdownEmpty(t *testing.T) {
	want := "## dsheets diff: app abc1234..def5678\n\nNo sheet or flow differences.\n"
	r := &Report{Repo: "app", Base: "abc1234", Head: "def5678"}
	if got := render(t, r, false); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestWriteMarkdownKeepsEachValueOnOneLine(t *testing.T) {
	r := &Report{Repo: "app", Base: "a", Head: "b", Sheets: []SheetChange{{
		Name: "demo", Path: "p", Status: Changed,
		Rows: []RowChange{{ID: "net", Status: Changed, Fields: []FieldChange{{Path: "formula", Before: "a\nb", After: "a\r\nc"}}}},
	}}}
	got := render(t, r, false)
	if !strings.Contains(got, "- formula: a\\nb\n+ formula: a\\r\\nc\n") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestWriteTextHasNoFences(t *testing.T) {
	got := renderText(t, sampleReport(), false, false)
	if strings.Contains(got, "```") || strings.Contains(got, "\x1b[") {
		t.Fatalf("fences or escape codes in plain terminal text:\n%q", got)
	}
	if !strings.Contains(got, "  net (changed)\n- formula: a - b\n+ formula: a - c\n") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestWriteTextColorsRemovedRedAndAddedGreen(t *testing.T) {
	got := renderText(t, sampleReport(), false, true)
	for _, want := range []string{
		"\x1b[31m- formula: a - b\x1b[0m\n",
		"\x1b[32m+ formula: a - c\x1b[0m\n",
		"  net (changed)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%q", want, got)
		}
	}
	if strings.Contains(got, "```") {
		t.Errorf("fence in colored terminal text:\n%q", got)
	}
}

func TestWriteJSONRoundTrip(t *testing.T) {
	r := sampleReport()
	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, &back) {
		t.Fatalf("round trip:\n%+v\n%+v", r, &back)
	}
	if !strings.Contains(buf.String(), `"base": "abc1234"`) {
		t.Fatalf("json keys are not snake_case lower:\n%s", buf.String())
	}
}
