package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/testrepo"
)

const shopRef = "product:flows/shop@quarter"

// reqHandler serves the orders clone's sheet with the PM's change opened
// from ref ("" opens the shop's change on branch quarter).
func reqHandler(t *testing.T, ref string) http.Handler {
	t.Helper()
	s := testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	work := testrepo.Clone(t, s.Orders)
	es, err := newEditServer(filepath.Join(work, "sheets", "specs", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	if ref == "" {
		ref = shopRef
	}
	es.openRequirements(ref, sources.NewFetcher(t.TempDir()))
	return serveHandler([]string{es.dir}, serveOptions{Edit: es}, newFetcher(true), io.Discard)
}

// reqDraft is the state's draft with shipping_cost's formula set to formula
// ("" leaves the sheet unchanged).
func reqDraft(t *testing.T, h http.Handler, formula string) editor.SheetDraft {
	t.Helper()
	st := getState(t, h)
	d := editor.SheetDraft{File: st.File, Token: st.Token}
	if formula != "" {
		for _, r := range st.Rows {
			if r.ID == "shipping_cost" {
				r.Formula = formula
				d.Rows = map[string]editor.RowData{"shipping_cost": r}
			}
		}
	}
	return d
}

type reqAnswer struct {
	View  *editor.ReqView `json:"view"`
	Stale string          `json:"stale"`
	Error string          `json:"error"`
}

func postReqs(t *testing.T, h http.Handler, d editor.SheetDraft) reqAnswer {
	t.Helper()
	rec := request(h, "POST", "/edit/requirements", map[string]any{"draft": d}, nil)
	if rec.Code != 200 {
		t.Fatalf("POST /edit/requirements: %d %s", rec.Code, rec.Body)
	}
	var a reqAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func statusOf(t *testing.T, v *editor.ReqView, row string) string {
	t.Helper()
	for _, it := range v.Items {
		if it.Kind == "Compute" && strings.Contains(it.What, "`"+row+"`") {
			return it.Status
		}
	}
	t.Fatalf("no Compute item for %s in %+v", row, v.Items)
	return ""
}

func TestEditRequirementsEndpoint(t *testing.T) {
	h := reqHandler(t, "")
	rec := request(h, "GET", "/edit/state", nil, nil)
	var st struct {
		Requirements struct {
			Flow string `json:"flow"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.Requirements.Flow != shopRef {
		t.Fatalf("state requirements: %v %s", err, rec.Body)
	}
	a := postReqs(t, h, reqDraft(t, h, ""))
	if a.View == nil || a.View.Flow != shopRef {
		t.Fatalf("%+v", a)
	}
	if s := statusOf(t, a.View, "shipping_cost"); s != "open" {
		t.Errorf("unchanged draft: shipping_cost %s", s)
	}
	start := time.Now()
	a = postReqs(t, h, reqDraft(t, h, `tier == "Silver" ? fee / 4 : fee`))
	t.Logf("second POST took %v", time.Since(start))
	if a.View == nil {
		t.Fatalf("%+v", a)
	}
	if s := statusOf(t, a.View, "shipping_cost"); s != "done" {
		t.Errorf("fixed draft: shipping_cost %s", s)
	}
}

func TestEditRequirementsStaleDraft(t *testing.T) {
	h := reqHandler(t, "")
	a := postReqs(t, h, reqDraft(t, h, "fee /"))
	if a.Stale == "" || a.View != nil {
		t.Fatalf("%+v", a)
	}
	d := reqDraft(t, h, "")
	d.Token = "old"
	if a := postReqs(t, h, d); a.Stale == "" || a.View != nil {
		t.Fatalf("token conflict: %+v", a)
	}
}

func TestEditRequirementsOpenError(t *testing.T) {
	h := reqHandler(t, "product:flows/shop@nope")
	st := getState(t, h)
	a := postReqs(t, h, reqDraft(t, h, ""))
	if a.View != nil || !strings.Contains(a.Error, "nope") {
		t.Fatalf("%+v", a)
	}
	rec := request(h, "POST", "/edit/check", editor.SheetDraft{File: st.File, Token: st.Token}, nil)
	if rec.Code != 200 {
		t.Fatalf("/edit/check: %d %s", rec.Code, rec.Body)
	}
}

func TestServeFlowNeedsEditOnSheet(t *testing.T) {
	dir := filepath.Join("..", "..", "specs", "order-checkout")
	for _, args := range [][]string{{"--flow", "p:f@b", dir}, {"--edit", "--flow", "p:f@b", filepath.Join("..", "..", "examples", "pieces", "1-storefront-team")}} {
		err := cmdServe(args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "--flow needs --edit on a sheet") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestEditRequirementsRefusesCrossSite(t *testing.T) {
	h := reqHandler(t, "")
	rec := request(h, "POST", "/edit/requirements", map[string]any{"draft": reqDraft(t, h, "")}, func(r *http.Request) {
		r.Header.Set("Origin", "http://evil.example")
	})
	if rec.Code != 403 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}
