package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/agree"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// sliceCases runs the PM's generated cases through each team's own sheet,
// end to end, prints where the teams differ, and returns the report.
func sliceCases(out io.Writer, a *flow.Analysis, fe *sources.Fetcher) (*agree.Report, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root := sources.RepoRoot(cwd)
	reg, err := sources.NewResolver(root, nil, fe).Registered()
	if err != nil {
		return nil, err
	}
	var teams agree.TeamFlows
	if reg == nil {
		fmt.Fprintln(out, "\nno registry: team flows cannot be found")
	} else {
		teams = agree.RegistryTeams(reg, fe, selfAlias(root, reg))
	}
	opt := agree.Options{Mode: agree.EndToEnd}
	if p := cases.Path(a.SheetDir); fileExists(p) {
		if opt.Filled, err = cases.Read(p); err != nil {
			return nil, err
		}
	}
	rep := agree.Run(a, teams, opt)
	printAgreement(out, rep)
	return rep, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// selfAlias is this repository's alias in the registry, "" when it has none.
func selfAlias(root string, reg map[string]sources.Source) string {
	o, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	origin := strings.TrimSpace(string(o))
	if err != nil || origin == "" {
		return ""
	}
	best := ""
	for alias, src := range reg {
		if sources.SameRepo(src.Git, origin) && (best == "" || alias < best) {
			best = alias
		}
	}
	return best
}

// printAgreement prints one line per team, then the checks that differ or
// cannot be told, each note once per team.
func printAgreement(out io.Writer, r *agree.Report) {
	fmt.Fprintf(out, "\nCases (%d, end to end):\n", r.Cases)
	var order []string
	byTeam := map[string][]agree.Check{}
	for _, c := range r.Checks {
		if _, ok := byTeam[c.Team]; !ok {
			order = append(order, c.Team)
		}
		byTeam[c.Team] = append(byTeam[c.Team], c)
	}
	width := 0
	for _, t := range order {
		width = max(width, len(t))
	}
	for _, t := range order {
		var ifaces []string
		n := map[string]int{}
		for _, c := range byTeam[t] {
			n[c.Status]++
			if c.Iface != "" && !slices.Contains(ifaces, c.Iface) {
				ifaces = append(ifaces, c.Iface)
			}
		}
		var counts []string
		if n[agree.Agrees] > 0 {
			counts = append(counts, fmt.Sprintf("%d agree", n[agree.Agrees]))
		}
		if n[agree.Differs] > 0 {
			counts = append(counts, fmt.Sprintf("%d differs", n[agree.Differs]))
		}
		if n[agree.CannotTell] > 0 {
			counts = append(counts, fmt.Sprintf("%d cannot tell", n[agree.CannotTell]))
		}
		fmt.Fprintf(out, "  %-*s  %s  %s\n", width, t, strings.Join(ifaces, ", "), strings.Join(counts, ", "))
		told := map[string]bool{}
		for _, c := range byTeam[t] {
			switch c.Status {
			case agree.Differs:
				fmt.Fprintf(out, "    case %s  %s  expected %s, got %s\n", c.Case, c.Field, shown(c.Expected), shown(c.Actual))
			case agree.CannotTell:
				if c.Note == "" || told[c.Note] {
					fmt.Fprintf(out, "    case %s  %s  cannot tell\n", c.Case, c.Field)
					continue
				}
				told[c.Note] = true
				fmt.Fprintf(out, "    case %s  %s  cannot tell: %s\n", c.Case, c.Field, c.Note)
			}
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
}

func shown(v any) string {
	if v == nil {
		return "nothing"
	}
	return fmt.Sprint(v)
}
