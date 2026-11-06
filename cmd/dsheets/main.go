// Command dsheets validates, evaluates, tests and renders derivation sheets.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/evidence"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/render"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/validate"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/verify"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/schema"
)

// errUsage marks a command-line mistake (exit code 2).
var errUsage = errors.New("usage")

// errFailed marks a check that ran and failed (exit code 1). Details have
// already been printed.
var errFailed = errors.New("failed")

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	switch {
	case err == nil:
	case errors.Is(err, errUsage):
		os.Exit(2)
	case errors.Is(err, errFailed):
		os.Exit(1)
	case errors.Is(err, errIncomplete):
		os.Exit(3)
	default:
		fmt.Fprintf(os.Stderr, "%s: %v\n", brand.Command, err)
		os.Exit(1)
	}
}

var commands = []struct {
	name, args, help string
	fn               func(args []string, out, errw io.Writer) error
}{
	{"validate", "<workflow>", "check sheet.toml and its data; report errors with file and line", cmdValidate},
	{"cases", "<workflow>", "generate cases.toml covering every rule, reject and situational condition", cmdCases},
	{"render", "<workflow|flow> --out page.html", "write the calculation sheet, or a flow's horizontal view, as one self-contained HTML file", cmdRender},
	{"verify", "<workflow> --actual results.json", "compare actual output payloads with cases.toml and record evidence", cmdVerify},
	{"eval", "<workflow> (--case ID | --input in.json | --request req.json)", "evaluate one input and print the payload and full trace", cmdEval},
	{"serve", "<workflow> [--addr localhost:8080]", "serve the rendered sheet on localhost, re-rendered on each request", cmdServe},
	{"slice", "<flow> [--out flow.html]", "place each row in a team's step and report what every boundary must carry", cmdSlice},
	{"interface", "import <schema> --out <file.toml>", "import an interface from OpenAPI, Avro or JSON Schema", cmdInterface},
	{"fetch", "<flow>", "fetch or update the git sources a flow refers to", cmdFetch},
	{"impact", "[--as alias] [--base branch]", "report which other teams' flows this repository's changes break", cmdImpact},
	{"schema", "[sheet|flow|interface]", "print a JSON Schema (default: sheet.toml)", cmdSchema},
}

func run(args []string, out, errw io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(out)
		if len(args) == 0 {
			return errUsage
		}
		return nil
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.fn(args[1:], out, errw)
		}
	}
	fmt.Fprintf(errw, "%s: unknown command %q\n\n", brand.Command, args[0])
	usage(errw)
	return errUsage
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "%s: %s tool\n\nUsage:\n", brand.Command, brand.Product)
	for _, c := range commands {
		fmt.Fprintf(w, "  %s %-9s %s\n      %s\n", brand.Command, c.name, c.args, c.help)
	}
	fmt.Fprintf(w, "\nA workflow is a folder such as specs/order-checkout containing %s; a flow is a folder with flow.toml.\nRun '%s <command> -h' for a command's flags.\n", sheet.FileName, brand.Command)
}

// parse parses flags that may come before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, errUsage
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func newFlags(name, args string, errw io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.Usage = func() {
		fmt.Fprintf(errw, "Usage: %s %s %s\n", brand.Command, name, args)
		fs.PrintDefaults()
	}
	return fs
}

func oneWorkflow(fs *flag.FlagSet, pos []string, errw io.Writer) (string, error) {
	if len(pos) != 1 {
		fs.Usage()
		return "", errUsage
	}
	return pos[0], nil
}

// load reads and validates a workflow. Errors are printed; warnings are
// printed only when verbose is set.
func load(dir string, errw io.Writer, showWarnings bool) (*sheet.Sheet, *validate.Report, error) {
	s, err := sheet.Load(dir)
	if err != nil {
		var pe *sheet.ParseError
		if errors.As(err, &pe) {
			fmt.Fprintf(errw, "%s:%d: error[toml]: %s\n", pe.File, pe.Line, pe.Message)
			return nil, nil, errFailed
		}
		return nil, nil, err
	}
	rep := validate.Validate(s)
	for _, d := range rep.Diagnostics {
		if d.Severity == validate.Error || showWarnings {
			fmt.Fprintln(errw, d)
		}
	}
	if !rep.OK() {
		fmt.Fprintf(errw, "%s: %d errors; fix them first (run '%s validate %s')\n", dir, rep.Count(validate.Error), brand.Command, dir)
		return s, rep, errFailed
	}
	return s, rep, nil
}

