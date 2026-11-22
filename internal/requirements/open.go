package requirements

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// ParseChangeRef splits alias:path@branch: the PM's repository by its registry
// alias, the flow's folder in it, and the PM's branch with the change.
func ParseChangeRef(ref string) (alias, path, branch string, err error) {
	alias, rest, ok := strings.Cut(ref, ":")
	if !ok || alias == "" {
		return "", "", "", errors.New("--flow names the PM's repository by its registry alias: alias:path@branch")
	}
	i := strings.LastIndex(rest, "@")
	if i < 0 || rest[i+1:] == "" {
		return "", "", "", errors.New("--flow needs @<branch>, the PM's branch with the change")
	}
	path, branch = strings.Trim(rest[:i], "/"), rest[i+1:]
	if path == "" {
		return "", "", "", errors.New("--flow names the PM's repository by its registry alias: alias:path@branch")
	}
	return alias, path, branch, nil
}

// OpenChange reads the PM's change named by ref (alias:path@branch) from the
// owner's repository top: it finds the PM's repository in top's registry,
// fetches the branch with its history and prepares the comparison. Top's
// working tree stands in for its repository, so unsaved drafts in it can be
// judged through an overlay.
func OpenChange(top, ref string, fe *sources.Fetcher) (*Prepared, error) {
	alias, path, branch, err := ParseChangeRef(ref)
	if err != nil {
		return nil, err
	}
	if fe == nil {
		fe = sources.NewFetcher(filepath.Join(top, sources.CacheDir))
	}
	src, err := sources.NewResolver(top, nil, fe).Lookup(alias)
	if err != nil {
		return nil, err
	}
	ch, err := fe.FetchChange(src, branch)
	if err != nil {
		return nil, err
	}
	if origin := sources.OriginURL(top); origin != "" {
		fe = fe.WithSubstitute(&sources.Substitute{URL: origin, Root: top})
	}
	return Prepare(Options{
		Flow:    filepath.Join(ch.Root, filepath.FromSlash(path)),
		Dir:     ch.Root,
		Base:    ch.Base,
		Head:    ch.Head,
		As:      alias,
		Branch:  branch,
		Fetcher: fe,
	})
}

// TeamsOf is the PM flow's step teams that own the repository top, in step
// order: those whose registry repositories include top's. When there are none,
// why says why.
func (p *Prepared) TeamsOf(top string) (teams []string, why string) {
	origin := sources.OriginURL(top)
	if origin == "" {
		return nil, "this repository has no origin remote, so it cannot be found in the registry"
	}
	alias := sources.SelfAlias(p.reg, origin)
	if alias == "" {
		return nil, fmt.Sprintf("this repository (%s) is not in the registry", origin)
	}
	for _, st := range p.headA.Steps {
		team := st.Team()
		if team == "" || slices.Contains(teams, team) {
			continue
		}
		if slices.Contains(sources.TeamAliases(team, p.reg), alias) {
			teams = append(teams, team)
		}
	}
	if len(teams) == 0 {
		return nil, fmt.Sprintf("this repository is %q in the registry, but in no team of this flow", alias)
	}
	return teams, ""
}
