// Package flow models a flow: an end-to-end derivation from an input
// interface to an output interface, sliced at boundaries owned by different
// teams, such as Kafka topics and API calls.
//
// A product manager writes the end-to-end sheet with the input and output.
// Each team then adds the boundary it owns as a stage, and names the rows it
// computes. Analysis places every row in a step, works out which values
// must cross each boundary, and compares that with the fields each
// interface carries.
package flow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/validate"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/schema"
)

// FileName is the flow file inside a flow folder.
const FileName = "flow.toml"

// Stage kinds.
const (
	KindHandoff = "handoff" // the next team takes over and only sees what the interface carries
	KindCall    = "call"    // a request and a response; the caller keeps its own state
)

// Flow is a parsed flow.toml.
type Flow struct {
	Dir, File string

	Slug     string
	Version  int
	Title    string
	SheetDir string

	Sources map[string]sources.Source
	Input   BoundarySpec
	Stages  []StageSpec
	Output  OutputSpec

	// Problems are shape errors found while loading.
	Problems []Problem
	lines    map[string]int
}

// BoundarySpec names an interface and, optionally, which row each field
// carries.
type BoundarySpec struct {
	Interface string
	Carries   map[string]string // field -> row ID
}

// StageSpec is one [[stage]].
type StageSpec struct {
	ID   string
	Kind string
	// Handoff.
	BoundarySpec
	Producer string
	// Call.
	Caller         string
	Callee         string
	CalleeComputes []string
	Request        BoundarySpec
	Response       BoundarySpec
	// Rows computed before this stage, by the producer or caller.
	Computes []string
	Index    int
}

// Owner is the team that computes the rows before the stage.
func (s StageSpec) Owner() string {
	if s.Kind == KindCall {
		return s.Caller
	}
	return s.Producer
}

// OutputSpec is the [output] table.
type OutputSpec struct {
	Interface string
	Producer  string
	Computes  []string
}

// Problem is an error or warning with its location.
type Problem struct {
	File    string
	Line    int
	Message string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message)
	}
	return fmt.Sprintf("%s: %s", p.File, p.Message)
}

