package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// inConsumer runs the test from a fresh repository whose dsheets.toml names
// this repository as the source "ex".
func inConsumer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	self, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	toml := "[sources]\nex = { path = \"" + filepath.ToSlash(self) + "\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "dsheets.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errw bytes.Buffer
	if err := run(args, &out, &errw); err != nil {
		t.Fatalf("%v: %v\n%s%s", args, err, out.String(), errw.String())
	}
	return out.String()
}

func TestSliceAliasArgument(t *testing.T) {
	inConsumer(t)
	if out := runOK(t, "slice", "ex:flows/checkout-to-payment"); !strings.Contains(out, "carried") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSliceWithAlias(t *testing.T) {
	inConsumer(t)
	runOK(t, "slice", "ex:flows/loyalty-report", "--with", "ex:flows/checkout-to-payment")
}

func TestRenderAliasFlows(t *testing.T) {
	inConsumer(t)
	page := filepath.Join(t.TempDir(), "flow.html")
	runOK(t, "render", "ex:flows/checkout-to-payment", "ex:flows/loyalty-report", "--out", page)
	if st, err := os.Stat(page); err != nil || st.Size() == 0 {
		t.Errorf("page not written: %v", err)
	}
}

func TestFetchListsAliases(t *testing.T) {
	inConsumer(t)
	out := runOK(t, "fetch", "ex:flows/checkout-to-payment")
	if !strings.Contains(out, "shipping") || !strings.Contains(out, "local") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSliceUnknownAliasArgument(t *testing.T) {
	inConsumer(t)
	var out, errw bytes.Buffer
	err := run([]string{"slice", "nope:flows/x"}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v", err)
	}
}
