# Cross-repository references and impact checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let any dsheets reference point into another team's repository through a registry of git submodules, follow branches by default, fetch only each team's sheets folder, and add `dsheets impact` so a producing team sees, in its own pull request, which other teams' flows its change breaks.

**Architecture:** `internal/sources` grows a shared `Fetcher` (one cache, shallow and sparse fetches, fetch-once state, optional substitution of one repository by a local folder), a `Project` (the repository-root `dsheets.toml`: `registry` and `[sources]`), and a `.gitmodules` parser. The `Resolver` looks aliases up in flow `[sources]`, then project `[sources]`, then the registry. Flows resolve their end-to-end `sheet` through the resolver; the CLI resolves `alias:path` arguments the same way. A new `internal/impact` package finds consuming flows across registered repositories and diffs their findings before and after the change.

**Tech Stack:** Go 1.27, git CLI (via `os/exec`, as today), `github.com/pelletier/go-toml/v2`.

**Spec:** `docs/superpowers/specs/2026-11-03-cross-repo-references-design.md`

## Global Constraints

- dsheets never writes to another team's repository; all git work happens in `.dsheets/sources/` of the repository where the command runs.
- No full clones: every git fetch is shallow (`--depth 1`) and, when the source has a folder, sparse (cone mode, so root files such as `dsheets.toml` and `.gitmodules` are still present).
- Alias lookup order: flow `[sources]`, then repository-root `dsheets.toml` `[sources]`, then the registry. First match wins.
- `ref` is optional; empty means the remote's default branch. A ref is *followed* when it is empty or names a branch on the remote (`git ls-remote --heads origin <ref>` non-empty); otherwise it is *pinned*.
- A followed source is fetched once per `Fetcher` lifetime (serve: again when older than one minute); a pinned one only when missing from the cache. `--offline` never touches the network.
- `registry` and `[sources]` are read only from the `dsheets.toml` at the repository root; `[display]` keeps the nearest-file rule.
- Registry: the submodule name is the alias; `url` is the clone URL (relative URLs resolve against the registry's own URL); `branch` is the followed branch (`.` or absent means default); the custom key `dsheets` is the folder. The recorded submodule commit is ignored.
- `impact` exit codes: 0 nothing new and all checked; 1 something new breaks (wins over 3); 2 usage; 3 nothing new but some consumers not checked.
- Existing behaviour without a registry must keep working: every current test passes unchanged except for the mechanical `NewResolver` signature update and the "needs ref" error, which is removed.
- Copy follows the repo's rules: plain neutral words ("team", "target"), no insurance jargon.

## Review Focus

1. **A submodule `url` written relative** (`../shipping-service.git`), which git allows → it resolves against the registry's URL, not the working directory. Test in Task 2.
2. **The same repository spelled two ways** (`git@github.com:example/shipping.git` vs `https://github.com/example/shipping`) → `impact` still recognises it as this repository, and a flow `[sources]` entry with the other spelling is still a consumer. Test in Task 1 (`SameRepo`) and Task 6.
3. **Error messages that contain local paths** (cache folder vs working tree) differ between the before and after runs → an unchanged error must not show up as both "new" and "resolved". Test in Task 6.
4. **A consumer flow that pins this repository** (`ref = "v3.2.0"`) → it is listed as pinned and is never checked against the working tree. Test in Task 6.
5. **A flow loaded from another repository that itself uses aliases** (its own `dsheets.toml` registry, or `registry = { path = "." }` in the registry repo) → its references resolve with its own repository's configuration while clones still land in the invoking repository's cache. Test in Task 3.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/sources/sources.go` (modify) | `Source` (adds `Dir`), `Split`, `Resolver` (lookup order, registry, warnings, revisions) |
| `internal/sources/fetch.go` (create) | `Fetcher`: cache, shallow/sparse git, followed vs pinned, fetch-once state, substitution |
| `internal/sources/project.go` (create) | `Project`: repository-root `dsheets.toml` (`registry`, `[sources]`) and the repository's own submodule paths |
| `internal/sources/registry.go` (create) | `ParseGitmodules`, `SameRepo`, relative-URL resolution |
| `internal/sources/testutil_test.go` (create) | git fixture helpers shared by the sources tests |
| `internal/sheet/display.go` (modify) | accept `registry` and `sources` keys in `dsheets.toml` |
| `internal/flow/flow.go` (modify) | `Flow.SheetRef` replaces `SheetDir`; `dir` in `[sources]`; `Flow.Aliases()` |
| `internal/flow/analyze.go` (modify) | resolve `SheetRef` through the resolver; carry resolver warnings |
| `internal/flow/export.go` (modify) | write `dir`, omit empty `ref` |
| `schema/flow.schema.json` (modify) | `dir`, optional `ref`, `alias:path` for `sheet` |
| `cmd/dsheets/flowcmd.go` (modify) | one `Fetcher` per command; `alias:path` arguments; `fetch` covers registry aliases |
| `cmd/dsheets/main.go` (modify) | `serve` keeps one fetcher with a one-minute refresh; register `impact`; exit code 3 |
| `internal/impact/impact.go` (create) | find consumers, run before/after, diff findings, `Report` |
| `internal/impact/report.go` (create) | text, JSON and HTML output of a `Report` |
| `cmd/dsheets/impactcmd.go` (create) | the `impact` command |
| `docs/repositories.md` (create), `docs/flows.md`, `docs/format.md`, `README.md`, `BACKLOG.md` (modify) | documentation and CI examples |

---

### Task 1: Fetcher — shallow, sparse, followed or pinned, fetched once

Deviation from the spec, noted: the spec says `slice` and `validate` warn on plain paths into submodules. Sheets hold no cross-repository references, so only flow commands (`slice`, `render`, `serve`) warn.

**Files:**
- Create: `internal/sources/fetch.go`, `internal/sources/registry.go` (`SameRepo` only), `internal/sources/testutil_test.go`, `internal/sources/fetch_test.go`, `internal/sources/registry_test.go`
- Modify: `internal/sources/sources.go` (move `git()` and the cache logic out of `Resolver.fetch`; add `Dir` to `Source`)

**Interfaces:**
- Produces:
  ```go
  type Source struct {
      Alias string
      Git   string // clone URL
      Ref   string // branch, tag or commit; "" = the remote's default branch
      Dir   string // folder inside the repository the alias means; "" = root
      Path  string // local folder (absolute once loaded)
  }

  // Substitute stands in a local folder for one repository, for impact.
  type Substitute struct {
      URL  string // compared with SameRepo
      Root string // the repository root on disk
  }

  type Fetcher struct {
      Cache   string
      Offline bool
      MaxAge  time.Duration // 0: a followed source is fetched once per Fetcher; >0: again when older
      Sub     *Substitute
      // unexported shared state: per cache key {dir, rev, followed, fetchedAt}, guarded by a mutex;
      // pinned hits under substitution
  }
  func NewFetcher(cache string) *Fetcher
  // Fetch returns the folder the source's alias means (clone root joined with Dir) and the short commit.
  func (f *Fetcher) Fetch(src Source) (root, rev string, err error)
  // WithSubstitute returns a fetcher sharing f's cache and fetch-once state, with s in place.
  func (f *Fetcher) WithSubstitute(s *Substitute) *Fetcher
  // PinnedHits lists sources matching Sub that were pinned, so were fetched normally.
  func (f *Fetcher) PinnedHits() []Source
  ```
  ```go
  // SameRepo reports whether two clone URLs name the same repository (in registry.go).
  func SameRepo(a, b string) bool
  ```

- [ ] **Step 1: Move the git fixture helpers into `testutil_test.go`**

`run(t, dir, args...)` as today, and `teamRepo(t)` extended to produce a repository with `dsheets.toml` at the root, `sheets/quote.toml` and `service/main.go`, a tag `v1` (`# v1` in `sheets/quote.toml`), then a second commit on the default branch `main` (`# v2`). Return the repository folder; tests use `"file://" + filepath.ToSlash(dir)` as the URL so `--depth` works.

- [ ] **Step 2: Write the failing tests in `fetch_test.go`**

```go
func TestFetchFollowsDefaultBranch(t *testing.T)   // Ref "" → file has "# v2"; rev non-empty
func TestFetchPinnedTag(t *testing.T)              // Ref "v1" → "# v1"
func TestFetchSparseDir(t *testing.T)              // Dir "sheets": root ends in "sheets"; quote.toml present;
                                                   // <clone>/service/main.go absent; <clone>/dsheets.toml present
func TestFetchShallow(t *testing.T)                // `git rev-list --count HEAD` in the clone == "1"
func TestFetchOncePerFetcher(t *testing.T)         // commit "# v3" upstream after first Fetch; second Fetch on same
                                                   // Fetcher still "# v2"; a NewFetcher on the same cache sees "# v3"
func TestFetchMaxAge(t *testing.T)                 // MaxAge 1ns: second Fetch sees "# v3"
func TestFetchPinnedNotRefetched(t *testing.T)     // Ref "v1", fetch, delete the upstream repo, NewFetcher.Fetch still ok
func TestFetchOffline(t *testing.T)                // empty cache + Offline → error containing "offline"
func TestSubstitute(t *testing.T)                  // Sub{URL: same repo, Root: tmp}: followed source → tmp/sheets, rev "working tree";
                                                   // Ref "v1" → normal fetch, PinnedHits() has one entry
func TestSameRepo(t *testing.T) {                  // in registry_test.go
    // true:  "https://github.com/example/shipping.git" vs "git@github.com:example/shipping"
    // true:  "https://GitHub.com/example/shipping/"    vs "ssh://git@github.com/example/shipping.git"
    // true:  "file:///D:/x/repo" vs "file://D:/x/repo/"  (Windows test fixtures)
    // false: "https://github.com/example/shipping.git" vs "https://github.com/example/shipping-service.git"
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/sources/ -run 'TestFetch|TestSubstitute|TestSameRepo' -v`
Expected: FAIL (undefined: NewFetcher)

- [ ] **Step 4: Implement `fetch.go`**

`SameRepo` normalises: lowercase host, `git@host:path` → `host/path`, drop scheme, user info, trailing `/` and `.git`; for `file://` compare cleaned local paths case-insensitively on Windows. Cache folder per source: `<Cache>/<alias>-<first 10 hex of sha1(Git + "@" + Ref + "#" + Dir)>`. First fetch: `git init -q`, `git remote add origin <url>`, `git sparse-checkout set --cone <Dir>` when `Dir != ""`, then decide followed/pinned with `git ls-remote --heads origin <Ref>` (Ref empty → followed), store the answer in `<clone>/.git/dsheets-ref` so later runs and `--offline` know it. Fetch: `git fetch --quiet --depth 1 --filter=blob:none origin <Ref or HEAD>` then `git -c advice.detachedHead=false checkout --quiet FETCH_HEAD`. A pinned full commit hash that the server refuses by hash falls back to fetching tags and branches at depth 1 and checking out the hash; if that fails, report the git error. Short rev: `git rev-parse --short HEAD`. Substitution happens before any of this: a followed source with `SameRepo(src.Git, Sub.URL)` returns `filepath.Join(Sub.Root, Dir)` and rev `"working tree"`; deciding followed needs the ls-remote answer, which is cached as above.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/sources/ -v`
Expected: PASS (the old `TestSourceErrors` "needs ref" assertion is deleted in Task 3; delete it now if it blocks)

- [ ] **Step 6: Commit**

```bash
git add internal/sources
git commit -m "Fetch git sources shallow and sparse, following branches by default"
```

---

### Task 2: Registry parsing

**Files:**
- Modify: `internal/sources/registry.go`, `internal/sources/registry_test.go`

**Interfaces:**
- Produces:
  ```go
  // ParseGitmodules reads a .gitmodules file. base is the registry's own clone
  // URL (or folder), for relative submodule URLs.
  func ParseGitmodules(text, base string) (map[string]Source, error)
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestParseGitmodules(t *testing.T) {
    text := `[submodule "shipping"]
    path = shipping
    url = https://github.com/example/shipping-service.git
    branch = main
    dsheets = sheets
[submodule "orders"]
	path = teams/orders
	url = ../orders-sheets.git
	branch = .
`
    got, err := ParseGitmodules(text, "https://github.com/example/dsheets-registry.git")
    // got["shipping"] == Source{Alias: "shipping", Git: "https://github.com/example/shipping-service.git", Ref: "main", Dir: "sheets"}
    // got["orders"]   == Source{Alias: "orders",   Git: "https://github.com/example/orders-sheets.git",   Ref: "",     Dir: ""}
}
func TestParseGitmodulesErrors(t *testing.T) // a section without url → error naming the submodule
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/sources/ -run 'Gitmodules' -v`
Expected: FAIL (undefined)

- [ ] **Step 3: Implement `registry.go`**

`.gitmodules` is git-config syntax: `[submodule "name"]` sections, `key = value` lines, `#`/`;` comments, keys case-insensitive. Parse it directly (no `git config` subprocess). Relative URLs (`./`, `../`) resolve against `base` with path semantics on the URL path.

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/sources/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sources
git commit -m "Parse a registry's .gitmodules"
```

---

### Task 3: Project file and resolver lookup order

**Files:**
- Create: `internal/sources/project.go`, `internal/sources/project_test.go`
- Modify: `internal/sources/sources.go`, `internal/sources/sources_test.go`, `internal/sheet/display.go`, `internal/sheet/display_test.go`

**Interfaces:**
- Consumes: `Fetcher`, `Source` (Task 1); `ParseGitmodules` (Task 2).
- Produces:
  ```go
  const ProjectFile = "dsheets.toml"

  type Project struct {
      Root       string             // repository root
      Registry   *Source            // nil without one; Path absolute when local (path = "." → Root)
      Sources    map[string]Source  // Path made absolute against Root
      Submodules []string           // absolute paths of this repository's own submodules, from its .gitmodules
  }
  // LoadProject reads <root>/dsheets.toml (absent → empty Project, nil error) and <root>/.gitmodules.
  func LoadProject(root string) (*Project, error)
  // RepoRoot is the nearest folder at or above dir holding .git, or dir. (rename of repoRoot)
  func RepoRoot(dir string) string

  type Resolver struct {
      Base    string            // plain references are relative to this folder
      Sources map[string]Source // the flow's own [sources]; Path relative to Base
      Project *Project          // may be nil
      Fetcher *Fetcher
      Warnings []string         // collected while resolving
      // unexported: registry entries (loaded once), alias -> rev
  }
  // NewResolver loads the Project of base's repository (errors become Warnings).
  func NewResolver(base string, flowSources map[string]Source, f *Fetcher) *Resolver
  func (r *Resolver) Resolve(ref string) (path, rev string, err error)
  // Lookup returns the source an alias names, by the lookup order, without fetching it (except the registry).
  func (r *Resolver) Lookup(alias string) (Source, error)
  // Registered returns every registry entry, or nil when there is no registry.
  func (r *Resolver) Registered() (map[string]Source, error)
  func (r *Resolver) Fetched() map[string]string  // alias -> rev, as today
  ```
- `dsheets.toml` keys: `registry = { git = "…", ref = "…" }` or `registry = { path = "." }`; `[sources]` with the same entry shape as a flow's (`git`, `ref`, `dir`, `path`).

- [ ] **Step 1: Write the failing tests**

In `project_test.go` / `sources_test.go`, using the Task 1 fixtures plus a registry fixture (a repo with a `.gitmodules` listing `shipping` → the team repo with `dsheets = sheets`, committed without running `git submodule add`; dsheets reads only the file):

```go
func TestLoadProject(t *testing.T)            // registry + [sources] parsed; path sources absolute; no file → empty Project
func TestResolveThroughRegistry(t *testing.T)  // consumer repo with dsheets.toml registry = { git = <registry url> };
                                               // Resolve("shipping:quote.toml") → .../sheets/quote.toml with "# v2"
func TestLookupOrder(t *testing.T)             // same alias in flow [sources] (Ref "v1"), project [sources], registry:
                                               // flow wins ("# v1"); without flow entry, project wins; else registry
func TestRegistryPathDot(t *testing.T)         // registry repo's own dsheets.toml registry = { path = "." } resolves "shipping:"
func TestNestedResolution(t *testing.T)        // a flow folder inside a cached clone gets a Resolver whose Project is
                                               // the clone's; the shared Fetcher's Cache is unchanged (no new cache inside the clone)
func TestPlainPathIntoSubmoduleWarns(t *testing.T) // repo with .gitmodules path=shipping; Resolve("../shipping/x.toml")
                                               // from a subfolder → Warnings has one entry containing `shipping:x.toml`
func TestUnknownAlias(t *testing.T)            // error mentions "[sources]" and "registry"
```

In `internal/sheet/display_test.go`: a `dsheets.toml` with `registry`, `[sources]` and `[display]` loads with no `project-file` issues; an unknown key `foo` still reports one.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/sources/ ./internal/sheet/ -v`
Expected: FAIL

- [ ] **Step 3: Implement `project.go` and the new `Resolver`**

Lookup: flow `Sources` → `Project.Sources` → registry (fetch `Project.Registry` through the Fetcher, or use its `Path`; read `.gitmodules` at its root; parse with the registry URL, or the folder for a path registry, as `base`). Git sources go through `r.Fetcher.Fetch`; path sources join `Dir` too. The submodule-path warning text: `"%s is inside submodule %s, which reads its recorded commit; refer to it as %s:%s"`. Remove the "needs ref" error. In `display.go`, `readProjectDisplay` skips `registry` and `sources` and its unknown-key message becomes `"…; " + ProjectFile + " holds [display], registry and [sources]"`.

- [ ] **Step 4: Update the existing callers to the new signature**

`internal/flow/flow_test.go` and `internal/flowrender/flowrender_test.go`: `sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir()))`. `cmd/dsheets/flowcmd.go` compiles in Task 5; for now pass `sources.NewFetcher(filepath.Join(sources.RepoRoot("."), sources.CacheDir))`.

- [ ] **Step 5: Run everything**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal cmd
git commit -m "Resolve aliases through flow sources, the repository's dsheets.toml and a registry"
```

---

### Task 4: Flows resolve their sheet and new source fields

**Files:**
- Modify: `internal/flow/flow.go`, `internal/flow/analyze.go`, `internal/flow/export.go`, `internal/flow/flow_test.go`, `schema/flow.schema.json`

**Interfaces:**
- Consumes: `Resolver` (Task 3).
- Produces:
  ```go
  // in Flow: SheetDir is replaced by
  SheetRef string // as written; "." when absent
  // Aliases lists the aliases the flow's references use (sheet, input, stages, output), sorted, unique.
  func (f *Flow) Aliases() []string
  // in Analysis:
  SheetDir string // where the end-to-end sheet was loaded from
  ```
  `[sources]` entries read `dir` into `Source.Dir`; flow-level `path` stays relative to the flow folder (the Resolver joins it).

- [ ] **Step 1: Write the failing tests in `flow_test.go`**

```go
func TestSheetFromSource(t *testing.T)  // temp flow copied from flows/checkout-to-payment with
                                        // sheet = "spec:order-checkout" and [sources] spec = { path = "<abs specs dir>" };
                                        // Analyze → no errors, a.SheetDir ends in "order-checkout"
func TestAliases(t *testing.T)          // example flow → []string{"shipping"} (whatever the example uses; assert against its flow.toml)
func TestResolverWarningsReported(t *testing.T) // a Resolver with a Warning set before build → it appears in a.Warnings
func TestExportWritesDir(t *testing.T)  // Sources{"shipping": {Git: u, Dir: "sheets"}} → exported flow.toml contains
                                        // `shipping = { git = "<u>", dir = "sheets" }` and no `ref`
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/flow/ -v`
Expected: FAIL

- [ ] **Step 3: Implement**

`Analyze` resolves `f.SheetRef` with `resolve.Resolve` (a plain ref resolves against the flow folder, as before), sets `a.SheetDir`, then loads the sheet; append `resolve.Warnings` to `a.Warnings` (as `Problem{File: f.File, Message: w}`) at the end. Export writes `{ git = …, ref = …, dir = … }` with empty keys omitted. Schema: `sources` entries gain `"dir": { "description": "Folder inside the repository that the alias means; default the root." }`; `ref` description becomes `"Branch to follow, or a tag or commit to pin; default: the repository's default branch."`; `sheet` description mentions `"alias:path"`.

- [ ] **Step 4: Run everything**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/flow schema
git commit -m "Let a flow's end-to-end sheet come from another repository"
```

---

### Task 5: CLI — aliases in arguments, one fetcher per run, serve refresh

**Files:**
- Modify: `cmd/dsheets/flowcmd.go`, `cmd/dsheets/main.go`
- Create: `cmd/dsheets/flowcmd_test.go`

**Interfaces:**
- Consumes: `Fetcher`, `Resolver`, `Flow.Aliases` (Tasks 1, 3, 4).
- Produces:
  ```go
  // newFetcher makes the run's fetcher: cache in the current repository.
  func newFetcher(offline bool) *sources.Fetcher
  // argPath resolves a command-line argument: "alias:path" through the current repository's
  // configuration, anything else unchanged.
  func argPath(arg string, f *sources.Fetcher) (string, error)
  func loadFlow(dir string, f *sources.Fetcher) (*flow.Analysis, error)          // replaces (offline, refresh)
  func flowPage(dirs []string, title string, check bool, fe *sources.Fetcher, w, errw io.Writer) error
  ```

- [ ] **Step 1: Write the failing tests in `flowcmd_test.go`**

Use `run([]string{...}, &out, &errw)` from inside a temp consumer repo (chdir with `t.Chdir`) whose `dsheets.toml` has `[sources] ex = { path = "<abs repo root>" }`:

```go
func TestSliceAliasArgument(t *testing.T) // run("slice", "ex:flows/checkout-to-payment") → nil; output contains "carried"
func TestSliceWithAlias(t *testing.T)     // run("slice", "ex:flows/loyalty-report", "--with", "ex:flows/checkout-to-payment") → nil
func TestRenderAliasFlows(t *testing.T)   // run("render", "ex:flows/checkout-to-payment", "--out", tmp) → file written
func TestFetchListsAliases(t *testing.T)  // run("fetch", "ex:flows/checkout-to-payment") lists every alias the flow uses
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/dsheets/ -v`
Expected: FAIL

- [ ] **Step 3: Implement**

`slice`, `render`, `fetch`, `serve` each make one fetcher (`--offline` where the command has it; add `--offline` to `render` and `serve`) and pass every positional flow and `--with` value through `argPath`. `isFlow` runs on the resolved path. `fetch` resolves `alias + ":."` for every alias in `f.Aliases()` plus the flow's declared `[sources]`, printing `alias  url @ ref-or-"default branch" (rev)`, with a fresh `NewFetcher` (fetch always refreshes). `serve` creates its fetcher once with `MaxAge = time.Minute` and reuses it in the handler. The cache is `filepath.Join(sources.RepoRoot(cwd), sources.CacheDir)`. Print `a.Warnings` as today. In `printAnalysis`, the header names the sheet and, when the flow used git sources, a line `sources: shipping@a1b2c3d, …` from `a.Revisions`.

- [ ] **Step 4: Run everything and the CLI by hand**

Run: `go test ./... && go build -o bin/dsheets ./cmd/dsheets && bin/dsheets slice flows/checkout-to-payment --offline`
Expected: PASS; slice output as before.

- [ ] **Step 5: Commit**

```bash
git add cmd
git commit -m "Accept alias:path for flows on the command line; fetch once per run"
```

---

### Task 6: `internal/impact` — consumers, before and after, new findings

**Files:**
- Create: `internal/impact/impact.go`, `internal/impact/impact_test.go`

**Interfaces:**
- Consumes: `Fetcher.WithSubstitute`, `PinnedHits`, `Resolver.Registered`, `Lookup`, `SameRepo`, `RepoRoot` (Tasks 1–3); `flow.Load`, `flow.Analyze`, `Flow.Aliases`, `Analysis.Errors`, `Boundaries[].Items` (Task 4).
- Produces:
  ```go
  type Options struct {
      Root    string // the repository being changed (working tree)
      As      string // its alias; "" = find by origin URL in the registry
      Base    string // base branch; "" = origin's default branch
      Fetcher *sources.Fetcher
  }
  type Finding struct {
      Repo, Flow       string // consuming repository alias; flow as "alias:path"
      Interface, Owner string // interface name and owner ("" for flow errors)
      Where            string // "input", "output" or "stage <id>"
      Field, Status    string // Status: flow.Missing, flow.Warn, or "error"
      Message          string // Item.Reason() or the error text, with local roots replaced by "alias:"
  }
  type Skipped struct{ Repo, Flow, Reason string }
  type Pinned struct{ Repo, Flow, Ref string }
  type Report struct {
      Self       string
      New        []Finding
      Resolved   []Finding
      Pinned     []Pinned
      NotChecked []Skipped
      Checked    int // consumer flows checked
  }
  func Run(o Options) (*Report, error)
  func (r *Report) ExitCode() int // 0, 1 or 3 as in Global Constraints
  ```

- [ ] **Step 1: Write the failing tests**

Fixtures (git, file:// URLs): a producer repo `shipping` with `sheets/interfaces/quote-response.toml` (field `fee`) on `main`; a consumer repo `orders` whose `sheets/flows/x/flow.toml` uses `shipping:interfaces/quote-response.toml` as a call response carrying `fee` (copy the shape from `flows/checkout-to-payment`, with its sheet copied into the consumer repo); a second consumer flow pinning `shipping` with `ref = "v1"`; a registry listing both with `dsheets = sheets`. The test's working tree is a separate clone of `shipping` with `origin` set to the producer URL, edited per test.

```go
func TestImpactNoChange(t *testing.T)      // unchanged tree → New empty, Checked == 1, ExitCode 0
func TestImpactDroppedField(t *testing.T)  // remove fee → one New finding: Repo "orders", Field "fee", Status flow.Missing,
                                           // Interface "quote-response"; ExitCode 1
func TestImpactOnlyNew(t *testing.T)       // consumer already lacks another field at base → not in New
func TestImpactResolved(t *testing.T)      // base lacks a field the consumer needs, tree adds it → in Resolved, ExitCode 0
func TestImpactDeletedFile(t *testing.T)   // delete quote-response.toml → New has Status "error" mentioning
                                           // "shipping:interfaces/quote-response.toml"; no local path in Message
func TestImpactStableErrors(t *testing.T)  // consumer flow with an unrelated error present before and after →
                                           // neither New nor Resolved (Review Focus 3); it is in NotChecked (fails at base)
func TestImpactPinned(t *testing.T)        // pinned consumer → in Pinned with Ref "v1", not in New even when fee is removed
func TestImpactFindsSelfByOrigin(t *testing.T) // As "", working tree's origin = producer file URL plus a trailing "/" → Self "shipping"
func TestImpactUnreachable(t *testing.T)   // registry lists a third repo at a missing URL → NotChecked has it; ExitCode 3
func TestImpactSkipsOwnFlows(t *testing.T) // a flow inside the producer repo is not checked
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/impact/ -v`
Expected: FAIL (package missing)

- [ ] **Step 3: Implement `Run`**

1. `res := sources.NewResolver(o.Root, nil, o.Fetcher)`; `reg := res.Registered()` (no registry → error `"impact needs a registry; set registry in dsheets.toml"`).
2. Self: `o.As`, else the entry with `SameRepo(entry.Git, origin)` where origin is `git -C Root remote get-url origin`; none → error suggesting `--as`.
3. Base checkout: a fetch of the self entry with `Ref: o.Base` through a fetcher without substitution (shallow, sparse like any other source) — its clone root is the "before" root.
4. Consumers: for every entry except self, plus the registry itself, fetch its root (unreachable → `NotChecked`), walk it for `flow.toml` (skip `.git`, `.dsheets`), `flow.Load`, keep flows where any alias in `Aliases()` has `res`-style `Lookup` (a Resolver built for that flow's folder) with `SameRepo(src.Git, self.Git)`; a flow whose lookup for that alias has a pinned ref → `Pinned`.
5. For each consumer: analyse with `o.Fetcher.WithSubstitute(&Substitute{URL: self.Git, Root: baseRoot})`, then with `Root: o.Root`. Before has errors or fails to load → `NotChecked` with the reason. Otherwise collect findings from both: every `Item` with status Missing or Warn, and every `Error`. Key = `Flow|Where|Interface|Field|Status|Message`, with each run's substituted root and cache paths in messages replaced by `alias:` first. New = after − before; Resolved = before − after.
6. Sort New, Resolved by Repo, Flow, Where, Field.

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/impact/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/impact
git commit -m "Find other teams' flows a change breaks, reporting only new problems"
```

---

### Task 7: `dsheets impact` command and its output

**Files:**
- Create: `internal/impact/report.go`, `internal/impact/report_test.go`, `cmd/dsheets/impactcmd.go`
- Modify: `cmd/dsheets/main.go` (command table, `errIncomplete` → exit 3), `internal/flowrender` (only if needed to embed a flow view, see Step 3)

**Interfaces:**
- Consumes: `Report`, `Run` (Task 6); `flowrender.Build`, `flowrender.Write`.
- Produces:
  ```go
  func (r *Report) WriteText(w io.Writer) error
  func (r *Report) WriteJSON(w io.Writer) error
  func (r *Report) WriteHTML(w io.Writer, views map[string]*flow.Analysis) error // views keyed by Finding.Flow, "after" analyses
  var errIncomplete = errors.New("incomplete") // exit code 3
  ```
  Flags: `--as`, `--base`, `--json`, `--out <file.html>`, `--offline`.

- [ ] **Step 1: Write the failing tests in `report_test.go`**

```go
func TestWriteText(t *testing.T) {
    r := &Report{Self: "shipping", Checked: 2, New: []Finding{{Repo: "orders", Flow: "orders:flows/checkout-to-payment",
        Interface: "quote-response", Owner: "Shipping team", Where: "stage shipping-quote", Field: "fee",
        Status: flow.Missing, Message: "missing; needed by Shipping cost (Orders team)"}}}
    // output contains, in order:
    //   "quote-response (Shipping team) no longer carries fee"
    //   "  orders:flows/checkout-to-payment, stage shipping-quote: needed by Shipping cost (Orders team)"
    // and a last line "1 new problem in 2 checked flows"
}
func TestWriteTextSections(t *testing.T) // Resolved, Pinned and NotChecked each print under headings
                                         // "Resolved by this change", "Pinned, not affected yet", "Not checked"
func TestWriteJSON(t *testing.T)         // round-trips into Report; exit_code field equals ExitCode()
func TestWriteHTML(t *testing.T)         // one self-contained page: contains "orders", the field, and the flow view's title
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/impact/ -run Write -v`
Expected: FAIL

- [ ] **Step 3: Implement**

Text groups New by `(Interface, Field, Status)` — the heading per group: Missing → `"<iface> (<owner>) no longer carries <field>"`; Warn → `"<iface> (<owner>) <field> no longer fits"`; error → `"<flow>: <message>"` — then one indented line per consuming flow. JSON uses lower-case snake keys plus `exit_code`. HTML: a page with the summary grouped by consuming repository, followed by each affected flow's checked view; reuse flowrender's page by rendering each view with `flowrender.Write` into an `<iframe srcdoc>` (escaped) so its styles stay isolated — no change to flowrender needed. To have the "after" analyses, extend `Report` with an unexported `views map[string]*flow.Analysis` filled by `Run`, or return it alongside; pick one and keep `WriteHTML`'s signature.

`impactcmd.go`: builds the fetcher (`newFetcher(*offline)`), calls `impact.Run`, writes text (or JSON), writes `--out` when given, and returns `errFailed` for 1, `errIncomplete` for 3. `main` maps `errIncomplete` to `os.Exit(3)`. Command table entry: `{"impact", "[--as alias] [--base branch]", "report which other teams' flows this repository's changes break", cmdImpact}`.

- [ ] **Step 4: Run everything**

Run: `go test ./... && go build -o bin/dsheets ./cmd/dsheets && bin/dsheets impact -h`
Expected: PASS; the help lists the five flags.

- [ ] **Step 5: Commit**

```bash
git add internal/impact cmd
git commit -m "Add dsheets impact"
```

---

### Task 8: Documentation and CI examples

**Files:**
- Create: `docs/repositories.md`
- Modify: `docs/flows.md` (section "Interfaces in other teams' repositories"), `docs/format.md:37`, `README.md` (commands table, "Flows across teams", layout), `BACKLOG.md` (item 15 done)

- [ ] **Step 1: Write `docs/repositories.md`**

Sections, in the voice of `docs/flows.md`: *Each team's own repository*; *The registry* (the `.gitmodules` example from the spec, the `dsheets` key, adding a team with `git submodule add` plus `git config -f .gitmodules submodule.<name>.dsheets sheets` and `branch`); *Using it from a team repository* (`registry =` in the root `dsheets.toml`, `alias:path` everywhere, the table of newly accepted places); *`[sources]`* (pinning, outside repositories, local checkouts, `dir`, lookup order); *Fetching* (shallow and sparse, followed vs pinned, `--offline`, the cache, serve's one-minute refresh); *`dsheets impact`* (what it checks, the example output from Task 7's test, exit-code table, not covered: `--with` links); *CI* with two complete GitHub Actions workflows:
  - producer: on `pull_request`, checkout with `fetch-depth: 0`, setup-go, `go install github.com/NandavardhanRadhakrishnan/DerivationSheets/cmd/dsheets@latest`, configure `git config --global url."https://x-access-token:${{ secrets.DSHEETS_READ_TOKEN }}@github.com/".insteadOf "https://github.com/"`, `actions/cache` on `.dsheets/sources`, `dsheets impact --out impact.html`, upload the page as an artifact; fail on exit 1 and 3.
  - consumer: on `push`, `pull_request` and `schedule` (daily), same setup, `for f in flows/*/; do dsheets slice "$f" --strict; done`.

- [ ] **Step 2: Update the other docs**

`docs/flows.md`: replace the section body with a short version (aliases, `ref` optional) and a link to `docs/repositories.md`. `docs/format.md:37`: `dsheets.toml` holds `[display]`, and at the repository root also `registry` and `[sources]`. README: add `impact` to the commands table and the `--offline` note, link `docs/repositories.md`. BACKLOG: mark item 15 done with the date.

- [ ] **Step 3: Check the examples in the docs actually run**

Run: `go build -o bin/dsheets ./cmd/dsheets && bin/dsheets slice flows/checkout-to-payment --offline && go test ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add docs README.md BACKLOG.md
git commit -m "Document cross-repository references, the registry and impact"
```