func engine(dir string, errw io.Writer) (*eval.Engine, error) {
	s, _, err := load(dir, errw, false)
	if err != nil {
		return nil, err
	}
	return eval.New(s)
}

func cmdValidate(args []string, out, errw io.Writer) error {
	fs := newFlags("validate", "<workflow> [--json]", errw)
	asJSON := fs.Bool("json", false, "print diagnostics as JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir, err := oneWorkflow(fs, pos, errw)
	if err != nil {
		return err
	}
	s, err := sheet.Load(dir)
	if err != nil {
		var pe *sheet.ParseError
		if errors.As(err, &pe) {
			if *asJSON {
				return writeJSON(out, []validate.Diagnostic{{Severity: validate.Error, Code: "toml", File: pe.File, Line: pe.Line, Message: pe.Message}})
			}
			fmt.Fprintf(out, "%s:%d: error[toml]: %s\n", pe.File, pe.Line, pe.Message)
			return errFailed
		}
		return err
	}
	rep := validate.Validate(s)
	if *asJSON {
		if err := writeJSON(out, rep.Diagnostics); err != nil {
			return err
		}
	} else {
		for _, d := range rep.Diagnostics {
			fmt.Fprintln(out, d)
		}
		fmt.Fprintf(out, "%s: %d rows, %d errors, %d warnings\n", s.File, len(s.Rows), rep.Count(validate.Error), rep.Count(validate.Warning))
	}
	if !rep.OK() {
		return errFailed
	}
	return nil
}

func cmdCases(args []string, out, errw io.Writer) error {
	fs := newFlags("cases", "<workflow> [--reference-time RFC3339] [--out cases.toml]", errw)
	refFlag := fs.String("reference-time", cases.DefaultReferenceTime, "request time used by every case; dates are placed relative to it")
	outFlag := fs.String("out", "", "output file (default <workflow>/cases.toml)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir, err := oneWorkflow(fs, pos, errw)
	if err != nil {
		return err
	}
	ref, err := time.Parse(time.RFC3339, *refFlag)
	if err != nil {
		return fmt.Errorf("--reference-time: %v", err)
	}
	e, err := engine(dir, errw)
	if err != nil {
		return err
	}
	path := *outFlag
	if path == "" {
		path = cases.Path(dir)
	}
	old, err := cases.Read(path)
	if err != nil {
		old = nil
	}
	content, sum := cases.Regenerate(e, old, ref)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	for _, l := range sum.Lines(path) {
		fmt.Fprintln(out, l)
	}
	return nil
}

func evidenceStore(dir, flagDir string) *evidence.FileStore {
	if flagDir != "" {
		return &evidence.FileStore{Root: flagDir}
	}
	return &evidence.FileStore{Root: evidence.DefaultRoot(dir)}
}

func cmdRender(args []string, out, errw io.Writer) error {
	fs := newFlags("render", "<workflow|flow> --out page.html", errw)
	outFlag := fs.String("out", "", "output HTML file (required; - for stdout)")
	evDir := fs.String("evidence-dir", "", "evidence folder (default: .evidence at the repository root)")
	noEv := fs.Bool("no-evidence", false, "leave evidence out of the page entirely")
	evMode := fs.String("evidence", "", "initial state of the page's evidence switch: shown or hidden (default: the sheet's display.evidence)")
	snap := fs.String("snapshot-date", "", "date of the database and config snapshot, YYYY-MM-DD (default: newest file in data/)")
	check := fs.Bool("check", false, "flows only: mark missing and unfit fields, and list what each team has to agree")
	title := fs.String("title", "", "with several flows: the title of the combined grid")
	offline := fs.Bool("offline", false, "do not fetch git sources; use what is already fetched")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) <= 1 {
		if _, err := oneWorkflow(fs, pos, errw); err != nil {
			return err
		}
	}
	fe := newFetcher(*offline)
	dirs, err := argPaths(pos, fe)
	if err != nil {
		return err
	}
	dir := dirs[0]
	if *outFlag == "" {
		fs.Usage()
		return errUsage
	}
	if *check && !isFlow(dir) {
		return fmt.Errorf("--check applies to flows; %s is a sheet (use validate and verify for sheets)", dir)
	}
	if isFlow(dir) {
		var buf bytes.Buffer
		if err := flowPage(dirs, *title, *check, fe, &buf, errw); err != nil {
			return err
		}
		if *outFlag == "-" {
			_, err := out.Write(buf.Bytes())
			return err
		}
		if err := os.WriteFile(*outFlag, buf.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s (flow view)\n", *outFlag)
		return nil
	}
	mode, err := evidenceMode(*evMode, *noEv)
	if err != nil {
		return err
	}
	page, err := buildPage(dir, *evDir, mode, *snap, errw)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := render.Write(&buf, page); err != nil {
		return err
	}
	if *outFlag == "-" {
		_, err := out.Write(buf.Bytes())
		return err
	}
	if err := os.WriteFile(*outFlag, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (%s)\n", *outFlag, page.EvidenceLine)
	return nil
}

// evidenceMode turns --evidence and --no-evidence into a render mode.
func evidenceMode(flagValue string, off bool) (string, error) {
	switch {
	case off && flagValue != "":
		return "", fmt.Errorf("use either --evidence or --no-evidence, not both")
	case off:
		return render.EvidenceOff, nil
	case flagValue == "" || flagValue == sheet.EvidenceShown || flagValue == sheet.EvidenceHidden:
		return flagValue, nil
	}
	return "", fmt.Errorf("--evidence must be %q or %q", sheet.EvidenceShown, sheet.EvidenceHidden)
}

func buildPage(dir, evDir, mode, snap string, errw io.Writer) (*render.Page, error) {
	e, err := engine(dir, errw)
	if err != nil {
		return nil, err
	}
	opt := render.Options{Now: time.Now(), Evidence: mode}
	if snap != "" {
		t, err := time.Parse("2006-01-02", snap)
		if err != nil {
			return nil, fmt.Errorf("--snapshot-date: %v", err)
		}
		opt.SnapshotDate = t
	}
	if mode != render.EvidenceOff {
		run, err := evidenceStore(dir, evDir).Latest(e.Sheet.Slug, e.Sheet.Version)
		if err != nil {
			return nil, fmt.Errorf("reading evidence: %v", err)
		}
		opt.Run = run
	}
	return render.Build(e, opt), nil
}

func cmdVerify(args []string, out, errw io.Writer) error {
	fs := newFlags("verify", "<workflow> --actual results.json", errw)
	actual := fs.String("actual", "", "actual output payloads per case ID (required)")
	casesFlag := fs.String("cases", "", "cases file (default <workflow>/cases.toml)")
	evDir := fs.String("evidence-dir", "", "evidence folder (default: .evidence at the repository root)")
	dry := fs.Bool("dry-run", false, "compare without recording evidence")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir, err := oneWorkflow(fs, pos, errw)
	if err != nil {
		return err
	}
	if *actual == "" {
		fs.Usage()
		return errUsage
	}
	e, err := engine(dir, errw)
	if err != nil {
		return err
	}
	cpath := *casesFlag
	if cpath == "" {
		cpath = cases.Path(dir)
	}
	cf, err := cases.Read(cpath)
	if err != nil {
		return fmt.Errorf("%v (run '%s cases %s' first)", err, brand.Command, dir)
	}
	results, err := verify.ReadResults(*actual)
	if err != nil {
		return err
	}
	rep := verify.Verify(e, cf, results, *actual, time.Now())
	for _, n := range rep.Notes {
		fmt.Fprintln(out, "note:", n)
	}
	for _, p := range rep.Problems {
		fmt.Fprintln(out, "contradicted:", p)
	}
	verified, contradicted := 0, 0
	for _, t := range rep.Run.Rows {
		if len(t.Contradictions) > 0 {
			contradicted++
		} else if t.Exercised > 0 {
			verified++
		}
	}
	fmt.Fprintf(out, "%d cases compared: %d rows agree, %d rows contradicted\n", rep.Run.Observations, verified, contradicted)
	if !*dry {
		path, err := evidenceStore(dir, *evDir).Save(rep.Run)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "evidence recorded in %s\n", path)
	}
	if len(rep.Problems) > 0 {
		return errFailed
	}
	return nil
}

func cmdEval(args []string, out, errw io.Writer) error {
	fs := newFlags("eval", "<workflow> (--case ID | --input in.json | --request req.json [--context ctx.json]) [--json]", errw)
	caseID := fs.String("case", "", "evaluate a case from cases.toml")
	inputFile := fs.String("input", "", "JSON object of input values keyed by row ID")
	reqFile := fs.String("request", "", "incoming request JSON; inputs are read by their source paths")
	ctxFile := fs.String("context", "", "context JSON for context.* sources, used with --request")
	asJSON := fs.Bool("json", false, "print the result and trace as JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir, err := oneWorkflow(fs, pos, errw)
	if err != nil {
		return err
	}
	e, err := engine(dir, errw)
	if err != nil {
		return err
	}
	var in eval.Input
	switch {
	case *caseID != "":
		cf, err := cases.Read(cases.Path(dir))
		if err != nil {
			return err
		}
		for _, c := range cf.Cases {
			if c.ID == *caseID {
				in = c.Input
			}
		}
		if in == nil {
			return fmt.Errorf("no case %q in %s", *caseID, cases.Path(dir))
		}
	case *inputFile != "":
		m, err := readJSONObject(*inputFile)
		if err != nil {
			return err
		}
		in = m
	case *reqFile != "":
		req, err := readJSONObject(*reqFile)
		if err != nil {
			return err
		}
		ctx := map[string]any{}
		if *ctxFile != "" {
			if ctx, err = readJSONObject(*ctxFile); err != nil {
				return err
			}
		}
		in = eval.FromSources(e.Sheet, req, ctx)
	default:
		fs.Usage()
		return errUsage
	}
	res := e.Evaluate(in)
	if *asJSON {
		return writeJSON(out, map[string]any{
			"payload": res.Payload, "calls": res.CallPayloads, "reject": res.Reject, "error": res.Err, "trace": res.Steps,
		})
	}
	printTrace(out, e, res)
	if res.Err != nil {
		return errFailed
	}
	return nil
}

func printTrace(w io.Writer, e *eval.Engine, res *eval.Result) {
	fmt.Fprintln(w, "Trace:")
	for _, st := range res.Steps {
		val := sheet.FormatValue(st.Value)
		if st.Status == eval.StatusUnknown {
			val = "?"
		}
		line := fmt.Sprintf("  %-16s %-8s %-14s", st.Row, st.Rule, val)
		if st.Status != eval.StatusValue {
			line += " [" + st.Status + "]"
		}
		if st.Detail != "" {
			line += "  " + st.Detail
		}
		if st.Rejected && res.Reject != nil {
			line += fmt.Sprintf("  REJECT %s: %s", res.Reject.Code, res.Reject.Message)
		}
		if st.Send != nil {
			line += fmt.Sprintf("  -> %s: %s", st.Send.Field, st.Send.Reason)
		}
		for _, cs := range st.CallSends {
			line += fmt.Sprintf("  -> %s.%s: %s", cs.Target, cs.Field, cs.Reason)
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	switch {
	case res.Err != nil:
		fmt.Fprintf(w, "\nError: %v\n", res.Err)
	case res.Reject != nil:
		fmt.Fprintf(w, "\nRejected: %s (%s)\n", res.Reject.Code, res.Reject.Message)
	default:
		fmt.Fprintln(w, "\nPayload:")
		b, _ := json.MarshalIndent(res.Payload, "  ", "  ")
		fmt.Fprintf(w, "  %s\n", b)
		targets := make([]string, 0, len(res.CallPayloads))
		for t := range res.CallPayloads {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		for _, t := range targets {
			fmt.Fprintf(w, "\nCall %s:\n", t)
			b, _ := json.MarshalIndent(res.CallPayloads[t], "  ", "  ")
			fmt.Fprintf(w, "  %s\n", b)
		}
	}
}

func cmdServe(args []string, out, errw io.Writer) error {
	fs := newFlags("serve", "<workflow> [--addr localhost:8080]", errw)
	addr := fs.String("addr", "localhost:8080", "listen address; keep it on localhost")
	evDir := fs.String("evidence-dir", "", "evidence folder (default: .evidence at the repository root)")
	evMode := fs.String("evidence", "", "initial state of the evidence switch: shown or hidden (default: the sheet's display.evidence)")
	noEv := fs.Bool("no-evidence", false, "leave evidence out of the page entirely")
	check := fs.Bool("check", false, "flows only: mark missing and unfit fields, and list what each team has to agree")
	title := fs.String("title", "", "with several flows: the title of the combined grid")
	offline := fs.Bool("offline", false, "do not fetch git sources; use what is already fetched")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) <= 1 {
		if _, err := oneWorkflow(fs, pos, errw); err != nil {
			return err
		}
	}
	// One fetcher for the server's lifetime: a reload fetches followed
	// branches again only once their last fetch is a minute old.
	fe := newFetcher(*offline)
	fe.MaxAge = time.Minute
	dirs, err := argPaths(pos, fe)
	if err != nil {
		return err
	}
	dir := dirs[0]
	mode, err := evidenceMode(*evMode, *noEv)
	if err != nil {
		return err
	}
	h := serveHandler(pos, serveOptions{Title: *title, Check: *check, EvidenceDir: *evDir, Mode: mode}, fe, errw)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "serving %s at http://%s/ (re-rendered on every request; Ctrl+C to stop)\n", dir, ln.Addr())
	return http.Serve(ln, h)
}

// serveOptions are the page settings serve passes to every request.
type serveOptions struct {
	Title, EvidenceDir, Mode string
	Check                    bool
}

// serveHandler re-renders the page on every request. Arguments given as
// alias:path are resolved again each time, so a flow from another
// repository follows its branch; the fetcher's MaxAge keeps that cheap.
func serveHandler(pos []string, o serveOptions, fe *sources.Fetcher, errw io.Writer) http.Handler {
	fail := func(w http.ResponseWriter, msgs *bytes.Buffer, err error) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, "%s%v\n", msgs.String(), err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		var msgs bytes.Buffer
		dirs, err := argPaths(pos, fe)
		if err != nil {
			fail(w, &msgs, err)
			return
		}
		if isFlow(dirs[0]) {
			var buf bytes.Buffer
			if err := flowPage(dirs, o.Title, o.Check, fe, &buf, &msgs); err != nil {
				fail(w, &msgs, err)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(buf.Bytes())
			return
		}
		page, err := buildPage(dirs[0], o.EvidenceDir, o.Mode, "", &msgs)
		if err != nil {
			fail(w, &msgs, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := render.Write(w, page); err != nil {
			fmt.Fprintln(errw, err)
		}
	})
	return mux
}

func cmdSchema(args []string, out, errw io.Writer) error {
	which := "sheet"
	if len(args) > 0 {
		which = args[0]
	}
	data := map[string][]byte{"sheet": schema.Sheet, "flow": schema.Flow, "interface": schema.Interface}[which]
	if data == nil {
		fmt.Fprintf(errw, "%s schema: expected sheet, flow or interface\n", brand.Command)
		return errUsage
	}
	_, err := out.Write(data)
	return err
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return m, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
