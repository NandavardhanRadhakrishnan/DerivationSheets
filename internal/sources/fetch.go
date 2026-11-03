package sources

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Substitute stands in a local folder for one repository: impact checks
// other teams' flows against a working tree this way.
type Substitute struct {
	URL  string // compared with SameRepo
	Root string // the repository root on disk
}

// Fetcher fetches git sources into one cache. Fetches are shallow (one
// commit) and sparse (only the source's folder, plus files at the root), so
// a team's service code is never downloaded.
//
// A source that follows a branch is fetched once per Fetcher, or again when
// older than MaxAge; a pinned tag or commit only when it is not cached yet.
type Fetcher struct {
	Cache   string
	Offline bool
	// MaxAge, when set, fetches a followed source again once its last fetch
	// is older; serve uses it so a page open for long does not go stale.
	MaxAge time.Duration
	// Sub, when set, replaces a followed source of that repository.
	Sub *Substitute

	s      *fetchShared
	pinned *[]Source
}

// fetchShared is the state fetchers made by WithSubstitute share.
type fetchShared struct {
	mu       sync.Mutex
	at       map[string]time.Time // clone folder -> last fetch in this run
	followed map[string]bool      // url@ref -> whether ref is a branch
}

// NewFetcher makes a fetcher that clones into cache.
func NewFetcher(cache string) *Fetcher {
	return &Fetcher{Cache: cache, pinned: &[]Source{},
		s: &fetchShared{at: map[string]time.Time{}, followed: map[string]bool{}}}
}

// WithSubstitute returns a fetcher sharing f's cache and fetch state, with s
// standing in for its repository.
func (f *Fetcher) WithSubstitute(s *Substitute) *Fetcher {
	g := *f
	g.Sub, g.pinned = s, &[]Source{}
	return &g
}

// PinnedHits lists sources of the substituted repository that were pinned
// to a tag or commit, so were fetched as usual instead of substituted.
func (f *Fetcher) PinnedHits() []Source { return *f.pinned }

// refFile records, inside a clone, whether its ref follows a branch, so
// later and offline runs need not ask the remote.
const refFile = "dsheets-ref"

// Followed reports whether a source follows a branch: its ref is empty (the
// default branch) or names a branch on the remote. Tags and commits are pinned.
func (f *Fetcher) Followed(src Source) (bool, error) {
	if src.Ref == "" {
		return true, nil
	}
	key := src.Git + "@" + src.Ref
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if v, ok := f.s.followed[key]; ok {
		return v, nil
	}
	if data, err := os.ReadFile(filepath.Join(f.cloneDir(src), ".git", refFile)); err == nil {
		v := strings.TrimSpace(string(data)) == "followed"
		f.s.followed[key] = v
		return v, nil
	}
	if f.Offline {
		return false, nil
	}
	out, err := git("", "ls-remote", "--heads", src.Git, "refs/heads/"+src.Ref)
	if err != nil {
		return false, err
	}
	f.s.followed[key] = out != ""
	return out != "", nil
}

// Fetch returns the folder the source's alias means (the clone's root joined
// with Dir) and the short commit checked out.
func (f *Fetcher) Fetch(src Source) (root, rev string, err error) {
	followed, err := f.Followed(src)
	if err != nil {
		return "", "", err
	}
	if f.Sub != nil && SameRepo(src.Git, f.Sub.URL) {
		if followed {
			root, err := within(f.Sub.Root, src.Dir, src.Git)
			return root, "working tree", err
		}
		*f.pinned = append(*f.pinned, src)
	}
	dir := f.cloneDir(src)
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	_, statErr := os.Stat(filepath.Join(dir, ".git", refFile))
	cached := statErr == nil
	last, fetched := f.s.at[dir]
	fresh := fetched && (f.MaxAge == 0 || time.Since(last) < f.MaxAge)
	switch {
	case !cached && f.Offline:
		return "", "", fmt.Errorf("not fetched yet and --offline is set; run fetch first")
	case !cached || (followed && !fresh && !f.Offline):
		if err := fetchInto(dir, src, followed); err != nil {
			return "", "", err
		}
		f.s.at[dir] = time.Now()
	}
	if rev, err = git(dir, "rev-parse", "--short", "HEAD"); err != nil {
		return "", "", err
	}
	root, err = within(dir, src.Dir, src.Git)
	return root, rev, err
}

// within joins a source's folder to a repository root, checking it exists.
func within(repo, sub, url string) (string, error) {
	root := filepath.Join(repo, filepath.FromSlash(sub))
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", fmt.Errorf("folder %s not found in %s", sub, url)
	}
	return root, nil
}

func (f *Fetcher) cloneDir(src Source) string {
	sum := sha1.Sum([]byte(src.Git + "@" + src.Ref + "#" + src.Dir))
	return filepath.Join(f.Cache, src.Alias+"-"+hex.EncodeToString(sum[:])[:10])
}

var commitHash = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// fetchInto creates or updates a shallow, sparse clone and checks out the
// source's ref.
func fetchInto(dir string, src Source, followed bool) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		steps := [][]string{{"init", "--quiet"}, {"remote", "add", "origin", src.Git}}
		if src.Dir != "" {
			steps = append(steps, []string{"sparse-checkout", "set", "--cone", src.Dir})
		}
		for _, args := range steps {
			if _, err := git(dir, args...); err != nil {
				return err
			}
		}
	}
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := git(dir, "fetch", "--quiet", "--depth", "1", "--filter=blob:none", "origin", ref); err != nil {
		if !commitHash.MatchString(src.Ref) {
			return err
		}
		// Some servers refuse a commit by hash: fetch the history without
		// file contents, then check the commit out.
		if _, err2 := git(dir, "fetch", "--quiet", "--filter=blob:none", "origin", "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"); err2 != nil {
			return err
		}
		ref = src.Ref
	} else {
		ref = "FETCH_HEAD"
	}
	if _, err := git(dir, "-c", "advice.detachedHead=false", "checkout", "--quiet", "--force", ref); err != nil {
		return err
	}
	kind := "pinned"
	if followed {
		kind = "followed"
	}
	return os.WriteFile(filepath.Join(dir, ".git", refFile), []byte(kind+"\n"), 0o644)
}
