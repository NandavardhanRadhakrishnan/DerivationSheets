// Package store reads and writes the files the editor changes. It is the only
// code in the editor that touches them on disk: a file is loaded with a token
// identifying its version and saved only if the token still matches, so a
// `git pull`, another editor or an LLM changing a file in the meantime is
// never overwritten. It also answers what a file was in the last commit.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

var (
	ErrConflict     = errors.New("changed on disk since it was loaded")
	ErrNotCommitted = errors.New("not in the last commit")
	ErrOutside      = errors.New("outside the repository")
	ErrReadOnly     = errors.New("belongs to another repository")
)

// File is a loaded file and the token of the version loaded.
type File struct {
	Path  string // slash-separated, relative to the store root
	Data  []byte
	Token string
}

// Write asks to replace Path with Data if it still has Token. Token "" means
// the file must not exist yet (creating it).
type Write struct {
	Path  string
	Data  []byte
	Token string
}

// ConflictError lists every path whose token no longer matches.
type ConflictError struct{ Paths []string }

func (e *ConflictError) Error() string {
	return strings.Join(e.Paths, ", ") + ": " + ErrConflict.Error()
}

func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// Store loads and saves files by path relative to its root.
type Store interface {
	// Load reads a file. A missing file is an error matching fs.ErrNotExist.
	Load(path string) (File, error)
	// Save writes every file, after checking every token; on a conflict
	// nothing is written. It returns the new tokens, in order.
	Save(ws ...Write) (tokens []string, err error)
	// Committed returns the file's content in the last commit, or
	// ErrNotCommitted.
	Committed(path string) ([]byte, error)
}

// Token identifies a version of a file by its content.
func Token(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Local is a store over a folder in a git working tree.
type Local struct {
	root string
	mu   sync.Mutex
}

// NewLocal returns a store rooted at a folder.
func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a folder", root)
	}
	return &Local{root: abs}, nil
}

// clean checks that p stays inside the root and returns it slash-separated.
func clean(p string) (string, error) {
	s := filepath.ToSlash(p)
	if s == "" || path.IsAbs(s) || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return "", fmt.Errorf("%s: %w", p, ErrOutside)
	}
	c := path.Clean(s)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%s: %w", p, ErrOutside)
	}
	// A colon names an alternate data stream on Windows (.git::$INDEX_ALLOCATION
	// is the .git folder); no sheet, flow or interface needs one.
	if strings.Contains(c, ":") {
		return "", fmt.Errorf("%s: %w", p, ErrOutside)
	}
	return c, nil
}

// confined checks that no part of c that exists is a link or junction, so
// the path cannot lead out of the root.
func (s *Local) confined(c string) error {
	dir := s.root
	for _, part := range strings.Split(c, "/") {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return fmt.Errorf("%s: goes through a link: %w", c, ErrOutside)
		}
	}
	return nil
}

// writable refuses paths that belong to another repository: the git folder,
// fetched sources under .dsheets, and any nested checkout (a folder between
// the root and the file holding a .git entry, such as a submodule). Names
// are compared by spelling and, for folders that exist, by identity, which
// catches every other spelling Windows accepts (case, trailing dots, GIT~1).
func (s *Local) writable(c string) error {
	parts := strings.Split(c, "/")
	for i, part := range parts {
		name := strings.ToLower(strings.TrimRight(part, ". "))
		if name == ".git" || i == 0 && name == ".dsheets" {
			return fmt.Errorf("%s: %w", c, ErrReadOnly)
		}
	}
	same := func(a, b string) bool {
		ai, err := os.Lstat(a)
		if err != nil {
			return false
		}
		bi, err := os.Lstat(b)
		return err == nil && os.SameFile(ai, bi)
	}
	dir := s.root
	for i, part := range parts {
		parent := dir
		dir = filepath.Join(dir, part)
		if same(dir, filepath.Join(parent, ".git")) || i == 0 && same(dir, filepath.Join(parent, ".dsheets")) {
			return fmt.Errorf("%s: %w", c, ErrReadOnly)
		}
		if i < len(parts)-1 {
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				return fmt.Errorf("%s: %w", c, ErrReadOnly)
			}
		}
	}
	return nil
}

func (s *Local) abs(c string) string { return filepath.Join(s.root, filepath.FromSlash(c)) }

