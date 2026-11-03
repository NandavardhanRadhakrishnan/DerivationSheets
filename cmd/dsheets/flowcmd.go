package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flowrender"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/iface"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// isFlow reports whether a folder holds a flow.toml.
func isFlow(path string) bool {
	if strings.HasSuffix(path, flow.FileName) {
		return true
	}
	_, err := os.Stat(filepath.Join(path, flow.FileName))
	return err == nil
}

func cmdInterface(args []string, out, errw io.Writer) error {
	if len(args) == 0 || args[0] != "import" {
		fmt.Fprintf(errw, "Usage: %s interface import <schema-file> --out <file.toml> [flags]\n", brand.Command)
		return errUsage
	}
	fs := newFlags("interface import", "<schema-file> --out <file.toml> [flags]", errw)
	outFile := fs.String("out", "", "interface file to write (required)")
	ptr := fs.String("pointer", "", "JSON pointer to a schema, e.g. #/components/schemas/Order")
	op := fs.String("operation", "", "OpenAPI operationId whose body to import")
	part := fs.String("part", "request", "with --operation: request, response, or response:<status>")
	name := fs.String("name", "", "interface name (default: from the schema)")
	title := fs.String("title", "", "display title")
	kind := fs.String("kind", "", "api-request, api-response, kafka-topic, event, file or other (default: from the format)")
	owner := fs.String("owner", "", "owning team")
	status := fs.String("status", "", "existing (default) or proposed")
	version := fs.String("version", "", "the interface's own version")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 1 || *outFile == "" {
		fs.Usage()
		return errUsage
	}
	src := pos[0]
	// Record the source relative to the output, so the file stays portable.
	in, err := iface.Import(src, iface.ImportOptions{Pointer: *ptr, Operation: *op, Part: *part,
		Name: *name, Title: *title, Kind: *kind, Owner: *owner, Status: *status, Version: *version})
	if err != nil {
		return err
	}
	if absSrc, err := filepath.Abs(src); err == nil {
		if absOut, err := filepath.Abs(filepath.Dir(*outFile)); err == nil {
			if rel, err := filepath.Rel(absOut, absSrc); err == nil {
				in.Imported = filepath.ToSlash(rel)
			}
		}
	}
	schemaRel := ""
	outDir, _ := filepath.Abs(filepath.Dir(*outFile))
	if root := findUp(outDir, "schema/interface.schema.json"); root != "" {
		if rel, err := filepath.Rel(outDir, root); err == nil {
			schemaRel = filepath.ToSlash(rel)
		}
	}
	if err := os.MkdirAll(filepath.Dir(*outFile), 0o755); err != nil {
		return err
	}
	if err := iface.Write(*outFile, in, schemaRel); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s: %s (%s), %d fields\n", *outFile, in.Name, in.Kind, len(in.Fields))
	if in.Owner == "" {
		fmt.Fprintln(out, "  note: no --owner given; set owner so missing fields are routed to the right team")
	}
	return nil
}

// findUp looks for rel in dir and its parents, returning the full path.
func findUp(dir, rel string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		p := filepath.Join(d, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

func loadFlow(dir string, offline, refresh bool) (*flow.Analysis, error) {
	f, err := flow.Load(dir)
	if err != nil {
		return nil, err
	}
	fe := sources.NewFetcher(filepath.Join(sources.RepoRoot("."), sources.CacheDir))
	fe.Offline = offline
	return flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, fe)), nil
}

func cmdFetch(args []string, out, errw io.Writer) error {
	fs := newFlags("fetch", "<flow>", errw)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir, err := oneWorkflow(fs, pos, errw)
	if err != nil {
		return err
	}
	f, err := flow.Load(dir)
	if err != nil {
		return err
	}
	r := sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(filepath.Join(sources.RepoRoot("."), sources.CacheDir)))
	aliases := make([]string, 0, len(f.Sources))
	for a := range f.Sources {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	failed := false
	for _, a := range aliases {
		path, rev, err := r.Resolve(a + ":.")
		if err != nil {
			fmt.Fprintf(out, "%-12s error: %v\n", a, err)
			failed = true
			continue
		}
		src := f.Sources[a]
		if src.Git != "" {
			fmt.Fprintf(out, "%-12s %s @ %s (%s)\n", a, src.Git, src.Ref, rev)
		} else {
			fmt.Fprintf(out, "%-12s local %s\n", a, path)
		}
	}
	if failed {
		return errFailed
	}
	return nil
}

func cmdSlice(args []string, out, errw io.Writer) error {
	fs := newFlags("slice", "<flow> [--out flow.html] [--strict]", errw)
	outFile := fs.String("out", "", "also write the flow view, with the review marked, to this HTML file")
	strict := fs.Bool("strict", false, "exit 1 when any boundary has missing fields or warnings")
	export := fs.String("export", "", "also write each team's piece as its own flow and sheet into this folder")
	var with multiFlag
	fs.Var(&with, "with", "another flow this one shares an interface with; repeat for more. Traces what a branch lacks upstream, and what a feed-in must produce for the flows downstream")
	offline := fs.Bool("offline", false, "do not fetch git sources; use what is already fetched")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir, err := oneWorkflow(fs, pos, errw)
	if err != nil {
		return err
	}
	a, err := loadFlow(dir, *offline, false)
	if err != nil {
		return err
	}
	if len(with) > 0 && len(a.Errors) == 0 {
		var others []*flow.Analysis
		for _, w := range with {
			o, err := loadFlow(w, *offline, false)
			if err != nil {
				return err
			}
			if len(o.Errors) > 0 {
				fmt.Fprintf(out, "%s:\n", w)
				printAnalysis(out, o)
				return errFailed
			}
			others = append(others, o)
		}
		flow.Connect(a, others)
	}
	printAnalysis(out, a)
	if len(a.Errors) > 0 {
		return errFailed
	}
	if *outFile != "" {
		if err := writeFlowView(*outFile, a); err != nil {
			return err
		}
		fmt.Fprintf(out, "\nwrote %s\n", *outFile)
	}
	if *export != "" {
		if err := exportPieces(a, *export, out); err != nil {
			return err
		}
	}
	c := a.Counts()
	if *strict && (c[flow.Missing] > 0 || c[flow.Warn] > 0) {
		return errFailed
	}
	return nil
}

