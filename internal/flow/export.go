package flow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"derivationsheets/internal/sheet"
	"derivationsheets/internal/sources"
)

// Piece is one team's part of a sliced flow: from the boundary it
// receives to the boundary it produces. A caller's piece spans its calls;
// each callee answers in a piece of its own.
type Piece struct {
	Team  string
	Steps []int // step indexes, in order
	In    int   // boundary received
	Out   int   // boundary produced
	Calls []int // request boundaries of calls made inside the piece
}

// Pieces groups the steps of a sliced flow by team piece.
func (a *Analysis) Pieces() []Piece {
	var out []Piece
	pieceOf := map[int]int{} // step -> piece index
	for _, st := range a.Steps {
		if st.KeepsFrom >= 0 {
			pi := pieceOf[st.KeepsFrom]
			out[pi].Steps = append(out[pi].Steps, st.Index)
			out[pi].Out = st.Index + 1
			pieceOf[st.Index] = pi
			continue
		}
		p := Piece{Team: st.Team(), Steps: []int{st.Index}, In: st.Index, Out: st.Index + 1}
		pieceOf[st.Index] = len(out)
		out = append(out, p)
	}
	for i := range out {
		for _, s := range out[i].Steps {
			if b := a.Boundaries[s+1]; b.Role == RoleRequest && s+1 != out[i].Out {
				out[i].Calls = append(out[i].Calls, s+1)
			}
		}
	}
	return out
}

// Exported describes one written piece.
type Exported struct {
	Piece    Piece
	Dir      string
	Receives string
	Calls    []string
	Produces string
	Missing  int // fields the piece needs that its boundaries do not carry yet
}

// Export writes each team's piece as its own flow and sheet under dir:
// dir/<n>-<team>/flow.toml and sheet.toml, with the data files its rows
// use. The analysis must be free of errors.
func (a *Analysis) Export(dir string) ([]Exported, error) {
	text, err := os.ReadFile(a.Sheet.File)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")
	var out []Exported
	for n, p := range a.Pieces() {
		pdir := filepath.Join(dir, fmt.Sprintf("%d-%s", n+1, slugify(p.Team)))
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			return nil, err
		}
		ex, err := a.exportPiece(n+1, p, pdir, lines)
		if err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return out, nil
}

