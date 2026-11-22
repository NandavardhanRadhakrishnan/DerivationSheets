package requirements

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/testrepo"
)

func TestParseChangeRef(t *testing.T) {
	a, p, b, err := ParseChangeRef("product:flows/shop@quarter")
	if err != nil || a != "product" || p != "flows/shop" || b != "quarter" {
		t.Fatalf("%q %q %q %v", a, p, b, err)
	}
	for ref, want := range map[string]string{
		"flows/shop@quarter": "--flow names the PM's repository by its registry alias: alias:path@branch",
		"product:flows/shop": "--flow needs @<branch>, the PM's branch with the change",
		"product:@x":         "--flow names the PM's repository by its registry alias: alias:path@branch",
	} {
		if _, _, _, err := ParseChangeRef(ref); err == nil || err.Error() != want {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

func TestOpenChange(t *testing.T) {
	s := testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	work := testrepo.Clone(t, s.Orders)
	p, err := OpenChange(work, "product:flows/shop@quarter", sources.NewFetcher(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if teams, why := p.TeamsOf(work); !slices.Equal(teams, []string{"Orders team"}) {
		t.Fatalf("%v %s", teams, why)
	}
	file := filepath.Join(work, "sheets", "specs", "orders", "sheet.toml")
	ov := flow.Overlay{flow.OverlayKey(file): []byte(strings.Replace(read(t, file), "fee / 2", "fee / 4", 1))}
	if q := find(p.Decide(ov).Requirements, Compute, "Orders team", "shipping_cost"); q.Status != StatusDone {
		t.Fatalf("%+v", q)
	}
}

// A repository registered under two aliases is its teams under either: here
// the flow's team is keyed on the second alias in alias order.
func TestTeamsOfSecondAlias(t *testing.T) {
	s := testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	mods := filepath.Join(s.Product, ".gitmodules")
	text := read(t, mods)
	head, rest, _ := strings.Cut(text, `[submodule "orders"]`)
	rest = strings.Replace(rest, "team = Orders team", "team = Ledger team", 1)
	text = head + `[submodule "orders"]` + rest + fmt.Sprintf("[submodule %q]\n\tpath = orders2\n\turl = %s\n\tbranch = main\n\tdsheets = sheets\n\tteam = Orders team\n",
		"orders2", filepath.ToSlash(s.Orders))
	if err := os.WriteFile(mods, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	testrepo.Git(t, s.Product, "commit", "--quiet", "-am", "second alias")
	work := testrepo.Clone(t, s.Orders)
	p, err := OpenChange(work, "product:flows/shop@quarter", sources.NewFetcher(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if teams, why := p.TeamsOf(work); !slices.Equal(teams, []string{"Orders team"}) {
		t.Fatalf("%v %s", teams, why)
	}
}
