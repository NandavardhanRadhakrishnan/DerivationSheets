package editor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

const flowPath = "flows/checkout-to-payment/flow.toml"
const orderPlaced = "interfaces/orders/order-placed.toml"
const paymentRequest = "interfaces/payments/payment-request.toml"

// flowWorkspace copies the checkout-to-payment flow, its interfaces and
// its sheet into a git repository with one commit.
func flowWorkspace(t *testing.T) (string, *Editor) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, d := range []string{"flows", "interfaces", filepath.Join("specs", "order-checkout"), "schema"} {
		copyDir(t, filepath.Join("..", "..", d), filepath.Join(dir, d))
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "core.autocrlf", "false")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "one")
	st, err := store.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, &Editor{Store: st, Root: dir, Fetcher: sources.NewFetcher(t.TempDir())}
}

// flowDraft starts a draft from the flow as saved: every stage, the
// output and the input as the file has them.
func flowDraft(t *testing.T, ed *Editor) FlowDraft {
	t.Helper()
	f := load(t, ed, flowPath)
	doc, err := tomledit.Parse(f.Data, tomledit.Flow)
	if err != nil {
		t.Fatal(err)
	}
	out := OutputFromDoc(doc)
	in := InputFromDoc(doc)
	d := FlowDraft{File: flowPath, Token: f.Token, Stages: StagesFromDoc(doc), Output: &out, Input: &in, Tokens: map[string]string{}}
	for _, p := range []string{orderPlaced, paymentRequest} {
		d.Tokens[p] = load(t, ed, p).Token
	}
	return d
}

func saveFlow(t *testing.T, ed *Editor, d FlowDraft) *FlowResult {
	t.Helper()
	res, err := ed.SaveFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saved {
		t.Fatalf("not saved: %+v", res)
	}
	return res
}

// addedLines lists the lines of after that are not in before, in order.
func addedLines(before, after string) []string {
	have := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		have[l]++
	}
	var out []string
	for _, l := range strings.Split(after, "\n") {
		if have[l] > 0 {
			have[l]--
			continue
		}
		out = append(out, l)
	}
	return out
}

func status(a *flow.Analysis, ifaceName, row string) string {
	for _, b := range a.Boundaries {
		if b.Iface == nil || b.Iface.Name != ifaceName {
			continue
		}
		for _, it := range b.Items {
			if it.Row == row {
				return it.Status
			}
		}
	}
	return ""
}

func TestFlowUnchangedRoundTrip(t *testing.T) {
	_, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	orig := load(t, ed, flowPath).Data
	content, err := d.Apply(orig)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(orig) {
		t.Fatalf("a draft of the flow as saved changes it:\n%s", content)
	}
	res, err := ed.CheckFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged || res.Analysis == nil || len(res.Analysis.Errors) > 0 {
		t.Errorf("unchanged %v, analysis %+v", res.Unchanged, res.Analysis)
	}
}

func TestPinWritesComputes(t *testing.T) {
	dir, ed := flowWorkspace(t)
	before := string(load(t, ed, flowPath).Data)
	d := flowDraft(t, ed)
	// Move loyalty_tier from the storefront's step 1 to the caller's step 2.
	d.Stages[0].Computes = []string{"customer_years"}
	d.Stages[1].Computes = append(d.Stages[1].Computes, "loyalty_tier")
	res := saveFlow(t, ed, d)
	data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(flowPath)))
	got := addedLines(before, string(data))
	want := []string{
		`version = 2`,
		`computes  = ["customer_years"]`,
		`computes = ["zone", "discount_rate", "discount", "free_shipping", "loyalty_tier"]`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changed lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !res.VersionRaised || res.Version != 2 {
		t.Errorf("version %d raised %v", res.Version, res.VersionRaised)
	}
}

func TestUnpin(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Stages[1].Computes = []string{"discount_rate", "discount", "free_shipping"}
	res, err := ed.CheckFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Analysis == nil || len(res.Analysis.Errors) > 0 {
		t.Fatalf("analysis: %+v", res.Analysis)
	}
	saveFlow(t, ed, d)
	data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(flowPath)))
	if strings.Contains(string(data), `"zone", "discount_rate"`) {
		t.Errorf("zone still pinned:\n%s", data)
	}
}

