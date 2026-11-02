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

	"derivationsheets/internal/brand"
	"derivationsheets/internal/flow"
	"derivationsheets/internal/flowrender"
	"derivationsheets/internal/iface"
	"derivationsheets/internal/sources"
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
	r := sources.NewResolver(f.Dir, f.Sources)
	r.Offline, r.Refresh = offline, refresh
	return flow.Analyze(f, r), nil
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
	r := sources.NewResolver(f.Dir, f.Sources)
	r.Refresh = true
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
	outFile := fs.String("out", "", "also write the horizontal flow view to this HTML file")
	strict := fs.Bool("strict", false, "exit 1 when any boundary has missing fields or warnings")
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
			owner := st.Owner
			if owner == "" {
				owner = "(no team named)"
			}
			role := ""
			switch {
			case st.Role == flow.StepCallee:
				role = ", answering the call"
			case st.KeepsFrom >= 0:
				role = ", continuing after the call"
			}
			names := make([]string, len(st.Rows))
			for k, id := range st.Rows {
				names[k] = id
			}
			list := strings.Join(names, ", ")
			if list == "" {
				list = "nothing"
			}
			fmt.Fprintf(w, "\n  ↓ %s%s computes %s\n", owner, role, list)
		}
		fmt.Fprintln(w)
		if b.Iface == nil {
			fmt.Fprintf(w, "%s: (no interface)\n", strings.ToUpper(b.Role))
			continue
		}
		in := b.Iface
		meta := []string{in.Kind, in.Status}
		if in.Owner != "" {
			meta = append(meta, in.Owner)
		}
		where := in.Ref
		if in.Revision != "" {
			where += " @ " + in.Revision
		}
		fmt.Fprintf(w, "%s %s (%s)  [%s]\n", strings.ToUpper(b.Role), in.Name, strings.Join(meta, ", "), where)
		for _, it := range b.Items {
			line := fmt.Sprintf("  %s %-20s", statusMark[it.Status], it.Field)
			if it.Row != "" {
				line += " " + it.Row
				if it.Auto {
					line += " (matched by name)"
				}
			}
			if len(it.Consumers) > 0 && it.Status != flow.Carried {
				line += "  needed by " + strings.Join(it.Consumers, ", ")
			}
			if it.Note != "" {
				line += "  — " + it.Note
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
	c := a.Counts()
	fmt.Fprintf(w, "\n%d carried, %d missing, %d warnings, %d unused\n", c[flow.Carried], c[flow.Missing], c[flow.Warn], c[flow.Unused])
}

// writeFlowView writes the horizontal flow view.
func writeFlowView(path string, a *flow.Analysis) error {
	var buf bytes.Buffer
	if err := flowrender.Write(&buf, flowrender.Build(a, time.Now())); err != nil {
		return err
	}
	if path == "-" {
		_, err := os.Stdout.Write(buf.Bytes())
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// flowPage analyses a flow and renders it, printing problems to errw.
func flowPage(dir string, w io.Writer, errw io.Writer) error {
	a, err := loadFlow(dir, false, false)
	if err != nil {
		return err
	}
	if len(a.Errors) > 0 {
		printAnalysis(errw, a)
		return errFailed
	}
	return flowrender.Write(w, flowrender.Build(a, time.Now()))
}
