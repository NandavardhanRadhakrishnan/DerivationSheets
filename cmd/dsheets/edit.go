package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/expr"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/render"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// maxDraft is the largest request body the edit endpoints read.
const maxDraft = 1 << 20

// editServer answers the edit endpoints for the one sheet serve --edit
// serves.
type editServer struct {
	ed   *editor.Editor
	file string // the sheet, relative to the store root
	dir  string // the sheet's folder
	// evDir and mode render a draft as the served page is rendered; serve
	// sets them when it registers the endpoints.
	evDir, mode string
}

// newEditServer sets up editing for a sheet folder. The store root is the
// folder's git repository, so the version can be compared with the last
// commit; a folder outside git is its own root.
func newEditServer(dir string) (*editServer, error) {
	if isFlow(dir) {
		return nil, fmt.Errorf("--edit works on sheets for now; %s is a flow", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, sheet.FileName)); err != nil {
		return nil, err
	}
	root, file := abs, sheet.FileName
	if top, prefix, ok := gitPrefix(abs); ok {
		root, file = top, prefix+sheet.FileName
	}
	st, err := store.NewLocal(root)
	if err != nil {
		return nil, err
	}
	return &editServer{ed: &editor.Editor{Store: st, Root: root}, file: file, dir: abs}, nil
}

// gitPrefix returns the repository holding dir and dir's path inside it,
// slash-separated and ending in "/" unless empty.
func gitPrefix(dir string) (top, prefix string, ok bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel", "--show-prefix").Output()
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) < 1 || lines[0] == "" {
		return "", "", false
	}
	if len(lines) > 1 {
		prefix = strings.TrimSpace(lines[1])
	}
	return filepath.FromSlash(strings.TrimSpace(lines[0])), prefix, true
}

// checkEditArgs refuses what --edit cannot serve safely: anything from
// another repository, several pages, or an address other machines reach.
func checkEditArgs(pos []string, addr string) error {
	if len(pos) != 1 {
		return fmt.Errorf("--edit serves one sheet at a time")
	}
	if alias, _ := sources.Split(pos[0]); alias != "" {
		return fmt.Errorf("--edit cannot edit %s: files from another repository are read-only", pos[0])
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr: %v", err)
	}
	if !loopback(host) {
		return fmt.Errorf("--edit listens only on localhost, 127.0.0.1 or ::1, not %q", addr)
	}
	return nil
}

func loopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// register adds the edit endpoints to mux.
func (es *editServer) register(mux *http.ServeMux) {
	mux.HandleFunc("/edit/state", es.guard(false, es.state))
	mux.HandleFunc("/edit/check", es.guard(true, es.check))
	mux.HandleFunc("/edit/save", es.guard(true, es.save))
}

// guard lets through only requests from the page itself. Every endpoint
// needs a loopback Host, which stops a page on another name that resolves to
// 127.0.0.1 (DNS rebinding). POSTs also need a JSON body under maxDraft and,
// when the browser says, the same origin, which stops other sites posting
// to the editor.
func (es *editServer) guard(post bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if !loopback(strings.Trim(host, "[]")) {
			jsonError(w, http.StatusForbidden, "requests must be addressed to localhost")
			return
		}
		if !post {
			if r.Method != http.MethodGet {
				jsonError(w, http.StatusMethodNotAllowed, "use GET")
				return
			}
			h(w, r)
			return
		}
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			jsonError(w, http.StatusForbidden, "requests from other sites are refused")
			return
		}
		if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
			jsonError(w, http.StatusForbidden, "requests from other sites are refused")
			return
		}
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			jsonError(w, http.StatusUnsupportedMediaType, "send application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxDraft)
		h(w, r)
	}
}

func (es *editServer) state(w http.ResponseWriter, r *http.Request) {
	st, err := es.stateJSON()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(st)
}

// celFunctions are the standard CEL functions offered while typing a
// formula, after the custom ones.
var celFunctions = []expr.FunctionDoc{
	{Signature: "size(x)", Description: "The length of a string or list."},
	{Signature: "int(x)", Description: "x as a whole number."},
	{Signature: "double(x)", Description: "x as a number with a fraction."},
	{Signature: "string(x)", Description: "x as text."},
	{Signature: "has(x.f)", Description: "Whether the field f is present."},
}

