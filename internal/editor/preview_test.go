package editor

import (
	"strings"
	"testing"
)

func TestDraftEngineShowsEdit(t *testing.T) {
	dir, ed := workspace(t, false)
	before := onDisk(t, dir, sheetPath)
	e, reason, err := ed.DraftEngine(formulaDraft(t, ed, "subtotal * 0.5"))
	if err != nil || e == nil {
		t.Fatalf("engine %v, reason %q, err %v", e, reason, err)
	}
	if got := e.Sheet.ByID["discount"].Formula; got != "subtotal * 0.5" {
		t.Errorf("formula %q", got)
	}
	if onDisk(t, dir, sheetPath) != before {
		t.Error("file written")
	}
}

func TestDraftEngineNewRow(t *testing.T) {
	_, ed := workspace(t, false)
	f := load(t, ed, sheetPath)
	d := SheetDraft{File: sheetPath, Token: f.Token, Rows: map[string]RowData{
		"gift_note": {Name: "Gift note", Source: "request.order.giftNote", Type: "string"},
	}}
	e, reason, err := ed.DraftEngine(d)
	if err != nil || e == nil {
		t.Fatalf("engine %v, reason %q, err %v", e, reason, err)
	}
	if e.Sheet.ByID["gift_note"] == nil {
		t.Error("new row missing")
	}
}

func TestDraftEngineRefusesCycle(t *testing.T) {
	_, ed := workspace(t, false)
	// An unknown name is an error, but the draft can still be shown.
	if e, reason, err := ed.DraftEngine(formulaDraft(t, ed, "subtotal * coupon_rate")); err != nil || e == nil {
		t.Errorf("unknown name: engine %v, reason %q, err %v", e, reason, err)
	}
	d := formulaDraft(t, ed, "total * 0.1")
	r := d.Rows["discount"]
	r.Uses = []string{"total"} // the page fills uses from the formula
	d.Rows["discount"] = r
	e, reason, err := ed.DraftEngine(d)
	if err != nil {
		t.Fatal(err)
	}
	if e != nil || !strings.Contains(reason, "circular") {
		t.Errorf("engine %v, reason %q", e, reason)
	}
	// A draft that cannot be applied is the caller's fault, not a reason.
	f := load(t, ed, sheetPath)
	if _, _, err := ed.DraftEngine(SheetDraft{File: sheetPath, Token: f.Token, Deleted: []string{"ghost"}}); err == nil {
		t.Error("no error for a broken draft")
	}
}

// caseRow finds one row's result in one case.
func caseRow(t *testing.T, cs []CaseView, id, row string) CaseRow {
	t.Helper()
	for _, c := range cs {
		if c.ID == id {
			return c.Rows[row]
		}
	}
	t.Fatalf("no case %q among %d", id, len(cs))
	return CaseRow{}
}

func TestDraftCasesFormula(t *testing.T) {
	_, ed := workspace(t, false)
	cs, reason, err := ed.DraftCases(formulaDraft(t, ed, "round(subtotal * discount_rate) * 2"))
	if err != nil || reason != "" {
		t.Fatalf("reason %q, err %v", reason, err)
	}
	if got := caseRow(t, cs, "loyalty-tier-silver", "discount"); got.Value != "10" || got.None {
		t.Errorf("silver discount %+v, want 10 (twice 5)", got)
	}
	if got := caseRow(t, cs, "loyalty-tier-gold", "discount"); got.Value != "20" {
		t.Errorf("gold discount %+v, want 20", got)
	}
	if got := caseRow(t, cs, "loyalty-tier-gold", "is_member"); got.Value != "Yes" {
		t.Errorf("is_member %+v, want Yes", got)
	}
	if got := caseRow(t, cs, "loyalty-tier-gold", "loyalty_tier"); got.Value != "Gold" || got.Rule != 4 {
		t.Errorf("loyalty_tier %+v, want Gold by rule 4", got)
	}
}

func TestDraftCasesRejected(t *testing.T) {
	_, ed := workspace(t, false)
	cs, _, err := ed.DraftCases(formulaDraft(t, ed, "round(subtotal * discount_rate)"))
	if err != nil {
		t.Fatal(err)
	}
	if got := caseRow(t, cs, "reject-destination-not-served", "discount"); !got.None || got.Note != "rejected earlier (DESTINATION_NOT_SERVED)" {
		t.Errorf("discount %+v", got)
	}
	if got := caseRow(t, cs, "reject-destination-not-served", "zone"); got.None || got.Value != "NONE" || got.Note != "rejected here (DESTINATION_NOT_SERVED)" {
		t.Errorf("zone %+v", got)
	}
}

func TestDraftCasesLeaveOutDeletedRows(t *testing.T) {
	_, ed := workspace(t, false)
	f := load(t, ed, sheetPath)
	d := SheetDraft{File: sheetPath, Token: f.Token, Rows: map[string]RowData{}, Deleted: []string{"customs_value"}}
	cs, reason, err := ed.DraftCases(d)
	if err != nil || reason != "" || len(cs) == 0 {
		t.Fatalf("%d cases, reason %q, err %v", len(cs), reason, err)
	}
	for _, c := range cs {
		if _, ok := c.Rows["customs_value"]; ok {
			t.Fatalf("case %s has the deleted row", c.ID)
		}
	}
}
