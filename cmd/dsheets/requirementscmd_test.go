package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requirementsRepo commits the order-checkout fixture on main, then switches
// to branch feature, leaves the working directory inside it, and returns it.
func requirementsRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join("..", "..")
	for _, d := range []string{"specs/order-checkout", "flows/checkout-to-payment", "interfaces", "schema"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(d)), func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, data, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	gitIn(t, dir, "switch", "-q", "-c", "feature")
	t.Chdir(dir)
	return dir
}

// doubleShipping commits a formula change on the current branch.
func doubleShipping(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "specs", "order-checkout", "sheet.toml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.Replace(s, `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`, 1)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "-am", "double it")
}

func TestRequirementsPrints(t *testing.T) {
	doubleShipping(t, requirementsRepo(t))
	out := runOK(t, "requirements", "flows/checkout-to-payment")
	for _, want := range []string{"Orders team: checkout-to-payment (base ", "Compute", "shipping_cost: free_shipping ? 0 : shipping_fee -> free_shipping ? 0 : shipping_fee * 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRequirementsJSON(t *testing.T) {
	doubleShipping(t, requirementsRepo(t))
	out := runOK(t, "requirements", "flows/checkout-to-payment", "--json")
	var got struct {
		Flow         string
		Requirements []struct{ Kind, Team string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Flow != "checkout-to-payment" || len(got.Requirements) != 1 || got.Requirements[0].Kind != "Compute" {
		t.Errorf("got %+v", got)
	}
}

func TestRequirementsEmptyIsQuiet(t *testing.T) {
	requirementsRepo(t)
	if out := runOK(t, "requirements", "flows/checkout-to-payment"); out != "" {
		t.Errorf("a flow with no change prints nothing, got %q", out)
	}
}

func TestRequirementsOutFolder(t *testing.T) {
	dir := requirementsRepo(t)
	doubleShipping(t, dir)
	target := filepath.Join(t.TempDir(), "reqs")
	runOK(t, "requirements", "flows/checkout-to-payment", "--out", target)
	for _, name := range []string{"orders-team.md", "orders-team.json"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestRequirementsExplicitRange(t *testing.T) {
	dir := requirementsRepo(t)
	doubleShipping(t, dir)
	if out := runOK(t, "requirements", "flows/checkout-to-payment", "HEAD..HEAD"); out != "" {
		t.Errorf("comparing HEAD with itself prints nothing, got %q", out)
	}
	if out := runOK(t, "requirements", "flows/checkout-to-payment", "main..HEAD"); !strings.Contains(out, "Compute") {
		t.Errorf("main..HEAD:\n%s", out)
	}
}

func TestRequirementsUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if err := run([]string{"requirements"}, &out, &errw); err != errUsage {
		t.Errorf("no flow: got %v, want errUsage", err)
	}
	if err := run([]string{"requirements", "a", "b..c", "d"}, &out, &errw); err != errUsage {
		t.Errorf("too many arguments: got %v, want errUsage", err)
	}
}
