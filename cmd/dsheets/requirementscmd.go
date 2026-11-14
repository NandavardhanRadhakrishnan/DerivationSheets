package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/requirements"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

func cmdRequirements(args []string, out, errw io.Writer) error {
	fs := newFlags("requirements", "<flow> [base]..[head] [--all] [--json] [--out dir] [--as alias] [--offline] [--apply [--clone alias=path] [--branch alias=name] [--change label] [--no-push]]", errw)
	all := fs.Bool("all", false, "also include label and formatting changes (names, descriptions, comments)")
	asJSON := fs.Bool("json", false, "print the requirements as JSON")
	outDir := fs.String("out", "", "also write one markdown and one JSON file per team into this folder")
	as := fs.String("as", "", "this repository's alias in the registry (default: found from the origin remote)")
	offline := fs.Bool("offline", false, "do not fetch; use what is already fetched")
	apply := fs.Bool("apply", false, "write each owner's interface edits to a new branch in a clone of its repository, commit and push it")
	noPush := fs.Bool("no-push", false, "with --apply, stop after the local commit")
	change := fs.String("change", "", "with --apply, the label of the change in commits and the record file (default: the flow's name)")
	var clones, branches multiFlag
	fs.Var(&clones, "clone", "with --apply, use this existing clone for a repository, as alias=path; repeat for more")
	fs.Var(&branches, "branch", "with --apply, name a repository's new branch, as alias=name; repeat for more (default dsheets/<flow>)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		return errUsage
	}
	if !*apply && (*noPush || *change != "" || len(clones) > 0 || len(branches) > 0) {
		fmt.Fprintln(errw, "--no-push, --change, --clone and --branch only apply with --apply")
		fs.Usage()
		return errUsage
	}
	cloneMap, err := aliasPairs(clones)
	if err != nil {
		fmt.Fprintln(errw, "--clone:", err)
		return errUsage
	}
	branchMap, err := aliasPairs(branches)
	if err != nil {
		fmt.Fprintln(errw, "--branch:", err)
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
		if err := res.WriteFolder(*outDir); err != nil {
			return err
		}
	}
	if !*apply {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := sources.RepoRoot(cwd)
	reg, err := sources.NewResolver(root, nil, opt.Fetcher).Registered()
	if err != nil {
		return err
	}
	applied, err := requirements.Apply(res, requirements.ApplyOptions{Registry: reg, Clones: cloneMap, Branches: branchMap,
		Change: *change, NoPush: *noPush, Work: filepath.Join(root, ".dsheets", "work")})
	report := out
	if *asJSON {
		report = errw // keep the JSON on stdout parseable
	}
	for _, a := range applied {
		where := "pushed"
		if !a.Pushed {
			where = "not pushed"
		}
		fmt.Fprintf(report, "%s: branch %s in %s (%s)\n\n%s\n", a.Repo, a.Branch, a.Dir, where, a.Description)
	}
	return err
}

// aliasPairs reads repeated alias=value flags.
func aliasPairs(vals []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vals {
		alias, val, ok := strings.Cut(v, "=")
		if !ok || alias == "" || val == "" {
			return nil, fmt.Errorf("%q is not alias=value", v)
		}
		out[alias] = val
	}
	return out, nil
}
