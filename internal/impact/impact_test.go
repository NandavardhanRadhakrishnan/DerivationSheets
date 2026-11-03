package impact

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

func git(t *testing.T, dir string, args ...string) {
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

// copyDir copies a folder of this repository into dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fileURL(dir string) string { return "file://" + filepath.ToSlash(dir) }

// dropField removes one [[field]] from an interface file.
func dropField(text, name string) string {
	parts := strings.Split(text, "[[field]]")
	kept := parts[:1]
	for _, p := range parts[1:] {
		if !strings.Contains(p, `name     = "`+name+`"`) {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "[[field]]")
}

const repoRoot = "../.."

type fixture struct {
	producer, orders, registry string
	tree                       string // the producer's working tree under review
	cache                      string
}

// setup builds a producer (shipping) with the quote interfaces, a consumer
// (orders) with the example flow reading them through the registry and a
// second flow pinning shipping to v1, and a registry listing both. extra is
// appended to the registry's .gitmodules. prepare may change the producer's
// main branch before the working tree is cloned from it.
func setup(t *testing.T, extra string, prepare func(producer string)) fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	fx := fixture{producer: t.TempDir(), orders: t.TempDir(), registry: t.TempDir(), cache: t.TempDir()}

	// Producer: interfaces under sheets/, a flow of its own, service code.
	git(t, fx.producer, "init", "-q", "-b", "main")
	write(t, filepath.Join(fx.producer, "dsheets.toml"), "registry = { git = \""+fileURL(fx.registry)+"\" }\n")
	copyDir(t, filepath.Join(repoRoot, "interfaces", "shipping"), filepath.Join(fx.producer, "sheets", "interfaces"))
	write(t, filepath.Join(fx.producer, "sheets", "flows", "own", "flow.toml"), "flow = \"own\"\nsheet = \"shipping:specs/none\"\n")
	write(t, filepath.Join(fx.producer, "service", "main.go"), "package main\n")
	git(t, fx.producer, "add", ".")
	git(t, fx.producer, "commit", "-q", "-m", "shipping")
	git(t, fx.producer, "tag", "v1")

	// Consumer: the example flow, reading shipping through the registry.
	git(t, fx.orders, "init", "-q", "-b", "main")
	write(t, filepath.Join(fx.orders, "dsheets.toml"), "registry = { git = \""+fileURL(fx.registry)+"\" }\n")
	sheets := filepath.Join(fx.orders, "sheets")
	copyDir(t, filepath.Join(repoRoot, "specs", "order-checkout"), filepath.Join(sheets, "specs", "order-checkout"))
	for _, team := range []string{"storefront", "orders", "payments"} {
		copyDir(t, filepath.Join(repoRoot, "interfaces", team), filepath.Join(sheets, "interfaces", team))
	}
	text := read(t, filepath.Join(repoRoot, "flows", "checkout-to-payment", "flow.toml"))
	text = strings.Replace(text, "[sources]\nshipping = { path = \"../../interfaces/shipping\" }\n", "", 1)
	text = strings.ReplaceAll(text, "shipping:quote-", "shipping:interfaces/quote-")
	write(t, filepath.Join(sheets, "flows", "checkout-to-payment", "flow.toml"), text)
	pinned := strings.Replace(text, "[input]", "[sources]\nshipping = { git = \""+fileURL(fx.producer)+"\", ref = \"v1\", dir = \"sheets\" }\n\n[input]", 1)
	write(t, filepath.Join(sheets, "flows", "pinned", "flow.toml"), pinned)
	git(t, fx.orders, "add", ".")
	git(t, fx.orders, "commit", "-q", "-m", "orders")

	git(t, fx.registry, "init", "-q", "-b", "main")
	write(t, filepath.Join(fx.registry, ".gitmodules"),
		"[submodule \"shipping\"]\n\tpath = shipping\n\turl = "+fileURL(fx.producer)+"\n\tdsheets = sheets\n"+
			"[submodule \"orders\"]\n\tpath = orders\n\turl = "+fileURL(fx.orders)+"\n\tdsheets = sheets\n"+extra)
	git(t, fx.registry, "add", ".")
	git(t, fx.registry, "commit", "-q", "-m", "registry")

	if prepare != nil {
		prepare(fx.producer)
	}
	fx.tree = filepath.Join(t.TempDir(), "tree")
	git(t, filepath.Dir(fx.tree), "clone", "-q", fileURL(fx.producer), fx.tree)
	return fx
}

func (fx fixture) run(t *testing.T, as string) *Report {
	t.Helper()
	r, err := Run(Options{Root: fx.tree, As: as, Fetcher: sources.NewFetcher(fx.cache)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (fx fixture) response() string {
	return filepath.Join(fx.tree, "sheets", "interfaces", "quote-response.toml")
}

func TestImpactNoChange(t *testing.T) {
	fx := setup(t, "", nil)
	r := fx.run(t, "")
	if r.Self != "shipping" {
		t.Errorf("Self = %q", r.Self)
	}
	// The example already lacks fields elsewhere; none of that is new.
	if len(r.New) != 0 || len(r.NotChecked) != 0 || r.Checked != 1 || r.ExitCode() != 0 {
		t.Errorf("report = %+v", r)
	}
	// The commits compared are named, so a run can be reproduced.
	if r.Sources["orders"] == "" || r.Sources["shipping (base)"] == "" {
		t.Errorf("sources = %v", r.Sources)
	}
	var buf strings.Builder
	r.WriteText(&buf)
	if !strings.Contains(buf.String(), "sources: orders@"+r.Sources["orders"]) {
		t.Errorf("text lacks the sources line:\n%s", buf.String())
	}
}

func (fx fixture) request() string {
	return filepath.Join(fx.tree, "sheets", "interfaces", "quote-request.toml")
}

func TestImpactDroppedField(t *testing.T) {
	fx := setup(t, "", nil)
	write(t, fx.request(), dropField(read(t, fx.request()), "zone"))
	r := fx.run(t, "shipping")
	if len(r.New) != 1 || r.ExitCode() != 1 {
		t.Fatalf("new = %+v", r.New)
	}
	f := r.New[0]
	if f.Repo != "orders" || f.Flow != "orders:flows/checkout-to-payment" || f.Field != "zone" ||
		f.Status != flow.Missing || f.Interface != "quote-request" || f.Owner != "Shipping team" ||
		f.Where != "stage shipping-quote" || !strings.Contains(f.Message, "needed by") {
		t.Errorf("finding = %+v", f)
	}
	// Shipping removed the field from its own interface: the report does not
	// tell Shipping to ask itself to add it back.
	if f.Cause != CauseRemoved || strings.Contains(f.Message, "ask ") {
		t.Errorf("cause %q, message %q", f.Cause, f.Message)
	}
	if r.Views[f.Flow] == nil {
		t.Error("no view kept for the affected flow")
	}
}

// A field the flow carries by name, in carries, is an error when dropped.
func TestImpactDroppedCarriedField(t *testing.T) {
	fx := setup(t, "", nil)
	write(t, fx.response(), dropField(read(t, fx.response()), "fee"))
	r := fx.run(t, "shipping")
	if len(r.New) == 0 || r.New[0].Status != StatusError || !strings.Contains(r.New[0].Message, `"fee"`) {
		t.Errorf("new = %+v", r.New)
	}
}

func TestImpactResolved(t *testing.T) {
	var original string
	fx := setup(t, "", func(producer string) {
		path := filepath.Join(producer, "sheets", "interfaces", "quote-request.toml")
		original = read(t, path)
		write(t, path, dropField(original, "zone"))
		git(t, producer, "commit", "-q", "-am", "drop zone")
	})
	write(t, fx.request(), original)
	r := fx.run(t, "shipping")
	if len(r.New) != 0 || len(r.Resolved) != 1 || r.Resolved[0].Field != "zone" || r.ExitCode() != 0 {
		t.Errorf("new %+v, resolved %+v", r.New, r.Resolved)
	}
}

func TestImpactDeletedFile(t *testing.T) {
	fx := setup(t, "", nil)
	if err := os.Remove(fx.response()); err != nil {
		t.Fatal(err)
	}
	r := fx.run(t, "shipping")
	if len(r.New) == 0 || r.ExitCode() != 1 {
		t.Fatalf("new = %+v", r.New)
	}
	f := r.New[0]
	if f.Status != "error" || !strings.Contains(f.Message, "shipping:interfaces/quote-response.toml") {
		t.Errorf("finding = %+v", f)
	}
	if strings.Contains(f.Message, filepath.ToSlash(fx.tree)) || strings.Contains(f.Message, fx.tree) {
		t.Errorf("message has a local path: %s", f.Message)
	}
}

func TestImpactPinned(t *testing.T) {
	fx := setup(t, "", nil)
	write(t, fx.response(), dropField(read(t, fx.response()), "fee"))
	r := fx.run(t, "shipping")
	if len(r.Pinned) != 1 || r.Pinned[0].Ref != "v1" || r.Pinned[0].Flow != "orders:flows/pinned" {
		t.Errorf("pinned = %+v", r.Pinned)
	}
	for _, f := range r.New {
		if f.Flow == "orders:flows/pinned" {
			t.Errorf("pinned flow checked: %+v", f)
		}
	}
}

// A flow that reads this repository through a pinned alias and a followed
// one is checked: the followed reads see the change.
func TestImpactMixedPinnedAndFollowed(t *testing.T) {
	fx := setup(t, "", nil)
	text := read(t, filepath.Join(fx.orders, "sheets", "flows", "checkout-to-payment", "flow.toml"))
	text = strings.Replace(text, "shipping:interfaces/quote-request", "aaa:interfaces/quote-request", 1)
	text = strings.Replace(text, "[input]", "[sources]\naaa = { git = \""+fileURL(fx.producer)+"\", ref = \"v1\", dir = \"sheets\" }\n\n[input]", 1)
	write(t, filepath.Join(fx.orders, "sheets", "flows", "mixed", "flow.toml"), text)
	git(t, fx.orders, "add", ".")
	git(t, fx.orders, "commit", "-q", "-m", "mixed")
	write(t, fx.response(), dropField(read(t, fx.response()), "fee"))
	r := fx.run(t, "shipping")
	found := false
	for _, f := range r.New {
		found = found || f.Flow == "orders:flows/mixed"
	}
	if !found {
		t.Errorf("mixed flow not checked: new %+v, pinned %+v", r.New, r.Pinned)
	}
}

func TestImpactFindsSelfByOrigin(t *testing.T) {
	fx := setup(t, "", nil)
	git(t, fx.tree, "remote", "set-url", "origin", fileURL(fx.producer)+"/")
	if r := fx.run(t, ""); r.Self != "shipping" {
		t.Errorf("Self = %q", r.Self)
	}
}

func TestImpactUnknownSelf(t *testing.T) {
	fx := setup(t, "", nil)
	git(t, fx.tree, "remote", "set-url", "origin", "https://example.com/elsewhere.git")
	_, err := Run(Options{Root: fx.tree, Fetcher: sources.NewFetcher(fx.cache)})
	if err == nil || !strings.Contains(err.Error(), "--as") {
		t.Errorf("error = %v", err)
	}
}

func TestImpactUnreachable(t *testing.T) {
	missing := fileURL(filepath.Join(t.TempDir(), "missing"))
	fx := setup(t, "[submodule \"billing\"]\n\tpath = billing\n\turl = "+missing+"\n", nil)
	r := fx.run(t, "shipping")
	if len(r.NotChecked) != 1 || r.NotChecked[0].Repo != "billing" || r.ExitCode() != 3 {
		t.Errorf("not checked = %+v, exit %d", r.NotChecked, r.ExitCode())
	}
}

// breakConsumer commits an unrelated error into the consumer's flow.
func (fx fixture) breakConsumer(t *testing.T) {
	t.Helper()
	path := filepath.Join(fx.orders, "sheets", "flows", "checkout-to-payment", "flow.toml")
	text := read(t, path)
	broken := strings.Replace(text, `computes  = ["customer_years", "loyalty_tier"]`, `computes  = ["customer_years", "loyalty_tier", "no_such_row"]`, 1)
	if broken == text {
		t.Fatal("fixture: computes line not found")
	}
	write(t, path, broken)
	git(t, fx.orders, "commit", "-q", "-am", "break")
}

// A consumer flow already failing before this change is listed, its
// unchanged error is neither new nor resolved, and it does not change the
// exit code.
func TestImpactFailsAtBase(t *testing.T) {
	fx := setup(t, "", nil)
	fx.breakConsumer(t)
	r := fx.run(t, "shipping")
	if len(r.New) != 0 || len(r.Resolved) != 0 || len(r.NotChecked) != 0 || r.ExitCode() != 0 {
		t.Errorf("report = %+v", r)
	}
	if len(r.Failing) != 1 || r.Failing[0].Flow != "orders:flows/checkout-to-payment" || !strings.Contains(r.Failing[0].Reason, "no_such_row") {
		t.Errorf("failing = %+v", r.Failing)
	}
}

// A new error in a flow that already failed is still reported.
func TestImpactFailsAtBaseNewError(t *testing.T) {
	fx := setup(t, "", nil)
	fx.breakConsumer(t)
	write(t, fx.response(), dropField(read(t, fx.response()), "fee"))
	r := fx.run(t, "shipping")
	if len(r.New) != 1 || !strings.Contains(r.New[0].Message, `"fee"`) || r.ExitCode() != 1 {
		t.Errorf("new = %+v", r.New)
	}
}

func TestImpactDirSpelledWithDot(t *testing.T) {
	fx := setup(t, "", nil)
	path := filepath.Join(fx.registry, ".gitmodules")
	write(t, path, strings.Replace(read(t, path), "dsheets = sheets", "dsheets = ./sheets/", 1))
	git(t, fx.registry, "commit", "-q", "-am", "dot")
	r := fx.run(t, "shipping")
	if r.Checked != 1 || len(r.Failing) != 0 || len(r.NotChecked) != 0 || len(r.New) != 0 {
		t.Errorf("report = %+v", r)
	}
}

func TestImpactUnknownSelfHidesToken(t *testing.T) {
	fx := setup(t, "", nil)
	git(t, fx.tree, "remote", "set-url", "origin", "https://x-access-token:SECRET123@example.com/elsewhere.git")
	_, err := Run(Options{Root: fx.tree, Fetcher: sources.NewFetcher(fx.cache)})
	if err == nil || strings.Contains(err.Error(), "SECRET123") || !strings.Contains(err.Error(), "example.com/elsewhere") {
		t.Errorf("error = %v", err)
	}
}
