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

// WriteMarkdown writes the report as markdown that also reads as plain text.
// Label changes (names, descriptions) and formatting-only changes appear
// only when all is true, much as `git diff -w` hides whitespace.
func (r *Report) WriteMarkdown(w io.Writer, all bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "## dsheets diff: %s %s..%s\n\n", r.Repo, r.Base, r.Head)
	shown := 0
	for _, s := range r.Sheets {
		shown += writeSheet(&b, s, all)
	}
	for _, f := range r.Flows {
		shown += writeFlow(&b, f, all)
	}
	for _, i := range r.Interfaces {
		shown += writeInterface(&b, i, all)
	}
	for _, c := range r.Cases {
		shown += writeCases(&b, c)
	}
	if shown == 0 {
		b.WriteString("No sheet or flow differences.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
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

func heading(b *strings.Builder, kind, name, path string, status Status, oldPath string) {
	fmt.Fprintf(b, "### %s `%s` - %s", kind, name, path)
	switch {
	case oldPath != "":
		fmt.Fprintf(b, " (moved from %s)", oldPath)
	case status == Added || status == Removed:
		fmt.Fprintf(b, " (%s)", status)
	}
	b.WriteString("\n\n")
}

// problem writes the lines for a file that could not be compared and
// reports whether there was one.
func problem(b *strings.Builder, side string) bool {
	if side == "" {
		return false
	}
	fmt.Fprintf(b, "Not compared: the file is not valid TOML at %s.\n\n", side)
	return true
}

func writeSheet(b *strings.Builder, s SheetChange, all bool) int {
	var body strings.Builder
	if problem(&body, s.Unparseable) {
		heading(b, "Sheet", s.Name, s.Path, s.Status, s.OldPath)
		b.WriteString(body.String())
		return 1
	}
	meta := visible(s.Meta, all)
	writeFields(&body, meta)
	if len(meta) > 0 {
		body.WriteString("\n")
	}
	rows := 0
	for _, r := range s.Rows {
		fs := visible(r.Fields, all)
		if len(fs) == 0 && r.Status == Changed {
			continue
		}
		rows++
		fmt.Fprintf(&body, "**%s** - %s\n", r.ID, r.Status)
		writeRowFields(&body, fs, r.Status)
		body.WriteString("\n")
	}
	textOnly := all && s.TextOnly
	if textOnly {
		body.WriteString("Formatting or comments only.\n\n")
	}
	if len(meta)+rows == 0 && !textOnly && s.OldPath == "" && s.Status == Changed {
		return 0
	}
	heading(b, "Sheet", s.Name, s.Path, s.Status, s.OldPath)
	b.WriteString(body.String())
	return 1
}

func writeFlow(b *strings.Builder, f FlowChange, all bool) int {
	var body strings.Builder
	if problem(&body, f.Unparseable) {
		heading(b, "Flow", f.Name, f.Path, f.Status, f.OldPath)
		b.WriteString(body.String())
		return 1
	}
	meta := visible(append(append([]FieldChange{}, f.Meta...), f.Stages...), all)
	writeFields(&body, meta)
	if len(meta) > 0 {
		body.WriteString("\n")
	}
	for _, bc := range f.Boundaries {
		fmt.Fprintf(&body, "**%s**\n", bc.Where)
		if bc.Interface.Path != "" {
			fmt.Fprintf(&body, "- interface: `%s` -> `%s`\n", bc.Interface.Before, bc.Interface.After)
		}
		if len(bc.Added) > 0 {
			fmt.Fprintf(&body, "- now carries: %s\n", codeList(bc.Added))
		}
		if len(bc.Dropped) > 0 {
			fmt.Fprintf(&body, "- no longer carries: %s\n", codeList(bc.Dropped))
		}
		for _, rw := range bc.Rewired {
			fmt.Fprintf(&body, "- `%s` now carries `%s` (was `%s`)\n", rw.Path, rw.After, rw.Before)
		}
		body.WriteString("\n")
	}
	if len(f.Computes) > 0 {
		body.WriteString("Who computes what:\n")
		for _, c := range f.Computes {
			fmt.Fprintf(&body, "- `%s`: %s -> %s\n", c.Row, team(c.Before), team(c.After))
		}
		body.WriteString("\n")
	}
	textOnly := all && f.TextOnly
	if textOnly {
		body.WriteString("Formatting or comments only.\n\n")
	}
	if body.Len() == 0 && f.OldPath == "" && f.Status == Changed {
		return 0
	}
	heading(b, "Flow", f.Name, f.Path, f.Status, f.OldPath)
	b.WriteString(body.String())
	return 1
}

func writeInterface(b *strings.Builder, i InterfaceChange, all bool) int {
	var body strings.Builder
	if problem(&body, i.Unparseable) {
		heading(b, "Interface", i.Name, i.Path, i.Status, i.OldPath)
		b.WriteString(body.String())
		return 1
	}
	fs := visible(append(append([]FieldChange{}, i.Meta...), i.Fields...), all)
	writeFields(&body, fs)
	if len(fs) > 0 {
		body.WriteString("\n")
	}
	textOnly := all && i.TextOnly
	if textOnly {
		body.WriteString("Formatting or comments only.\n\n")
	}
	if len(fs) == 0 && !textOnly && i.OldPath == "" && i.Status == Changed {
		return 0
	}
	heading(b, "Interface", i.Name, i.Path, i.Status, i.OldPath)
	b.WriteString(body.String())
	return 1
}

func writeCases(b *strings.Builder, c CasesChange) int {
	heading(b, "Cases", c.Sheet, c.Path, c.Status, "")
	for _, r := range c.Cases {
		fmt.Fprintf(b, "**%s** - %s\n", r.ID, r.Status)
		writeRowFields(b, r.Fields, r.Status)
		b.WriteString("\n")
	}
	return 1
}

// writeRowFields writes a row's fields. An added or removed row lists what
// it has; a changed row shows before and after.
func writeRowFields(b *strings.Builder, fs []FieldChange, status Status) {
	for _, f := range fs {
		switch status {
		case Added:
			fmt.Fprintf(b, "- `%s`: `%s`\n", f.Path, f.After)
		case Removed:
			fmt.Fprintf(b, "- `%s`: `%s`\n", f.Path, f.Before)
		default:
			writeField(b, f)
		}
	}
}

func writeFields(b *strings.Builder, fs []FieldChange) {
	for _, f := range fs {
		writeField(b, f)
	}
}

func writeField(b *strings.Builder, f FieldChange) {
	switch {
	case f.Before == "":
		fmt.Fprintf(b, "- `%s` added: `%s`\n", f.Path, f.After)
	case f.After == "":
		fmt.Fprintf(b, "- `%s` removed (was `%s`)\n", f.Path, f.Before)
	default:
		fmt.Fprintf(b, "- `%s`\n  - before: `%s`\n  - after: `%s`\n", f.Path, f.Before, f.After)
	}
}

func codeList(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "`" + n + "`"
	}
	return strings.Join(parts, ", ")
}

func team(name string) string {
	if name == "" {
		return "(nobody)"
	}
	return name
}
