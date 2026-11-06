package store

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// repo makes a git repository with a/sheet.toml ("version = 1\n") committed.
func repo(t *testing.T) (string, *Local) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a", "sheet.toml"), "version = 1\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "one")
	s, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, s
}

func TestLoadSave(t *testing.T) {
	dir, s := repo(t)
	f, err := s.Load("a/sheet.toml")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Data) != "version = 1\n" || f.Token != Token(f.Data) || f.Path != "a/sheet.toml" {
		t.Fatalf("Load = %+v", f)
	}
	toks, err := s.Save(Write{Path: "a/sheet.toml", Data: []byte("version = 2\n"), Token: f.Token})
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 || toks[0] != Token([]byte("version = 2\n")) {
		t.Fatalf("tokens = %v", toks)
	}
	if got := read(t, filepath.Join(dir, "a", "sheet.toml")); got != "version = 2\n" {
		t.Fatalf("disk = %q", got)
	}
}

func TestLoadMissing(t *testing.T) {
	_, s := repo(t)
	if _, err := s.Load("a/none.toml"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveConflict(t *testing.T) {
	dir, s := repo(t)
	f, err := s.Load("a/sheet.toml")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "a", "sheet.toml"), "x = 1\n")
	_, err = s.Save(Write{Path: "a/sheet.toml", Data: []byte("version = 2\n"), Token: f.Token})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Paths) != 1 || ce.Paths[0] != "a/sheet.toml" {
		t.Fatalf("conflict = %#v", err)
	}
	if got := read(t, filepath.Join(dir, "a", "sheet.toml")); got != "x = 1\n" {
		t.Fatalf("disk = %q", got)
	}
}

func TestSaveAllOrNothing(t *testing.T) {
	dir, s := repo(t)
	write(t, filepath.Join(dir, "a", "flow.toml"), "flow = \"f\"\n")
	f1, _ := s.Load("a/sheet.toml")
	f2, _ := s.Load("a/flow.toml")
	write(t, filepath.Join(dir, "a", "flow.toml"), "flow = \"g\"\n")
	_, err := s.Save(
		Write{Path: "a/sheet.toml", Data: []byte("version = 2\n"), Token: f1.Token},
		Write{Path: "a/flow.toml", Data: []byte("flow = \"h\"\n"), Token: f2.Token},
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if got := read(t, filepath.Join(dir, "a", "sheet.toml")); got != "version = 1\n" {
		t.Fatalf("sheet written: %q", got)
	}
	if got := read(t, filepath.Join(dir, "a", "flow.toml")); got != "flow = \"g\"\n" {
		t.Fatalf("flow = %q", got)
	}
}

func TestSaveCreate(t *testing.T) {
	dir, s := repo(t)
	if _, err := s.Save(Write{Path: "a/new.toml", Data: []byte("a = 1\n")}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "a", "new.toml")); got != "a = 1\n" {
		t.Fatalf("disk = %q", got)
	}
	if _, err := s.Save(Write{Path: "a/new.toml", Data: []byte("a = 2\n")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create: err = %v", err)
	}
}

func TestPathsConfined(t *testing.T) {
	dir, s := repo(t)
	abs := filepath.Join(dir, "a", "sheet.toml")
	for _, p := range []string{"../x.toml", "a/../../x.toml", abs, filepath.ToSlash(abs)} {
		if _, err := s.Load(p); !errors.Is(err, ErrOutside) {
			t.Errorf("Load(%q): err = %v", p, err)
		}
		if _, err := s.Save(Write{Path: p, Data: []byte("x")}); !errors.Is(err, ErrOutside) {
			t.Errorf("Save(%q): err = %v", p, err)
		}
	}
	if _, err := s.Save(Write{Path: ".dsheets/sources/s/sheet.toml", Data: []byte("x")}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Save into .dsheets: err = %v", err)
	}
	if _, err := s.Save(Write{Path: ".git/config", Data: []byte("x")}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Save into .git: err = %v", err)
	}
	// Windows ignores case and trailing dots and spaces in names.
	for _, p := range []string{".GIT/config", ".git./hooks/pre-commit", ".git /config", "a/.Git/config", ".DSheets/sources/s/sheet.toml"} {
		if _, err := s.Save(Write{Path: p, Data: []byte("x")}); !errors.Is(err, ErrReadOnly) {
			t.Errorf("Save(%q): err = %v", p, err)
		}
	}
	team := filepath.Join(dir, "team")
	write(t, filepath.Join(team, "sheet.toml"), "sheet = \"t\"\n")
	run(t, team, "init", "-q")
	f, err := s.Load("team/sheet.toml")
	if err != nil {
		t.Fatalf("Load from nested checkout: %v", err)
	}
	if _, err := s.Save(Write{Path: "team/sheet.toml", Data: []byte("x"), Token: f.Token}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Save into nested checkout: err = %v", err)
	}
	if got := read(t, filepath.Join(team, "sheet.toml")); got != "sheet = \"t\"\n" {
		t.Errorf("nested checkout written: %q", got)
	}
}

func TestCommitted(t *testing.T) {
	dir, s := repo(t)
	write(t, filepath.Join(dir, "a", "sheet.toml"), "version = 5\n")
	data, err := s.Committed("a/sheet.toml")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version = 1\n" {
		t.Fatalf("Committed = %q", data)
	}
	write(t, filepath.Join(dir, "a", "new.toml"), "version = 1\n")
	if _, err := s.Committed("a/new.toml"); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("untracked: err = %v", err)
	}
	plain := t.TempDir()
	write(t, filepath.Join(plain, "sheet.toml"), "version = 1\n")
	ps, err := NewLocal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Committed("sheet.toml"); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("not a repository: err = %v", err)
	}
}

func TestCommittedVersion(t *testing.T) {
	dir, s := repo(t)
	v, ok, err := CommittedVersion(s, "a/sheet.toml")
	if err != nil || !ok || v != 1 {
		t.Fatalf("committed = %d %v %v", v, ok, err)
	}
	write(t, filepath.Join(dir, "a", "new.toml"), "version = 1\n")
	if v, ok, err := CommittedVersion(s, "a/new.toml"); err != nil || ok || v != 0 {
		t.Fatalf("untracked = %d %v %v", v, ok, err)
	}
	write(t, filepath.Join(dir, "a", "plain.toml"), "title = \"x\"\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "two")
	if v, ok, err := CommittedVersion(s, "a/plain.toml"); err != nil || ok || v != 0 {
		t.Fatalf("no version = %d %v %v", v, ok, err)
	}
}
