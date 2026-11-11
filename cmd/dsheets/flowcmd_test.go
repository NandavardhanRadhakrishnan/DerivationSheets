package main

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestImpactNeedsRegistry(t *testing.T) {
	inConsumer(t)
	var out, errw bytes.Buffer
	err := run([]string{"impact"}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "registry") {
		t.Errorf("error = %v", err)
	}
}

func TestImpactUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if err := run([]string{"impact", "extra"}, &out, &errw); err != errUsage {
		t.Errorf("error = %v; want usage", err)
	}
	for _, flag := range []string{"-as", "-base", "-json", "-out", "-offline"} {
		if !strings.Contains(errw.String(), flag) {
			t.Errorf("usage lacks %s:\n%s", flag, errw.String())
		}
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

// gitRepoOfExample commits the example flow, sheet and interfaces into a
// repository, keeping their relative layout, and returns it.
func gitRepoOfExample(t *testing.T) string {
	t.Helper()
	self, _ := filepath.Abs(filepath.Join("..", ".."))
	dir := t.TempDir()
	for _, sub := range []string{"flows/checkout-to-payment", "specs/order-checkout", "interfaces"} {
		src := filepath.Join(self, filepath.FromSlash(sub))
		filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(self, p)
			data, _ := os.ReadFile(p)
			os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
			return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
		})
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "example")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A served flow given as alias:path follows its branch on reload.
func TestServeRefetchesAliasFlows(t *testing.T) {
	repo := gitRepoOfExample(t)
	inConsumer(t)
	cwd, _ := os.Getwd()
	os.WriteFile(filepath.Join(cwd, "dsheets.toml"), []byte("[sources]\nup = { git = \"file://"+filepath.ToSlash(repo)+"\" }\n"), 0o644)
	fe := newFetcher(false)
	fe.MaxAge = time.Nanosecond
	h := serveHandler([]string{"up:flows/checkout-to-payment"}, serveOptions{}, fe, io.Discard)
	get := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if !strings.Contains(get(), "Checkout to payment") {
		t.Fatal("first page lacks the flow title")
	}
	path := filepath.Join(repo, "flows", "checkout-to-payment", "flow.toml")
	data, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(data), `title   = "Checkout to payment"`, `title   = "Checkout renamed"`, 1)), 0o644)
	gitIn(t, repo, "commit", "-q", "-am", "rename")
	time.Sleep(time.Millisecond)
	if !strings.Contains(get(), "Checkout renamed") {
		t.Error("reload still shows the startup commit")
	}
}

// TestImportKeepsLocal re-imports over a file with a field marked local:
// the field is kept, still marked, and the command says so.
func TestImportKeepsLocal(t *testing.T) {
	dir := t.TempDir()
	src, err := filepath.Abs(filepath.Join("..", "..", "interfaces", "orders", "order-placed.avsc"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "order-placed.toml")
	runOK(t, "interface", "import", src, "--out", out, "--owner", "Orders team")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	added := string(data) + "\n[[field]]\nname     = \"shippingMethod\"\ntype     = \"string\"\nrequired = true\nlocal    = true\n"
	if err := os.WriteFile(out, []byte(added), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runOK(t, "interface", "import", src, "--out", out, "--owner", "Orders team")
	if !strings.Contains(got, "kept shippingMethod: not in order-placed.avsc yet") {
		t.Errorf("output does not name the kept field:\n%s", got)
	}
	data, _ = os.ReadFile(out)
	if !strings.Contains(string(data), "name     = \"shippingMethod\"\ntype     = \"string\"\nrequired = true\nlocal    = true\n") {
		t.Errorf("shippingMethod not kept:\n%s", data)
	}
}
