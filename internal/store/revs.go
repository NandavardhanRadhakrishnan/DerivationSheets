package store

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotInRev marks a path that does not exist in a revision.
var ErrNotInRev = errors.New("not in that revision")

// Revs reads files and changes at any revision of a git repository. Paths
// are slash-separated and relative to the repository's top-level folder.
type Revs struct{ top string }

// Change is one file that differs between two revisions. Status is 'A'
// (added), 'D' (deleted) or 'M' (modified); renames are reported as a
// deletion and an addition.
type Change struct {
	Status byte
	Path   string
}

// NewRevs opens the repository whose working tree contains dir.
func NewRevs(dir string) (*Revs, error) {
	out, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s: not inside a git repository", dir)
	}
	return &Revs{top: filepath.Clean(strings.TrimSpace(string(out)))}, nil
}

// Top returns the repository's top-level folder.
func (r *Revs) Top() string { return r.top }

// Resolve returns the commit a revision names.
func (r *Revs) Resolve(rev string) (string, error) {
	out, err := gitOut(r.top, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s: not a revision in this repository", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeBase returns the best common ancestor of two revisions.
func (r *Revs) MergeBase(a, b string) (string, error) {
	out, err := gitOut(r.top, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("no common ancestor of %s and %s", a, b)
	}
	return strings.TrimSpace(string(out)), nil
}

// DefaultBranch names the first of origin/HEAD, origin/main, origin/master,
// main and master that exists. origin/HEAD is often unset, for instance when
// the remote was added after the repository was created.
func (r *Revs) DefaultBranch() (string, error) {
	for _, name := range []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"} {
		if _, err := r.Resolve(name); err == nil {
			return name, nil
		}
	}
	return "", errors.New("cannot tell the default branch (no origin/HEAD, origin/main, origin/master, main or master); pass an explicit base")
}

// Changed lists the files that differ between base and head, compared
// directly.
func (r *Revs) Changed(base, head string) ([]Change, error) {
	out, err := gitOut(r.top, "-c", "core.quotepath=off", "diff", "--name-status", "--no-renames", "-z", base, head)
	if err != nil {
		return nil, fmt.Errorf("comparing %s with %s: %v", base, head, err)
	}
	parts := strings.Split(string(out), "\x00")
	var cs []Change
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] == "" {
			break
		}
		cs = append(cs, Change{Status: parts[i][0], Path: parts[i+1]})
	}
	return cs, nil
}

// ReadAt returns a file's content at a revision, or an error matching
// ErrNotInRev.
func (r *Revs) ReadAt(rev, path string) ([]byte, error) {
	c, err := clean(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", "-C", r.top, "show", rev+":"+c)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("%s at %s: %w (%s)", c, rev, ErrNotInRev, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return out, nil
}

func gitOut(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Output()
}
