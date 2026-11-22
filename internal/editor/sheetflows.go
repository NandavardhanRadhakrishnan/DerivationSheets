package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/validate"
)

// FlowFix is a flow.toml of this repository that a sheet save rewrites:
// the rows it removed from computes lists, because the sheet no longer
// derives them.
type FlowFix struct {
	File    string   `json:"file"` // relative to the store root
	Removed []string `json:"removed"`

	content []byte // the new flow.toml
	token   string // the version of flow.toml it replaces
}

// flowDirsMaxAge is how long the list of this repository's flow folders is
// used before the repository is walked again.
const flowDirsMaxAge = 10 * time.Second

// flowDirs are the folders under Root holding a flow.toml. The walk is
// kept for flowDirsMaxAge, and forgotten after a save; the flows themselves
// are read through Store every time.
func (ed *Editor) flowDirs() []string {
	ed.dirsMu.Lock()
	defer ed.dirsMu.Unlock()
	if ed.dirsAt.IsZero() || time.Since(ed.dirsAt) >= flowDirsMaxAge {
		ed.dirs, ed.dirsAt = flow.FindDirs(ed.Root), time.Now()
	}
	return ed.dirs
}

// forgetFlowDirs makes the next flowDirs walk the repository again.
func (ed *Editor) forgetFlowDirs() {
	ed.dirsMu.Lock()
	defer ed.dirsMu.Unlock()
	ed.dirs, ed.dirsAt = nil, time.Time{}
}

// sheetFlow is a flow of this repository whose end-to-end sheet is the
// sheet being edited.
type sheetFlow struct {
	file     store.File
	abs      string     // flow.toml on disk
	sheetDir string     // the sheet's folder, as the flow resolves it
	before   *flow.Flow // as saved
}

// sheetFlows lists the flows under Root whose sheet, without a source
// alias, is the folder of sheetFile (a store path). Flows that do not
// parse are left out: they cannot be fixed here.
func (ed *Editor) sheetFlows(sheetFile string) ([]sheetFlow, error) {
	want, err := os.Stat(filepath.Dir(filepath.Join(ed.Root, filepath.FromSlash(sheetFile))))
	if err != nil {
		return nil, err
	}
	var out []sheetFlow
	for _, dir := range ed.flowDirs() {
		abs := filepath.Join(dir, flow.FileName)
		rel, err := filepath.Rel(ed.Root, abs)
		if err != nil {
			continue
		}
		file, err := ed.Store.Load(filepath.ToSlash(rel))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		f, err := flow.Parse(dir, abs, file.Data)
		if err != nil {
			continue
		}
		if alias, _ := sources.Split(f.SheetRef); alias != "" {
			continue
		}
		sheetDir := filepath.Join(dir, filepath.FromSlash(f.SheetRef))
		if fi, err := os.Stat(sheetDir); err != nil || !os.SameFile(fi, want) {
			continue
		}
		out = append(out, sheetFlow{file: file, abs: abs, sheetDir: sheetDir, before: f})
	}
	return out, nil
}

// fix removes from the flow's computes and callee_computes lists the rows
// after does not derive: rows it lacks, and inputs. ok is false when
// nothing is removed.
func (sf sheetFlow) fix(after *sheet.Sheet) (fx FlowFix, ok bool, err error) {
	derives := func(id string) bool {
		r := after.ByID[id]
		return r != nil && !r.IsInput()
	}
	var removed []string
	keep := func(ids []string) []string {
		var out []string
		for _, id := range ids {
			if derives(id) {
				out = append(out, id)
			} else if !slices.Contains(removed, id) {
				removed = append(removed, id)
			}
		}
		return out
	}
	doc, err := tomledit.Parse(sf.file.Data, tomledit.Flow)
	if err != nil {
		return FlowFix{}, false, nil
	}
	d := FlowDraft{Stages: StagesFromDoc(doc)}
	for i := range d.Stages {
		d.Stages[i].Computes = keep(d.Stages[i].Computes)
		d.Stages[i].CalleeComputes = keep(d.Stages[i].CalleeComputes)
	}
	if table(doc, "output") != nil {
		out := OutputFromDoc(doc)
		out.Computes = keep(out.Computes)
		d.Output = &out
	}
	if len(removed) == 0 {
		return FlowFix{}, false, nil
	}
	content, err := d.Apply(sf.file.Data)
	if err != nil {
		return FlowFix{}, false, fmt.Errorf("%s: %w", sf.file.Path, err)
	}
	return FlowFix{File: sf.file.Path, Removed: removed, content: content, token: sf.file.Token}, true, nil
}

