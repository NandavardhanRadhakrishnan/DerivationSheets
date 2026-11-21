package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/agree"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/cases"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// sliceCases runs the PM's generated cases through each team's own sheet,
// end to end, prints where the teams differ, and returns the report.
func sliceCases(out io.Writer, a *flow.Analysis, fe *sources.Fetcher) *agree.Report {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	root := sources.RepoRoot(cwd)
	var teams agree.TeamFlows
	reg, err := sources.NewResolver(root, nil, fe).Registered()
	switch {
	case err != nil:
		fmt.Fprintf(out, "\nregistry: %v; team flows cannot be found\n", err)
	case reg == nil:
		fmt.Fprintln(out, "\nno registry: team flows cannot be found")
	default:
		teams = agree.RegistryTeams(reg, fe, sources.SelfAlias(reg, sources.OriginURL(root)))
	}
	opt := agree.Options{Mode: agree.EndToEnd}
	var why string
	if opt.Filled, why = cases.ReadFilled(a.SheetDir); why != "" {
		fmt.Fprintf(out, "\n%s; running without its filled values\n", why)
	}
	rep := agree.Run(a, teams, opt)
	printAgreement(out, rep)
	return rep
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
