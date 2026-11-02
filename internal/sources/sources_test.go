package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// teamRepo makes a git repository with one interface file at two tags.
func teamRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "quote.toml"), []byte("interface = \"quote\"\n# v1\n"), 0o644)
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "v1")
	run(t, dir, "tag", "v1")
	os.WriteFile(filepath.Join(dir, "quote.toml"), []byte("interface = \"quote\"\n# v2\n"), 0o644)
	run(t, dir, "commit", "-q", "-am", "v2")
	run(t, dir, "tag", "v2")
	return dir
}

func TestGitSourcePinned(t *testing.T) {
	repo := teamRepo(t)
	flowDir := t.TempDir()
	url := "file://" + filepath.ToSlash(repo)
	r := NewResolver(flowDir, map[string]Source{"shipping": {Alias: "shipping", Git: url, Ref: "v1"}})
	r.Cache = filepath.Join(t.TempDir(), "cache")
	path, rev, err := r.Resolve("shipping:quote.toml")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# v1") || rev == "" {
		t.Errorf("got %q at revision %q; want the v1 file", data, rev)
	}
	// Offline with no clone fails clearly.
	r2 := NewResolver(flowDir, map[string]Source{"shipping": {Alias: "shipping", Git: url, Ref: "v2"}})
	r2.Cache, r2.Offline = filepath.Join(t.TempDir(), "empty"), true
	if _, _, err := r2.Resolve("shipping:quote.toml"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline error = %v", err)
	}
}

func TestSourceErrors(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]Source{"x": {Alias: "x", Git: "file:///nowhere"}})
	if _, _, err := r.Resolve("x:a.toml"); err == nil || !strings.Contains(err.Error(), "needs ref") {
		t.Errorf("missing ref error = %v", err)
	}
	if _, _, err := r.Resolve("nope:a.toml"); err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Errorf("unknown alias error = %v", err)
	}
	if a, p := Split(`C:\x\y.toml`); a != "" || p != `C:\x\y.toml` {
		t.Errorf("drive letter read as alias %q", a)
	}
}
