package requirements

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

const paymentFile = "interfaces/payments/payment-request.toml"

// seedRemote makes a bare repository on main holding one interface file with
// the given content (committed as is) and returns its path.
func seedRemote(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "-q", "--bare", "-b", "main", bare)
	git(t, root, "init", "-q", "-b", "main", seed)
	git(t, seed, "config", "core.autocrlf", "false")
	p := filepath.Join(seed, filepath.FromSlash(paymentFile))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "first")
	git(t, seed, "remote", "add", "origin", bare)
	git(t, seed, "push", "-q", "origin", "main")
	return bare
}

func paymentsOriginal(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(paymentFile)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func schemaResult(field string) *Result {
	q := Requirement{ID: MakeID(Schema, "Payments team", "checkout", "payment-request:"+field), Kind: Schema, Team: "Payments team",
		Repo: "payments", Flow: "checkout", Subject: "payment-request:" + field,
		What: "add `" + field + "` (number, required) to payment-request.toml", File: paymentFile,
		Edit: &SchemaEdit{Op: "add", Field: field, Type: "number", Required: true}}
	return &Result{Flow: "checkout", Base: "abc1234", Requirements: []Requirement{q}}
}

func applyOpts(t *testing.T, bare string) ApplyOptions {
	return ApplyOptions{
		Registry: map[string]sources.Source{"payments": {Alias: "payments", Git: bare, Ref: "main", Dir: "interfaces"}},
		Work:     filepath.Join(t.TempDir(), "work"),
		Change:   "Add handling fee",
	}
}

func TestApplyAddsField(t *testing.T) {
	orig := paymentsOriginal(t)
	bare := seedRemote(t, orig)
	got, err := Apply(schemaResult("handlingFee"), applyOpts(t, bare))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Repo != "payments" || got[0].Branch != "dsheets/checkout" || !got[0].Pushed {
		t.Fatalf("got %+v", got)
	}
	file := git(t, bare, "show", "dsheets/checkout:"+paymentFile)
	if !strings.HasPrefix(file+"\n", orig) && !strings.HasPrefix(file, strings.TrimSuffix(orig, "\n")) {
		t.Errorf("untouched lines changed:\n%s", file)
	}
	if !strings.Contains(file, "name     = \"handlingFee\"\ntype     = \"number\"\nrequired = true") {
		t.Errorf("field not added:\n%s", file)
	}
	id := schemaResult("handlingFee").Requirements[0].ID
	record := git(t, bare, "show", "dsheets/checkout:interfaces/changes/add-handling-fee.md")
	for _, want := range []string{"Add handling fee", "checkout", "abc1234", id} {
		if !strings.Contains(record, want) {
			t.Errorf("record file lacks %q:\n%s", want, record)
		}
	}
	msg := git(t, bare, "log", "-1", "--format=%B", "dsheets/checkout")
	for _, want := range []string{"Dsheets-Requirement: " + id, "Dsheets-Change: Add handling fee"} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(got[0].Description, id) {
		t.Errorf("description lacks the id:\n%s", got[0].Description)
	}
	if main := git(t, bare, "show", "main:"+paymentFile); strings.Contains(main, "handlingFee") {
		t.Error("main was changed")
	}
}

func TestApplyNoPush(t *testing.T) {
	bare := seedRemote(t, paymentsOriginal(t))
	o := applyOpts(t, bare)
	o.NoPush = true
	got, err := Apply(schemaResult("handlingFee"), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Pushed {
		t.Fatalf("got %+v", got)
	}
	if out := git(t, bare, "branch", "--list", "dsheets/*"); out != "" {
		t.Errorf("remote has %q, want no branch", out)
	}
	if out := git(t, got[0].Dir, "branch", "--list", "dsheets/checkout"); out == "" {
		t.Error("local clone has no branch")
	}
}

func TestApplyRefusesExistingBranch(t *testing.T) {
	bare := seedRemote(t, paymentsOriginal(t))
	if _, err := Apply(schemaResult("handlingFee"), applyOpts(t, bare)); err != nil {
		t.Fatal(err)
	}
	before := git(t, bare, "rev-parse", "dsheets/checkout")
	_, err := Apply(schemaResult("handlingFee"), applyOpts(t, bare))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want a refusal naming the existing branch, got %v", err)
	}
	if after := git(t, bare, "rev-parse", "dsheets/checkout"); after != before {
		t.Error("the existing branch moved")
	}
}

func TestApplySkipsDeclaredField(t *testing.T) {
	bare := seedRemote(t, paymentsOriginal(t))
	got, err := Apply(schemaResult("currency"), applyOpts(t, bare))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v; want nothing to apply", got, err)
	}
	if out := git(t, bare, "branch", "--list", "dsheets/*"); out != "" {
		t.Errorf("remote has %q, want no branch", out)
	}
}

func TestApplyBranchOverride(t *testing.T) {
	bare := seedRemote(t, paymentsOriginal(t))
	o := applyOpts(t, bare)
	o.Branches = map[string]string{"payments": "ab/handling-fee"}
	got, err := Apply(schemaResult("handlingFee"), o)
	if err != nil || len(got) != 1 || got[0].Branch != "ab/handling-fee" {
		t.Fatalf("got %+v, %v", got, err)
	}
	git(t, bare, "rev-parse", "--verify", "ab/handling-fee")
}

func TestApplyUsesGivenClone(t *testing.T) {
	bare := seedRemote(t, paymentsOriginal(t))
	clone := filepath.Join(t.TempDir(), "mine")
	git(t, ".", "clone", "-q", bare, clone)
	o := applyOpts(t, bare)
	o.Clones = map[string]string{"payments": clone}
	got, err := Apply(schemaResult("handlingFee"), o)
	if err != nil || len(got) != 1 || got[0].Dir != clone {
		t.Fatalf("got %+v, %v", got, err)
	}
	if git(t, clone, "rev-parse", "--abbrev-ref", "HEAD") != "dsheets/checkout" {
		t.Error("the given clone is not on the new branch")
	}
	// A dirty clone is refused.
	if err := os.WriteFile(filepath.Join(clone, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Branches = map[string]string{"payments": "another"}
	if _, err := Apply(schemaResult("handlingFee"), o); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("want a refusal for a dirty clone, got %v", err)
	}
}

func TestApplyCRLF(t *testing.T) {
	bare := seedRemote(t, strings.ReplaceAll(paymentsOriginal(t), "\n", "\r\n"))
	clone := filepath.Join(t.TempDir(), "mine")
	git(t, ".", "-c", "core.autocrlf=false", "clone", "-q", bare, clone)
	git(t, clone, "config", "core.autocrlf", "false")
	o := applyOpts(t, bare)
	o.Clones = map[string]string{"payments": clone}
	o.NoPush = true
	if _, err := Apply(schemaResult("handlingFee"), o); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(clone, filepath.FromSlash(paymentFile)))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "handlingFee") || strings.Count(s, "\n") != strings.Count(s, "\r\n") {
		t.Errorf("line endings were not kept:\n%q", s)
	}
}

func TestApplyRefusesAnUnknownType(t *testing.T) {
	bare := seedRemote(t, paymentsOriginal(t))
	r := schemaResult("handlingFee")
	r.Requirements[0].Edit.Type = ""
	_, err := Apply(r, applyOpts(t, bare))
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("want a refusal about the unknown type, got %v", err)
	}
	if out := git(t, bare, "branch", "--list", "dsheets/*"); out != "" {
		t.Errorf("nothing should be pushed, remote has %q", out)
	}
}
