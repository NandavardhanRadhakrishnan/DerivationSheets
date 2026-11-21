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

func TestRunWithRegistryFileAtBase(t *testing.T) {
	dir := fixtureRepo(t, true)
	if err := os.WriteFile(filepath.Join(dir, "dsheets.toml"), []byte("registry = { path = \".\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "registry")
	git(t, dir, "switch", "-c", "feature")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")
	o := runOpts(dir, t)
	o.Base = "main" // the registry here names no alias for this repository
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requirements) != 1 {
		t.Fatalf("got %+v", res.Requirements)
	}
}

func TestRunSchemaFileWhenHeadIsARevision(t *testing.T) {
	dir := fixtureRepo(t, true)
	first := git(t, dir, "rev-parse", "HEAD")
	addHandling(t, dir, true)
	git(t, dir, "commit", "-am", "add handling")
	o := runOpts(dir, t)
	o.Base, o.Head = first, "HEAD"
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	s := find(res.Requirements, Schema, "Payments team", "handlingFee")
	if s == nil || s.File != "interfaces/payments/payment-request.toml" || s.Repo != filepath.Base(dir) {
		t.Fatalf("schema = %+v", s)
	}
}

func TestRunRefusesAFetchedFlow(t *testing.T) {
	dir := fixtureRepo(t, true)
	o := runOpts(dir, t)
	o.Flow = filepath.Join(dir, ".dsheets", "sources", "abc123", "flows", "x")
	if _, err := Run(o); err == nil || !strings.Contains(err.Error(), "fetched") {
		t.Fatalf("want a refusal naming the fetched copy, got %v", err)
	}
}

// A flow whose payments interface comes from another repository, fetched into
// <repo>/.dsheets/sources as in real use. With the head being the working
// tree, the Schema requirement belongs to the fetched repository and its file
// path is relative to that clone, not to the flow's own repository.
func TestRunSchemaFileOfAFetchedSource(t *testing.T) {
	dir := fixtureRepo(t, true)

	origin := t.TempDir()
	data, err := os.ReadFile(filepath.Join(dir, "interfaces", "payments", "payment-request.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(origin, "interfaces", "payments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "interfaces", "payments", "payment-request.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, origin, "init", "-b", "main")
	git(t, origin, "add", "-A")
	git(t, origin, "commit", "-m", "payments interfaces")

	edit(t, dir, "flows/checkout-to-payment/flow.toml", "[sources]\n",
		"[sources]\npayments = { git = \""+filepath.ToSlash(origin)+"\", dir = \"interfaces/payments\" }\n")
	edit(t, dir, "flows/checkout-to-payment/flow.toml", `interface = "../../interfaces/payments/payment-request.toml"`, `interface = "payments:payment-request.toml"`)
	git(t, dir, "commit", "-am", "payments interface comes from its own repository")

	git(t, dir, "switch", "-c", "feature")
	addHandling(t, dir, true)
	git(t, dir, "commit", "-am", "add handling")

	o := runOpts(dir, t)
	o.Fetcher = nil // the real cache: <repo>/.dsheets/sources
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	s := find(res.Requirements, Schema, "Payments team", "handlingFee")
	if s == nil {
		t.Fatalf("no schema requirement for handlingFee in %+v", res.Requirements)
	}
	if s.File != "interfaces/payments/payment-request.toml" {
		t.Errorf("file = %q, want the path inside the fetched repository", s.File)
	}
	if s.Repo == filepath.Base(dir) {
		t.Errorf("repo = %q, the flow's own repository; the interface belongs to the fetched one", s.Repo)
	}
}

func TestRunNoRegistryIsUndocumented(t *testing.T) {
	dir := fixtureRepo(t, true)
	git(t, dir, "switch", "-c", "feature")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")
	res, err := Run(runOpts(dir, t))
	if err != nil {
		t.Fatal(err)
	}
	q := find(res.Requirements, Compute, "", "shipping_cost")
	if q == nil || q.Status != StatusUndocumented || !strings.Contains(q.Note, "registry") {
		t.Fatalf("got %+v", q)
	}
}

func TestRunNoCheckLeavesOpen(t *testing.T) {
	dir := fixtureRepo(t, true)
	git(t, dir, "switch", "-c", "feature")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")
	o := runOpts(dir, t)
	o.NoCheck = true
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requirements) == 0 {
		t.Fatal("no requirements")
	}
	for _, q := range res.Requirements {
		if q.Status != StatusOpen || q.Note != "" {
			t.Errorf("%s: status %q, note %q", q.ID, q.Status, q.Note)
		}
	}
}

func TestCounts(t *testing.T) {
	r := &Result{Requirements: []Requirement{{Status: StatusDone}, {Status: StatusOpen}, {}, {Status: StatusDone}}}
	if done, total := r.Counts(); done != 2 || total != 4 {
		t.Errorf("Counts = %d of %d", done, total)
	}
}

// An owner's sheet, fetched through the registry, closes the item end to end.
func TestRunClosesAnItemFromTheOwnersSheet(t *testing.T) {
	dir := fixtureRepo(t, true)

	owners := t.TempDir()
	if err := os.MkdirAll(filepath.Join(owners, "sheets", "specs", "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	ordersSheet := `sheet = "orders"
version = 1

[[row]]
id = "free_shipping"
source = "request.freeShipping"
type = "boolean"

[[row]]
id = "shipping_fee"
source = "call.shipping-quote.fee"
type = "number"

[[row]]
id = "shipping_cost"
uses = ["free_shipping", "shipping_fee"]
formula = "free_shipping ? 0 : shipping_fee * 2"
`
	if err := os.WriteFile(filepath.Join(owners, "sheets", "specs", "orders", "sheet.toml"), []byte(ordersSheet), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, owners, "init", "-b", "main")
	git(t, owners, "add", "-A")
	git(t, owners, "commit", "-m", "orders sheets")

	if err := os.WriteFile(filepath.Join(dir, "dsheets.toml"), []byte("registry = { path = \".\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modules := "[submodule \"orders\"]\n    path = orders\n    url = " + filepath.ToSlash(owners) + "\n    branch = main\n    dsheets = sheets\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte(modules), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "registry")
	git(t, dir, "switch", "-c", "feature")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")

	o := runOpts(dir, t)
	o.Base = "main" // the registry here names no alias for this repository
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	q := find(res.Requirements, Compute, "Orders team", "shipping_cost")
	if q == nil || q.Status != StatusDone {
		t.Fatalf("the owner's sheet already has the rule, got %+v", q)
	}
	var out strings.Builder
	if err := res.WriteMarkdown(&out); err != nil || out.String() != "" {
		t.Errorf("everything done prints nothing, got %q, %v", out.String(), err)
	}
	if done, total := res.Counts(); done != total || total == 0 {
		t.Errorf("Counts = %d of %d", done, total)
	}
}

// gitRepo makes a repository on main holding files, by path, and copies of
// the fixture files in copies (destination path -> fixture path).
func gitRepo(t *testing.T, files, copies map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for to, from := range copies {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(from)))
		if err != nil {
			t.Fatal(err)
		}
		files[to] = string(data)
	}
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "init", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "sheets")
	return dir
}

const ordersFlow = `flow    = "orders"
version = 1
title   = "Orders"
sheet   = "../../specs/orders"

[input]
interface = "../../interfaces/orders/order-placed.toml"

[[stage]]
id     = "quote"
kind   = "call"
caller = "Orders team"
callee = "Shipping team"

  [stage.request]
  interface = "../../interfaces/shipping/quote-request.toml"

  [stage.response]
  interface = "../../interfaces/shipping/quote-response.toml"

[output]
interface = "../../interfaces/payments/payment-request.toml"
producer  = "Orders team"
`

// ordersByCases is the Orders team's sheet: shipping_cost is written
// differently from the PM's rule, but gives the same values.
const ordersByCases = `sheet   = "orders"
version = 1

[[row]]
id     = "country"
name   = "country"
source = "request.country"
type   = "string"

[[row]]
id     = "subtotal"
name   = "subtotal"
source = "request.subtotal"
type   = "number"

[[row]]
id     = "weight_kg"
name   = "weight_kg"
source = "request.weightKg"
type   = "number"

  [[row.send]]
  target = "quote"
  field  = "weightKg"
  usage  = "R"

[[row]]
id     = "shipping_method"
name   = "shipping_method"
source = "request.shippingMethod"
type   = "string"

  [[row.send]]
  target = "quote"
  field  = "method"
  usage  = "R"

[[row]]
id     = "loyalty_tier"
name   = "loyalty_tier"
source = "request.loyaltyTier"
type   = "string"

[[row]]
id     = "zone"
name   = "zone"
uses   = ["country"]
lookup = { from = "db.shipping_zones", key = ["country"] }
values_file = "data/shipping_zones.csv"

  [[row.reject]]
  when    = "zone == \"NONE\""
  code    = "DESTINATION_NOT_SERVED"
  message = "We do not ship to this country"

  [[row.send]]
  target = "quote"
  field  = "zone"
  usage  = "R"

[[row]]
id   = "free_shipping"
name   = "free_shipping"
uses = ["subtotal", "loyalty_tier"]

  [row.table]
  hit_policy = "first"
  columns = ["subtotal", "loyalty_tier", "=>"]
  rows = [
    [">= 100", "-",    true],
    ["-",      "Gold", true],
    ["-",      "-",    false],
  ]

[[row]]
id     = "shipping_fee"
name   = "shipping_fee"
source = "call.quote.fee"
type   = "number"

[[row]]
id      = "shipping_cost"
name   = "shipping_cost"
uses    = ["free_shipping", "shipping_fee"]
formula = "free_shipping ? 0 : (shipping_fee + 0) * 2"

  [row.send]
  field = "shippingCost"
  usage = "R"
`

const shippingFlow = `flow    = "ship"
version = 1
title   = "Shipping"
sheet   = "../../specs/ship"

[input]
interface = "../../interfaces/shipping/quote-request.toml"

[output]
interface = "../../interfaces/shipping/quote-response.toml"
producer  = "Shipping team"
`

const shippingSheet = `sheet   = "ship"
version = 1

[[row]]
id     = "zone"
name   = "zone"
source = "request.zone"
type   = "string"

[[row]]
id     = "shipping_method"
name   = "shipping_method"
source = "request.method"
type   = "string"

[[row]]
id     = "weight_kg"
name   = "weight_kg"
source = "request.weightKg"
type   = "number"

[[row]]
id     = "shipping_fee"
name   = "shipping_fee"
uses   = ["zone", "shipping_method", "weight_kg"]
lookup = { from = "db.shipping_rates", key = ["zone", "shipping_method", "weight_kg"] }
values_file = "data/shipping_rates.csv"

  [row.send]
  field = "fee"
  usage = "R"
`

// teamInterfaces are the fixture's interface files a team repository keeps
// a copy of, under sheets/interfaces.
func teamInterfaces() map[string]string {
	out := map[string]string{}
	for _, f := range []string{"orders/order-placed.toml", "orders/order-placed.avsc", "shipping/quote-request.toml",
		"shipping/quote-response.toml", "shipping/shipping.openapi.yaml", "payments/payment-request.toml", "payments/payment-request.schema.json"} {
		out["sheets/interfaces/"+f] = "interfaces/" + f
	}
	return out
}

// The Orders team's own flow, fetched through the registry, runs the PM's
// cases: its rule is written differently, but every case agrees, so the
// item is done even though the rule text differs.
func TestRunClosesComputeByCases(t *testing.T) {
	dir := fixtureRepo(t, true)
	// The order-placed topic carries the shipping method, so the Orders team
	// can ask for a quote.
	edit(t, dir, "interfaces/orders/order-placed.toml", "[[field]]\nname     = \"weightKg\"",
		"[[field]]\nname     = \"shippingMethod\"\ntype     = \"string\"\nrequired = true\n\n[[field]]\nname     = \"weightKg\"")
	git(t, dir, "commit", "-am", "order-placed carries the shipping method")

	oc := teamInterfaces()
	oc["sheets/specs/orders/data/shipping_zones.csv"] = "specs/order-checkout/data/shipping_zones.csv"
	orders := gitRepo(t, map[string]string{
		"sheets/flows/orders/flow.toml":  ordersFlow,
		"sheets/specs/orders/sheet.toml": ordersByCases,
	}, oc)
	sc := teamInterfaces()
	sc["sheets/specs/ship/data/shipping_rates.csv"] = "specs/order-checkout/data/shipping_rates.csv"
	shipping := gitRepo(t, map[string]string{
		"sheets/flows/ship/flow.toml":  shippingFlow,
		"sheets/specs/ship/sheet.toml": shippingSheet,
	}, sc)

	if err := os.WriteFile(filepath.Join(dir, "dsheets.toml"), []byte("registry = { path = \".\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modules := "[submodule \"orders\"]\n    path = orders\n    url = " + filepath.ToSlash(orders) + "\n    branch = main\n    dsheets = sheets\n" +
		"[submodule \"ship\"]\n    path = ship\n    url = " + filepath.ToSlash(shipping) + "\n    branch = main\n    dsheets = sheets\n    team = Shipping team\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte(modules), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "registry")
	git(t, dir, "switch", "-c", "feature")
	edit(t, dir, "specs/order-checkout/sheet.toml", `"free_shipping ? 0 : shipping_fee"`, `"free_shipping ? 0 : shipping_fee * 2"`)
	git(t, dir, "commit", "-am", "double it")

	o := runOpts(dir, t)
	o.Base = "main"
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	q := find(res.Requirements, Compute, "Orders team", "shipping_cost")
	if q == nil || q.Status != StatusDone {
		t.Fatalf("every case agrees, so the item is done; got %+v", q)
	}
	if strings.Contains(q.Note, "rule text") {
		t.Errorf("judged by cases, not by rule text: note %q", q.Note)
	}
}
