package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteMarkdown writes the report as markdown for a pull-request comment or
// a CI summary. Changes sit in fenced `diff` blocks, which GitHub, GitLab and
// Azure DevOps draw with removed lines red and added lines green. Label
// changes (names, descriptions) and formatting-only changes appear only when
// all is true, much as `git diff -w` hides whitespace.
func (r *Report) WriteMarkdown(w io.Writer, all bool) error {
	return r.write(w, all, true, false)
}

// WriteText writes the same report for a terminal: no fences, and with color
// the removed lines are red and the added lines green, as `git diff` does.
func (r *Report) WriteText(w io.Writer, all, color bool) error {
	return r.write(w, all, false, color)
}

// A change line starts with "- " (removed), "+ " (added) or two spaces
// (context, such as the name of the row the lines below belong to).
type printer struct {
	b           strings.Builder
	fence, hues bool
}

func (r *Report) write(w io.Writer, all, fence, color bool) error {
	p := &printer{fence: fence, hues: color}
	fmt.Fprintf(&p.b, "## dsheets diff: %s %s..%s\n\n", r.Repo, r.Base, r.Head)
	shown := 0
	for _, s := range r.Sheets {
		shown += p.sheet(s, all)
	}
	for _, f := range r.Flows {
		shown += p.flow(f, all)
	}
	for _, i := range r.Interfaces {
		shown += p.iface(i, all)
	}
	for _, c := range r.Cases {
		shown += p.cases(c)
	}
	if shown == 0 {
		p.b.WriteString("No sheet or flow differences.\n")
	}
	_, err := io.WriteString(w, p.b.String())
	return err
}

// block writes change lines, fenced for markdown and colored for a terminal.
func (p *printer) block(lines []string) {
	if len(lines) == 0 {
		return
	}
	if p.fence {
		p.b.WriteString("```diff\n")
	}
	for _, l := range lines {
		switch {
		case p.hues && strings.HasPrefix(l, "- "):
			l = "\x1b[31m" + l + "\x1b[0m"
		case p.hues && strings.HasPrefix(l, "+ "):
			l = "\x1b[32m" + l + "\x1b[0m"
		}
		p.b.WriteString(l + "\n")
	}
	if p.fence {
		p.b.WriteString("```\n")
	}
	p.b.WriteString("\n")
}

func (p *printer) heading(kind, name, path string, status Status, oldPath string) {
	fmt.Fprintf(&p.b, "### %s `%s` - %s", kind, name, path)
	switch {
	case oldPath != "":
		fmt.Fprintf(&p.b, " (moved from %s)", oldPath)
	case status == Added || status == Removed:
		fmt.Fprintf(&p.b, " (%s)", status)
	}
	p.b.WriteString("\n\n")
}

// problem writes the line for a file that could not be compared and reports
// whether there was one.
func (p *printer) problem(side string) bool {
	if side == "" {
		return false
	}
	fmt.Fprintf(&p.b, "Not compared: the file is not valid TOML at %s.\n\n", side)
	return true
}

// visible drops label changes unless all.
func visible(fs []FieldChange, all bool) []FieldChange {
	var out []FieldChange
	for _, f := range fs {
		if all || !f.Label {
			out = append(out, f)
		}
	}
	return out
}

// oneLine keeps a value on one line, so every change line stays one line.
func oneLine(s string) string {
	return strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(s)
}

// fieldLines shows a change as removed and added lines: a value that is new
// has only the added line, one that is gone only the removed line.
func fieldLines(f FieldChange) []string {
	var out []string
	if f.Before != "" {
		out = append(out, "- "+f.Path+": "+oneLine(f.Before))
	}
	if f.After != "" {
		out = append(out, "+ "+f.Path+": "+oneLine(f.After))
	}
	return out
}

func fieldsLines(fs []FieldChange) []string {
	var out []string
	for _, f := range fs {
		out = append(out, fieldLines(f)...)
	}
	return out
}

