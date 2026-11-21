package agree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sheet"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

// teamDirs maps a team to its own flow folder in the shop fixture.
var teamDirs = map[string]string{
	"Cart team":     "cart/flows/cart",
	"Orders team":   "orders/flows/orders",
	"Shipping team": "shipping/flows/ship",
}

func analyse(t *testing.T, dir string) *flow.Analysis {
	t.Helper()
	f, err := flow.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))
}

// shop copies testdata/shop to a temp folder, replaces whole files by
// relative path from patch, analyses the PM flow and serves the teams' flows.
func shop(t *testing.T, patch map[string]string) (*flow.Analysis, TeamFlows) {
	t.Helper()
	src := filepath.Join("testdata", "shop")
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, text := range patch {
		p := filepath.Join(dst, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("patch of a file the fixture lacks: %s", rel)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pm := analyse(t, filepath.Join(dst, "product", "flows", "shop"))
	if len(pm.Errors) > 0 {
		t.Fatalf("PM flow: %v", pm.Errors)
	}
	teams := func(team string) ([]*flow.Analysis, []string) {
		d, ok := teamDirs[team]
		if !ok {
			return nil, []string{"no flows for " + team}
		}
		return []*flow.Analysis{analyse(t, filepath.Join(dst, filepath.FromSlash(d)))}, nil
	}
	return pm, teams
}

// pmCaseWith returns the first generated case whose input rows have the
// given values: pairs of row id and value.
func pmCaseWith(t *testing.T, pm *flow.Analysis, kv ...any) pmCase {
	t.Helper()
	for _, c := range pmCases(pm, Options{}) {
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if !sheet.Equal(c.Res.Values[kv[i].(string)], kv[i+1]) {
				ok = false
			}
		}
		if ok {
			return c
		}
	}
	t.Fatalf("no generated case with %v", kv)
	return pmCase{}
}

// boundary returns the first boundary with the role.
func boundary(pm *flow.Analysis, role string) *flow.Boundary {
	for _, b := range pm.Boundaries {
		if b.Role == role {
			return b
		}
	}
	return nil
}

// replaceIn returns the fixture file's text with old replaced by new.
func replaceIn(t *testing.T, rel, old, new string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "shop", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, old) {
		t.Fatalf("%s has no %q", rel, old)
	}
	return strings.Replace(s, old, new, 1)
}