var statusMark = map[string]string{flow.Carried: "✓", flow.Missing: "✕", flow.Unused: "○", flow.Warn: "⚠"}

func printAnalysis(w io.Writer, a *flow.Analysis) {
	for _, p := range a.Errors {
		fmt.Fprintln(w, "error:", p)
	}
	for _, p := range a.Warnings {
		fmt.Fprintln(w, "warning:", p)
	}
	if len(a.Errors) > 0 || a.Sheet == nil {
		return
	}
	f := a.Flow
	title := f.Title
	if title == "" {
		title = f.Slug
	}
	fmt.Fprintf(w, "%s (flow %s, version %d), end-to-end sheet %s\n", title, f.Slug, f.Version, a.Sheet.File)
	for i, b := range a.Boundaries {
		if i > 0 {
			st := a.Steps[i-1]
			list := strings.Join(st.Rows, ", ")
			if list == "" {
				list = "nothing"
			}
			fmt.Fprintf(w, "\n  ↓ %s · %s · %s: computes %s\n", st.Label(), st.Team(), st.RoleLabel(), list)
		}
		fmt.Fprintln(w)
		if b.Iface == nil {
			fmt.Fprintf(w, "%s · (no interface)\n", b.RoleLabel())
			continue
		}
		in := b.Iface
		meta := iface.KindLabel(in.Kind)
		if in.Owner != "" {
			meta += " · " + in.Owner
		}
		meta += " · " + in.Status
		where := in.Ref
		if in.Revision != "" {
			where += " @ " + in.Revision
		}
		fmt.Fprintf(w, "%s · %s (%s)  [%s]\n", b.RoleLabel(), in.Name, meta, where)
		for _, it := range b.Items {
			line := fmt.Sprintf("  %s %-20s", statusMark[it.Status], it.Field)
			if it.Row != "" {
				line += " " + it.Row
				if it.Auto {
					line += " (matched by name)"
				}
			}
			// Every line reads: mark, field, row, then one reason.
			reason := it.Reason()
			if reason != "" {
				line = strings.TrimRight(line, " ") + ": " + reason
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
	c := a.Counts()
	fmt.Fprintf(w, "\n%d carried, %d missing, %d warnings, %d unused\n", c[flow.Carried], c[flow.Missing], c[flow.Warn], c[flow.Unused])
}

// writeFlowView writes the horizontal flow view in check mode: slice is the
// review command.
func writeFlowView(path string, a *flow.Analysis) error {
	var buf bytes.Buffer
	if err := flowrender.Write(&buf, flowrender.Build(a, time.Now(), true)); err != nil {
		return err
	}
	if path == "-" {
		_, err := os.Stdout.Write(buf.Bytes())
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// flowPage analyses a flow and renders it, printing problems to errw. check
// adds the review layer to the plain view.
func flowPage(dirs []string, title string, check bool, w io.Writer, errw io.Writer) error {
	var as []*flow.Analysis
	for _, d := range dirs {
		if !isFlow(d) {
			return fmt.Errorf("%s: several folders combine into one grid, so each must hold %s", d, flow.FileName)
		}
		a, err := loadFlow(d, false, false)
		if err != nil {
			return err
		}
		if len(a.Errors) > 0 {
			fmt.Fprintf(errw, "%s:\n", d)
			printAnalysis(errw, a)
			return errFailed
		}
		as = append(as, a)
	}
	if len(as) == 1 {
		return flowrender.Write(w, flowrender.Build(as[0], time.Now(), check))
	}
	if check {
		// Each flow is also checked against the others it shares interfaces with.
		for i, a := range as {
			others := append(append([]*flow.Analysis{}, as[:i]...), as[i+1:]...)
			flow.Connect(a, others)
		}
	}
	p, err := flowrender.BuildCombined(title, as, time.Now(), check)
	if err != nil {
		return err
	}
	return flowrender.Write(w, p)
}

// exportPieces writes each team's piece and checks it on its own.
func exportPieces(a *flow.Analysis, dir string, out io.Writer) error {
	pieces, err := a.Export(dir)
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "exported %d pieces to %s:", len(pieces), dir)
	fmt.Fprintln(out)
	for _, ex := range pieces {
		line := fmt.Sprintf("  %s: %s receives %s", filepath.Base(ex.Dir), ex.Piece.Team, ex.Receives)
		for _, c := range ex.Calls {
			line += ", calls " + c
		}
		line += ", produces " + ex.Produces
		fmt.Fprintln(out, line)
		pa, err := loadFlow(ex.Dir, true, false)
		if err != nil {
			return err
		}
		if len(pa.Errors) > 0 {
			fmt.Fprintln(out, "    the exported piece has errors:")
			for _, p := range pa.Errors {
				fmt.Fprintln(out, "      "+p.String())
			}
			return errFailed
		}
		c := pa.Counts()
		fmt.Fprintln(out, fmt.Sprintf("    checks on its own: %d carried, %d missing, %d warnings", c[flow.Carried], c[flow.Missing], c[flow.Warn]))
	}
	return nil
}

// multiFlag collects a repeated string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
