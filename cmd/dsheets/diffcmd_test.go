package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const diffSheetV1 = "sheet = \"demo\"\nversion = 1\n\n[[row]]\nid = \"net\"\nformula = \"a - b\"\n"

// diffRepo commits a sheet, then a changed version on branch feature, and
// leaves the working directory inside it.
func diffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, "specs", "demo"), 0o755)
	p := filepath.Join(dir, "specs", "demo", "sheet.toml")
	os.WriteFile(p, []byte(diffSheetV1), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	gitIn(t, dir, "switch", "-q", "-c", "feature")
	os.WriteFile(p, []byte(strings.Replace(diffSheetV1, "a - b", "a - c", 1)), 0o644)
	gitIn(t, dir, "commit", "-q", "-am", "edit")
	t.Chdir(dir)
	return dir
}

func TestDiffShowsRowChangeAndExitsZero(t *testing.T) {
	diffRepo(t)
	out := runOK(t, "diff")
	for _, want := range []string{"### Sheet `demo`", "**net** - changed", "before: `a - b`", "after: `a - c`"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDiffArgumentForms(t *testing.T) {
	diffRepo(t)
	for _, args := range [][]string{{"diff", "main..HEAD"}, {"diff", "main"}, {"diff", "main.."}, {"diff", "..HEAD"}} {
		out := runOK(t, args...)
		if !strings.Contains(out, "**net** - changed") && !strings.Contains(out, "No sheet or flow differences.") {
			t.Errorf("%v:\n%s", args, out)
		}
	}
	if out := runOK(t, "diff", "main..HEAD"); !strings.Contains(out, "**net** - changed") {
		t.Errorf("main..HEAD:\n%s", out)
	}
	if out := runOK(t, "diff", "HEAD..HEAD"); !strings.Contains(out, "No sheet or flow differences.") {
		t.Errorf("HEAD..HEAD:\n%s", out)
	}
}

func TestDiffJSON(t *testing.T) {
	diffRepo(t)
	out := runOK(t, "diff", "--json")
	if !strings.Contains(out, `"sheets"`) || !strings.Contains(out, `"formula"`) {
		t.Errorf("json:\n%s", out)
	}
}

func TestDiffBadRefIsAnError(t *testing.T) {
	diffRepo(t)
	var out, errw bytes.Buffer
	err := run([]string{"diff", "no-such-branch..HEAD"}, &out, &errw)
	if err == nil || errors.Is(err, errFailed) || errors.Is(err, errUsage) || !strings.Contains(err.Error(), "no-such-branch") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiffThreeDotsIsAUsageError(t *testing.T) {
	diffRepo(t)
	var out, errw bytes.Buffer
	if err := run([]string{"diff", "main...HEAD"}, &out, &errw); !errors.Is(err, errUsage) {
		t.Fatalf("err = %v, want usage error", err)
	}
}
