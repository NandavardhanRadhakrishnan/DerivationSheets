package sources

import (
	"os"
	"strings"
	"testing"
)

func TestGitSourcePinned(t *testing.T) {
	repo := teamRepo(t)
	flowDir := t.TempDir()
	url := fileURL(repo)
	r := NewResolver(flowDir, map[string]Source{"shipping": {Alias: "shipping", Git: url, Ref: "v1"}}, NewFetcher(t.TempDir()))
	path, rev, err := r.Resolve("shipping:sheets/quote.toml")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# v1") || rev == "" {
		t.Errorf("got %q at revision %q; want the v1 file", data, rev)
	}
	// Offline with no clone fails clearly.
	offline := NewFetcher(t.TempDir())
	offline.Offline = true
	r2 := NewResolver(flowDir, map[string]Source{"shipping": {Alias: "shipping", Git: url, Ref: "v2"}}, offline)
	if _, _, err := r2.Resolve("shipping:sheets/quote.toml"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline error = %v", err)
	}
}

func TestSourceErrors(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, NewFetcher(t.TempDir()))
	if _, _, err := r.Resolve("nope:a.toml"); err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Errorf("unknown alias error = %v", err)
	}
	if a, p := Split(`C:\x\y.toml`); a != "" || p != `C:\x\y.toml` {
		t.Errorf("drive letter read as alias %q", a)
	}
}
