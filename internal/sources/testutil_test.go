package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fileURL is a clone URL for a local repository; file:// makes git use the
// network code path, so --depth and --filter apply as they would remotely.
func fileURL(dir string) string { return "file://" + filepath.ToSlash(dir) }

// teamRepo makes a team repository on branch main: dsheets.toml at the root,
// sheets/quote.toml and service code beside it. Tag v1 has "# v1"; main has
// a second commit with "# v2".
func teamRepo(t *testing.T) string {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "dsheets.toml"), "[display]\nevidence = \"shown\"\n")
	write(t, filepath.Join(dir, "sheets", "quote.toml"), "interface = \"quote\"\n# v1\n")
	write(t, filepath.Join(dir, "service", "main.go"), "package main\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "v1")
	run(t, dir, "tag", "v1")
	commitQuote(t, dir, "# v2")
	return dir
}

// commitQuote changes sheets/quote.toml on the current branch.
func commitQuote(t *testing.T, dir, marker string) {
	t.Helper()
	write(t, filepath.Join(dir, "sheets", "quote.toml"), "interface = \"quote\"\n"+marker+"\n")
	run(t, dir, "commit", "-q", "-am", marker)
}