func (a *Analysis) exportPiece(n int, p Piece, pdir string, lines []string) (Exported, error) {
	in, outB := a.Boundaries[p.In], a.Boundaries[p.Out]
	ex := Exported{Piece: p, Dir: pdir, Receives: in.Iface.Name, Produces: outB.Iface.Name}
	inPiece := map[int]bool{}
	for _, s := range p.Steps {
		inPiece[s] = true
	}
	final := outB.Role == RoleOutput

	// Rows the piece computes, and local rows (context, data, config) they read.
	computed := map[string]bool{}
	local := map[string]bool{}
	for _, r := range a.Engine.Graph.Order {
		if s, ok := a.Place[r.ID]; ok && inPiece[s] {
			computed[r.ID] = true
			for _, d := range a.Engine.Graph.Deps[r.ID] {
				if isLocal(a.Sheet.ByID[d]) {
					local[d] = true
				}
			}
		}
	}

	// Values that enter: across the boundary received, or back from a call.
	type entry struct {
		row    *sheet.Row
		source string
		open   string
	}
	var entries []entry
	entering := map[string]bool{}
	addEntries := func(b *Boundary, kind string) {
		for _, r := range a.Engine.Graph.Order {
			if !b.Needs(r.ID) || entering[r.ID] || computed[r.ID] {
				continue
			}
			field, open := proposedField(r), ""
			if c := b.CarryOf(r.ID); c != nil {
				field = c.Field.Name
			} else {
				open = fmt.Sprintf("%s has no field for this yet; %s is the proposed name", b.Iface.Name, field)
				ex.Missing++
			}
			src := "request." + field
			if kind == "call" {
				src = "call." + b.StageID + "." + field
			}
			entering[r.ID] = true
			entries = append(entries, entry{r, src, open})
		}
	}
	addEntries(in, "request")
	for _, ci := range p.Calls {
		addEntries(a.Boundaries[ci+1], "call")
		ex.Calls = append(ex.Calls, fmt.Sprintf("%s (%s)", a.Boundaries[ci].StageID, a.Steps[ci].Team()))
	}

	// Sends: to the boundary produced, and to each call made.
	sends := map[string][]string{}
	addSends := func(b *Boundary, target string) {
		for _, r := range a.Engine.Graph.Order {
			if !b.Needs(r.ID) || !(computed[r.ID] || entering[r.ID]) {
				continue
			}
			if final && target == "" {
				continue // the output keeps the sheet's own send rules
			}
			field := proposedField(r)
			if c := b.CarryOf(r.ID); c != nil {
				field = c.Field.Name
			} else {
				ex.Missing++
			}
			sends[r.ID] = append(sends[r.ID], sendBlock(&sheet.Send{Target: target, Field: field, Usage: sheet.UsageRequired}))
		}
	}
	addSends(outB, "")
	for _, ci := range p.Calls {
		addSends(a.Boundaries[ci], a.Boundaries[ci].StageID)
	}
	if final {
		for _, r := range a.Engine.Graph.Order {
			if r.Send != nil && (computed[r.ID] || entering[r.ID]) {
				sends[r.ID] = append([]string{sendBlock(r.Send)}, sends[r.ID]...)
			}
		}
	}

	// sheet.toml
	var b strings.Builder
	slug := fmt.Sprintf("%s-%d-%s", a.Flow.Slug, n, slugify(p.Team))
	fmt.Fprintf(&b, "#:schema %s\n", schemaRef(pdir, "sheet.schema.json"))
	fmt.Fprintf(&b, "# %s's piece of flow %s, exported by slice. Edit it as your own sheet.\n", p.Team, a.Flow.Slug)
	fmt.Fprintf(&b, "sheet   = %q\nversion = %d\ntitle   = %q\n", slug, a.Flow.Version, fmt.Sprintf("%s: %s to %s", p.Team, in.Iface.Name, outB.Iface.Name))
	writeRow := func(block string, id string) {
		b.WriteString("\n")
		b.WriteString(block)
		for _, sb := range sends[id] {
			b.WriteString("\n")
			b.WriteString(sb)
		}
	}
	if len(entries) > 0 {
		fmt.Fprintf(&b, "\n# ---------- Received ----------\n")
	}
	for _, e := range entries {
		var rb strings.Builder
		fmt.Fprintf(&rb, "[[row]]\nid     = %q\nname   = %q\nsource = %q\n", e.row.ID, e.row.DisplayName(), e.source)
		if t := rowType(e.row); t != "" {
			fmt.Fprintf(&rb, "type   = %q\n", t)
		}
		if e.open != "" {
			fmt.Fprintf(&rb, "open_questions = [%q]\n", e.open)
		}
		writeRow(rb.String(), e.row.ID)
	}
	var dataFiles []string
	copyRows := func(title string, keep map[string]bool) {
		first := true
		for _, r := range a.Engine.Graph.Order {
			if !keep[r.ID] {
				continue
			}
			if first {
				fmt.Fprintf(&b, "\n# ---------- %s ----------\n", title)
				first = false
			}
			writeRow(rowBlock(a.Sheet, lines, r), r.ID)
			if r.Lookup != nil && r.Lookup.ValuesFile != "" {
				dataFiles = append(dataFiles, r.Lookup.ValuesFile)
			}
			if r.SourceKind() == "config" || (r.Lookup != nil && r.Lookup.SourceKind() == "config" && r.Lookup.Inline == nil) {
				dataFiles = append(dataFiles, sheet.ConfigFile)
			}
		}
	}
	copyRows("Context and data", local)
	copyRows("Computed", computed)
	if err := os.WriteFile(filepath.Join(pdir, sheet.FileName), []byte(b.String()), 0o644); err != nil {
		return ex, err
	}
	for _, f := range uniqStrings(dataFiles) {
		src := filepath.Join(a.Sheet.Dir, filepath.FromSlash(f))
		data, err := os.ReadFile(src)
		if err != nil {
			continue // a missing data file is reported by validate
		}
		dst := filepath.Join(pdir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return ex, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return ex, err
		}
	}

	// flow.toml
	var fb strings.Builder
	fmt.Fprintf(&fb, "#:schema %s\n", schemaRef(pdir, "flow.schema.json"))
	fmt.Fprintf(&fb, "# %s's piece of flow %s, exported by slice.\n", p.Team, a.Flow.Slug)
	fmt.Fprintf(&fb, "flow    = %q\nversion = %d\ntitle   = %q\nsheet   = \".\"\n", slug, a.Flow.Version, fmt.Sprintf("%s: %s to %s", p.Team, in.Iface.Name, outB.Iface.Name))
	if len(a.Flow.Sources) > 0 {
		fb.WriteString("\n[sources]\n")
		aliases := make([]string, 0, len(a.Flow.Sources))
		for al := range a.Flow.Sources {
			aliases = append(aliases, al)
		}
		sort.Strings(aliases)
		for _, al := range aliases {
			src := a.Flow.Sources[al]
			if src.Git != "" {
				fmt.Fprintf(&fb, "%s = { git = %q, ref = %q }\n", al, src.Git, src.Ref)
			} else {
				fmt.Fprintf(&fb, "%s = { path = %q }\n", al, relocate(a.Flow.Dir, src.Path, pdir))
			}
		}
	}
	ref := func(b *Boundary) string {
		if alias, _ := sources.Split(b.Ref); alias != "" {
			return b.Ref
		}
		return relocate(a.Flow.Dir, b.Ref, pdir)
	}
	fmt.Fprintf(&fb, "\n[input]\ninterface = %q\n", ref(in))
	rowsOf := func(step int) string {
		var ids []string
		for _, id := range a.Steps[step].Rows {
			ids = append(ids, strconv.Quote(id))
		}
		return "[" + strings.Join(ids, ", ") + "]"
	}
	for _, ci := range p.Calls {
		req, resp := a.Boundaries[ci], a.Boundaries[ci+1]
		fmt.Fprintf(&fb, "\n[[stage]]\nid       = %q\nkind     = \"call\"\ncaller   = %q\ncomputes = %s\ncallee   = %q\n", req.StageID, p.Team, rowsOf(ci-1), a.Steps[ci].Team())
		fmt.Fprintf(&fb, "\n  [stage.request]\n  interface = %q\n\n  [stage.response]\n  interface = %q\n", ref(req), ref(resp))
	}
	fmt.Fprintf(&fb, "\n[output]\ninterface = %q\nproducer  = %q\ncomputes  = %s\n", ref(outB), p.Team, rowsOf(p.Steps[len(p.Steps)-1]))
	if err := os.WriteFile(filepath.Join(pdir, FileName), []byte(fb.String()), 0o644); err != nil {
		return ex, err
	}
	return ex, nil
}

