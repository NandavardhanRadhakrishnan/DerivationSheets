package main

import (
	"io"
	"os"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/diff"
)

func cmdDiff(args []string, out, errw io.Writer) error {
	fs := newFlags("diff", "[base]..[head] [--all] [--cases] [--json] [--color auto|always|never]", errw)
	all := fs.Bool("all", false, "also show label and formatting changes (names, descriptions, comments)")
	cases := fs.Bool("cases", false, "also compare the generated cases.toml files")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	color := fs.String("color", "auto", "color the output: auto (on a terminal), always or never")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *color != "auto" && *color != "always" && *color != "never" {
		fs.Usage()
		return errUsage
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
	// On a terminal the changes print as `git diff` does, red and green when
	// color is on. Anywhere else (a pipe, a file, a CI step) they print as
	// markdown with fenced diff blocks, which pull-request pages color.
	term := isTerminal(out)
	useColor := *color == "always" || (*color == "auto" && term && os.Getenv("NO_COLOR") == "")
	if f, ok := out.(*os.File); ok && term && useColor && !enableColor(f) && *color == "auto" {
		useColor = false
	}
	if term || useColor {
		return r.WriteText(out, *all, useColor)
	}
	return r.WriteMarkdown(out, *all)
}

// isTerminal reports whether w is a console or terminal window.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
