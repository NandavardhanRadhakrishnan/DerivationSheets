package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/testrepo"
)

const shopRef = "product:flows/shop@quarter"

// reqHandler serves the orders clone's sheet with the PM's change opened
// from ref ("" opens the shop's change on branch quarter).
func reqHandler(t *testing.T, ref string) http.Handler {
	t.Helper()
	return reqHandlerWith(t, ref, nil, nil)
}

// reqHandlerWith is reqHandler with setup run on the shop and the clone
// before the server starts, and the change read through fe (nil: a new
// fetcher).
func reqHandlerWith(t *testing.T, ref string, fe *sources.Fetcher, setup func(s *testrepo.Shop, work string)) http.Handler {
	t.Helper()
	s := testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	work := testrepo.Clone(t, s.Orders)
	if setup != nil {
		setup(s, work)
	}
	es, err := newEditServer(filepath.Join(work, "sheets", "specs", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	if ref == "" {
		ref = shopRef
	}
	if fe == nil {
		fe = sources.NewFetcher(t.TempDir())
	}
	es.openRequirements(ref, fe)
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

// doublingOrders is the reviewer's Orders sheet for the false "done": fee
// is renamed shipping_fee, so the rule text of the draft below matches the
// PM's, but Orders sends Shipping twice the weight, so every case differs.
// extra is appended to the sheet.
func doublingOrders(t *testing.T, work, extra string) {
	t.Helper()
	text := `sheet   = "orders"
version = 1
title   = "Orders"

[[row]]
id     = "tier"
name   = "Tier"
source = "request.tier"
type   = "string"

[[row]]
id     = "weight"
name   = "Weight"
source = "request.weight"
type   = "number"

[[row]]
id      = "sent_weight"
name    = "Sent weight"
uses    = ["weight"]
formula = "weight * 2"

  [[row.send]]
  target = "quote"
  field  = "weight"
  usage  = "R"

[[row]]
id     = "shipping_fee"
name   = "Shipping fee"
source = "call.quote.fee"
type   = "number"

[[row]]
id      = "shipping_cost"
name    = "Shipping cost"
uses    = ["tier", "shipping_fee"]
formula = "tier == \"Silver\" ? shipping_fee / 2 : shipping_fee"

  [row.send]
  field = "shippingCost"
  usage = "R"
` + extra
	if err := os.WriteFile(filepath.Join(work, "sheets", "specs", "orders", "sheet.toml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An error already in the saved sheet leaves the Orders flow out of the
// PM's cases; the draft must then answer stale, never an item done by its
// rule text alone (D-22).
func TestEditRequirementsOldErrorIsStale(t *testing.T) {
	const fixed = `tier == "Silver" ? shipping_fee / 4 : shipping_fee`
	h := reqHandlerWith(t, "", nil, func(_ *testrepo.Shop, work string) { doublingOrders(t, work, "") })
	a := postReqs(t, h, reqDraft(t, h, fixed))
	if a.View == nil {
		t.Fatalf("valid sheet: %+v", a)
	}
	if s := statusOf(t, a.View, "shipping_cost"); s != "open" {
		t.Fatalf("valid sheet: shipping_cost %s, want open on the cases", s)
	}

	unused := `
[[row]]
id      = "spare"
name    = "Spare"
uses    = ["weight"]
formula = "weight + 1"
`
	h = reqHandlerWith(t, "", nil, func(_ *testrepo.Shop, work string) { doublingOrders(t, work, unused) })
	a = postReqs(t, h, reqDraft(t, h, fixed))
	if a.View != nil {
		for _, it := range a.View.Items {
			t.Logf("%s %s: %s (%s)", it.Kind, it.What, it.Status, it.Note)
		}
		t.Fatalf("sheet with an old error got a view: shipping_cost %s", statusOf(t, a.View, "shipping_cost"))
	}
	t.Logf("stale: %s", a.Stale)
	if !strings.Contains(a.Stale, "the sheet has errors") || !strings.Contains(a.Stale, "nothing uses this row") {
		t.Errorf("stale: %q", a.Stale)
	}
}

// The team's flow names another sheet, so the PM's cases never run on the
// one being edited: the draft was not checked.
func TestEditRequirementsOtherSheetIsStale(t *testing.T) {
	h := reqHandlerWith(t, "", nil, func(_ *testrepo.Shop, work string) {
		specs := filepath.Join(work, "sheets", "specs")
		data, err := os.ReadFile(filepath.Join(specs, "orders", "sheet.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(specs, "other"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(specs, "other", "sheet.toml"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		fl := filepath.Join(work, "sheets", "flows", "orders", "flow.toml")
		text, err := os.ReadFile(fl)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fl, []byte(strings.Replace(string(text), "../../specs/orders", "../../specs/other", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	a := postReqs(t, h, reqDraft(t, h, `tier == "Silver" ? fee / 4 : fee`))
	if a.View != nil {
		t.Fatalf("a draft the cases never ran on got a view: shipping_cost %s", statusOf(t, a.View, "shipping_cost"))
	}
	t.Logf("stale: %s", a.Stale)
	if !strings.Contains(a.Stale, "Orders team") {
		t.Errorf("stale: %q", a.Stale)
	}
}

// This repository pinned to a tag in the registry is read from the tag,
// not the working tree: the draft cannot be checked.
func TestEditRequirementsPinnedIsStale(t *testing.T) {
	h := reqHandlerWith(t, "", nil, func(s *testrepo.Shop, _ string) {
		testrepo.Git(t, s.Orders, "tag", "v1")
		mods := filepath.Join(s.Product, ".gitmodules")
		text, err := os.ReadFile(mods)
		if err != nil {
			t.Fatal(err)
		}
		head, rest, _ := strings.Cut(string(text), `[submodule "orders"]`)
		rest = strings.Replace(rest, "branch = main", "branch = v1", 1)
		if err := os.WriteFile(mods, []byte(head+`[submodule "orders"]`+rest), 0o644); err != nil {
			t.Fatal(err)
		}
		testrepo.Git(t, s.Product, "commit", "--quiet", "-am", "pin orders")
	})
	a := postReqs(t, h, reqDraft(t, h, `tier == "Silver" ? fee / 4 : fee`))
	t.Logf("stale: %s", a.Stale)
	if a.View != nil || !strings.Contains(a.Stale, "pinned to v1") {
		t.Fatalf("%+v", a)
	}
}

// Without an origin remote the working tree stands in for nothing: the
// cases run on the registry's copy, so the draft is not checked.
func TestEditRequirementsNoOriginIsStale(t *testing.T) {
	h := reqHandlerWith(t, "", nil, func(_ *testrepo.Shop, work string) {
		testrepo.Git(t, work, "remote", "remove", "origin")
	})
	a := postReqs(t, h, reqDraft(t, h, `tier == "Silver" ? fee / 4 : fee`))
	t.Logf("stale: %s", a.Stale)
	if a.View != nil || !strings.Contains(a.Stale, "no origin remote") {
		t.Fatalf("%+v", a)
	}
}

// A Fetch again that fails keeps the change last read, and says why.
func TestEditRequirementsRefreshFailureKeepsView(t *testing.T) {
	var shop *testrepo.Shop
	fe := sources.NewFetcher(t.TempDir())
	fe.MaxAge = time.Minute // as serve has it: Fetch again must still fetch
	h := reqHandlerWith(t, "", fe, func(s *testrepo.Shop, _ string) { shop = s })
	if a := postReqs(t, h, reqDraft(t, h, "")); a.View == nil {
		t.Fatalf("%+v", a)
	}
	testrepo.Git(t, shop.Product, "switch", "--quiet", "main")
	testrepo.Git(t, shop.Product, "branch", "--quiet", "-D", "quarter")
	rec := request(h, "POST", "/edit/requirements", map[string]any{"draft": reqDraft(t, h, ""), "refresh": true}, nil)
	var a struct {
		reqAnswer
		RefreshError string `json:"refresh_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if a.View == nil || a.RefreshError == "" || a.Error != "" {
		t.Fatalf("refresh to a missing branch: %s", rec.Body)
	}
	if s := statusOf(t, a.View, "shipping_cost"); s != "open" {
		t.Errorf("kept view: shipping_cost %s", s)
	}
	// Later answers still say the last Fetch again failed.
	rec = request(h, "POST", "/edit/requirements", map[string]any{"draft": reqDraft(t, h, "")}, nil)
	if !strings.Contains(rec.Body.String(), "refresh_error") {
		t.Errorf("next answer: %s", rec.Body)
	}
}

// A sheet on disk that does not parse is a reason the draft is not
// checked, not a failed request.
func TestEditRequirementsUnparsedSheetIsStale(t *testing.T) {
	var work string
	h := reqHandlerWith(t, "", nil, func(_ *testrepo.Shop, w string) { work = w })
	d := reqDraft(t, h, "")
	file := filepath.Join(work, "sheets", "specs", "orders", "sheet.toml")
	if err := os.WriteFile(file, []byte("sheet = \"orders\"\n[[row]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewLocal(work)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.Load(d.File)
	if err != nil {
		t.Fatal(err)
	}
	d.Token = f.Token
	a := postReqs(t, h, d)
	if a.View != nil || !strings.Contains(a.Stale, "sheet.toml cannot be read") || !strings.Contains(a.Stale, "close array") {
		t.Fatalf("%+v", a)
	}
}

// A branch updated after the change was first read is seen by Fetch again
// at once, though serve's MaxAge has not passed.
func TestEditRequirementsRefreshSeesNewCommit(t *testing.T) {
	var shop *testrepo.Shop
	fe := sources.NewFetcher(t.TempDir())
	fe.MaxAge = time.Minute
	h := reqHandlerWith(t, "", fe, func(s *testrepo.Shop, _ string) { shop = s })
	const fixed = `tier == "Silver" ? fee / 4 : fee`
	a := postReqs(t, h, reqDraft(t, h, fixed))
	if a.View == nil || statusOf(t, a.View, "shipping_cost") != "done" {
		t.Fatalf("before: %+v", a)
	}
	f := filepath.Join(shop.Product, "sheets", "specs", "shop", "sheet.toml")
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(strings.Replace(string(data), "shipping_fee / 4", "shipping_fee / 8", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	testrepo.Git(t, shop.Product, "commit", "--quiet", "-am", "eighth")
	rec := request(h, "POST", "/edit/requirements", map[string]any{"draft": reqDraft(t, h, fixed), "refresh": true}, nil)
	var b reqAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.View == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if s := statusOf(t, b.View, "shipping_cost"); s == "done" {
		t.Errorf("after refresh: shipping_cost still %s, the cached branch was read", s)
	}
}

// A repository whose team has no flow of its own is decided by the rule
// text read through the draft (D-25(a)): a view, not "not checked".
func TestEditRequirementsSheetOnlyTeamIsView(t *testing.T) {
	const fixed = `tier == "Silver" ? shipping_fee / 4 : shipping_fee`
	h := reqHandlerWith(t, "", nil, func(_ *testrepo.Shop, work string) {
		doublingOrders(t, work, "") // the rule text names shipping_fee
		if err := os.RemoveAll(filepath.Join(work, "sheets", "flows", "orders")); err != nil {
			t.Fatal(err)
		}
	})
	a := postReqs(t, h, reqDraft(t, h, ""))
	if a.View == nil {
		t.Fatalf("unchanged: %+v", a)
	}
	if s := statusOf(t, a.View, "shipping_cost"); s == "done" {
		t.Errorf("unchanged: shipping_cost %s", s)
	}
	a = postReqs(t, h, reqDraft(t, h, fixed))
	if a.View == nil {
		t.Fatalf("a team with no flow of its own got %q", a.Stale)
	}
	if s := statusOf(t, a.View, "shipping_cost"); s != "done" {
		t.Errorf("fixing draft: shipping_cost %s, want done by rule text", s)
	}
}