// Load reads a flow folder or a flow.toml path.
func Load(path string) (*Flow, error) {
	dir, file := path, filepath.Join(path, FileName)
	if strings.HasSuffix(strings.ToLower(path), ".toml") {
		dir, file = filepath.Dir(path), path
	}
	text, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(text, &raw); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, _ := de.Position()
			return nil, fmt.Errorf("%s:%d: %v", file, line, err)
		}
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	f := &Flow{Dir: dir, File: file, lines: scanLines(string(text)), Sources: map[string]sources.Source{}}
	problems, err := validate.SchemaProblems(schema.Flow, raw)
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		f.Problems = append(f.Problems, Problem{File: file, Line: f.Line(p.Location...), Message: strings.Join(p.Location, ".") + ": " + p.Message})
	}

	f.Slug, _ = raw["flow"].(string)
	if v, ok := raw["version"].(int64); ok {
		f.Version = int(v)
	}
	f.Title, _ = raw["title"].(string)
	sheetRel, _ := raw["sheet"].(string)
	if sheetRel == "" {
		sheetRel = "."
	}
	f.SheetDir = filepath.Join(dir, filepath.FromSlash(sheetRel))
	if srcs, ok := raw["sources"].(map[string]any); ok {
		for alias, v := range srcs {
			m, _ := v.(map[string]any)
			s := sources.Source{Alias: alias}
			s.Git, _ = m["git"].(string)
			s.Ref, _ = m["ref"].(string)
			s.Path, _ = m["path"].(string)
			f.Sources[alias] = s
		}
	}
	f.Input = boundarySpec(raw["input"])
	if out, ok := raw["output"].(map[string]any); ok {
		f.Output.Interface, _ = out["interface"].(string)
		f.Output.Producer, _ = out["producer"].(string)
		f.Output.Computes = strings_(out["computes"])
	}
	stages, _ := raw["stage"].([]any)
	for i, sv := range stages {
		m, _ := sv.(map[string]any)
		st := StageSpec{Index: i, BoundarySpec: boundarySpec(m)}
		st.ID, _ = m["id"].(string)
		st.Kind, _ = m["kind"].(string)
		st.Producer, _ = m["producer"].(string)
		st.Caller, _ = m["caller"].(string)
		st.Callee, _ = m["callee"].(string)
		st.Computes = strings_(m["computes"])
		st.CalleeComputes = strings_(m["callee_computes"])
		st.Request = boundarySpec(m["request"])
		st.Response = boundarySpec(m["response"])
		line := f.Line("stage", strconv.Itoa(i))
		switch st.Kind {
		case KindHandoff:
			if st.Interface == "" {
				f.Problems = append(f.Problems, Problem{file, line, fmt.Sprintf("stage %d: a handoff needs interface", i+1)})
			}
			if st.Caller != "" || st.Callee != "" || st.Request.Interface != "" || st.Response.Interface != "" || len(st.CalleeComputes) > 0 {
				f.Problems = append(f.Problems, Problem{file, line, fmt.Sprintf("stage %d: caller, callee, request and response belong to kind = \"call\"", i+1)})
			}
		case KindCall:
			if st.Request.Interface == "" || st.Response.Interface == "" {
				f.Problems = append(f.Problems, Problem{file, line, fmt.Sprintf("stage %d: a call needs [stage.request] and [stage.response] interfaces", i+1)})
			}
			if st.Interface != "" || st.Producer != "" {
				f.Problems = append(f.Problems, Problem{file, line, fmt.Sprintf("stage %d: interface and producer belong to kind = \"handoff\"; a call has request, response, caller and callee", i+1)})
			}
		}
		if st.ID == "" {
			ref := st.Interface
			if st.Kind == KindCall {
				ref = st.Request.Interface
			}
			_, p := sources.Split(ref)
			st.ID = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		}
		f.Stages = append(f.Stages, st)
	}
	return f, nil
}

func boundarySpec(v any) BoundarySpec {
	m, _ := v.(map[string]any)
	b := BoundarySpec{Carries: map[string]string{}}
	b.Interface, _ = m["interface"].(string)
	if c, ok := m["carries"].(map[string]any); ok {
		for k, v := range c {
			if s, ok := v.(string); ok {
				b.Carries[k] = s
			}
		}
	}
	return b
}

func strings_(v any) []string {
	xs, _ := v.([]any)
	var out []string
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

var (
	keyRe    = regexp.MustCompile(`^([A-Za-z0-9_\-]+)\s*=`)
	headerRe = regexp.MustCompile(`^\[\[?\s*([A-Za-z0-9_.\-]+)\s*\]\]?`)
)

// scanLines maps dotted locations to lines: "flow", "input.interface",
// "stage.0", "stage.0.request.interface", "output.computes".
func scanLines(text string) map[string]int {
	out := map[string]int{}
	section := ""
	stage := -1
	for i, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		n := i + 1
		if m := headerRe.FindStringSubmatch(t); m != nil && !keyRe.MatchString(t) {
			name := m[1]
			switch {
			case name == "stage" && strings.HasPrefix(t, "[["):
				stage++
				section = "stage." + strconv.Itoa(stage)
			case strings.HasPrefix(name, "stage.") && stage >= 0:
				section = "stage." + strconv.Itoa(stage) + "." + strings.TrimPrefix(name, "stage.")
			default:
				section = name
			}
			if _, ok := out[section]; !ok {
				out[section] = n
			}
			continue
		}
		if m := keyRe.FindStringSubmatch(t); m != nil {
			key := m[1]
			if section != "" {
				key = section + "." + key
			}
			if _, ok := out[key]; !ok {
				out[key] = n
			}
		}
	}
	return out
}

// Line returns the line of the most specific known location, or 0.
func (f *Flow) Line(loc ...string) int {
	for n := len(loc); n > 0; n-- {
		if l, ok := f.lines[strings.Join(loc[:n], ".")]; ok {
			return l
		}
	}
	return 0
}
