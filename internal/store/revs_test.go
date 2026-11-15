package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// revRepo makes a repository on branch main with one commit holding a.txt.
func revRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "first")
	return dir
}

func revOf(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", rev)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestRevsReadAt(t *testing.T) {
	dir := revRepo(t)
	first := revOf(t, dir, "HEAD")
	write(t, filepath.Join(dir, "a.txt"), "two\n")
	run(t, dir, "commit", "-am", "second")
	r, err := NewRevs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadAt(first, "a.txt")
	if err != nil || string(got) != "one\n" {
		t.Fatalf("at first = %q, %v", got, err)
	}
	got, err = r.ReadAt("HEAD", "a.txt")
	if err != nil || string(got) != "two\n" {
		t.Fatalf("at HEAD = %q, %v", got, err)
	}
	if _, err := r.ReadAt("HEAD", "missing.txt"); !errors.Is(err, ErrNotInRev) {
		t.Fatalf("missing: err = %v, want ErrNotInRev", err)
	}
}

func TestRevsChanged(t *testing.T) {
	dir := revRepo(t)
	write(t, filepath.Join(dir, "gone.txt"), "x\n")
	write(t, filepath.Join(dir, "old/moved.txt"), "m\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "more")
	base := revOf(t, dir, "HEAD")

	write(t, filepath.Join(dir, "a.txt"), "changed\n")
	write(t, filepath.Join(dir, "new.txt"), "n\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "mv", "old/moved.txt", "moved.txt")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", "edits")

	r, err := NewRevs(dir)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.Changed(base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]byte{}
	for _, c := range cs {
		got[c.Path] = c.Status
	}
	want := map[string]byte{"a.txt": 'M', "new.txt": 'A', "gone.txt": 'D', "old/moved.txt": 'D', "moved.txt": 'A'}
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for p, s := range want {
		if got[p] != s {
			t.Fatalf("%s = %c, want %c (all: %v)", p, got[p], s, got)
		}
	}
}

func TestRevsMergeBase(t *testing.T) {
	dir := revRepo(t)
	fork := revOf(t, dir, "HEAD")
	run(t, dir, "switch", "-c", "feature")
	write(t, filepath.Join(dir, "f.txt"), "f\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "feature work")
	run(t, dir, "switch", "main")
	write(t, filepath.Join(dir, "m.txt"), "m\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "main work")
	r, err := NewRevs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.MergeBase("main", "feature")
	if err != nil || got != fork {
		t.Fatalf("MergeBase = %q, %v; want %q", got, err, fork)
	}
}

func TestRevsDefaultBranchFallback(t *testing.T) {
	dir := revRepo(t)
	r, err := NewRevs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.DefaultBranch(); err != nil || got != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", got, err)
	}

	trunk := t.TempDir()
	run(t, trunk, "init", "-b", "trunk")
	write(t, filepath.Join(trunk, "a.txt"), "x\n")
	run(t, trunk, "add", ".")
	run(t, trunk, "commit", "-m", "first")
	r2, err := NewRevs(trunk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.DefaultBranch(); err == nil || !strings.Contains(err.Error(), "pass an explicit base") {
		t.Fatalf("err = %v, want one asking for an explicit base", err)
	}
}

func TestRevsDefaultBranchPrefersOrigin(t *testing.T) {
	up := revRepo(t)
	dir := t.TempDir()
	run(t, dir, "clone", up, ".")
	r, err := NewRevs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.DefaultBranch()
	if err != nil || got != "origin/HEAD" {
		t.Fatalf("DefaultBranch = %q, %v; want origin/HEAD", got, err)
	}
}

func TestRevsNotGit(t *testing.T) {
	if _, err := NewRevs(t.TempDir()); err == nil {
		t.Fatal("NewRevs on a plain folder: want an error")
	}
}

func TestExport(t *testing.T) {
	dir := revRepo(t)
	first := revOf(t, dir, "HEAD")
	write(t, filepath.Join(dir, "a.txt"), "two\n")
	run(t, dir, "commit", "-am", "second")
	r, err := NewRevs(dir)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := r.Export(first, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil || string(got) != "one\n" {
		t.Fatalf("a.txt = %q, %v; want the first revision's content", got, err)
	}
	if err := r.Export("no-such-rev", t.TempDir()); err == nil {
		t.Fatal("unknown revision exported without an error")
	}
}
