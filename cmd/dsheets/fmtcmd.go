package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// errNotFormatted marks `fmt --check` finding files that are not in
// canonical layout (exit code 1). The files have already been listed.
var errNotFormatted = fmt.Errorf("files are not formatted")

func cmdFmt(args []string, out, errw io.Writer) error {
	fs := newFlags("fmt", "[--check] [path...]", errw)
	check := fs.Bool("check", false, "write nothing; list files that are not in canonical layout and exit 1 if there are any")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		pos = []string{"."}
	}
	files, err := fmtTargets(pos)
	if err != nil {
		return err
	}
	var bad []string
	changed := 0
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		formatted, err := fmtFile(p, data)
		if err != nil {
			bad = append(bad, p)
			fmt.Fprintf(errw, "%s: %v\n", p, err)
			continue
		}
		if bytes.Equal(formatted, data) {
			continue
		}
		fmt.Fprintln(out, p)
		changed++
		if !*check {
			if err := os.WriteFile(p, formatted, 0o644); err != nil {
				return err
			}
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%d file(s) could not be parsed", len(bad))
	}
	if *check && changed > 0 {
		return errNotFormatted
	}
	return nil
}

// fmtLayout picks the layout for a file, or nil when dsheets does not
// format it. cases.toml is generated and starts with a "sheet" key like a
// sheet does, so files are told apart by name first.
func fmtLayout(path string, data []byte) *tomledit.Layout {
	switch filepath.Base(path) {
	case "sheet.toml":
		return tomledit.Sheet
	case "flow.toml":
		return tomledit.Flow
	case "cases.toml":
		return nil
	}
	if l := tomledit.LayoutFor(data); l == tomledit.Interface {
		return l
	}
	return nil
}

// fmtFile returns the file in canonical layout. Files dsheets does not
// format come back unchanged.
func fmtFile(path string, data []byte) ([]byte, error) {
	l := fmtLayout(path, data)
	if l == nil {
		return data, nil
	}
	doc, err := tomledit.Parse(data, l)
	if err != nil {
		return nil, err
	}
	return doc.Format()
}

// fmtTargets expands files and folders to the .toml files to look at.
// Folders are walked, skipping hidden folders (.git, .dsheets) and
// node_modules.
func fmtTargets(paths []string) ([]string, error) {
	var files []string
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, root)
			continue
		}
		err = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if p != root && (strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(e.Name(), ".toml") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}
