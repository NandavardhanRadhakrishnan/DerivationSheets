package main

import (
	"io"
	"os"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/diff"
)

func cmdDiff(args []string, out, errw io.Writer) error {
	fs := newFlags("diff", "[base]..[head] [--all] [--cases] [--json]", errw)
	all := fs.Bool("all", false, "also show label and formatting changes (names, descriptions, comments)")
	cases := fs.Bool("cases", false, "also compare the generated cases.toml files")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		fs.Usage()
		return errUsage
	}
	var opt diff.Options
	if len(pos) == 1 {
		if strings.Contains(pos[0], "...") {
			fs.Usage()
			return errUsage
		}
		if base, head, ok := strings.Cut(pos[0], ".."); ok {
			opt.Base, opt.Head = base, head
		} else {
			opt.Base = pos[0]
		}
	}
	if opt.Dir, err = os.Getwd(); err != nil {
		return err
	}
	opt.Cases = *cases
	r, err := diff.Run(opt)
	if err != nil {
		return err
	}
	if *asJSON {
		return r.WriteJSON(out)
	}
	return r.WriteMarkdown(out, *all)
}
