package editor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/eval"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/requirements"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/testrepo"
)

const shopChange = "product:flows/shop@quarter"

// view builds the shop with the PM's change on branch quarter, opens it from a
// clone of repo ("orders" or "ship"), applies the {old, new} edits to the
// clone's sheet as an unsaved draft, and returns the Requirements view.
func view(t *testing.T, repo string, edits [][2]string) *ReqView {
	t.Helper()
	return viewIn(t, repo, edits, false)
}

func viewIn(t *testing.T, repo string, edits [][2]string, noOrigin bool) *ReqView {
	t.Helper()
	s := testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	work := testrepo.Clone(t, map[string]string{"orders": s.Orders, "ship": s.Ship}[repo])
	if noOrigin {
		testrepo.Git(t, work, "remote", "remove", "origin")
	}
	p, err := requirements.OpenChange(work, shopChange, sources.NewFetcher(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, "sheets", "specs", repo)
	file := filepath.Join(dir, "sheet.toml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, e := range edits {
		if !strings.Contains(text, e[0]) {
			t.Fatalf("%s does not contain %q", file, e[0])
		}
		text = strings.Replace(text, e[0], e[1], 1)
	}
	sh, err := sheet.Parse(dir, file, text)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := eval.New(sh)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Decide(flow.Overlay{flow.OverlayKey(file): []byte(text)})
	teams, why := p.TeamsOf(work)
	return RequirementsView(shopChange, res, p.Analysis(), teams, why, eng)
}

// item finds the view's item of the given kind whose What or Row names row.
func item(t *testing.T, v *ReqView, kind, row string) ReqItem {
	t.Helper()
	for _, it := range v.Items {
		if it.Kind == kind && strings.Contains(it.What, "`"+row+"`") {
			return it
		}
	}
	t.Fatalf("no %s item for %s in %+v", kind, row, v.Items)
	return ReqItem{}
}

func TestRequirementsViewOwnItem(t *testing.T) {
	v := view(t, "orders", nil)
	if v.Flow != shopChange || !slices.Equal(v.Teams, []string{"Orders team"}) || v.Why != "" {
		t.Fatalf("%+v", v)
	}
	it := item(t, v, "Compute", "shipping_cost")
	if !it.Mine || it.Status != "open" || len(it.Cases) == 0 || it.Cases[0].Expected == "" || !strings.Contains(it.Cases[0].Input, "member") {
		t.Fatalf("%+v", it)
	}
	if !slices.Equal(it.Rows, []string{"shipping_cost"}) {
		t.Fatalf("rows %v", it.Rows)
	}
	if !v.Items[0].Mine {
		t.Fatalf("mine first: %+v", v.Items)
	}
}

func TestRequirementsViewDraftTurnsGreen(t *testing.T) {
	v := view(t, "orders", [][2]string{{"fee / 2", "fee / 4"}})
	if it := item(t, v, "Compute", "shipping_cost"); it.Status != "done" || len(it.Cases) != 0 {
		t.Fatalf("%+v", it)
	}
}

func TestRequirementsViewNeighbour(t *testing.T) {
	v := view(t, "ship", nil)
	it := item(t, v, "Compute", "shipping_cost")
	if it.Mine || it.Team != "Orders team" || it.Via != "the call to shipping-quote" {
		t.Fatalf("%+v", it)
	}
	if !slices.Equal(it.Rows, []string{"fee"}) {
		t.Fatalf("rows %v", it.Rows)
	}
}

func TestRequirementsViewUnknownTeam(t *testing.T) {
	v := viewIn(t, "orders", nil, true)
	if len(v.Teams) != 0 || !strings.Contains(v.Why, "origin") || len(v.Items) == 0 {
		t.Fatalf("%+v", v)
	}
	for _, it := range v.Items {
		if it.Mine || it.Via != "" {
			t.Fatalf("%+v", it)
		}
	}
}
