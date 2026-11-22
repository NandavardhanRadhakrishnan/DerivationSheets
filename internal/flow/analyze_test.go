package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestOverlayKeyCleansAndAbsolutes(t *testing.T) {
	rel := filepath.Join("a", "..", "b", "sheet.toml")
	abs, _ := filepath.Abs(filepath.Join("b", "sheet.toml"))
	if OverlayKey(rel) != filepath.Clean(abs) {
		t.Fatalf("got %q", OverlayKey(rel))
	}
}

func TestAnalyzeWithReadsSheetAndFlowFromOverlay(t *testing.T) {
	dir := filepath.Join("..", "agree", "testdata", "shop", "orders", "flows", "orders")
	sheetFile := filepath.Join("..", "agree", "testdata", "shop", "orders", "specs", "orders", "sheet.toml")
	ov := Overlay{OverlayKey(sheetFile): []byte(strings.Replace(mustRead(t, sheetFile), "fee / 2", "fee / 4", 1))}
	f, err := LoadWith(dir, ov)
	if err != nil {
		t.Fatal(err)
	}
	a := AnalyzeWith(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())), ov)
	if got := a.Sheet.ByID["shipping_cost"].Formula; !strings.Contains(got, "fee / 4") {
		t.Fatalf("formula %q", got)
	}
	ov[OverlayKey(filepath.Join(dir, FileName))] = []byte(strings.Replace(mustRead(t, filepath.Join(dir, FileName)), `title   = "Orders"`, `title   = "Orders draft"`, 1))
	if f, _ = LoadWith(dir, ov); f.Title != "Orders draft" {
		t.Fatalf("title %q", f.Title)
	}
}
