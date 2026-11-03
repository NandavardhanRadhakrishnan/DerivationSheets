package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// consumerRepo makes a repository with a dsheets.toml and a flow folder,
// returning the flow folder.
func consumerRepo(t *testing.T, projectTOML string) string {
	t.Helper()
	needGit(t)
	root := t.TempDir()
	run(t, root, "init", "-q", "-b", "main")
	if projectTOML != "" {
		write(t, filepath.Join(root, ProjectFile), projectTOML)
	}
	flow := filepath.Join(root, "flows", "x")
	if err := os.MkdirAll(flow, 0o755); err != nil {
		t.Fatal(err)
	}
	return flow
}

// registryRepo makes a registry listing team as "shipping", with its sheets
// in the sheets folder.
func registryRepo(t *testing.T, team string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, ".gitmodules"), "[submodule \"shipping\"]\n\tpath = shipping\n\turl = "+fileURL(team)+"\n\tdsheets = sheets\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "registry")
	return dir
}

func quoteVia(t *testing.T, r *Resolver) string {
	t.Helper()
	path, _, err := r.Resolve("shipping:quote.toml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLoadProject(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ProjectFile), `registry = { git = "https://example.com/registry.git" }
[sources]
local = { path = "../elsewhere", dir = "sheets" }
pinned = { git = "https://example.com/x.git", ref = "v1" }
`)
	write(t, filepath.Join(root, ".gitmodules"), "[submodule \"shipping\"]\n\tpath = teams/shipping\n\turl = https://example.com/s.git\n")
	p, err := LoadProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Registry == nil || p.Registry.Git != "https://example.com/registry.git" {
		t.Errorf("registry = %+v", p.Registry)
	}
	if got := p.Sources["local"]; got.Path != filepath.Join(filepath.Dir(root), "elsewhere") || got.Dir != "sheets" {
		t.Errorf("local = %+v; want an absolute path", got)
	}
	if got := p.Sources["pinned"]; got.Ref != "v1" || got.Alias != "pinned" {
		t.Errorf("pinned = %+v", got)
	}
	if got := p.Submodules["shipping"]; got.Path != filepath.Join(root, "teams", "shipping") {
		t.Errorf("submodule = %+v", got)
	}
	empty, err := LoadProject(t.TempDir())
	if err != nil || empty.Registry != nil || len(empty.Sources) != 0 {
		t.Errorf("no file: %+v, %v", empty, err)
	}
}

func TestLoadProjectErrors(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ProjectFile), "registry = \"https://example.com/registry.git\"\n")
	if _, err := LoadProject(root); err == nil || !strings.Contains(err.Error(), "registry") {
		t.Errorf("error = %v", err)
	}
}

func TestResolveThroughRegistry(t *testing.T) {
	team := teamRepo(t)
	reg := registryRepo(t, team)
	flow := consumerRepo(t, "registry = { git = \""+fileURL(reg)+"\" }\n")
	r := NewResolver(flow, nil, NewFetcher(t.TempDir()))
	if q := quoteVia(t, r); !strings.Contains(q, "# v2") {
		t.Errorf("got %q; want main through the registry", q)
	}
	if rev := r.Fetched()["shipping"]; rev == "" {
		t.Error("no revision recorded")
	}
	entries, err := r.Registered()
	if err != nil || len(entries) != 1 || entries["shipping"].Dir != "sheets" {
		t.Errorf("Registered() = %v, %v", entries, err)
	}
}

func TestLookupOrder(t *testing.T) {
	team := teamRepo(t)
	reg := registryRepo(t, team)
	cache := t.TempDir()
	projectSrc := "[sources]\nshipping = { git = \"" + fileURL(team) + "\", ref = \"v1\", dir = \"sheets\" }\n"
	registry := "registry = { git = \"" + fileURL(reg) + "\" }\n"

	onlyRegistry := consumerRepo(t, registry)
	if q := quoteVia(t, NewResolver(onlyRegistry, nil, NewFetcher(cache))); !strings.Contains(q, "# v2") {
		t.Errorf("registry: %q", q)
	}
	both := consumerRepo(t, registry+projectSrc)
	if q := quoteVia(t, NewResolver(both, nil, NewFetcher(cache))); !strings.Contains(q, "# v1") {
		t.Errorf("project [sources] should win over the registry: %q", q)
	}
	local := t.TempDir()
	write(t, filepath.Join(local, "quote.toml"), "# flow\n")
	flowSrc := map[string]Source{"shipping": {Alias: "shipping", Path: local}}
	if q := quoteVia(t, NewResolver(both, flowSrc, NewFetcher(cache))); !strings.Contains(q, "# flow") {
		t.Errorf("flow [sources] should win: %q", q)
	}
}

func TestRegistryPathDot(t *testing.T) {
	team := teamRepo(t)
	reg := registryRepo(t, team)
	write(t, filepath.Join(reg, ProjectFile), "registry = { path = \".\" }\n")
	flow := filepath.Join(reg, "flows", "x")
	if err := os.MkdirAll(flow, 0o755); err != nil {
		t.Fatal(err)
	}
	if q := quoteVia(t, NewResolver(flow, nil, NewFetcher(t.TempDir()))); !strings.Contains(q, "# v2") {
		t.Errorf("got %q", q)
	}
}

// A relative submodule URL in a local registry resolves against the
// registry's origin remote, as git does, not against its folder.
func TestRegistryPathDotRelativeURL(t *testing.T) {
	team := teamRepo(t)
	parent := filepath.Dir(team)
	reg := filepath.Join(parent, "registry")
	run(t, parent, "init", "-q", "-b", "main", "registry")
	write(t, filepath.Join(reg, ".gitmodules"), "[submodule \"shipping\"]\n\tpath = shipping\n\turl = ../"+filepath.Base(team)+"\n\tdsheets = sheets\n")
	write(t, filepath.Join(reg, ProjectFile), "registry = { path = \".\" }\n")
	run(t, reg, "add", ".")
	run(t, reg, "commit", "-q", "-m", "registry")
	clone := filepath.Join(t.TempDir(), "registry")
	run(t, filepath.Dir(clone), "clone", "-q", fileURL(reg), clone)
	if err := os.MkdirAll(filepath.Join(clone, "flows", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if q := quoteVia(t, NewResolver(filepath.Join(clone, "flows", "x"), nil, NewFetcher(t.TempDir()))); !strings.Contains(q, "# v2") {
		t.Errorf("got %q", q)
	}
}

func TestNestedResolution(t *testing.T) {
	team := teamRepo(t)
	// The orders repository names shipping in its own dsheets.toml and has a
	// flow folder in its sheets.
	orders := t.TempDir()
	run(t, orders, "init", "-q", "-b", "main")
	write(t, filepath.Join(orders, ProjectFile), "[sources]\nshipping = { git = \""+fileURL(team)+"\", dir = \"sheets\" }\n")
	write(t, filepath.Join(orders, "sheets", "flows", "x", "flow.toml"), "flow = \"x\"\n")
	run(t, orders, "add", ".")
	run(t, orders, "commit", "-q", "-m", "orders")

	cache := t.TempDir()
	fetcher := NewFetcher(cache)
	consumer := consumerRepo(t, "[sources]\norders = { git = \""+fileURL(orders)+"\", dir = \"sheets\" }\n")
	flowDir, _, err := NewResolver(consumer, nil, fetcher).Resolve("orders:flows/x")
	if err != nil {
		t.Fatal(err)
	}
	if q := quoteVia(t, NewResolver(flowDir, nil, fetcher)); !strings.Contains(q, "# v2") {
		t.Errorf("nested alias: %q", q)
	}
	if _, err := os.Stat(filepath.Join(RepoRoot(flowDir), CacheDir)); err == nil {
		t.Error("a cache was created inside the fetched clone")
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 2 {
		t.Errorf("cache holds %d clones; want orders and shipping", len(entries))
	}
}

func TestPlainPathIntoSubmoduleWarns(t *testing.T) {
	flow := consumerRepo(t, "")
	root := RepoRoot(flow)
	write(t, filepath.Join(root, ".gitmodules"), "[submodule \"shipping\"]\n\tpath = shipping\n\turl = https://example.com/s.git\n\tdsheets = sheets\n")
	r := NewResolver(flow, nil, NewFetcher(t.TempDir()))
	for i := 0; i < 2; i++ {
		if _, _, err := r.Resolve("../../shipping/sheets/x.toml"); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "shipping:x.toml") {
		t.Errorf("warnings = %v; want one suggesting shipping:x.toml", r.Warnings)
	}
}

func TestUnknownAlias(t *testing.T) {
	r := NewResolver(consumerRepo(t, ""), nil, NewFetcher(t.TempDir()))
	_, _, err := r.Resolve("nope:x.toml")
	if err == nil || !strings.Contains(err.Error(), "[sources]") || !strings.Contains(err.Error(), "registry") {
		t.Errorf("error = %v", err)
	}
}
