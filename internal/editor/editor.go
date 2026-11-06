package editor

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/validate"
)

// Editor checks and saves drafts. It reads and writes the files it edits
// only through Store; Root is the store's folder on disk, where loading a
// sheet finds its data files.
type Editor struct {
	Store store.Store
	Root  string
}

// Problem is a diagnostic, marked when the edit introduces it.
type Problem struct {
	validate.Diagnostic
	New  bool `json:"new"`            // an error the edit introduces
	Rule int  `json:"rule,omitempty"` // decision-table rule, from 1
}

// SheetResult is the outcome of checking or saving a sheet draft.
type SheetResult struct {
	Saved         bool              `json:"saved"`
	Unchanged     bool              `json:"unchanged,omitempty"` // the draft changes nothing; nothing was written
	Tokens        map[string]string `json:"tokens,omitempty"`
	Version       int               `json:"version"`
	VersionRaised bool              `json:"version_raised"`
	Problems      []Problem         `json:"problems"`
	Conflict      []string          `json:"conflict,omitempty"`
	Cases         []string          `json:"cases,omitempty"` // what regenerating cases.toml reported
}

// CheckSheet does everything SaveSheet does except writing.
func (ed *Editor) CheckSheet(d SheetDraft) (*SheetResult, error) { return ed.sheet(d, false) }

// SaveSheet applies a draft, refuses it if it introduces errors, raises the
// version once per commit, writes the sheet and regenerates its cases.
func (ed *Editor) SaveSheet(d SheetDraft) (*SheetResult, error) { return ed.sheet(d, true) }

// prepared is a draft turned into new content.
type prepared struct {
	res     *SheetResult
	content []byte
	after   *sheet.Sheet // nil when the content does not parse
	dir     string       // the sheet's folder, relative to the store root
}

func (ed *Editor) sheet(d SheetDraft, write bool) (*SheetResult, error) {
	cur, err := ed.Store.Load(d.File)
	if err != nil {
		return nil, err
	}
	if cur.Token != d.Token {
		return &SheetResult{Conflict: []string{cur.Path}, Problems: []Problem{}}, nil
	}
	p, err := ed.prepare(cur, d)
	if err != nil {
		return nil, err
	}
	if !write || p.res.Unchanged || hasNew(p.res.Problems) {
		return p.res, nil
	}

	ws := []store.Write{{Path: cur.Path, Data: p.content, Token: cur.Token}}
	casesFile := path.Join(p.dir, cases.FileName)
	caseLines, casesData, casesToken, ok := ed.regenerate(p, casesFile)
	if ok {
		ws = append(ws, store.Write{Path: casesFile, Data: casesData, Token: casesToken})
	}
	tokens, err := ed.Store.Save(ws...)
	var ce *store.ConflictError
	if errors.As(err, &ce) {
		p.res.Conflict = ce.Paths
		return p.res, nil
	}
	if err != nil {
		return nil, err
	}
	p.res.Saved = true
	p.res.Tokens = map[string]string{}
	for i, w := range ws {
		p.res.Tokens[w.Path] = tokens[i]
	}
	p.res.Cases = caseLines
	return p.res, nil
}

func hasNew(ps []Problem) bool {
	for _, p := range ps {
		if p.New {
			return true
		}
	}
	return false
}

// prepare applies the draft, raises the version when due and validates the
// result against the file as it was.
func (ed *Editor) prepare(cur store.File, d SheetDraft) (*prepared, error) {
	content, err := d.Apply(cur.Data)
	if err != nil {
		return nil, err
	}
	p := &prepared{res: &SheetResult{Problems: []Problem{}}, dir: path.Dir(cur.Path)}
	abs := filepath.Join(ed.Root, filepath.FromSlash(cur.Path))
	before, err := sheet.Parse(filepath.Dir(abs), abs, string(cur.Data))
	if err != nil {
		return nil, fmt.Errorf("%s does not parse; fix it by hand first: %w", cur.Path, err)
	}
	if bytes.Equal(content, cur.Data) {
		p.res.Unchanged = true
		p.res.Version = before.Version
		diags := validate.Validate(before).Diagnostics
		p.res.Problems = compare(diags, diags)
		return p, nil
	}

	committed, ok, err := store.CommittedVersion(ed.Store, cur.Path)
	if err != nil {
		return nil, err
	}
	if ok && before.Version == committed {
		doc, err := tomledit.Parse(content, tomledit.Sheet)
		if err == nil {
			doc.Set("version", int64(committed+1))
			if content, err = doc.Bytes(); err != nil {
				return nil, err
			}
			p.res.VersionRaised = true
		}
	}
	p.content = content

	after, err := sheet.Parse(filepath.Dir(abs), abs, string(content))
	var pe *sheet.ParseError
	switch {
	case errors.As(err, &pe):
		p.res.Problems = append(p.res.Problems, Problem{New: true, Diagnostic: validate.Diagnostic{
			Severity: validate.Error, Code: "toml", File: pe.File, Line: pe.Line, Message: pe.Message}})
		p.res.Version, p.res.VersionRaised = before.Version, false
		return p, nil
	case err != nil:
		return nil, err
	}
	p.after = after
	p.res.Version = after.Version
	p.res.Problems = compare(validate.Validate(before).Diagnostics, validate.Validate(after).Diagnostics)
	return p, nil
}

// compare marks the errors in after that are not among the errors in
// before, compared by code, row and message as a multiset.
func compare(before, after []validate.Diagnostic) []Problem {
	type key struct{ code, row, msg string }
	seen := map[key]int{}
	for _, d := range before {
		if d.Severity == validate.Error {
			seen[key{d.Code, d.Row, d.Message}]++
		}
	}
	out := []Problem{}
	for _, d := range after {
		p := Problem{Diagnostic: d}
		if d.Severity == validate.Error {
			k := key{d.Code, d.Row, d.Message}
			if seen[k] > 0 {
				seen[k]--
			} else {
				p.New = true
			}
		}
		out = append(out, p)
	}
	return out
}

// regenerate builds cases.toml for the new sheet as `dsheets cases` does,
// keeping its reference time and hand-written expected values. ok is false
// when it should not be written; lines then say why.
func (ed *Editor) regenerate(p *prepared, file string) (lines []string, data []byte, token string, ok bool) {
	for _, pr := range p.res.Problems {
		if pr.Severity == validate.Error {
			return []string{fmt.Sprintf("%s not regenerated: the sheet has errors", file)}, nil, "", false
		}
	}
	var old *cases.File
	f, err := ed.Store.Load(file)
	switch {
	case err == nil:
		if old, err = cases.ParseFile(f.Data); err != nil {
			return []string{fmt.Sprintf("%s not regenerated: it does not parse: %v", file, err)}, nil, "", false
		}
		token = f.Token
	case !errors.Is(err, fs.ErrNotExist):
		return []string{fmt.Sprintf("%s not regenerated: %v", file, err)}, nil, "", false
	}
	ref, err := cases.RefTime(old)
	if err != nil {
		return []string{fmt.Sprintf("%s not regenerated: %v", file, err)}, nil, "", false
	}
	e, err := eval.New(p.after)
	if err != nil {
		return []string{fmt.Sprintf("%s not regenerated: %v", file, err)}, nil, "", false
	}
	data, sum := cases.Regenerate(e, old, ref)
	return sum.Lines(file), data, token, true
}
