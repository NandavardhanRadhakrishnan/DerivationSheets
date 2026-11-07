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
