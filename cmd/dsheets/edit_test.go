package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
)

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if e.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// editRepo is a git repository holding a copy of specs/order-checkout in
// one commit; it returns the sheet's folder.
func editRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "specs", "order-checkout"), filepath.Join(dir, "specs", "order-checkout"))
	copyTree(t, filepath.Join("..", "..", "schema"), filepath.Join(dir, "schema"))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "one"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return filepath.Join(dir, "specs", "order-checkout")
}

func editHandler(t *testing.T, dir string) http.Handler {
	t.Helper()
	es, err := newEditServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	return serveHandler([]string{dir}, serveOptions{Edit: es}, newFetcher(true), io.Discard)
}

// request sends a request as the page itself would.
func request(h http.Handler, method, path string, body any, tweak func(*http.Request)) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Host = "localhost:8080"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost:8080")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if tweak != nil {
		tweak(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

type stateJSON struct {
	File     string            `json:"file"`
	Token    string            `json:"token"`
	Version  int               `json:"version"`
	Title    string            `json:"title"`
	Rows     []editor.RowData  `json:"rows"`
	IDs      []string          `json:"ids"`
	SavedIDs []string          `json:"saved_ids"`
	Extra    map[string]string `json:"-"`
}

func getState(t *testing.T, h http.Handler) stateJSON {
	t.Helper()
	rec := request(h, "GET", "/edit/state", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /edit/state: %d %s", rec.Code, rec.Body)
	}
	var st stateJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestServeWithoutEditIsReadOnly(t *testing.T) {
	dir := filepath.Join("..", "..", "specs", "order-checkout")
	h := serveHandler([]string{dir}, serveOptions{}, newFetcher(true), io.Discard)
	if rec := request(h, "GET", "/edit/state", nil, nil); rec.Code != 404 {
		t.Errorf("GET /edit/state without --edit: %d", rec.Code)
	}
	if rec := request(h, "POST", "/edit/save", map[string]any{}, nil); rec.Code != 404 {
		t.Errorf("POST /edit/save without --edit: %d", rec.Code)
	}
	if rec := request(h, "GET", "/", nil, nil); rec.Code != 200 || strings.Contains(rec.Body.String(), "edit-state") {
		t.Errorf("GET /: %d, mentions edit-state: %v", rec.Code, strings.Contains(rec.Body.String(), "edit-state"))
	}
}

func TestEditState(t *testing.T) {
	dir := editRepo(t)
	st := getState(t, editHandler(t, dir))
	data, _ := os.ReadFile(filepath.Join(dir, "sheet.toml"))
	if len(st.Rows) != 20 || st.Rows[0].ID != "signup_date" || st.Token != store.Token(data) {
		t.Errorf("%d rows, first %q, token ok %v", len(st.Rows), st.Rows[0].ID, st.Token == store.Token(data))
	}
	if st.File != "specs/order-checkout/sheet.toml" || st.Version != 1 || st.Title != "Online order checkout" {
		t.Errorf("file %q version %d title %q", st.File, st.Version, st.Title)
	}
	if len(st.IDs) != 20 || !slices.Equal(st.IDs, st.SavedIDs) {
		t.Errorf("ids %v saved %v", st.IDs, st.SavedIDs)
	}
}

func TestEditEndpointsRefuseCrossSite(t *testing.T) {
	h := editHandler(t, editRepo(t))
	draft := map[string]any{"file": "specs/order-checkout/sheet.toml"}
	for _, c := range []struct {
		name  string
		tweak func(*http.Request)
		code  int
	}{
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, 403},
		{"cross-site fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"text/plain", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"rebound host name", func(r *http.Request) {
			r.Host = "evil.example:8080"
			r.Header.Set("Origin", "http://evil.example:8080")
		}, 403},
		{"GET", func(r *http.Request) { r.Method = "GET" }, 405},
	} {
		for _, path := range []string{"/edit/check", "/edit/save"} {
			if rec := request(h, "POST", path, draft, c.tweak); rec.Code != c.code {
				t.Errorf("%s %s: %d, want %d", c.name, path, rec.Code, c.code)
			}
		}
	}
	if rec := request(h, "GET", "/edit/state", nil, func(r *http.Request) { r.Host = "evil.example:8080" }); rec.Code != 403 {
		t.Errorf("state for a rebound host name: %d", rec.Code)
	}
	big := strings.Repeat("x", 2<<20)
	if rec := request(h, "POST", "/edit/check", map[string]any{"file": big}, nil); rec.Code != 413 {
		t.Errorf("2 MB body: %d", rec.Code)
	}
}

func TestEditCheckUsesAndCycle(t *testing.T) {
	h := editHandler(t, editRepo(t))
	st := getState(t, h)
	var discount editor.RowData
	for _, r := range st.Rows {
		if r.ID == "discount" {
			discount = r
		}
	}
	discount.Formula = "subtotal * discount_rate"
	rec := request(h, "POST", "/edit/check", editor.SheetDraft{File: st.File, Token: st.Token, Rows: map[string]editor.RowData{"discount": discount}}, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var res struct {
		editor.SheetResult
		Uses  map[string][]string `json:"uses"`
		Cycle map[string][]string `json:"cycle"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !slices.Equal(res.Uses["discount"], []string{"subtotal", "discount_rate"}) || !slices.Contains(res.Cycle["discount"], "total") || res.Saved {
		t.Errorf("uses %v cycle %v saved %v", res.Uses, res.Cycle["discount"], res.Saved)
	}
}

func TestEditSaveRoundTrip(t *testing.T) {
	dir := editRepo(t)
	h := editHandler(t, dir)
	st := getState(t, h)
	title := "Checkout renamed"
	rec := request(h, "POST", "/edit/save", editor.SheetDraft{File: st.File, Token: st.Token, Title: &title}, nil)
	var res editor.SheetResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || !res.Saved || res.Version != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if page := request(h, "GET", "/", nil, nil); !strings.Contains(page.Body.String(), "Checkout renamed") {
		t.Error("page does not show the new title")
	}
	// The browser may not pick another file.
	other := editor.SheetDraft{File: "schema/sheet.schema.json", Token: st.Token, Title: &title}
	if rec := request(h, "POST", "/edit/save", other, nil); rec.Code != 400 {
		t.Errorf("draft for another file: %d %s", rec.Code, rec.Body)
	}
	// A broken draft is the browser's fault.
	bad := editor.SheetDraft{File: st.File, Token: res.Tokens[st.File], Deleted: []string{"ghost"}}
	if rec := request(h, "POST", "/edit/save", bad, nil); rec.Code != 400 {
		t.Errorf("broken draft: %d %s", rec.Code, rec.Body)
	}
}

func TestEditRefusesAliasAndRemoteAddr(t *testing.T) {
	dir := editRepo(t)
	for _, args := range [][]string{
		{"--edit", "shipping:x"},
		{"--edit", "--addr", "0.0.0.0:0", dir},
		{"--edit", "--addr", ":0", dir},
		{"--edit", dir, dir},
		{"--edit", filepath.Join("..", "..", "flows", "checkout-to-payment")},
	} {
		if err := run(append([]string{"serve"}, args...), io.Discard, io.Discard); err == nil {
			t.Errorf("serve %v: no error", args)
		}
	}
}