var sendHeader = regexp.MustCompile(`^\s*\[\[?\s*row\.send\s*\]\]?\s*$`)
var anyHeader = regexp.MustCompile(`^\s*\[`)

// rowBlock copies a row's text from sheet.toml, without its send blocks,
// which the export writes itself. Trailing comments and blank lines, which
// usually introduce the next section, are dropped.
func rowBlock(s *sheet.Sheet, lines []string, r *sheet.Row) string {
	start := s.Pos.Rows[r.Index].Line - 1
	end := len(lines)
	if r.Index+1 < len(s.Pos.Rows) {
		end = s.Pos.Rows[r.Index+1].Line - 1
	}
	var out []string
	inSend := false
	for _, l := range lines[start:end] {
		switch {
		case sendHeader.MatchString(l):
			inSend = true
			continue
		case anyHeader.MatchString(l) && !strings.HasPrefix(strings.TrimSpace(l), "[[row]]"):
			inSend = false
		}
		if !inSend {
			out = append(out, l)
		}
	}
	for len(out) > 0 {
		t := strings.TrimSpace(out[len(out)-1])
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n") + "\n"
}

// sendBlock writes a send rule as an array-of-tables entry, so a row can
// have several.
func sendBlock(sd *sheet.Send) string {
	var b strings.Builder
	b.WriteString("  [[row.send]]\n")
	if sd.Target != "" {
		fmt.Fprintf(&b, "  target = %q\n", sd.Target)
	}
	fmt.Fprintf(&b, "  field  = %q\n  usage  = %q\n", sd.Field, sd.Usage)
	if sd.When != "" {
		fmt.Fprintf(&b, "  when      = %q\n", sd.When)
	}
	if sd.Otherwise != "" {
		fmt.Fprintf(&b, "  otherwise = %q\n", sd.Otherwise)
	}
	if sd.Map != nil {
		keys := sd.MapOrder
		if len(keys) == 0 {
			for k := range sd.Map {
				keys = append(keys, k)
			}
			sort.Strings(keys)
		}
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s = %q", tomlKey(k), sheet.FormatValue(sd.Map[k]))
		}
		fmt.Fprintf(&b, "  map    = { %s }\n", strings.Join(parts, ", "))
	}
	return b.String()
}

func tomlKey(k string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_\-]+$`).MatchString(k) {
		return k
	}
	return strconv.Quote(k)
}

// relocate rewrites a path relative to one folder as relative to another,
// or absolute when no relative path exists (different drives).
func relocate(fromDir, p, toDir string) string {
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(fromDir, filepath.FromSlash(p))
	}
	abs, _ = filepath.Abs(abs)
	to, _ := filepath.Abs(toDir)
	if rel, err := filepath.Rel(to, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(abs)
}

// schemaRef points an exported file at the schema folder of this repo.
func schemaRef(dir, name string) string {
	abs, _ := filepath.Abs(dir)
	for d := abs; ; d = filepath.Dir(d) {
		p := filepath.Join(d, "schema", name)
		if _, err := os.Stat(p); err == nil {
			if rel, err := filepath.Rel(abs, p); err == nil {
				return filepath.ToSlash(rel)
			}
			return filepath.ToSlash(p)
		}
		if filepath.Dir(d) == d {
			return "../../schema/" + name
		}
	}
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func uniqStrings(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
