package editor

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
)

// DraftEngine builds an engine for the sheet a draft describes, without
// writing anything, so the page can show the draft before it is saved.
// A draft with errors is still shown, as an unfinished row is part of the
// work being reviewed; the check reports the errors. When the draft cannot
// be shown, e is nil and reason says why: its result does not parse or
// makes a cycle. err is for a file that cannot be read or a draft that
// cannot be applied.
func (ed *Editor) DraftEngine(d SheetDraft) (e *eval.Engine, reason string, err error) {
	cur, err := ed.Store.Load(d.File)
	if err != nil {
		return nil, "", err
	}
	content, err := d.Apply(cur.Data)
	if err != nil {
		return nil, "", err
	}
	abs := filepath.Join(ed.Root, filepath.FromSlash(cur.Path))
	s, err := sheet.Parse(filepath.Dir(abs), abs, string(content))
	var pe *sheet.ParseError
	switch {
	case errors.As(err, &pe):
		return nil, fmt.Sprintf("line %d: %s", pe.Line, pe.Message), nil
	case err != nil:
		return nil, "", err
	}
	if e, err = eval.New(s); err != nil {
		return nil, err.Error(), nil
	}
	return e, "", nil
}