func TestAddFieldImportedLocal(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Output, d.Input, d.Stages = nil, nil, nil
	d.Fields = []FieldEdit{{Interface: orderPlaced, Name: "shippingMethod", Type: "string", Required: true, Add: true}}
	res, err := ed.CheckFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "order-placed.avsc") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if got := status(res.Analysis, "order-placed", "shipping_method"); got != flow.Carried {
		t.Errorf("shipping_method at order-placed is %q in the draft, want carried", got)
	}
	before := load(t, ed, orderPlaced).Data
	saveFlow(t, ed, d)
	data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(orderPlaced)))
	want := string(before) + "\n[[field]]\nname     = \"shippingMethod\"\ntype     = \"string\"\nrequired = true\nlocal    = true\n"
	if string(data) != want {
		t.Errorf("order-placed.toml:\n%s\nwant:\n%s", data, want)
	}
	if f := load(t, ed, flowPath); f.Token != d.Token {
		t.Error("flow.toml was written although it did not change")
	}
}

func TestAddFieldNotImported(t *testing.T) {
	dir, ed := flowWorkspace(t)
	// A proposed interface written by hand: no imported key.
	p := filepath.Join(dir, filepath.FromSlash(paymentRequest))
	data, _ := os.ReadFile(p)
	var kept []string
	for _, l := range strings.SplitAfter(string(data), "\n") {
		if !strings.HasPrefix(l, "imported") {
			kept = append(kept, l)
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(kept, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	d := flowDraft(t, ed)
	d.Fields = []FieldEdit{{Interface: paymentRequest, Name: "giftWrap", Type: "boolean", Add: true}}
	res := saveFlow(t, ed, d)
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
	data, _ = os.ReadFile(p)
	if !strings.HasSuffix(string(data), "\n[[field]]\nname     = \"giftWrap\"\ntype     = \"boolean\"\nrequired = false\n") {
		t.Errorf("payment-request.toml ends:\n%s", data[len(data)-200:])
	}
}

func TestFixFieldRequired(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Fields = []FieldEdit{{Interface: orderPlaced, Name: "weightKg", Type: "number", Required: true}}
	res := saveFlow(t, ed, d)
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "importing again undoes") {
		t.Errorf("warnings %q", res.Warnings)
	}
	data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(orderPlaced)))
	if !strings.Contains(string(data), "name     = \"weightKg\"\ntype     = \"number\"\nrequired = true\n") {
		t.Errorf("weightKg not required:\n%s", data)
	}
}

func TestAliasInterfaceReadOnly(t *testing.T) {
	_, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	for _, p := range []string{"shipping:quote-request.toml", "interfaces/shipping/quote-request.toml", "../outside.toml"} {
		d.Fields = []FieldEdit{{Interface: p, Name: "x", Type: "string", Add: true}}
		_, err := ed.SaveFlow(d)
		if !errors.Is(err, ErrDraft) {
			t.Errorf("%s: err %v, want a draft error", p, err)
		}
	}
}

func TestMapWritesCarries(t *testing.T) {
	dir, ed := flowWorkspace(t)
	before := string(load(t, ed, flowPath).Data)
	d := flowDraft(t, ed)
	d.Stages[0].Carries = map[string]string{"weightKg": "weight_kg"}
	saveFlow(t, ed, d)
	data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(flowPath)))
	got := addedLines(before, string(data))
	want := []string{`version = 2`, `carries   = { weightKg = "weight_kg" }`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changed lines:\n%s", strings.Join(got, "\n"))
	}
}

func TestAddStage(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Stages[1].Computes = []string{"discount_rate", "discount", "free_shipping"}
	added := StageData{ID: "zone-event", Kind: "handoff", Interface: "../../interfaces/orders/order-placed.toml",
		Producer: "Orders team", Computes: []string{"zone"}}
	d.Stages = []StageData{d.Stages[0], added, d.Stages[1]}
	res, err := ed.SaveFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saved {
		t.Fatalf("not saved: %+v", res.Problems)
	}
	data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(flowPath)))
	want := "computes  = [\"customer_years\", \"loyalty_tier\"]\n\n[[stage]]\nid        = \"zone-event\"\nkind      = \"handoff\"\n" +
		"interface = \"../../interfaces/orders/order-placed.toml\"\nproducer  = \"Orders team\"\ncomputes  = [\"zone\"]\n\n" +
		"# 2. The Orders team asks"
	if !strings.Contains(string(data), want) {
		t.Errorf("flow.toml:\n%s", data)
	}
	f, err := flow.Load(filepath.Join(dir, "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Stages) != 3 || f.Stages[1].ID != "zone-event" || f.Stages[2].ID != "shipping-quote" {
		t.Errorf("stages %+v", f.Stages)
	}
}

