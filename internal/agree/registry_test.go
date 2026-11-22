package agree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRegistryTeamsReadsOverlay(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "shop"))
	const url = "https://example.com/orders.git"
	reg := map[string]sources.Source{"orders": {Alias: "orders", Git: url, Dir: "orders", Team: "Orders team"}}
	fe := sources.NewFetcher(t.TempDir()).WithSubstitute(&sources.Substitute{URL: url, Root: root})
	sheetFile := filepath.Join(root, "orders", "specs", "orders", "sheet.toml")
	ov := flow.Overlay{flow.OverlayKey(sheetFile): []byte(strings.Replace(mustRead(t, sheetFile), "fee / 2", "fee / 4", 1))}
	flows, notes := RegistryTeams(reg, fe, "", ov)("Orders team")
	if len(flows) != 1 || len(notes) != 0 {
		t.Fatalf("flows %d, notes %v", len(flows), notes)
	}
	if !strings.Contains(flows[0].Sheet.ByID["shipping_cost"].Formula, "fee / 4") {
		t.Fatal("overlay not read")
	}
}