// rowLines shows a row under its id. An added row has only added lines and a
// removed row only removed lines.
func rowLines(r RowChange, fs []FieldChange) []string {
	out := []string{fmt.Sprintf("  %s (%s)", r.ID, r.Status)}
	for _, f := range fs {
		switch r.Status {
		case Added:
			out = append(out, "+ "+f.Path+": "+oneLine(f.After))
		case Removed:
			out = append(out, "- "+f.Path+": "+oneLine(f.Before))
		default:
			out = append(out, fieldLines(f)...)
		}
	}
	return out
}

func (p *printer) sheet(s SheetChange, all bool) int {
	if s.Unparseable != "" {
		p.heading("Sheet", s.Name, s.Path, s.Status, s.OldPath)
		p.problem(s.Unparseable)
		return 1
	}
	lines := fieldsLines(visible(s.Meta, all))
	changed := len(lines)
	for _, r := range s.Rows {
		fs := visible(r.Fields, all)
		if len(fs) == 0 && r.Status == Changed {
			continue
		}
		changed++
		lines = append(lines, rowLines(r, fs)...)
	}
	textOnly := all && s.TextOnly
	if changed == 0 && !textOnly && s.OldPath == "" && s.Status == Changed {
		return 0
	}
	p.heading("Sheet", s.Name, s.Path, s.Status, s.OldPath)
	p.block(lines)
	if textOnly {
		p.b.WriteString("Formatting or comments only.\n\n")
	}
	return 1
}

func (p *printer) flow(f FlowChange, all bool) int {
	if f.Unparseable != "" {
		p.heading("Flow", f.Name, f.Path, f.Status, f.OldPath)
		p.problem(f.Unparseable)
		return 1
	}
	lines := fieldsLines(visible(append(append([]FieldChange{}, f.Meta...), f.Stages...), all))
	for _, bc := range f.Boundaries {
		lines = append(lines, "  "+bc.Where)
		lines = append(lines, fieldLines(bc.Interface)...)
		for _, n := range bc.Dropped {
			lines = append(lines, "- carries: "+n)
		}
		for _, n := range bc.Added {
			lines = append(lines, "+ carries: "+n)
		}
		for _, rw := range bc.Rewired {
			lines = append(lines, "- carries "+rw.Path+": "+rw.Before, "+ carries "+rw.Path+": "+rw.After)
		}
	}
	if len(f.Computes) > 0 {
		lines = append(lines, "  who computes what")
		for _, c := range f.Computes {
			if c.Before != "" {
				lines = append(lines, "- "+c.Row+": "+c.Before)
			}
			if c.After != "" {
				lines = append(lines, "+ "+c.Row+": "+c.After)
			}
		}
	}
	textOnly := all && f.TextOnly
	if len(lines) == 0 && !textOnly && f.OldPath == "" && f.Status == Changed {
		return 0
	}
	p.heading("Flow", f.Name, f.Path, f.Status, f.OldPath)
	p.block(lines)
	if textOnly {
		p.b.WriteString("Formatting or comments only.\n\n")
	}
	return 1
}

func (p *printer) iface(i InterfaceChange, all bool) int {
	if i.Unparseable != "" {
		p.heading("Interface", i.Name, i.Path, i.Status, i.OldPath)
		p.problem(i.Unparseable)
		return 1
	}
	lines := fieldsLines(visible(append(append([]FieldChange{}, i.Meta...), i.Fields...), all))
	textOnly := all && i.TextOnly
	if len(lines) == 0 && !textOnly && i.OldPath == "" && i.Status == Changed {
		return 0
	}
	p.heading("Interface", i.Name, i.Path, i.Status, i.OldPath)
	p.block(lines)
	if textOnly {
		p.b.WriteString("Formatting or comments only.\n\n")
	}
	return 1
}

func (p *printer) cases(c CasesChange) int {
	p.heading("Cases", c.Sheet, c.Path, c.Status, "")
	var lines []string
	for _, r := range c.Cases {
		lines = append(lines, rowLines(r, r.Fields)...)
	}
	p.block(lines)
	return 1
}
