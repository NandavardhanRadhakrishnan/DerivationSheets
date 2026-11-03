package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readQuote(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "quote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func shipping(repo, ref string) Source {
	return Source{Alias: "shipping", Git: fileURL(repo), Ref: ref, Dir: "sheets"}
}

func TestFetchFollowsDefaultBranch(t *testing.T) {
	repo := teamRepo(t)
	root, rev, err := NewFetcher(t.TempDir()).Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v2") || rev == "" {
		t.Errorf("got %q at %q; want the main branch file", q, rev)
	}
}

func TestFetchPinnedTag(t *testing.T) {
	repo := teamRepo(t)
	root, _, err := NewFetcher(t.TempDir()).Fetch(shipping(repo, "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v1") {
		t.Errorf("got %q; want the v1 file", q)
	}
}

func TestFetchSparseDir(t *testing.T) {
	repo := teamRepo(t)
	root, _, err := NewFetcher(t.TempDir()).Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != "sheets" {
		t.Errorf("root = %s; want the sheets folder", root)
	}
	clone := filepath.Dir(root)
	if _, err := os.Stat(filepath.Join(clone, "service", "main.go")); err == nil {
		t.Error("service code was checked out; only the sheets folder should be")
	}
	if _, err := os.Stat(filepath.Join(clone, "dsheets.toml")); err != nil {
		t.Error("root dsheets.toml missing; cone mode keeps root files")
	}
}

func TestFetchShallow(t *testing.T) {
	repo := teamRepo(t)
	root, _, err := NewFetcher(t.TempDir()).Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.TrimSpace(run(t, filepath.Dir(root), "rev-list", "--count", "HEAD")); n != "1" {
		t.Errorf("clone has %s commits; want 1 (shallow)", n)
	}
}

func TestFetchOncePerFetcher(t *testing.T) {
	repo := teamRepo(t)
	cache := t.TempDir()
	f := NewFetcher(cache)
	if _, _, err := f.Fetch(shipping(repo, "")); err != nil {
		t.Fatal(err)
	}
	commitQuote(t, repo, "# v3")
	root, _, err := f.Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v2") {
		t.Errorf("same fetcher fetched again: %q", q)
	}
	root, _, err = NewFetcher(cache).Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v3") {
		t.Errorf("a new run did not follow the branch: %q", q)
	}
}

func TestFetchMaxAge(t *testing.T) {
	repo := teamRepo(t)
	f := NewFetcher(t.TempDir())
	f.MaxAge = time.Nanosecond
	if _, _, err := f.Fetch(shipping(repo, "")); err != nil {
		t.Fatal(err)
	}
	commitQuote(t, repo, "# v3")
	time.Sleep(time.Millisecond)
	root, _, err := f.Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v3") {
		t.Errorf("expired fetch not refreshed: %q", q)
	}
}

func TestFetchPinnedNotRefetched(t *testing.T) {
	repo := teamRepo(t)
	cache := t.TempDir()
	if _, _, err := NewFetcher(cache).Fetch(shipping(repo, "v1")); err != nil {
		t.Fatal(err)
	}
	gone := repo + "-gone"
	if err := os.Rename(repo, gone); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(gone, repo)
	root, _, err := NewFetcher(cache).Fetch(shipping(repo, "v1"))
	if err != nil {
		t.Fatalf("pinned source fetched again: %v", err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v1") {
		t.Errorf("got %q", q)
	}
}

func TestFetchOffline(t *testing.T) {
	repo := teamRepo(t)
	f := NewFetcher(t.TempDir())
	f.Offline = true
	if _, _, err := f.Fetch(shipping(repo, "")); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline error = %v", err)
	}
}

func TestFetchMissingDir(t *testing.T) {
	repo := teamRepo(t)
	src := shipping(repo, "")
	src.Dir = "nowhere"
	if _, _, err := NewFetcher(t.TempDir()).Fetch(src); err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("missing folder error = %v", err)
	}
}

func TestSubstitute(t *testing.T) {
	repo := teamRepo(t)
	tree := t.TempDir()
	write(t, filepath.Join(tree, "sheets", "quote.toml"), "interface = \"quote\"\n# tree\n")
	f := NewFetcher(t.TempDir()).WithSubstitute(&Substitute{URL: fileURL(repo) + "/", Root: tree})
	root, rev, err := f.Fetch(shipping(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(tree, "sheets") || rev != "working tree" {
		t.Errorf("followed source = %s @ %s; want the substitute", root, rev)
	}
	root, _, err = f.Fetch(shipping(repo, "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if q := readQuote(t, root); !strings.Contains(q, "# v1") {
		t.Errorf("pinned source substituted: %q", q)
	}
	if hits := f.PinnedHits(); len(hits) != 1 || hits[0].Ref != "v1" {
		t.Errorf("pinned hits = %v", hits)
	}
}

func TestFollowed(t *testing.T) {
	repo := teamRepo(t)
	f := NewFetcher(t.TempDir())
	for ref, want := range map[string]bool{"": true, "main": true, "v1": false} {
		got, err := f.Followed(shipping(repo, ref))
		if err != nil || got != want {
			t.Errorf("Followed(%q) = %v, %v; want %v", ref, got, err, want)
		}
	}
}
