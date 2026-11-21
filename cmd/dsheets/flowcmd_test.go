package main

import (
	"bytes"
	"errors"
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

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func replaceInFile(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s has no %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shopWorld builds a PM repository and the team repositories its registry
// lists, from the shop fixture. With registry false the PM repository has no
// registry and reads the interfaces through a source of its own flow.
func shopWorld(t *testing.T, registry bool) (pm, repos string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	fixture := filepath.Join("..", "..", "internal", "agree", "testdata", "shop")
	root := t.TempDir()
	pm = filepath.Join(root, "pm")
	repos = filepath.Join(root, "repos")
	url := func(name string) string { return filepath.ToSlash(filepath.Join(repos, name)) }

	// The interfaces live in a repository of their own, named by alias.
	copyDir(t, filepath.Join(fixture, "interfaces"), filepath.Join(repos, "shop", "interfaces"))
	teams := map[string]string{"cart": "Cart", "orders": "Orders", "shipping": "Shipping"}
	for dir := range teams {
		copyDir(t, filepath.Join(fixture, dir), filepath.Join(repos, dir))
		matches, _ := filepath.Glob(filepath.Join(repos, dir, "flows", "*", "flow.toml"))
		for _, m := range matches {
			replaceInFile(t, m, "[input]", "[sources]\nshop = { path = \""+url("shop")+"\" }\n\n[input]")
			data, _ := os.ReadFile(m)
			s := strings.ReplaceAll(string(data), "../../../interfaces/", "shop:interfaces/")
			if err := os.WriteFile(m, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"shop", "cart", "orders", "shipping"} {
		gitCmd(t, filepath.Join(repos, name), "init", "-b", "main")
		gitCmd(t, filepath.Join(repos, name), "add", "-A")
		gitCmd(t, filepath.Join(repos, name), "commit", "-m", "first")
	}

	// The PM's part of the fixture, with its interfaces read through the alias.
	copyDir(t, filepath.Join(fixture, "product"), pm)
	flowFile := filepath.Join(pm, "flows", "shop", "flow.toml")
	data, err := os.ReadFile(flowFile)
	if err != nil {
		t.Fatal(err)
	}
	pmFlow := strings.ReplaceAll(string(data), "../../../interfaces/", "shop:interfaces/")
	if registry {
		modules := "[submodule \"shop\"]\n    path = shop\n    url = " + url("shop") + "\n    branch = main\n    dsheets = .\n"
		for dir, team := range teams {
			modules += "[submodule \"" + dir + "\"]\n    path = " + dir + "\n    url = " + url(dir) + "\n    branch = main\n    dsheets = .\n    team = \"" + team + " team\"\n"
		}
		for name, text := range map[string]string{"dsheets.toml": "registry = { path = \".\" }\n", ".gitmodules": modules} {
			if err := os.WriteFile(filepath.Join(pm, name), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		pmFlow = strings.Replace(pmFlow, "[input]", "[sources]\nshop = { path = \""+url("shop")+"\" }\n\n[input]", 1)
	}
	if err := os.WriteFile(flowFile, []byte(pmFlow), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, pm, "init", "-b", "main")
	gitCmd(t, pm, "add", "-A")
	gitCmd(t, pm, "commit", "-m", "pm")
	t.Chdir(pm)
	return pm, repos
}

// A PM repository whose registry lists the team repositories: the cases run
// through each team's own sheet, and a team that gets a rule wrong is named.
// The fixture's PM has no cases.toml, which is not needed.
func TestSliceCases(t *testing.T) {
	_, repos := shopWorld(t, true)
	out := runOK(t, "slice", "flows/shop", "--cases")
	if !strings.Contains(out, "Cases (") || !strings.Contains(out, "Orders team") {
		t.Fatalf("output:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "differs") {
			t.Errorf("nothing differs yet, got %q", line)
		}
	}

	// Orders writes the tier in lower case: it is wrong for Silver cases.
	replaceInFile(t, filepath.Join(repos, "orders", "specs", "orders", "sheet.toml"), `\"Silver\"`, `\"silver\"`)
	gitCmd(t, filepath.Join(repos, "orders"), "commit", "-am", "lower case")
	var buf, errw bytes.Buffer
	err := run([]string{"slice", "flows/shop", "--cases", "--strict"}, &buf, &errw)
	if !errors.Is(err, errFailed) {
		t.Fatalf("want errFailed, got %v\n%s%s", err, buf.String(), errw.String())
	}
	if !strings.Contains(buf.String(), "shippingCost  expected") {
		t.Errorf("output:\n%s", buf.String())
	}
}

// Without a registry there is nothing to run: every check is cannot tell,
// and the run still ends well, even with --strict.
func TestSliceCasesWithoutRegistry(t *testing.T) {
	shopWorld(t, false)
	out := runOK(t, "slice", "flows/shop", "--cases", "--strict")
	if !strings.Contains(out, "no registry: team flows cannot be found") || !strings.Contains(out, "Cases (") {
		t.Fatalf("output:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "agree") || strings.Contains(line, "differs") {
			t.Errorf("every check is cannot tell, got %q", line)
		}
	}
	if !strings.Contains(out, "cannot tell") {
		t.Errorf("output:\n%s", out)
	}
}

// A registry that cannot be read and a cases.toml that cannot be parsed
// are notes, not a stop: the report still prints.
func TestSliceCasesDegrades(t *testing.T) {
	pm, _ := shopWorld(t, false)
	// A registry folder with no .gitmodules; the flow does not need it.
	if err := os.WriteFile(filepath.Join(pm, "dsheets.toml"), []byte("registry = { path = \".\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pm, "specs", "shop", "cases.toml"), []byte("not [ toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runOK(t, "slice", "flows/shop", "--cases", "--strict")
	for _, want := range []string{"registry: ", "team flows cannot be found", "cases.toml could not be read", "Cases ("} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}