// Load reads a file and its token.
func (s *Local) Load(p string) (File, error) {
	c, err := clean(p)
	if err != nil {
		return File{}, err
	}
	if err := s.confined(c); err != nil {
		return File{}, err
	}
	data, err := os.ReadFile(s.abs(c))
	if err != nil {
		return File{}, err
	}
	return File{Path: c, Data: data, Token: Token(data)}, nil
}

// Save checks every token, then writes every file through a temporary file
// in the same folder. If replacing one file fails, the files already
// replaced get their old content back.
func (s *Local) Save(ws ...Write) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make([]string, len(ws))
	olds := make([][]byte, len(ws)) // nil: the file did not exist
	var conflicts []string
	for i, w := range ws {
		c, err := clean(w.Path)
		if err != nil {
			return nil, err
		}
		if err := s.confined(c); err != nil {
			return nil, err
		}
		if err := s.writable(c); err != nil {
			return nil, err
		}
		for _, prev := range paths[:i] {
			if prev == c || sameFile(s.abs(prev), s.abs(c)) {
				return nil, fmt.Errorf("%s: written twice in one save", c)
			}
		}
		paths[i] = c
		cur, err := os.ReadFile(s.abs(c))
		tok := ""
		switch {
		case err == nil:
			tok, olds[i] = Token(cur), cur
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		if tok != w.Token {
			conflicts = append(conflicts, c)
		}
	}
	if len(conflicts) > 0 {
		return nil, &ConflictError{Paths: conflicts}
	}
	tmps := make([]string, len(ws))
	cleanup := func() {
		for _, t := range tmps {
			if t != "" {
				os.Remove(t)
			}
		}
	}
	for i, w := range ws {
		t, err := writeTemp(s.abs(paths[i]), w.Data)
		if err != nil {
			cleanup()
			return nil, err
		}
		tmps[i] = t
	}
	tokens := make([]string, len(ws))
	for i, w := range ws {
		if err := rename(tmps[i], s.abs(paths[i])); err != nil {
			cleanup()
			for j := i - 1; j >= 0; j-- {
				if olds[j] == nil {
					os.Remove(s.abs(paths[j]))
				} else if t, terr := writeTemp(s.abs(paths[j]), olds[j]); terr == nil {
					if rename(t, s.abs(paths[j])) != nil {
						os.Remove(t)
					}
				}
			}
			return nil, fmt.Errorf("%s: %w (no file was changed)", paths[i], err)
		}
		tmps[i] = ""
		tokens[i] = Token(w.Data)
	}
	return tokens, nil
}

// rename is os.Rename; tests replace it to make a write fail.
var rename = os.Rename

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// writeTemp writes data to a temporary file next to file, with the existing
// file's mode, and returns its name.
func writeTemp(file string, data []byte) (string, error) {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".dsheets-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, mode)
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// Committed reads the file from HEAD with git.
func (s *Local) Committed(p string) ([]byte, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	c = s.trueCase(c)
	cmd := exec.Command("git", "-C", s.root, "show", "HEAD:./"+c)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("%s: %w (%s)", c, ErrNotCommitted, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return out, nil
}

// trueCase spells c as the folders on disk do. git paths are case-sensitive
// while Windows and macOS file names are not, so a path the store loads and
// saves fine could otherwise look uncommitted.
func (s *Local) trueCase(c string) string {
	dir := s.root
	parts := strings.Split(c, "/")
	for i, part := range parts {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return c
		}
		match := ""
		for _, e := range entries {
			if e.Name() == part {
				match = part
				break
			}
			if match == "" && strings.EqualFold(e.Name(), part) {
				match = e.Name()
			}
		}
		if match == "" {
			return c
		}
		parts[i] = match
		dir = filepath.Join(dir, match)
	}
	return strings.Join(parts, "/")
}

// CommittedVersion reads the integer top-level `version` of the committed
// file. ok is false when the file is not committed or has no integer
// version.
func CommittedVersion(s Store, p string) (version int, ok bool, err error) {
	data, err := s.Committed(p)
	if errors.Is(err, ErrNotCommitted) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return 0, false, fmt.Errorf("%s in the last commit: %v", p, err)
	}
	v, ok := raw["version"].(int64)
	if !ok {
		return 0, false, nil
	}
	return int(v), true, nil
}
