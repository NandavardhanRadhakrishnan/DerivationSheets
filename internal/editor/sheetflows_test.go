package editor

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// inputDraft turns a derived row into an input row read from source,
// keeping its name and sends.
func inputDraft(t *testing.T, ed *Editor, id, source, typ string) SheetDraft {
	t.Helper()
	f := load(t, ed, sheetPath)
	r := rowOf(t, f.Data, id)
	r = RowData{ID: id, Name: r.Name, Source: source, Type: typ, Sends: r.Sends}
	return SheetDraft{File: sheetPath, Token: f.Token, Rows: map[string]RowData{id: r}}
}

func TestSaveSheetDropsStaleComputes(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := inputDraft(t, ed, "free_shipping", "request.order.freeShipping", "boolean")
	res, err := ed.SaveSheet(d)
	if err != nil || !res.Saved {
		t.Fatalf("%v %+v", err, res)
	}
	want := []FlowFix{{File: flowPath, Removed: []string{"free_shipping"}}}
	if !reflect.DeepEqual(res.Flows, want) {
		t.Fatalf("flows %+v", res.Flows)
	}
	if strings.Contains(onDisk(t, dir, flowPath), `"free_shipping"]`) {
		t.Fatal("computes still names free_shipping")
	}
	if _, ok := res.Tokens[flowPath]; !ok {
		t.Errorf("no token for %s: %v", flowPath, res.Tokens)
	}
	if !strings.Contains(onDisk(t, dir, flowPath), "version = 2") {
		t.Errorf("flow version not raised:\n%s", onDisk(t, dir, flowPath))
	}
	f, _ := flow.Load(filepath.Join(dir, "flows", "checkout-to-payment"))
	if a := flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, ed.fetcher())); len(a.Errors) > 0 {
		t.Fatalf("%v", a.Errors)
	}
}

func TestCheckSheetReportsFlowFixWithoutWriting(t *testing.T) {
	dir, ed := flowWorkspace(t)
	before := onDisk(t, dir, flowPath)
	d := inputDraft(t, ed, "free_shipping", "request.order.freeShipping", "boolean")
	res, err := ed.CheckSheet(d)
	if err != nil || res.Saved || hasNew(res.Problems) {
		t.Fatalf("%v %+v", err, res)
	}
	want := []FlowFix{{File: flowPath, Removed: []string{"free_shipping"}}}
	if !reflect.DeepEqual(res.Flows, want) {
		t.Fatalf("flows %+v", res.Flows)
	}
	if onDisk(t, dir, flowPath) != before {
		t.Error("CheckSheet wrote flow.toml")
	}
}

func TestSaveSheetRefusesAFlowErrorItCannotFix(t *testing.T) {
	dir, ed := flowWorkspace(t)
	sheetBefore, flowBefore := onDisk(t, dir, sheetPath), onDisk(t, dir, flowPath)
	f := load(t, ed, sheetPath)
	r := rowOf(t, f.Data, "shipping_cost")
	r.Uses = []string{"free_shipping"}
	r.Formula = "free_shipping ? 0 : 5"
	// weight_kg goes too: shipping_fee was its only use, and an unused row
	// is a sheet error, which would stop the flow being analysed.
	d := SheetDraft{File: sheetPath, Token: f.Token, Rows: map[string]RowData{"shipping_cost": r}, Deleted: []string{"shipping_fee", "weight_kg"}}
	res, err := ed.SaveSheet(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved {
		t.Fatalf("saved: %+v", res)
	}
	found := false
	for _, p := range res.Problems {
		if p.Code == "flow" && p.New && p.File == flowPath && strings.Contains(p.Message, "shipping_fee") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no new flow problem naming shipping_fee: %+v", res.Problems)
	}
	if onDisk(t, dir, sheetPath) != sheetBefore || onDisk(t, dir, flowPath) != flowBefore {
		t.Error("a refused save changed files on disk")
	}
}

func TestSaveSheetWithoutFlowsIsUnchanged(t *testing.T) {
	dir, ed := workspace(t, true)
	res, err := ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil || !res.Saved {
		t.Fatalf("%v %+v", err, res)
	}
	if len(res.Flows) != 0 {
		t.Errorf("flows %+v", res.Flows)
	}
	if len(res.Tokens) != 2 {
		t.Errorf("tokens %v; want the sheet and cases.toml", res.Tokens)
	}
	if !strings.Contains(onDisk(t, dir, sheetPath), "discount_rate * 2") {
		t.Error("sheet not saved")
	}
}

func TestDraftOverlayHoldsSheetAndFlowFix(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := inputDraft(t, ed, "free_shipping", "request.order.freeShipping", "boolean")
	ov, eng, reason, err := ed.DraftOverlay(d)
	if err != nil || eng == nil || reason != "" {
		t.Fatalf("%v %v %q", err, eng, reason)
	}
	sheetData, ok := ov[flow.OverlayKey(filepath.Join(dir, filepath.FromSlash(sheetPath)))]
	if !ok || !strings.Contains(string(sheetData), `"request.order.freeShipping"`) {
		t.Errorf("overlay sheet:\n%s", sheetData)
	}
	flowData, ok := ov[flow.OverlayKey(filepath.Join(dir, filepath.FromSlash(flowPath)))]
	if !ok || strings.Contains(string(flowData), `"free_shipping"]`) {
		t.Errorf("overlay flow:\n%s", flowData)
	}
	if len(ov) != 2 {
		t.Errorf("overlay has %d files; want the sheet and one flow", len(ov))
	}
}

// The walk for this repository's flows is cached: a flow added to the
// repository is found once the list is older than flowDirsMaxAge, or after
// a save.
func TestSheetFlowsCachesTheWalk(t *testing.T) {
	dir, ed := flowWorkspace(t)
	count := func() int {
		t.Helper()
		fs, err := ed.sheetFlows(sheetPath)
		if err != nil {
			t.Fatal(err)
		}
		return len(fs)
	}
	n := count()
	if n == 0 {
		t.Fatal("no flow of the sheet")
	}
	from := filepath.Join(dir, "flows", "checkout-to-payment")
	copyDir(t, from, filepath.Join(dir, "flows", "copy-one"))
	if got := count(); got != n {
		t.Fatalf("a fresh list was walked again: %d flows, want %d", got, n)
	}
	ed.dirsAt = ed.dirsAt.Add(-flowDirsMaxAge - time.Second)
	if got := count(); got != n+1 {
		t.Fatalf("after the list aged: %d flows, want %d", got, n+1)
	}
	copyDir(t, from, filepath.Join(dir, "flows", "copy-two"))
	res, err := ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil || !res.Saved {
		t.Fatalf("%v %+v", err, res)
	}
	if got := count(); got != n+2 {
		t.Fatalf("after a save: %d flows, want %d", got, n+2)
	}
}
