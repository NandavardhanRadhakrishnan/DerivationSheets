package main

import (
	"io"
	"os"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/requirements"
)

func cmdRequirements(args []string, out, errw io.Writer) error {
	fs := newFlags("requirements", "<flow> [base]..[head] [--all] [--json] [--out dir] [--as alias] [--offline]", errw)
	all := fs.Bool("all", false, "also include label and formatting changes (names, descriptions, comments)")
	asJSON := fs.Bool("json", false, "print the requirements as JSON")
	outDir := fs.String("out", "", "also write one markdown and one JSON file per team into this folder")
	as := fs.String("as", "", "this repository's alias in the registry (default: found from the origin remote)")
	offline := fs.Bool("offline", false, "do not fetch; use what is already fetched")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		return errUsage
	}
	opt := requirements.Options{As: *as, All: *all, Fetcher: newFetcher(*offline)}
	if len(pos) == 2 {
		if strings.Contains(pos[1], "...") {
			fs.Usage()
			return errUsage
		}
		if base, head, ok := strings.Cut(pos[1], ".."); ok {
			opt.Base, opt.Head = base, head
		} else {
			opt.Base = pos[1]
		}
	}
	if opt.Flow, err = argPath(pos[0], opt.Fetcher); err != nil {
		return err
	}
	if opt.Dir, err = os.Getwd(); err != nil {
		return err
	}
	res, err := requirements.Run(opt)
	if err != nil {
		return err
	}
	if *asJSON {
		err = res.WriteJSON(out)
	} else {
		err = res.WriteMarkdown(out)
	}
	if err != nil {
		return err
	}
	if *outDir != "" {
		return res.WriteFolder(*outDir)
	}
	return nil
}
