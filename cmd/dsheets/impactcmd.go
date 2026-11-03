package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/impact"
)

// errIncomplete marks an impact check that found nothing new but could not
// check every consumer (exit code 3). Details have already been printed.
var errIncomplete = errors.New("incomplete")

func cmdImpact(args []string, out, errw io.Writer) error {
	fs := newFlags("impact", "[--as alias] [--base branch] [--json] [--out impact.html]", errw)
	as := fs.String("as", "", "this repository's alias in the registry (default: found from the origin remote)")
	base := fs.String("base", "", "branch to compare with (default: the branch the registry follows)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	outFile := fs.String("out", "", "also write the report, with each affected flow drawn, to this HTML file")
	offline := fs.Bool("offline", false, "do not fetch; use what is already fetched")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		fs.Usage()
		return errUsage
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	r, err := impact.Run(impact.Options{Root: cwd, As: *as, Base: *base, Fetcher: newFetcher(*offline)})
	if err != nil {
		return err
	}
	if *asJSON {
		err = r.WriteJSON(out)
	} else {
		err = r.WriteText(out)
	}
	if err != nil {
		return err
	}
	if *outFile != "" {
		var buf bytes.Buffer
		if err := r.WriteHTML(&buf); err != nil {
			return err
		}
		if err := os.WriteFile(*outFile, buf.Bytes(), 0o644); err != nil {
			return err
		}
		if !*asJSON {
			fmt.Fprintf(out, "wrote %s\n", *outFile)
		}
	}
	switch r.ExitCode() {
	case 1:
		return errFailed
	case 3:
		return errIncomplete
	}
	return nil
}
