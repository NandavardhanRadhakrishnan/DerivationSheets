package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
)

// flowRepo is a git repository holding the checkout-to-payment flow, the
// interfaces and the order-checkout sheet in one commit; it returns the
// flow's folder.
func flowRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, d := range []string{"flows", "interfaces", filepath.Join("specs", "order-checkout"), "schema"} {
		copyTree(t, filepath.Join("..", "..", d), filepath.Join(dir, d))
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.autocrlf", "false"}, {"add", "."}, {"commit", "-q", "-m", "one"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return filepath.Join(dir, "flows", "checkout-to-payment")
}

type flowStateJSON struct {
	File       string               `json:"file"`
	Token      string               `json:"token"`
	Version    int                  `json:"version"`
	Stages     []editor.StageData   `json:"stages"`
	Output     editor.OutputData    `json:"output"`
	Input      editor.BoundaryData  `json:"input"`
	Panel      *editor.FlowPanel    `json:"panel"`
	Tokens     map[string]string    `json:"tokens"`
	Sheet      string               `json:"sheet"`
	Interfaces []ifaceChoice        `json:"interfaces"`
	Problems   []editor.FlowProblem `json:"problems"`
}

func getFlowState(t *testing.T, h http.Handler) flowStateJSON {
	t.Helper()
	rec := request(h, "GET", "/edit/flow/state", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /edit/flow/state: %d %s", rec.Code, rec.Body)
	}
	var st flowStateJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestFlowEditState(t *testing.T) {
	h := editHandler(t, flowRepo(t))
	st := getFlowState(t, h)
	if st.File != "flows/checkout-to-payment/flow.toml" || st.Version != 1 || len(st.Stages) != 2 || st.Stages[1].ID != "shipping-quote" {
		t.Fatalf("file %q version %d stages %+v", st.File, st.Version, st.Stages)
	}
	if st.Panel == nil || len(st.Panel.Boundaries) != 5 {
		t.Fatalf("panel %+v", st.Panel)
	}
	if b := st.Panel.Boundaries[2]; b.Name != "quote-request" || b.Path != "" || b.ReadOnly != "belongs to shipping" {
		t.Errorf("quote-request %+v", b)
	}
	if _, ok := st.Tokens["interfaces/orders/order-placed.toml"]; !ok || len(st.Tokens) != 3 {
		t.Errorf("tokens %v: want the three interfaces in this repository the flow reaches by path", st.Tokens)
	}
	if st.Sheet != "/sheet" {
		t.Errorf("sheet %q", st.Sheet)
	}
	found := false
	for _, c := range st.Interfaces {
		if c.Ref == "../../interfaces/orders/order-placed.toml" && c.Name == "order-placed" {
			found = true
		}
	}
	if !found {
		t.Errorf("interfaces %+v", st.Interfaces)
	}
	page := request(h, "GET", "/", nil, nil)
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, `id="flow-edit-state"`) || !strings.Contains(body, `data-row="shipping_method"`) ||
		!strings.Contains(body, "To agree") {
		t.Errorf("GET /: %d, editor markup or review marks missing", page.Code)
	}
}

func TestFlowEditServesSheet(t *testing.T) {
	h := editHandler(t, flowRepo(t))
	rec := request(h, "GET", "/sheet", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="edit-state"`) || !strings.Contains(rec.Body.String(), `"back":"/"`) {
		t.Fatalf("GET /sheet: %d, editable %v", rec.Code, strings.Contains(rec.Body.String(), `id="edit-state"`))
	}
	st := getState(t, h)
	if st.File != "specs/order-checkout/sheet.toml" || len(st.Rows) != 20 {
		t.Errorf("sheet state: file %q, %d rows", st.File, len(st.Rows))
	}
}

func TestFlowEditCheckAndSave(t *testing.T) {
	dir := flowRepo(t)
	h := editHandler(t, dir)
	st := getFlowState(t, h)
	d := editor.FlowDraft{File: st.File, Token: st.Token, Stages: st.Stages, Output: &st.Output, Input: &st.Input, Tokens: st.Tokens,
		Fields: []editor.FieldEdit{{Interface: "interfaces/orders/order-placed.toml", Name: "shippingMethod", Type: "string", Required: true, Add: true}}}
	rec := request(h, "POST", "/edit/flow/check", d, nil)
	var res struct {
		Saved    bool              `json:"saved"`
		Page     string            `json:"page"`
		Stale    string            `json:"stale"`
		Warnings []string          `json:"warnings"`
		Panel    *editor.FlowPanel `json:"panel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != 200 {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}
	if res.Stale != "" || !strings.Contains(res.Page, `id="flow-main"`) || strings.Contains(res.Page, "flow-edit-state") || len(res.Warnings) != 1 {
		t.Errorf("stale %q, page has main %v, warnings %q", res.Stale, strings.Contains(res.Page, `id="flow-main"`), res.Warnings)
	}
	if marks := res.Panel.Rows["shipping_method"].Marks; len(marks) != 0 {
		t.Errorf("shipping_method marks after adding the field: %+v", marks)
	}
	rec = request(h, "POST", "/edit/flow/save", d, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || !res.Saved {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "..", "..", "interfaces", "orders", "order-placed.toml"))
	if !strings.Contains(string(data), "name     = \"shippingMethod\"") || !strings.Contains(string(data), "local    = true") {
		t.Errorf("order-placed.toml:\n%s", data)
	}
}

func TestFlowEditRefusesCrossSite(t *testing.T) {
	h := editHandler(t, flowRepo(t))
	rec := request(h, "POST", "/edit/flow/save", map[string]any{}, func(r *http.Request) {
		r.Header.Set("Origin", "http://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site save: %d", rec.Code)
	}
	rec = request(h, "POST", "/edit/flow/check", map[string]any{"file": "specs/order-checkout/sheet.toml"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("check of another file: %d", rec.Code)
	}
}

func TestFlowWithoutEditHasNoEditor(t *testing.T) {
	dir := filepath.Join("..", "..", "flows", "checkout-to-payment")
	h := serveHandler([]string{dir}, serveOptions{}, newFetcher(true), io.Discard)
	rec := request(h, "GET", "/", nil, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "flow-main") || strings.Contains(rec.Body.String(), "data-row") {
		t.Errorf("GET / without --edit: %d, has editor markup", rec.Code)
	}
	if rec := request(h, "GET", "/edit/flow/state", nil, nil); rec.Code != 404 {
		t.Errorf("GET /edit/flow/state without --edit: %d", rec.Code)
	}
}
