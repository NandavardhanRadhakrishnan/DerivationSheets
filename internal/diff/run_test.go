package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func put(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", msg)
}

const demoSheet = `sheet = "demo"
version = 1

[[row]]
id = "net"
name = "Net"
formula = "a - b"

[[row]]
id = "tax"
formula = "net * 2"
`

// newRepo makes a repository on main holding a sheet and unrelated files.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	put(t, dir, "specs/demo/sheet.toml", demoSheet)
	put(t, dir, "specs/demo/data/rates.csv", "k,v\na,1\n")
	put(t, dir, "Cargo.toml", "[package]\nname = \"x\"\n")
	commit(t, dir, "first")
	return dir
}

func TestRunSheetChange(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "specs/demo/sheet.toml", strings.Replace(demoSheet, "a - b", "a - c", 1))
	commit(t, dir, "edit")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets) != 1 || r.Sheets[0].Path != "specs/demo/sheet.toml" || r.Sheets[0].Name != "demo" {
		t.Fatalf("sheets = %+v", r.Sheets)
	}
	rows := r.Sheets[0].Rows
	if len(rows) != 1 || rows[0].ID != "net" || rows[0].Fields[0].After != "a - c" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(r.Flows)+len(r.Interfaces)+len(r.Cases) != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestRunIgnoresOtherToml(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "Cargo.toml", "[package]\nname = \"y\"\n")
	put(t, dir, ".hidden/sheet.toml", demoSheet)
	commit(t, dir, "unrelated")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets)+len(r.Flows)+len(r.Interfaces) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestRunCsvIgnored(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "specs/demo/data/rates.csv", "k,v\na,2\n")
	commit(t, dir, "csv")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets)+len(r.Flows)+len(r.Interfaces)+len(r.Cases) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestRunFolderMove(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "mv", "specs/demo", "specs/renamed")
	put(t, dir, "specs/renamed/sheet.toml", strings.Replace(demoSheet, "a - b", "a - c", 1))
	commit(t, dir, "move and edit")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets) != 1 {
		t.Fatalf("sheets = %+v", r.Sheets)
	}
	s := r.Sheets[0]
	if s.Status != Changed || s.Path != "specs/renamed/sheet.toml" || s.OldPath != "specs/demo/sheet.toml" || len(s.Rows) != 1 {
		t.Fatalf("got %+v", s)
	}
}

func TestRunNewSheetListsRows(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "specs/second/sheet.toml", strings.Replace(demoSheet, `"demo"`, `"second"`, 1))
	commit(t, dir, "new sheet")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets) != 1 || r.Sheets[0].Status != Added || len(r.Sheets[0].Rows) != 2 {
		t.Fatalf("got %+v", r.Sheets)
	}
}

func TestRunDeletedSheet(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "rm", "-q", "specs/demo/sheet.toml")
	commit(t, dir, "delete")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets) != 1 || r.Sheets[0].Status != Removed || len(r.Sheets[0].Rows) != 2 {
		t.Fatalf("got %+v", r.Sheets)
	}
}

func TestRunInterfaceDetectedByKey(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "interfaces/quote.toml", "interface = \"quote\"\n\n[[field]]\nname = \"zip\"\ntype = \"string\"\n")
	commit(t, dir, "add interface")
	put(t, dir, "interfaces/quote.toml", "interface = \"quote\"\n\n[[field]]\nname = \"zip\"\ntype = \"number\"\n")
	commit(t, dir, "change interface")
	r, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Interfaces) != 1 || r.Interfaces[0].Name != "quote" || len(r.Interfaces[0].Fields) != 1 {
		t.Fatalf("got %+v", r.Interfaces)
	}
}

func TestRunCasesFlag(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "specs/demo/cases.toml", "sheet = \"demo\"\n\n[[case]]\nid = \"one\"\ninput = { a = 1 }\n")
	commit(t, dir, "cases")
	put(t, dir, "specs/demo/cases.toml", "sheet = \"demo\"\n\n[[case]]\nid = \"one\"\ninput = { a = 2 }\n")
	commit(t, dir, "cases edit")
	off, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Cases) != 0 {
		t.Fatalf("cases without the flag: %+v", off.Cases)
	}
	on, err := Run(Options{Dir: dir, Base: "HEAD~1", Head: "HEAD", Cases: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(on.Cases) != 1 || on.Cases[0].Sheet != "demo" || len(on.Cases[0].Cases) != 1 || on.Cases[0].Cases[0].ID != "one" {
		t.Fatalf("got %+v", on.Cases)
	}
}

func TestRunDefaultBase(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "switch", "-c", "feature")
	put(t, dir, "specs/demo/sheet.toml", strings.Replace(demoSheet, "a - b", "a - c", 1))
	commit(t, dir, "feature edit")
	// main moves on with an unrelated sheet change that must not show up.
	git(t, dir, "switch", "main")
	put(t, dir, "specs/other/sheet.toml", strings.Replace(demoSheet, `"demo"`, `"other"`, 1))
	commit(t, dir, "main work")
	git(t, dir, "switch", "feature")
	// An uncommitted edit must not show up either.
	put(t, dir, "specs/demo/sheet.toml", strings.Replace(demoSheet, "a - b", "a - zzz", 1))

	r, err := Run(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets) != 1 || r.Sheets[0].Name != "demo" || r.Sheets[0].Rows[0].Fields[0].After != "a - c" {
		t.Fatalf("got %+v", r.Sheets)
	}
}

func TestRunBadRef(t *testing.T) {
	dir := newRepo(t)
	_, err := Run(Options{Dir: dir, Base: "no-such-branch"})
	if err == nil || !strings.Contains(err.Error(), "no-such-branch") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunNoDifferences(t *testing.T) {
	dir := newRepo(t)
	r, err := Run(Options{Dir: dir, Base: "HEAD", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sheets)+len(r.Flows)+len(r.Interfaces)+len(r.Cases) != 0 || r.Base == "" || r.Repo == "" {
		t.Fatalf("got %+v", r)
	}
}
