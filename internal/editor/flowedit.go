package editor

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// FlowProblem is an error or warning from analysing the draft flow, marked
// when the edit introduces it.
type FlowProblem struct {
	Severity string `json:"severity"` // "error" or "warning"
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	New      bool   `json:"new"`
}

// FlowResult is the outcome of checking or saving a flow draft.
type FlowResult struct {
	Saved         bool              `json:"saved"`
	Unchanged     bool              `json:"unchanged,omitempty"`
	Tokens        map[string]string `json:"tokens,omitempty"` // new tokens of the files written, by path
	Version       int               `json:"version"`
	VersionRaised bool              `json:"version_raised"`
	Problems      []FlowProblem     `json:"problems"`
	Conflict      []string          `json:"conflict,omitempty"`
	// Warnings are about the edit itself, such as a field added to an
	// interface imported from a schema the field also belongs in.
	Warnings []string `json:"warnings,omitempty"`
	// Analysis is the draft flow sliced, for the page to draw; nil when
	// flow.toml does not parse.
	Analysis *flow.Analysis `json:"-"`
}

// CheckFlow does everything SaveFlow does except writing.
func (ed *Editor) CheckFlow(d FlowDraft) (*FlowResult, error) { return ed.flowRun(d, false) }

// SaveFlow applies a flow draft, refuses it if it introduces errors,
// raises the flow's version once per commit, and writes flow.toml and the
// interface files it changes together.
func (ed *Editor) SaveFlow(d FlowDraft) (*FlowResult, error) { return ed.flowRun(d, true) }

func (ed *Editor) fetcher() *sources.Fetcher {
	if ed.Fetcher == nil {
		ed.Fetcher = sources.NewFetcher(filepath.Join(sources.RepoRoot(ed.Root), sources.CacheDir))
	}
	return ed.Fetcher
}

// IfacePath is the store path of an interface a flow refers to, or why it
// cannot be edited: it is reached through a source alias, or lies outside
// the store root.
func (ed *Editor) IfacePath(f *flow.Flow, ref string) (path, readOnly string) {
	if ref == "" {
		return "", "no interface"
	}
	if alias, _ := sources.Split(ref); alias != "" {
		return "", "belongs to " + alias
	}
	abs, err := filepath.Abs(filepath.Join(f.Dir, filepath.FromSlash(ref)))
	if err != nil {
		return "", err.Error()
	}
	rel, err := filepath.Rel(ed.Root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "outside this repository"
	}
	return filepath.ToSlash(rel), ""
}

// ifaceRefs lists every interface reference of a flow.
func ifaceRefs(f *flow.Flow) []string {
	refs := []string{f.Input.Interface}
	for _, st := range f.Stages {
		refs = append(refs, st.Interface, st.Request.Interface, st.Response.Interface)
	}
	return append(refs, f.Output.Interface)
}

func (ed *Editor) flowRun(d FlowDraft, write bool) (*FlowResult, error) {
	res := &FlowResult{Problems: []FlowProblem{}}
	cur, err := ed.Store.Load(d.File)
	if err != nil {
		return nil, err
	}
	if cur.Token != d.Token {
		res.Conflict = []string{cur.Path}
		return res, nil
	}
	abs := filepath.Join(ed.Root, filepath.FromSlash(cur.Path))
	dir := filepath.Dir(abs)
	before, err := flow.Parse(dir, abs, cur.Data)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse; fix it by hand first: %w", cur.Path, err)
	}
	content, err := d.Apply(cur.Data)
	if err != nil {
		return nil, err
	}
	after, parseErr := flow.Parse(dir, abs, content)
	refFlow := after
	if parseErr != nil {
		refFlow = before
	}
	ifaces, err := ed.applyFields(d, refFlow, res)
	if err != nil || len(res.Conflict) > 0 {
		return res, err
	}

	changed := !bytes.Equal(content, cur.Data)
	res.Version = before.Version
	if !changed && len(ifaces) == 0 {
		res.Unchanged = true
	}
	if changed && parseErr == nil {
		committed, ok, err := store.CommittedVersion(ed.Store, cur.Path)
		if err != nil {
			return nil, err
		}
		if ok && before.Version == committed {
			doc, err := tomledit.Parse(content, tomledit.Flow)
			if err == nil {
				doc.Set("version", int64(committed+1))
				if content, err = doc.Bytes(); err != nil {
					return nil, err
				}
				if after, parseErr = flow.Parse(dir, abs, content); parseErr == nil {
					res.Version, res.VersionRaised = after.Version, true
				}
			}
		}
	}

	beforeA := flow.Analyze(before, sources.NewResolver(before.Dir, before.Sources, ed.fetcher()))
	var afterErrs, afterWarns []flow.Problem
	if parseErr != nil {
		afterErrs = []flow.Problem{{File: cur.Path, Message: parseErr.Error()}}
	} else {
		ov := flow.Overlay{}
		for _, f := range ifaces {
			p, err := filepath.Abs(filepath.Join(ed.Root, filepath.FromSlash(f.Path)))
			if err == nil {
				ov[filepath.Clean(p)] = f.Data
			}
		}
		res.Analysis = flow.AnalyzeWith(after, sources.NewResolver(after.Dir, after.Sources, ed.fetcher()), ov)
		afterErrs, afterWarns = res.Analysis.Errors, res.Analysis.Warnings
	}
	res.Problems = compareFlow(beforeA.Errors, afterErrs, afterWarns, ed.Root)

	if !write || res.Unchanged || hasNewFlow(res.Problems) {
		return res, nil
	}
	var ws []store.Write
	if changed {
		ws = append(ws, store.Write{Path: cur.Path, Data: content, Token: cur.Token})
	}
	ws = append(ws, ifaces...)
	tokens, err := ed.Store.Save(ws...)
	var ce *store.ConflictError
	if errors.As(err, &ce) {
		res.Conflict = ce.Paths
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	res.Saved = true
	res.Tokens = map[string]string{}
	for i, w := range ws {
		res.Tokens[w.Path] = tokens[i]
	}
	return res, nil
}

