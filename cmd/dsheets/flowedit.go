package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flowrender"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// ifaceChoice is an interface file a stage can name: its reference from
// the flow's folder, and its name.
type ifaceChoice struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

// flowState is what the flow page starts from: flow.toml as the editor
// sees it, the panel for the flow as saved, the versions of the interface
// files it can change, and the interface files a stage can pick.
func (es *editServer) flowState() (map[string]any, *editor.FlowResult, error) {
	f, err := es.ed.Store.Load(es.flowFile)
	if err != nil {
		return nil, nil, err
	}
	doc, err := tomledit.Parse(f.Data, tomledit.Flow)
	if err != nil {
		return nil, nil, fmt.Errorf("%s does not parse: %v", f.Path, err)
	}
	res, err := es.ed.CheckFlow(editor.FlowDraft{File: f.Path, Token: f.Token})
	if err != nil {
		return nil, nil, err
	}
	st := map[string]any{
		"file": f.Path, "token": f.Token, "version": 0, "title": "",
		"input": editor.InputFromDoc(doc), "stages": editor.StagesFromDoc(doc), "output": editor.OutputFromDoc(doc),
		"problems": res.Problems, "sheet": "", "sheet_note": es.sheetNote,
	}
	if k := doc.Get("version"); k != nil {
		st["version"] = k.Value
	}
	if k := doc.Get("title"); k != nil {
		st["title"] = k.Value
	}
	if es.file != "" {
		st["sheet"] = "/sheet"
	}
	panel := es.ed.Panel(res.Analysis)
	st["panel"] = panel
	st["tokens"] = es.ifaceTokens(panel)
	dir := filepath.Dir(filepath.Join(es.ed.Root, filepath.FromSlash(f.Path)))
	st["interfaces"] = es.ifaceChoices(dir)
	return st, res, nil
}

// ifaceTokens are the versions of the interface files the panel can edit.
func (es *editServer) ifaceTokens(p *editor.FlowPanel) map[string]string {
	out := map[string]string{}
	if p == nil {
		return out
	}
	for _, b := range p.Boundaries {
		if b.Path == "" {
			continue
		}
		if f, err := es.ed.Store.Load(b.Path); err == nil {
			out[b.Path] = f.Token
		}
	}
	return out
}

// ifaceChoices lists the interface files in the repository, by their
// reference from the flow's folder. Hidden folders, such as .git and the
// .dsheets cache of other teams' repositories, are skipped.
func (es *editServer) ifaceChoices(flowDir string) []ifaceChoice {
	out := []ifaceChoice{}
	filepath.WalkDir(es.ed.Root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if p != es.ed.Root && (strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(e.Name(), ".toml") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || tomledit.LayoutFor(data) != tomledit.Interface {
			return nil
		}
		doc, err := tomledit.Parse(data, tomledit.Interface)
		if err != nil {
			return nil
		}
		ref, err := filepath.Rel(flowDir, p)
		if err != nil {
			return nil
		}
		name := ""
		if k := doc.Get("interface"); k != nil {
			name, _ = k.Value.(string)
		}
		out = append(out, ifaceChoice{Ref: filepath.ToSlash(ref), Name: name})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// flowPage renders the flow page with the editor: review marks always
// on.
func (es *editServer) flowPage(w http.ResponseWriter) error {
	st, res, err := es.flowState()
	if err != nil {
		return err
	}
	a := res.Analysis
	if a == nil || len(a.Errors) > 0 {
		var b strings.Builder
		if a != nil {
			printAnalysis(&b, a)
		}
		return fmt.Errorf("the flow cannot be drawn, so it cannot be edited here; fix it by hand first:\n%s", b.String())
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	p := flowrender.Build(a, time.Now(), true)
	p.Edit = flowrender.NewEditView(data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return flowrender.Write(w, p)
}

func (es *editServer) flowStateEndpoint(w http.ResponseWriter, r *http.Request) {
	st, _, err := es.flowState()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, st)
}

// flowDraft reads the request body as a draft of the served flow.
func (es *editServer) flowDraft(w http.ResponseWriter, r *http.Request) (editor.FlowDraft, bool) {
	var d editor.FlowDraft
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonError(w, http.StatusRequestEntityTooLarge, "the draft is larger than 1 MB")
		} else {
			jsonError(w, http.StatusBadRequest, "reading the draft: "+err.Error())
		}
		return d, false
	}
	if d.File != es.flowFile {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("this server edits %s, not %q", es.flowFile, d.File))
		return d, false
	}
	return d, true
}

// flowReply is a flow check or save: the result, the panel for the draft,
// and the page as the draft draws it, or why it cannot be drawn.
type flowReply struct {
	*editor.FlowResult
	Panel  *editor.FlowPanel `json:"panel,omitempty"`
	Page   string            `json:"page,omitempty"`
	Stale  string            `json:"stale,omitempty"`
	IfaceT map[string]string `json:"iface_tokens,omitempty"` // versions of interface files the draft's panel can edit
}

func (es *editServer) reply(res *editor.FlowResult) flowReply {
	out := flowReply{FlowResult: res}
	if len(res.Conflict) > 0 {
		return out
	}
	out.Panel = es.ed.Panel(res.Analysis)
	out.IfaceT = es.ifaceTokens(out.Panel)
	a := res.Analysis
	switch {
	case a == nil:
		out.Stale = "flow.toml does not parse"
	case len(a.Errors) > 0:
		msgs := make([]string, len(a.Errors))
		for i, p := range a.Errors {
			msgs[i] = p.Message
		}
		out.Stale = "the flow cannot be drawn until this is fixed: " + strings.Join(msgs, "; ")
	default:
		page, err := renderFlowDraft(a)
		if err != nil {
			out.Stale = err.Error()
		} else {
			out.Page = page
		}
	}
	return out
}

// renderFlowDraft draws a draft flow with the editor's markup; a panic
// becomes an error, and the page keeps showing what it showed.
func renderFlowDraft(a *flow.Analysis) (page string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the draft cannot be drawn yet: %v", r)
		}
	}()
	p := flowrender.Build(a, time.Now(), true)
	p.Edit = &flowrender.EditView{}
	var b strings.Builder
	if err := flowrender.Write(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (es *editServer) flowCheck(w http.ResponseWriter, r *http.Request) {
	d, ok := es.flowDraft(w, r)
	if !ok {
		return
	}
	res, err := es.ed.CheckFlow(d)
	if err != nil {
		draftError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, es.reply(res))
}

func (es *editServer) flowSave(w http.ResponseWriter, r *http.Request) {
	d, ok := es.flowDraft(w, r)
	if !ok {
		return
	}
	res, err := es.ed.SaveFlow(d)
	if err != nil {
		draftError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, es.reply(res))
}
