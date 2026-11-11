package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const messySheet = "sheet = \"s\"\nversion = 1\n\n[[row]]\nname = \"A\"\nid = \"a\"\n"

func TestFmtRewritesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sheet.toml")
	os.WriteFile(p, []byte(messySheet), 0o644)

	out := runOK(t, "fmt", dir)
	if !strings.Contains(out, "sheet.toml") {
		t.Fatalf("changed file not listed: %q", out)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "id   = \"a\"\nname = \"A\"") {
		t.Fatalf("not in canonical order and alignment:\n%s", got)
	}
	if out := runOK(t, "fmt", dir); out != "" {
		t.Fatalf("second run changed %q", out)
	}
}

func TestFmtCheckWritesNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sheet.toml")
	os.WriteFile(p, []byte(messySheet), 0o644)

	var out, errw bytes.Buffer
	if err := run([]string{"fmt", "--check", dir}, &out, &errw); err != errNotFormatted {
		t.Fatalf("err = %v, want errNotFormatted", err)
	}
	if !strings.Contains(out.String(), "sheet.toml") {
		t.Fatalf("file not listed: %q", out.String())
	}
	if got, _ := os.ReadFile(p); string(got) != messySheet {
		t.Fatalf("--check changed the file:\n%s", got)
	}
}

func TestFmtLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	// cases.toml starts with a sheet key but is generated; data files have no layout.
	files := map[string]string{
		"cases.toml":         messySheet,
		"config.toml":        "[tax]\nz = 1\na = 2\n",
		".hidden/sheet.toml": messySheet,
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	if out := runOK(t, "fmt", dir); out != "" {
		t.Fatalf("formatted files it should skip: %q", out)
	}
	for name, body := range files {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != body {
			t.Errorf("%s changed:\n%s", name, got)
		}
	}
}

func TestFmtReportsUnparsableFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sheet.toml"), []byte("sheet = \n"), 0o644)
	var out, errw bytes.Buffer
	if err := run([]string{"fmt", dir}, &out, &errw); err == nil || !strings.Contains(errw.String(), "sheet.toml") {
		t.Fatalf("err = %v, stderr %q", err, errw.String())
	}
}

// The repository's own files must stay canonical; the pre-commit hook
// enforces the same for staged files.
func TestRepositoryFilesAreFormatted(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"fmt", "--check", "../.."}, &out, &errw)
	if err != nil {
		t.Fatalf("run `dsheets fmt` (%v):\n%s%s", err, out.String(), errw.String())
	}
}