// flowFixes fixes this repository's flows whose sheet is sheetFile for the
// sheet's new content: it removes the rows after no longer derives from
// their computes lists, raising each changed flow's version once per
// commit. problems are the flow errors the edit introduces that remain
// after the fixes, which refuse the save.
func (ed *Editor) flowFixes(sheetFile string, content []byte, after *sheet.Sheet) (fixes []FlowFix, problems []Problem, err error) {
	flows, err := ed.sheetFlows(sheetFile)
	if err != nil {
		return nil, nil, err
	}
	// A sheet with errors cannot be sliced; its own problems refuse the
	// save, and the flows' would only repeat them.
	analyse := validate.Validate(after).OK()
	for _, sf := range flows {
		fx, ok, err := sf.fix(after)
		if err != nil {
			return nil, nil, err
		}
		next, f := sf.file.Data, sf.before
		if ok {
			if fx.content, _, err = ed.raiseVersion(sf.file.Path, sf.before.Version, fx.content, tomledit.Flow); err != nil {
				return nil, nil, err
			}
			fixes = append(fixes, fx)
			next = fx.content
			if f, err = flow.Parse(filepath.Dir(sf.abs), sf.abs, next); err != nil {
				problems = append(problems, flowProblem(sf.file.Path, 0, err.Error()))
				continue
			}
		}
		if !analyse {
			continue
		}
		ov := flow.Overlay{
			flow.OverlayKey(filepath.Join(sf.sheetDir, sheet.FileName)): content,
			flow.OverlayKey(sf.abs): next,
		}
		beforeA := flow.Analyze(sf.before, sources.NewResolver(sf.before.Dir, sf.before.Sources, ed.fetcher()))
		afterA := flow.AnalyzeWith(f, sources.NewResolver(f.Dir, f.Sources, ed.fetcher()), ov)
		for _, p := range compareFlow(beforeA.Errors, afterA.Errors, nil, ed.Root) {
			if !p.New {
				continue
			}
			line := 0
			if p.File == sf.file.Path {
				line = p.Line
			}
			problems = append(problems, flowProblem(sf.file.Path, line, p.Message))
		}
	}
	return fixes, problems, nil
}

func flowProblem(file string, line int, msg string) Problem {
	return Problem{New: true, Diagnostic: validate.Diagnostic{
		Severity: validate.Error, Code: "flow", File: file, Line: line, Message: msg}}
}

// DraftOverlay is the draft's sheet content and the flow.toml files saving
// it would fix, by flow.OverlayKey, with the draft's engine. reason says
// why the draft cannot be evaluated, as DraftEngine gives it; the overlay
// then holds the sheet alone.
func (ed *Editor) DraftOverlay(d SheetDraft) (ov flow.Overlay, eng *eval.Engine, reason string, err error) {
	cur, err := ed.Store.Load(d.File)
	if err != nil {
		return nil, nil, "", err
	}
	content, err := d.Apply(cur.Data)
	if err != nil {
		return nil, nil, "", err
	}
	abs := filepath.Join(ed.Root, filepath.FromSlash(cur.Path))
	ov = flow.Overlay{flow.OverlayKey(abs): content}
	s, err := sheet.Parse(filepath.Dir(abs), abs, string(content))
	var pe *sheet.ParseError
	switch {
	case errors.As(err, &pe):
		return ov, nil, fmt.Sprintf("line %d: %s", pe.Line, pe.Message), nil
	case err != nil:
		return nil, nil, "", err
	}
	if eng, err = eval.New(s); err != nil {
		return ov, nil, err.Error(), nil
	}
	flows, err := ed.sheetFlows(cur.Path)
	if err != nil {
		return nil, nil, "", err
	}
	for _, sf := range flows {
		fx, ok, err := sf.fix(s)
		if err != nil {
			return nil, nil, "", err
		}
		if ok {
			ov[flow.OverlayKey(sf.abs)] = fx.content
		}
	}
	return ov, eng, "", nil
}

// raiseVersion raises the version in content to one past the committed
// one, when the file as it was (at version before) still has the committed
// version: the version rises once per commit. Content that does not parse
// is returned as it is.
func (ed *Editor) raiseVersion(path string, before int, content []byte, kind *tomledit.Layout) ([]byte, bool, error) {
	committed, ok, err := store.CommittedVersion(ed.Store, path)
	if err != nil {
		return nil, false, err
	}
	if !ok || before != committed {
		return content, false, nil
	}
	doc, err := tomledit.Parse(content, kind)
	if err != nil {
		return content, false, nil
	}
	doc.Set("version", int64(committed+1))
	out, err := doc.Bytes()
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
