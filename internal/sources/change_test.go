package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func shopChange(t *testing.T) (Source, string, string) {
	t.Helper()
	needGit(t)
	s := testrepo.NewShop(t)
	mainBefore := testrepo.Git(t, s.Product, "rev-parse", "main")
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	head := testrepo.Git(t, s.Product, "rev-parse", "HEAD")
	testrepo.Git(t, s.Product, "switch", "main") // main moves on after the branch
	testrepo.Git(t, s.Product, "commit", "--allow-empty", "-m", "later")
	return Source{Alias: "product", Git: filepath.ToSlash(s.Product), Dir: "sheets"}, mainBefore, head
}

func TestFetchChange(t *testing.T) {
	src, mainBefore, head := shopChange(t)
	ch, err := NewFetcher(t.TempDir()).FetchChange(src, "quarter")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Base != mainBefore || ch.Head != head {
		t.Fatalf("base %s head %s", ch.Base, ch.Head)
	}
	if !strings.Contains(read(t, filepath.Join(ch.Root, "specs", "shop", "sheet.toml")), "shipping_fee / 4") {
		t.Fatal("not the branch")
	}
}

func TestFetchChangeUnknownBranch(t *testing.T) {
	src, _, _ := shopChange(t)
	_, err := NewFetcher(t.TempDir()).FetchChange(src, "nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestFetchChangeOffline(t *testing.T) {
	src, _, _ := shopChange(t)
	cache := t.TempDir()
	first, err := NewFetcher(cache).FetchChange(src, "quarter")
	if err != nil {
		t.Fatal(err)
	}
	off := NewFetcher(cache)
	off.Offline = true
	again, err := off.FetchChange(src, "quarter")
	if err != nil || *again != *first {
		t.Fatalf("got %+v, %v; want %+v", again, err, first)
	}
	empty := NewFetcher(t.TempDir())
	empty.Offline = true
	if _, err := empty.FetchChange(src, "quarter"); err == nil || !strings.Contains(err.Error(), "not fetched yet and --offline is set") {
		t.Fatalf("got %v", err)
	}
}

// twoBranches makes branches "quarter" and "tenth", both from main.
func twoBranches(t *testing.T) Source {
	t.Helper()
	needGit(t)
	s := testrepo.NewShop(t)
	f := "sheets/specs/shop/sheet.toml"
	s.Change(t, s.Product, "quarter", [3]string{f, "shipping_fee / 2", "shipping_fee / 4"})
	testrepo.Git(t, s.Product, "switch", "main")
	s.Change(t, s.Product, "tenth", [3]string{f, "shipping_fee / 2", "shipping_fee / 10"})
	testrepo.Git(t, s.Product, "switch", "main")
	return Source{Alias: "product", Git: filepath.ToSlash(s.Product), Dir: "sheets"}
}

func TestFetchChangeBranchesInOneFetcher(t *testing.T) {
	src := twoBranches(t)
	f := NewFetcher(t.TempDir())
	var heads [2]string
	for i, b := range []string{"quarter", "tenth", "quarter"} {
		ch, err := f.FetchChange(src, b)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"quarter": "shipping_fee / 4", "tenth": "shipping_fee / 10"}[b]
		if !strings.Contains(read(t, filepath.Join(ch.Root, "specs", "shop", "sheet.toml")), want) {
			t.Fatalf("call %d for %s did not get that branch", i, b)
		}
		if i == 0 {
			heads[0] = ch.Head
		} else if i == 1 {
			heads[1] = ch.Head
		} else if ch.Head != heads[0] || heads[0] == heads[1] {
			t.Fatalf("head %s, want %s", ch.Head, heads[0])
		}
	}
}

func TestFetchChangeOfflineOtherBranch(t *testing.T) {
	src := twoBranches(t)
	cache := t.TempDir()
	if _, err := NewFetcher(cache).FetchChange(src, "quarter"); err != nil {
		t.Fatal(err)
	}
	off := NewFetcher(cache)
	off.Offline = true
	_, err := off.FetchChange(src, "tenth")
	if err == nil || !strings.Contains(err.Error(), "tenth") || !strings.Contains(err.Error(), "--offline is set") {
		t.Fatalf("got %v", err)
	}
}

func TestFetchChangeMissingBranchKeepsHintOff(t *testing.T) {
	src := twoBranches(t)
	f := NewFetcher(t.TempDir())
	if _, err := f.FetchChange(src, "quarter"); err != nil {
		t.Fatal(err)
	}
	_, err := f.FetchChange(src, "nope")
	if err == nil || !strings.Contains(err.Error(), "branch nope not found") || strings.Contains(err.Error(), "damaged") {
		t.Fatalf("got %v", err)
	}
}
