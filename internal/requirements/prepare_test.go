package requirements

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/testrepo"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// shopChange: the PM quarters shipping for Silver members; Orders' sheet
// still halves. o substitutes a working clone of Orders.
func shopChange(t *testing.T) (s *testrepo.Shop, ordersWork string, o Options) {
	s = testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	ordersWork = testrepo.Clone(t, s.Orders)
	fe := sources.NewFetcher(t.TempDir()).WithSubstitute(&sources.Substitute{URL: filepath.ToSlash(s.Orders), Root: ordersWork})
	return s, ordersWork, Options{Flow: filepath.Join(s.Product, "sheets", "flows", "shop"), Dir: s.Product, Base: "main", Fetcher: fe}
}

func TestDecideOpensThenClosesOnTheDraft(t *testing.T) {
	_, work, o := shopChange(t)
	p, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	q := find(p.Decide(nil).Requirements, Compute, "Orders team", "shipping_cost")
	if q == nil || q.Status != StatusOpen || len(q.Cases) == 0 {
		t.Fatalf("got %+v", q)
	}
	file := filepath.Join(work, "sheets", "specs", "orders", "sheet.toml")
	ov := flow.Overlay{flow.OverlayKey(file): []byte(strings.Replace(read(t, file), "fee / 2", "fee / 4", 1))}
	if q := find(p.Decide(ov).Requirements, Compute, "Orders team", "shipping_cost"); q.Status != StatusDone {
		t.Fatalf("with the draft: %+v", q)
	}
	if q := find(p.Decide(nil).Requirements, Compute, "Orders team", "shipping_cost"); q.Status != StatusOpen {
		t.Fatal("Decide changed the prepared state")
	}
}

func TestRunEqualsPrepareDecide(t *testing.T) {
	_, _, o := shopChange(t)
	a, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	b := p.Decide(nil)
	if a.Flow != b.Flow || a.Base != b.Base || a.Head != b.Head || len(a.Requirements) != len(b.Requirements) {
		t.Fatalf("%+v vs %+v", a, b)
	}
	for i, q := range a.Requirements {
		r := b.Requirements[i]
		if q.ID != r.ID || q.Status != r.Status || q.Note != r.Note || len(q.Cases) != len(r.Cases) {
			t.Errorf("item %d: %+v vs %+v", i, q, r)
		}
	}
	if p.Analysis() == nil {
		t.Fatal("no analysis")
	}
}

func TestEditorLines(t *testing.T) {
	s, _, o := shopChange(t) // product is on branch quarter
	o.Base, o.As = "main", "product"
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := "dsheets serve --edit sheets/specs/orders --flow product:flows/shop@quarter"
	if res.Editor["Orders team"] != want {
		t.Fatalf("got %q", res.Editor["Orders team"])
	}
	var b strings.Builder
	res.WriteMarkdown(&b)
	if !strings.Contains(b.String(), "open in the editor: "+want) {
		t.Fatal(b.String())
	}
	var j strings.Builder
	res.WriteJSON(&j)
	if !strings.Contains(j.String(), `"editor"`) {
		t.Fatal(j.String())
	}
	testrepo.Git(t, s.Product, "switch", "--detach")
	if res, _ := Run(o); len(res.Editor) != 0 {
		t.Fatalf("detached head gave %v", res.Editor)
	}
}
