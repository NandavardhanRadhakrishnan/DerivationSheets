package requirements

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

func git(t *testing.T, dir string, args ...string) string {
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

// fixtureRepo makes a repository on main holding the order-checkout fixture.
// With withFlow false the first commit leaves the flow out.
func fixtureRepo(t *testing.T, withFlow bool) string {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, dir)
	git(t, dir, "init", "-b", "main")
	if withFlow {
		git(t, dir, "add", "-A")
	} else {
		git(t, dir, "add", "specs", "interfaces", "schema")
	}
	git(t, dir, "commit", "-m", "first")
	return dir
}

func runOpts(dir string, t *testing.T) Options {
	return Options{Flow: filepath.Join(dir, "flows", "checkout-to-payment"), Dir: dir,
		Fetcher: sources.NewFetcher(t.TempDir())}
}

func TestRunFormulaChange(t *testing.T) {
	dir := fixtureRepo(t, true)
	git(t, dir, "switch", "-c", "feature")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")
	res, err := Run(runOpts(dir, t))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requirements) != 1 || res.Requirements[0].Kind != Compute || res.Requirements[0].Subject != "shipping_cost" {
		t.Fatalf("got %+v", res.Requirements)
	}
	if res.Flow != "checkout-to-payment" || res.Base != git(t, dir, "rev-parse", "--short=7", "main") {
		t.Errorf("flow %q, base %q", res.Flow, res.Base)
	}
}

func TestRunNewFlowNoBase(t *testing.T) {
	dir := fixtureRepo(t, false)
	git(t, dir, "switch", "-c", "feature")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "add the flow")
	res, err := Run(runOpts(dir, t))
	if err != nil {
		t.Fatal(err)
	}
	if find(res.Requirements, Compute, "Shipping team", "shipping_fee") == nil {
		t.Errorf("a new flow lists every computed row, got %+v", res.Requirements)
	}
}

func TestRunNoChange(t *testing.T) {
	dir := fixtureRepo(t, true)
	git(t, dir, "switch", "-c", "feature")
	res, err := Run(runOpts(dir, t))
	if err != nil || len(res.Requirements) != 0 {
		t.Fatalf("got %+v, %v; want none", res, err)
	}
}

func TestRunExplicitBase(t *testing.T) {
	dir := fixtureRepo(t, true)
	first := git(t, dir, "rev-parse", "HEAD")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")
	o := runOpts(dir, t)
	o.Base = first
	res, err := Run(o)
	if err != nil || len(res.Requirements) != 1 {
		t.Fatalf("got %+v, %v; want one requirement", res, err)
	}
}

func TestRunSchemaFileIsRepoRelative(t *testing.T) {
	dir := fixtureRepo(t, true)
	git(t, dir, "switch", "-c", "feature")
	addHandling(t, dir, true)
	git(t, dir, "commit", "-am", "add handling")
	res, err := Run(runOpts(dir, t))
	if err != nil {
		t.Fatal(err)
	}
	s := find(res.Requirements, Schema, "Payments team", "handlingFee")
	if s == nil || s.File != "interfaces/payments/payment-request.toml" || s.Repo != filepath.Base(dir) {
		t.Fatalf("schema = %+v", s)
	}
}
