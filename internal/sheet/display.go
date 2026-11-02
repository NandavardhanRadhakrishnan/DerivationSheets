package sheet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"derivationsheets/internal/brand"
)

// ProjectFile holds project-wide display defaults. It is looked up from the
// workflow folder upward, stopping at the repository root.
const ProjectFile = brand.Command + ".toml"

// Evidence visibility when the page opens.
const (
	EvidenceShown  = "shown"
	EvidenceHidden = "hidden"
)

// Column label keys, in display order.
var ColumnKeys = []string{"number", "value", "rule", "send", "evidence"}

// Section title keys, in display order.
var SectionKeys = []string{"request", "context", "data", "derived"}

// DefaultDisplay is used where neither the project nor the sheet says
// otherwise.
var DefaultDisplay = Display{
	Evidence: EvidenceShown,
	Columns: map[string]string{
		"number":   "#",
		"value":    "Value",
		"rule":     "Rule",
		"send":     "Sent to partner as",
		"evidence": "Evidence",
	},
	Sections: map[string]string{
		"request": "From the request",
		"context": "From context",
		"data":    "From database and config",
		"derived": "Derived",
	},
}

// Display holds presentation settings for the rendered sheet. They never
// affect evaluation.
type Display struct {
	// Evidence is the initial state of the evidence switch: "shown" or "hidden".
	Evidence string
	// Columns maps column keys (see ColumnKeys) to header labels.
	Columns map[string]string
	// Sections maps section keys (see SectionKeys) to titles.
	Sections map[string]string
}

// Merge returns d with every value set in over replacing it.
func (d Display) Merge(over Display) Display {
	out := Display{Evidence: d.Evidence, Columns: map[string]string{}, Sections: map[string]string{}}
	for k, v := range d.Columns {
		out.Columns[k] = v
	}
	for k, v := range d.Sections {
		out.Sections[k] = v
	}
	if over.Evidence != "" {
		out.Evidence = over.Evidence
	}
	for k, v := range over.Columns {
		out.Columns[k] = v
	}
	for k, v := range over.Sections {
		out.Sections[k] = v
	}
	return out
}

// parseDisplay reads a [display] table. Problems are returned as messages
// with the dotted key they concern.
func parseDisplay(raw any) (Display, []string) {
	var d Display
	var problems []string
	m, ok := raw.(map[string]any)
	if raw == nil {
		return d, nil
	}
	if !ok {
		return d, []string{"display: must be a table"}
	}
	for k, v := range m {
		switch k {
		case "evidence":
			s, _ := v.(string)
			if s != EvidenceShown && s != EvidenceHidden {
				problems = append(problems, fmt.Sprintf("display.evidence: must be %q or %q", EvidenceShown, EvidenceHidden))
				continue
			}
			d.Evidence = s
		case "columns":
			d.Columns, problems = labels("display.columns", v, ColumnKeys, problems)
		case "sections":
			d.Sections, problems = labels("display.sections", v, SectionKeys, problems)
		default:
			problems = append(problems, fmt.Sprintf("display.%s: unknown key; expected evidence, columns or sections", k))
		}
	}
	sort.Strings(problems)
	return d, problems
}

func labels(where string, v any, keys []string, problems []string) (map[string]string, []string) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, append(problems, where+": must be a table")
	}
	out := map[string]string{}
	for k, x := range m {
		known := false
		for _, want := range keys {
			known = known || k == want
		}
		s, isString := x.(string)
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("%s.%s: unknown key; expected one of %s", where, k, strings.Join(keys, ", ")))
		case !isString || strings.TrimSpace(s) == "":
			problems = append(problems, fmt.Sprintf("%s.%s: must be a non-empty string", where, k))
		default:
			out[k] = s
		}
	}
	return out, problems
}

// FindProjectFile returns the nearest dsheets.toml at or above dir, without
// leaving the repository (the folder that holds .git), or "".
func FindProjectFile(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		p := filepath.Join(d, ProjectFile)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil || filepath.Dir(d) == d {
			return ""
		}
	}
}

// loadDisplay layers built-in defaults, the project file and the sheet's own
// [display] table.
func (s *Sheet) loadDisplay() {
	d := DefaultDisplay
	if path := FindProjectFile(s.Dir); path != "" {
		s.ProjectFile = path
		pd, issues := readProjectDisplay(path)
		s.LoadIssues = append(s.LoadIssues, issues...)
		d = d.Merge(pd)
	}
	// Problems in the sheet's own [display] are reported by the JSON Schema
	// check, so they are not repeated here; invalid values are just ignored.
	sd, _ := parseDisplay(s.Raw["display"])
	s.Display = d.Merge(sd)
}

func readProjectDisplay(path string) (Display, []Issue) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Display{}, []Issue{{File: path, Code: "project-file", Message: err.Error()}}
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		line := 0
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, _ = de.Position()
		}
		return Display{}, []Issue{{File: path, Line: line, Code: "project-file", Message: err.Error()}}
	}
	var issues []Issue
	for k := range raw {
		if k != "display" {
			issues = append(issues, Issue{File: path, Line: displayLine(string(data), k), Code: "project-file", Message: k + ": unknown key; " + ProjectFile + " only holds [display]"})
		}
	}
	d, problems := parseDisplay(raw["display"])
	for _, p := range problems {
		issues = append(issues, Issue{File: path, Line: displayLine(string(data), p), Code: "project-file", Message: p})
	}
	return d, issues
}

// displayLine finds the line of the key named at the start of a problem,
// such as "display.columns.foo: ...". It returns the line of the last key
// segment found after the matching table header, or the header's line.
func displayLine(text, problem string) int {
	key, _, _ := strings.Cut(problem, ":")
	parts := strings.Split(key, ".")
	lines := strings.Split(text, "\n")
	best := 0
	table := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			table = strings.Trim(t, "[] ")
			if table == key || strings.HasPrefix(key, table+".") && best == 0 {
				best = i + 1
			}
			continue
		}
		name, _, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		full := strings.TrimSpace(name)
		if table != "" {
			full = table + "." + full
		}
		if full == key || len(parts) > 0 && strings.HasSuffix(full, "."+parts[len(parts)-1]) && strings.HasPrefix(key, table) {
			return i + 1
		}
	}
	return best
}
