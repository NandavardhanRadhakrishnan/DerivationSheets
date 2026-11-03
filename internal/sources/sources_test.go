package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSourcePinned(t *testing.T) {
	repo := teamRepo(t)
	flowDir := t.TempDir()
	url := fileURL(repo)
	r := NewResolver(flowDir, map[string]Source{"shipping": {Alias: "shipping", Git: url, Ref: "v1"}})
	r.Cache = filepath.Join(t.TempDir(), "cache")
	path, rev, err := r.Resolve("shipping:sheets/quote.toml")
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
	if _, _, err := r2.Resolve("shipping:sheets/quote.toml"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline error = %v", err)
	}
}

func TestSourceErrors(t *testing.T) {
	r := NewResolver(t.TempDir(), nil)
	if _, _, err := r.Resolve("nope:a.toml"); err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Errorf("unknown alias error = %v", err)
	}
	if a, p := Split(`C:\x\y.toml`); a != "" || p != `C:\x\y.toml` {
		t.Errorf("drive letter read as alias %q", a)
	}
}