// stateJSON is what the page starts from: the sheet's rows as the editor
// sees them, the token of the version loaded, and the functions a formula
// can call.
func (es *editServer) stateJSON() ([]byte, error) {
	f, err := es.ed.Store.Load(es.file)
	if err != nil {
		return nil, err
	}
	doc, err := tomledit.Parse(f.Data, tomledit.Sheet)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse: %v", f.Path, err)
	}
	st := map[string]any{"file": f.Path, "token": f.Token, "version": 0, "title": ""}
	if k := doc.Get("version"); k != nil {
		st["version"] = k.Value
	}
	if k := doc.Get("title"); k != nil {
		st["title"] = k.Value
	}
	rows, ids := []editor.RowData{}, []string{}
	for _, t := range doc.Tables {
		if t.Name == "row" {
			rd := editor.RowFromTable(t)
			rows = append(rows, rd)
			ids = append(ids, rd.ID)
		}
	}
	// Every row in the file has been saved, so its ID is fixed; only rows
	// still in the browser's draft can change theirs.
	st["rows"], st["ids"], st["saved_ids"] = rows, ids, ids
	// Lookup data in files is shown, not edited. A sheet that does not load
	// still opens; its lookups then show no file data.
	st["lookup_files"] = map[string]editor.LookupFile{}
	if s, err := sheet.Parse(es.dir, filepath.Join(es.dir, sheet.FileName), string(f.Data)); err == nil {
		st["lookup_files"] = editor.LookupFiles(s)
	}
	var fns []map[string]string
	for _, d := range append(append([]expr.FunctionDoc{}, expr.CustomFunctions...), celFunctions...) {
		name, _, _ := strings.Cut(d.Signature, "(")
		fns = append(fns, map[string]string{"name": name, "signature": d.Signature, "description": d.Description})
	}
	st["functions"] = fns
	return json.Marshal(st)
}

// draft reads the request body as a draft for the served sheet.
func (es *editServer) draft(w http.ResponseWriter, r *http.Request) (editor.SheetDraft, bool) {
	var d editor.SheetDraft
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonError(w, http.StatusRequestEntityTooLarge, "the draft is larger than 1 MB")
		} else {
			jsonError(w, http.StatusBadRequest, "reading the draft: "+err.Error())
		}
		return d, false
	}
	if d.File != es.file {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("this server edits %s, not %q", es.file, d.File))
		return d, false
	}
	return d, true
}

func (es *editServer) check(w http.ResponseWriter, r *http.Request) {
	d, ok := es.draft(w, r)
	if !ok {
		return
	}
	res, err := es.ed.CheckSheet(d)
	if err != nil {
		draftError(w, err)
		return
	}
	out := struct {
		*editor.SheetResult
		*editor.Refs
		Preview *preview `json:"preview,omitempty"`
	}{SheetResult: res}
	if len(res.Conflict) == 0 {
		if out.Refs, err = es.ed.SheetRefs(d); err != nil {
			draftError(w, err)
			return
		}
		if out.Preview, err = es.preview(d); err != nil {
			draftError(w, err)
			return
		}
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// preview is the sheet page as the draft would render it, and the cases
// it would generate, so the page can show an edit before it is saved.
type preview struct {
	Page  string `json:"page,omitempty"`  // the whole page, from which the browser takes the sheet
	Stale string `json:"stale,omitempty"` // why the draft cannot be shown; the page keeps what it shows
	// Cases are what saving would write to cases.toml, with every row's
	// result in each; CasesStale says why there are none.
	Cases      []editor.CaseView  `json:"cases,omitempty"`
	Unreached  []editor.Unreached `json:"unreached,omitempty"`
	CasesStale string             `json:"cases_stale,omitempty"`
}

// preview renders the draft. Rows the draft deletes are kept on the page,
// marked, until the deletion is saved; the cases leave them out, as saving
// would.
func (es *editServer) preview(d editor.SheetDraft) (*preview, error) {
	shown := d
	shown.Deleted = nil
	e, reason, err := es.ed.DraftEngine(shown)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return &preview{Stale: reason}, nil
	}
	page, err := es.renderDraft(e)
	if err != nil {
		return &preview{Stale: err.Error()}, nil
	}
	set, reason, err := es.ed.DraftCases(d)
	if err != nil {
		return nil, err
	}
	pv := &preview{Page: page, CasesStale: reason}
	if set != nil {
		pv.Cases, pv.Unreached = set.Cases, set.Unreached
	}
	return pv, nil
}

// renderDraft renders an engine built from a draft, which may have
// validation errors the renderer was never written for: a panic becomes
// an error, and the page keeps showing what it showed.
func (es *editServer) renderDraft(e *eval.Engine) (page string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the draft cannot be drawn yet: %v", r)
		}
	}()
	p, err := enginePage(e, es.dir, es.evDir, es.mode, "")
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := render.Write(&buf, p); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (es *editServer) save(w http.ResponseWriter, r *http.Request) {
	d, ok := es.draft(w, r)
	if !ok {
		return
	}
	res, err := es.ed.SaveSheet(d)
	if err != nil {
		draftError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, res)
}

// draftError answers 400 for a draft that cannot be applied, 500 otherwise.
func draftError(w http.ResponseWriter, err error) {
	if errors.Is(err, editor.ErrDraft) {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonError(w, http.StatusInternalServerError, err.Error())
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	writeJSONStatus(w, code, map[string]string{"error": msg})
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		io.WriteString(w, "\n")
	}
}
