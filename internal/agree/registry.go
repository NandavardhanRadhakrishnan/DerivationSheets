package agree

import (
	"fmt"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// RegistryTeams serves the team flows of every repository the registry
// lists for a team (sources.TeamAliases), fetched on the branch the registry
// follows, analysed with flow.AnalyzeWith, reading unsaved sheets and flows from ov; self is skipped.
func RegistryTeams(reg map[string]sources.Source, fe *sources.Fetcher, self string, ov flow.Overlay) TeamFlows {
	type repo struct {
		flows []*flow.Analysis
		notes []string
	}
	cache := map[string]*repo{}
	read := func(alias string) *repo {
		if r := cache[alias]; r != nil {
			return r
		}
		r := &repo{}
		cache[alias] = r
		root, _, err := fe.Fetch(reg[alias])
		if err != nil {
			r.notes = append(r.notes, fmt.Sprintf("could not read %s: %v", alias, err))
			return r
		}
		for _, dir := range flow.FindDirs(root) {
			f, err := flow.LoadWith(dir, ov)
			if err != nil {
				r.notes = append(r.notes, fmt.Sprintf("%s: flow %s left out: %v", alias, dir, err))
				continue
			}
			a := flow.AnalyzeWith(f, sources.NewResolver(f.Dir, f.Sources, fe), ov)
			if len(a.Errors) > 0 {
				r.notes = append(r.notes, fmt.Sprintf("%s: flow %s left out: %s", alias, f.Slug, a.Errors[0].Message))
				continue
			}
			r.flows = append(r.flows, a)
		}
		return r
	}
	return func(team string) ([]*flow.Analysis, []string) {
		aliases := sources.TeamAliases(team, reg)
		if len(aliases) == 0 {
			return nil, []string{fmt.Sprintf("no repository for %s in the registry (name it with team = %q in .gitmodules)", team, team)}
		}
		var flows []*flow.Analysis
		var notes []string
		for _, alias := range aliases {
			if alias == self {
				notes = append(notes, fmt.Sprintf("%s is this repository: its flows are not read from the registry", alias))
				continue
			}
			r := read(alias)
			flows = append(flows, r.flows...)
			notes = append(notes, r.notes...)
		}
		return flows, notes
	}
}