func TestDeleteStage(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Stages[1].Computes = append([]string{"customer_years", "loyalty_tier"}, d.Stages[1].Computes...)
	d.Stages = d.Stages[1:]
	d.Deleted = []int{0}
	res, err := ed.CheckFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := d.Apply(load(t, ed, flowPath).Data)
	if strings.Contains(string(content), "order-placed") || strings.Count(string(content), "[[stage]]") != 1 {
		t.Errorf("stage not deleted:\n%s", content)
	}
	// The comment above the deleted stage goes with it; the one above the
	// call stays.
	if strings.Contains(string(content), "The storefront publishes") || !strings.Contains(string(content), "# 2. The Orders team asks") {
		t.Errorf("comments:\n%s", content)
	}
	if hasNewFlow(res.Problems) {
		t.Errorf("problems %+v", res.Problems)
	}
	if res.Analysis.Place["loyalty_tier"] != 0 || res.Analysis.Steps[0].Owner != "Orders team" {
		t.Errorf("loyalty_tier placed at step %d, owned by %q", res.Analysis.Place["loyalty_tier"], res.Analysis.Steps[0].Owner)
	}
	_ = dir
}

func TestStageSilentDropRefused(t *testing.T) {
	_, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Stages = d.Stages[1:]
	if _, err := ed.CheckFlow(d); !errors.Is(err, ErrDraft) {
		t.Errorf("err %v, want a draft error naming the stage left out", err)
	}
	d = flowDraft(t, ed)
	two := 1
	d.Stages[1].ID, d.Stages[1].Orig = "renamed", &two
	if _, err := ed.CheckFlow(d); !errors.Is(err, ErrDraft) {
		t.Errorf("renaming a saved stage: err %v, want a draft error", err)
	}
}

func TestFlowConflict(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Stages[1].Computes = []string{"discount_rate", "discount", "free_shipping"}
	d.Fields = []FieldEdit{{Interface: orderPlaced, Name: "shippingMethod", Type: "string", Add: true}}
	p := filepath.Join(dir, filepath.FromSlash(orderPlaced))
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(data, "# edited elsewhere\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	flowBefore := load(t, ed, flowPath)
	res, err := ed.SaveFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved || len(res.Conflict) != 1 || res.Conflict[0] != orderPlaced {
		t.Errorf("result %+v", res)
	}
	if load(t, ed, flowPath).Token != flowBefore.Token {
		t.Error("flow.toml was written despite the conflict")
	}
}

func TestFlowIntroducedError(t *testing.T) {
	_, ed := flowWorkspace(t)
	d := flowDraft(t, ed)
	d.Stages[0].Computes = append(d.Stages[0].Computes, "no_such_row")
	res, err := ed.SaveFlow(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved || !hasNewFlow(res.Problems) {
		t.Fatalf("saved %v problems %+v", res.Saved, res.Problems)
	}
	if !strings.Contains(res.Problems[0].Message, "no_such_row") {
		t.Errorf("problems %+v", res.Problems)
	}
}

func TestFlowPanel(t *testing.T) {
	_, ed := flowWorkspace(t)
	res, err := ed.CheckFlow(flowDraft(t, ed))
	if err != nil {
		t.Fatal(err)
	}
	p := ed.Panel(res.Analysis)
	if p == nil || len(p.Steps) != 4 || len(p.Boundaries) != 5 {
		t.Fatalf("panel %+v", p)
	}
	if s := p.Steps[2]; s.Stage != 1 || s.Key != "callee_computes" || s.Team != "Shipping team" {
		t.Errorf("step 3 %+v", s)
	}
	if b := p.Boundaries[1]; b.Path != orderPlaced || b.ReadOnly != "" || b.Imported != "order-placed.avsc" || b.Part != "handoff" {
		t.Errorf("order-placed %+v", b)
	}
	if b := p.Boundaries[2]; b.Path != "" || b.ReadOnly != "belongs to shipping" || b.Part != "request" {
		t.Errorf("quote-request %+v", b)
	}
	sm := p.Rows["shipping_method"]
	if !sm.Input || len(sm.Marks) != 1 || sm.Marks[0].Boundary != 1 || sm.Marks[0].Field != "shippingMethod" || sm.Marks[0].Status != flow.Missing {
		t.Errorf("shipping_method %+v", sm)
	}
	w := p.Rows["weight_kg"]
	if len(w.Marks) != 1 || w.Marks[0].Fix != "required" {
		t.Errorf("weight_kg %+v", w)
	}
	// loyalty_tier is pinned to step 1; discount_rate, computed in step 2,
	// needs it, so it can go in step 1 or 2.
	lt := p.Rows["loyalty_tier"]
	if !lt.Pinned || lt.Step != 0 || !slices.Equal(lt.Can, []int{0, 1}) {
		t.Errorf("loyalty_tier %+v", lt)
	}
	// shipping_cost needs the callee's shipping_fee: the callee's step or the last.
	if sc := p.Rows["shipping_cost"]; !slices.Equal(sc.Can, []int{2, 3}) {
		t.Errorf("shipping_cost can %v", sc.Can)
	}
}
