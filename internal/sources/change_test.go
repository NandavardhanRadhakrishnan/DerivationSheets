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
