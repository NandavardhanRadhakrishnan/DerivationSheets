// Package testrepo builds git repositories for tests: the order-checkout shop
// fixture of internal/agree/testdata/shop as four teams' repositories and a
// registry. It is imported only by tests.
package testrepo

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Shop is the shop fixture as four git repositories on main. In each, the
// team's folder of the fixture is sheets/ and the shared interfaces are
// sheets/interfaces/, so a sparse fetch of sheets/ reaches them (flow.toml references are rewritten to ../../interfaces/).
// Product also holds the registry: dsheets.toml and .gitmodules.
type Shop struct{ Product, Cart, Orders, Ship string }

// Git runs git in dir and returns its trimmed output, failing the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewShop builds the four repositories under a temporary folder.
func NewShop(t testing.TB) *Shop {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, self, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(self), "..", "agree", "testdata", "shop")
	base := t.TempDir()
	teams := []struct{ folder, repo, name string }{
		{"product", "product", "Product team"},
		{"cart", "cart", "Cart team"},
		{"orders", "orders", "Orders team"},
		{"shipping", "ship", "Shipping team"},
	}
	paths := map[string]string{}
	for _, tm := range teams {
		paths[tm.repo] = filepath.Join(base, tm.repo)
	}
	var modules strings.Builder
	for _, tm := range teams {
		dir := paths[tm.repo]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		Git(t, dir, "init", "--quiet", "-b", "main")
		Git(t, dir, "config", "core.autocrlf", "false")
		copyTree(t, filepath.Join(fixture, tm.folder), filepath.Join(dir, "sheets"))
		copyTree(t, filepath.Join(fixture, "interfaces"), filepath.Join(dir, "sheets", "interfaces"))
		toml := fmt.Sprintf("registry = { git = %q }\n", filepath.ToSlash(paths["product"]))
		if tm.repo == "product" {
			toml = "registry = { path = \".\" }\n"
		}
		write(t, filepath.Join(dir, "dsheets.toml"), toml)
		fmt.Fprintf(&modules, "[submodule %q]\n\tpath = %s\n\turl = %s\n\tbranch = main\n\tdsheets = sheets\n\tteam = %s\n",
			tm.repo, tm.repo, filepath.ToSlash(dir), tm.name)
	}
	write(t, filepath.Join(paths["product"], ".gitmodules"), modules.String())
	for _, tm := range teams {
		Git(t, paths[tm.repo], "add", "-A")
		Git(t, paths[tm.repo], "commit", "--quiet", "-m", "first")
	}
	return &Shop{Product: paths["product"], Cart: paths["cart"], Orders: paths["orders"], Ship: paths["ship"]}
}

// Change switches repo to a new branch, applies each {file, old, new}
// replacement (file relative to the repository), commits, and stays there.
func (s *Shop) Change(t testing.TB, repo, branch string, edits ...[3]string) {
	t.Helper()
	Git(t, repo, "switch", "--quiet", "-c", branch)
	for _, e := range edits {
		file := filepath.Join(repo, filepath.FromSlash(e[0]))
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), e[1]) {
			t.Fatalf("%s does not contain %q", e[0], e[1])
		}
		write(t, file, strings.Replace(string(data), e[1], e[2], 1))
	}
	Git(t, repo, "commit", "--quiet", "-am", "change "+branch)
}

// Clone makes a working clone whose origin is repo and returns its folder.
func Clone(t testing.TB, repo string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	Git(t, filepath.Dir(dir), "clone", "--quiet", "-c", "core.autocrlf=false", filepath.ToSlash(repo), dir)
	return dir
}

func write(t testing.TB, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t testing.TB, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if d.Name() == "flow.toml" {
			// The shared interfaces sit inside the team folder, so a sparse fetch reaches them.
			data = []byte(strings.ReplaceAll(string(data), "../../../interfaces/", "../../interfaces/"))
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