func hasNewFlow(ps []FlowProblem) bool {
	return slices.ContainsFunc(ps, func(p FlowProblem) bool { return p.New })
}

// compareFlow marks the errors after the edit that were not there before,
// compared by message as a multiset: line numbers move with an edit.
func compareFlow(before, after, warnings []flow.Problem, root string) []FlowProblem {
	seen := map[string]int{}
	for _, p := range before {
		seen[p.Message]++
	}
	rel := func(file string) string {
		if r, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
		return file
	}
	out := []FlowProblem{}
	for _, p := range after {
		fp := FlowProblem{Severity: "error", File: rel(p.File), Line: p.Line, Message: p.Message}
		if seen[p.Message] > 0 {
			seen[p.Message]--
		} else {
			fp.New = true
		}
		out = append(out, fp)
	}
	for _, p := range warnings {
		out = append(out, FlowProblem{Severity: "warning", File: rel(p.File), Line: p.Line, Message: p.Message})
	}
	return out
}

var fieldTypes = []string{"string", "number", "boolean", "date", "datetime", "object", "array"}

// applyFields applies the draft's interface edits. Each file must be an
// interface the flow refers to without a source alias. It returns the
// files that change; a file changed on disk since the page loaded it is
// added to res.Conflict instead.
func (ed *Editor) applyFields(d FlowDraft, f *flow.Flow, res *FlowResult) ([]store.Write, error) {
	editable := map[string]bool{}
	for _, ref := range ifaceRefs(f) {
		if p, why := ed.IfacePath(f, ref); why == "" {
			editable[p] = true
		}
	}
	var order []string
	byPath := map[string][]FieldEdit{}
	for _, fe := range d.Fields {
		if alias, _ := sources.Split(fe.Interface); alias != "" {
			return nil, draftErr("cannot edit %s: read-only, it belongs to %s", fe.Interface, alias)
		}
		if !editable[fe.Interface] {
			return nil, draftErr("cannot edit %s: it is not an interface of this flow in this repository", fe.Interface)
		}
		if _, ok := byPath[fe.Interface]; !ok {
			order = append(order, fe.Interface)
		}
		byPath[fe.Interface] = append(byPath[fe.Interface], fe)
	}
	var ws []store.Write
	for _, p := range order {
		file, err := ed.Store.Load(p)
		if err != nil {
			return nil, err
		}
		if tok, ok := d.Tokens[p]; !ok || tok != file.Token {
			res.Conflict = append(res.Conflict, file.Path)
			continue
		}
		doc, err := tomledit.Parse(file.Data, tomledit.Interface)
		if err != nil {
			return nil, fmt.Errorf("%s does not parse; fix it by hand first: %w", p, err)
		}
		name := str2(doc.Get("interface"))
		imported := str2(doc.Get("imported"))
		for _, fe := range byPath[p] {
			if fe.Name == "" {
				return nil, draftErr("%s: a field needs a name", p)
			}
			if fe.Type != "" && !slices.Contains(fieldTypes, fe.Type) {
				return nil, draftErr("%s: field %s: type %q is not one of %s", p, fe.Name, fe.Type, strings.Join(fieldTypes, ", "))
			}
			t := doc.Find("field", "name", fe.Name)
			if fe.Add {
				if t != nil {
					return nil, draftErr("%s already has a field %s", name, fe.Name)
				}
				t = &tomledit.Table{Name: "field", Array: true}
				t.Set("name", fe.Name)
				if fe.Type != "" {
					t.Set("type", fe.Type)
				}
				t.Set("required", fe.Required)
				if imported != "" {
					t.Set("local", true)
					res.Warnings = append(res.Warnings, fmt.Sprintf(
						"%s is imported from %s: add %s there too. Until then it is marked local = true, so importing again keeps it.",
						name, imported, fe.Name))
				}
				doc.Insert(len(doc.Tables), t)
				continue
			}
			if t == nil {
				return nil, draftErr("%s has no field %s", name, fe.Name)
			}
			if fe.Type != "" {
				set(t, "type", fe.Type)
			}
			set(t, "required", fe.Required)
			if imported != "" {
				if k := t.Get("local"); k == nil || k.Value != true {
					res.Warnings = append(res.Warnings, fmt.Sprintf(
						"%s is imported from %s: change %s there too, or importing again undoes this.", name, imported, fe.Name))
				}
			}
		}
		data, err := doc.Bytes()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(data, file.Data) {
			ws = append(ws, store.Write{Path: file.Path, Data: data, Token: file.Token})
		}
	}
	return ws, nil
}

func str2(k *tomledit.Key) string {
	if k == nil {
		return ""
	}
	s, _ := k.Value.(string)
	return s
}
