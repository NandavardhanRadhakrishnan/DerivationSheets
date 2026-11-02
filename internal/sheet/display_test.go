package sheet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimal = `sheet = "x"
version = 1

[[row]]
id = "a"
name = "A"
source = "request.a"

  [row.send]
  field = "a"
  usage = "R"
`

// project creates a repository with a dsheets.toml and one workflow.
func project(t *testing.T, projectTOML, sheetTOML string) string {
	t.Helper()
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	if projectTOML != "" {
		os.WriteFile(filepath.Join(root, ProjectFile), []byte(projectTOML), 0o644)
	}
	dir := filepath.Join(root, "specs", "x")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, FileName), []byte(sheetTOML), 0o644)
	return dir
}

func TestDisplayLayers(t *testing.T) {
	dir := project(t, `
[display]
evidence = "hidden"

[display.columns]
send  = "Sent to the warehouse as"
value = "Field"
`, minimal+`
[display.columns]
send = "Sent to billing as"

[display.sections]
derived = "Calculated"
`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.LoadIssues) > 0 {
		t.Fatalf("issues: %+v", s.LoadIssues)
	}
	d := s.Display
	checks := map[string]string{
		"evidence":         d.Evidence,
		"columns.send":     d.Columns["send"],
		"columns.value":    d.Columns["value"],
		"columns.rule":     d.Columns["rule"],
		"sections.derived": d.Sections["derived"],
		"sections.request": d.Sections["request"],
	}
	want := map[string]string{
		"evidence":         "hidden",             // project
		"columns.send":     "Sent to billing as", // sheet overrides project
		"columns.value":    "Field",              // project
		"columns.rule":     "Rule",               // default
		"sections.derived": "Calculated",         // sheet
		"sections.request": "From the request",   // default
	}
	for k, got := range checks {
		if got != want[k] {
			t.Errorf("%s = %q, want %q", k, got, want[k])
		}
	}
	if s.ProjectFile == "" {
		t.Error("project file not recorded")
	}
}

func TestProjectFileErrors(t *testing.T) {
	dir := project(t, `[display]
evidence = "sometimes"

[display.columns]
sent = "Sent as"
`, minimal)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.LoadIssues) != 2 {
		t.Fatalf("issues = %+v, want 2", s.LoadIssues)
	}
	for _, is := range s.LoadIssues {
		if is.Code != "project-file" || is.Line == 0 || !strings.HasSuffix(is.File, ProjectFile) {
			t.Errorf("issue %+v lacks code, line or file", is)
		}
	}
	lines := map[string]int{}
	for _, is := range s.LoadIssues {
		key, _, _ := strings.Cut(is.Message, ":")
		lines[key] = is.Line
	}
	if lines["display.evidence"] != 2 || lines["display.columns.sent"] != 5 {
		t.Errorf("issue lines = %v, want display.evidence on 2 and display.columns.sent on 5", lines)
	}
	// Invalid values fall back to the defaults.
	if s.Display.Evidence != EvidenceShown {
		t.Errorf("evidence = %q", s.Display.Evidence)
	}
}

func TestProjectFileStopsAtRepoRoot(t *testing.T) {
	outer := t.TempDir()
	os.WriteFile(filepath.Join(outer, ProjectFile), []byte("[display]\nevidence = \"hidden\"\n"), 0o644)
	repo := filepath.Join(outer, "repo")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	if got := FindProjectFile(repo); got != "" {
		t.Errorf("found %s outside the repository", got)
	}
}
