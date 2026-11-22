# Requirements Panel Implementation Plan (T-37, design section 6)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In `dsheets serve --edit <team sheet> --flow <alias>:<flow>@<branch>`, show the PM's open requirements for this team in a Requirements panel, rerun the PM's cases against the unsaved draft as the owner edits, and save the sheet together with the team flow edits it needs (absorbing T-31).

**Architecture:** `requirements.Run` splits into `Prepare` (read the PM's change once: base and head analyses, the item list) and `Decide(overlay)` (judge the items on the owners' sheets, with unsaved file contents taken from an overlay). The overlay, today used only for interface files, also reaches sheets and flows in `flow.AnalyzeWith`, `agree.RegistryTeams` and `requirements.Check`. The owner's editor reaches the PM's branch with a new history-keeping fetch (`sources.FetchChange`), and substitutes its own working tree for its repository, so the draft is what the PM's cases run against. Saving a sheet also rewrites this repository's flows that pin rows the draft no longer derives, in one store write.

**Tech Stack:** Go (standard library), existing packages `internal/requirements`, `internal/agree`, `internal/flow`, `internal/sources`, `internal/editor`, `internal/tomledit`; plain JavaScript and CSS in `internal/render/editor.js` and `editor.css`.

**Spec:** `docs/superpowers/specs/2026-11-21-boundary-agreement-design.md`, section 6 (and the decisions and constraints sections it relies on). Backlog: T-37, which absorbs T-31.

## Global Constraints

- Never close an item on a guess (D-22): a draft that cannot be evaluated never makes an item `done`; "cannot tell" stays `undocumented`.
- Each team edits only its own sheet: the editor writes only files in this repository (the sheet, its `cases.toml`, this repository's `flow.toml` files).
- `--edit` listens only on loopback; every new endpoint goes through `editServer.guard` (POST, JSON, same origin).
- Owner repositories are read through the registry and fetcher `requirements` already uses; `--offline` keeps its meaning.
- Values compare with `sheet.Equal`; fields match with the rules of `requirements.sameField`.
- Wording follows the project's neutral terms: "target", never "partner"; generic examples (shop, order-checkout).
- Restore `specs/` before running tests or merging (`git checkout -- specs/`). Run `go run ./cmd/dsheets fmt` after editing any committed fixture TOML.
- Windows: the worktree is CRLF (`core.autocrlf=true`); run `gofmt -l` on changed Go files before each commit. Overlay keys are always `filepath.Clean(filepath.Abs(path))` on both the writing and the reading side.

## Plan decisions (not in the spec; flagged to the developer)

1. **`--flow` needs an alias and a branch.** `--flow product:flows/shop@silver-half`. A missing alias or `@branch` is a usage error. The PM's flow on its followed branch has no change to show.
2. **The PM's repository is fetched with history.** The change is a fetch with the full commit history but no file contents (`--filter=blob:none`), sparse to the alias folder, so `requirements` can compare with the merge-base, as the PM's own run does. A shallow fetch of the followed branch's tip would list another merged change as a requirement.
3. **Neighbour items come only through calls.** For item (5) of the design, a team sees another team's Compute or Send item, marked as that team's, when it is the callee of a call made in that team's segment and the item's PM row depends on a row carried on that call's request or response. Handoff neighbours are not shown (every upstream team would see every downstream item).
4. **Single save is always on** in `serve --edit` on a sheet, with or without `--flow`. Saving the sheet removes the rows the draft no longer derives from `computes` and `callee_computes` in this repository's flows whose `sheet` is this sheet. If a flow error the edit introduces is still left after that, the save is refused, listing it. Today the sheet saves and the flow is left broken (T-31).
5. **Reruns are a separate request** (`/edit/requirements`), sent after each check returns, so a slow fetch never delays the grid.
6. **"Rows that produce these fields"** are this sheet's rows that send the item's fields, with their derived ancestors, in sheet order; input rows are not listed.
7. **The PM's change is read once** when serve starts, and again on "Fetch again" in the panel. Other teams' sheets follow the serve fetcher's one-minute MaxAge, as pages do today.

## Review Focus

- **A draft with errors, or one that does not parse:** the panel keeps the last result, marked stale with the reason. It never shows an item done because the draft's flow could not be analysed (Task 8: `TestEditRequirementsStaleDraft`).
- **The PM's branch deleted or not fetched yet with `--offline`:** serve still starts and edits work. The panel shows the error and offers Fetch again (Task 8: `TestEditRequirementsOpenError`).
- **An owner repository with no origin remote, or not in the registry:** the panel says why it cannot tell which team is this repository's. It lists every team's items as other teams', and never as "yours, done" (Task 7: `TestRequirementsViewUnknownTeam`).
- **Overlay paths on Windows:** an overlay key that does not match the analysed path silently uses the disk file, so the draft never turns green. Both sides go through one helper, `flow.OverlayKey` (Task 1: `TestOverlayKeyCleansAndAbsolutes`, and the green-turning tests in Tasks 3 and 8).
- **A rerun answer arriving after a newer one:** the page drops answers older than its latest request (Task 9: sequence check, verified in the browser step).

---

### Task 1: The overlay reaches sheets and flows

**Files:**
- Modify: `internal/flow/analyze.go` (`AnalyzeWith`, `Overlay` doc), `internal/flow/flow.go` (new `LoadWith`)
- Modify: `internal/agree/registry.go` (`RegistryTeams` signature), `internal/requirements/check.go` (`Check`, `readRepo`)
- Modify callers: `internal/requirements/run.go`, `cmd/dsheets/slicecases.go`, tests calling `Check` / `RegistryTeams`
- Test: `internal/flow/analyze_test.go`, `internal/agree/registry_test.go` (new), `internal/requirements/check_test.go`

**Interfaces:**
- Produces:
  - `func OverlayKey(path string) string` in `internal/flow`: the cleaned absolute path, the key for every overlay entry.
  - `func LoadWith(dir string, ov Overlay) (*Flow, error)`: reads `flow.toml` from `ov` when it has it, else `Load(dir)`.
  - `AnalyzeWith(f, resolve, ov)`: now also reads the sheet (`<dir>/sheet.toml`) from `ov`.
  - `agree.RegistryTeams(reg map[string]sources.Source, fe *sources.Fetcher, self string, ov flow.Overlay) TeamFlows`.
  - `requirements.Check(rs []Requirement, reg map[string]sources.Source, fe fetcher, self string, ov flow.Overlay)`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/flow/analyze_test.go
func TestOverlayKeyCleansAndAbsolutes(t *testing.T) {
	rel := filepath.Join("a", "..", "b", "sheet.toml")
	abs, _ := filepath.Abs(filepath.Join("b", "sheet.toml"))
	if OverlayKey(rel) != filepath.Clean(abs) { t.Fatalf("got %q", OverlayKey(rel)) }
}

func TestAnalyzeWithReadsSheetAndFlowFromOverlay(t *testing.T) {
	dir := filepath.Join("..", "agree", "testdata", "shop", "orders", "flows", "orders")
	sheetFile := filepath.Join("..", "agree", "testdata", "shop", "orders", "specs", "orders", "sheet.toml")
	data, _ := os.ReadFile(sheetFile)
	ov := Overlay{OverlayKey(sheetFile): []byte(strings.Replace(string(data), "fee / 2", "fee / 4", 1))}
	f, err := LoadWith(dir, ov)
	if err != nil { t.Fatal(err) }
	a := AnalyzeWith(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())), ov)
	if got := a.Sheet.ByID["shipping_cost"].Formula; !strings.Contains(got, "fee / 4") { t.Fatalf("formula %q", got) }
	ov[OverlayKey(filepath.Join(dir, FileName))] = []byte(strings.Replace(mustRead(t, filepath.Join(dir, FileName)), `title   = "Orders"`, `title   = "Orders draft"`, 1))
	if f, _ = LoadWith(dir, ov); f.Title != "Orders draft" { t.Fatalf("title %q", f.Title) }
}
```

```go
// internal/agree/registry_test.go: no git; a substitute serves the fixture folder
func TestRegistryTeamsReadsOverlay(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "shop"))
	const url = "https://example.com/orders.git"
	reg := map[string]sources.Source{"orders": {Alias: "orders", Git: url, Dir: "orders", Team: "Orders team"}}
	fe := sources.NewFetcher(t.TempDir()).WithSubstitute(&sources.Substitute{URL: url, Root: root})
	sheetFile := filepath.Join(root, "orders", "specs", "orders", "sheet.toml")
	ov := flow.Overlay{flow.OverlayKey(sheetFile): []byte(strings.Replace(mustRead(t, sheetFile), "fee / 2", "fee / 4", 1))}
	flows, notes := RegistryTeams(reg, fe, "", ov)("Orders team")
	if len(flows) != 1 || len(notes) != 0 { t.Fatalf("flows %d, notes %v", len(flows), notes) }
	if !strings.Contains(flows[0].Sheet.ByID["shipping_cost"].Formula, "fee / 4") { t.Fatal("overlay not read") }
}
```

```go
// internal/requirements/check_test.go, with the existing owner() and fakeFetcher helpers
func TestCheckReadsOverlay(t *testing.T) {
	dir := owner(t, /* a sheet whose shipping_cost rule differs from q.Rule */)
	q := Requirement{Kind: Compute, Team: "Orders team", Row: "shipping_cost", Rule: wanted}
	file := /* the sheet.toml owner() wrote */
	ov := flow.Overlay{flow.OverlayKey(file): /* the same sheet with q.Rule */}
	rs := []Requirement{q}
	Check(rs, reg, fakeFetcher{"orders": dir}, "", ov)
	if rs[0].Status != StatusDone { t.Fatalf("%+v", rs[0]) }
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/flow ./internal/agree ./internal/requirements -run "OverlayKey|ReadsSheetAndFlow|ReadsOverlay"`
Expected: build failures (`OverlayKey`, `LoadWith` undefined; too many arguments to `RegistryTeams`, `Check`).

- [ ] **Step 3: Implement**

- `OverlayKey`: `filepath.Abs`, then `filepath.Clean`; on error, `filepath.Clean(path)`. Use it in `loadIface` too (replacing its inline code) and in `editor.flowRun` where it builds the interface overlay.
- `AnalyzeWith`: when `ov[OverlayKey(filepath.Join(dir, sheet.FileName))]` exists, `sheet.Parse(dir, file, string(data))`, else `sheet.Load(dir)`. Update the doc comments of `Overlay` and `AnalyzeWith` ("interface files, sheets and flow.toml not yet saved").
- `RegistryTeams`: `flow.LoadWith(dir, ov)` and `flow.AnalyzeWith(..., ov)`.
- `Check`/`readRepo`: on finding `sheet.toml`, parse the overlay content when present.
- Callers that have no draft pass `nil`.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/flow ./internal/agree ./internal/requirements ./internal/editor ./cmd/dsheets`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/flow internal/agree internal/requirements internal/editor cmd/dsheets
git commit -m "flow, agree, requirements: unsaved sheets and flows reach analysis through the overlay"
```

---

### Task 2: A git fixture of the shop, and fetching a change with history

**Files:**
- Create: `internal/testrepo/shop.go` (test-support package, imported only by tests)
- Modify: `internal/sources/fetch.go` (new `FetchChange`)
- Test: `internal/sources/change_test.go`

**Interfaces:**
- Produces (`internal/testrepo`):
  - `type Shop struct{ Product, Cart, Orders, Ship string }`: four git repositories on `main`, one per folder of `internal/agree/testdata/shop` (`product`, `cart`, `orders`, `shipping`). In each, the team folder is copied to `sheets/` and the shared `interfaces/` to `interfaces/`, so the fixture's `../../../interfaces/...` paths resolve unchanged.
  - Product also has `dsheets.toml` = `registry = { path = "." }`, and a `.gitmodules` with `product`, `cart`, `orders` and `ship`. Each entry has `url` (a slash path), `branch = main`, `dsheets = sheets`, and a `team` key: `Product team`, `Cart team`, `Orders team`, `Shipping team`.
  - Each team repository has `dsheets.toml` = `registry = { git = "<product path>" }`. Every repository sets `core.autocrlf false`.
  - `func NewShop(t testing.TB) *Shop`
  - `func (s *Shop) Change(t testing.TB, repo, branch string, edits ...[3]string)` switches `repo` to a new `branch`, then applies each `{file, old, new}` replacement (file relative to the repository), commits, and stays on the branch.
  - `func Clone(t testing.TB, repo string) string`: a working clone whose origin is `repo`.
  - `func Git(t testing.TB, dir string, args ...string) string`
- Produces (`internal/sources`):
  - `type Change struct{ Root, Base, Head string }`. `Root` is the alias's folder in a sparse checkout of the change, `Base` the merge-base commit with the followed branch, and `Head` the branch's commit (full hashes).
  - `func (f *Fetcher) FetchChange(src Source, branch string) (*Change, error)`

- [ ] **Step 1: Write the failing tests**

```go
// internal/sources/change_test.go
func TestFetchChange(t *testing.T) {
	s := testrepo.NewShop(t)
	mainBefore := testrepo.Git(t, s.Product, "rev-parse", "main")
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	head := testrepo.Git(t, s.Product, "rev-parse", "HEAD")
	testrepo.Git(t, s.Product, "switch", "main") // main moves on after the branch
	testrepo.Git(t, s.Product, "commit", "--allow-empty", "-m", "later")
	src := Source{Alias: "product", Git: filepath.ToSlash(s.Product), Dir: "sheets"}
	ch, err := NewFetcher(t.TempDir()).FetchChange(src, "quarter")
	if err != nil { t.Fatal(err) }
	if ch.Base != mainBefore || ch.Head != head { t.Fatalf("base %s head %s", ch.Base, ch.Head) }
	if !strings.Contains(read(t, filepath.Join(ch.Root, "specs", "shop", "sheet.toml")), "shipping_fee / 4") { t.Fatal("not the branch") }
}

func TestFetchChangeUnknownBranch(t *testing.T) { /* FetchChange(src, "nope") error mentions "nope" */ }

func TestFetchChangeOffline(t *testing.T) {
	// The same cache with Offline set returns the same Change; an empty cache
	// with Offline set fails with "not fetched yet and --offline is set".
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/sources -run FetchChange`
Expected: build failure (`testrepo` package, `FetchChange` undefined).

- [ ] **Step 3: Implement `testrepo` and `FetchChange`**

`FetchChange` keeps one clone per source in the cache, at `<alias>-change-<sha1(url#dir)[:10]>`:
1. Initialise it as `fetchInto` does: init, `remote add`, and a cone sparse-checkout of `src.Dir`.
2. Fetch with `--filter=blob:none` and no `--depth`, using the refspecs `+<followed>:refs/dsheets/followed` and `+refs/heads/<branch>:refs/dsheets/change`. `<followed>` is `HEAD` when `src.Ref` is empty, `refs/heads/<ref>` when `Followed(src)` says it is a branch, and the ref itself for a tag or commit.
3. Run `checkout --quiet --detach refs/dsheets/change`.
4. `Base` is `merge-base refs/dsheets/followed refs/dsheets/change`; `Head` is `rev-parse refs/dsheets/change`.

Offline: use a clone that is already there, else return the `Fetch` offline error. Refetch only when the clone is older than `MaxAge`, tracked in `f.s.at` as `Fetch` does. A failed branch fetch gives `branch <b> not found in <url>`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/sources`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/testrepo internal/sources
git commit -m "sources: fetch a branch with its history to compare a change; testrepo: the shop as git repositories"
```

---

### Task 3: `requirements` prepares once and decides many times

**Files:**
- Modify: `internal/requirements/run.go` (split `Run`), `internal/requirements/model.go` (no change expected), `internal/agree/agree.go` (`Report.Inputs`, `Report.Sheets`)
- Test: `internal/requirements/prepare_test.go` (new), `internal/agree/chain_test.go`

**Interfaces:**
- Consumes: Task 1's overlay parameters; Task 2's `testrepo`.
- Produces:
  - `type Prepared struct{ ... }` (unexported fields: the result template with every item `open`, the head analysis, registry, self alias, the fetcher, the PM's filled cases and why they are missing, `noCheck`, branch, and the flow folder relative to the self alias root)
  - `func Prepare(o Options) (*Prepared, error)`: all of today's `Run` up to and including `locate`. It removes its exports before returning, so nothing later may read files under them: read `cases.ReadFilled` here.
  - `func (p *Prepared) Decide(ov flow.Overlay) *Result`: copies the items, then runs `Check`, `agree.Run` (isolated, `RegistryTeams(..., ov)`), `applyCases` and `explainCases`. It never changes `p`.
  - `func (p *Prepared) Analysis() *flow.Analysis`: the PM's flow at head.
  - `func Run(o Options) (*Result, error)`: `Prepare`, then `Decide(nil)`.
  - `Result.Report *agree.Report` (`json:"-"`): the run behind the cases, or nil.
  - `agree.Report.Inputs map[string]map[string]any`: case id → input.
  - `agree.Report.Sheets map[string][]string`: team → the sheet folders of its matched own flows, sorted, without duplicates.

- [ ] **Step 1: Write the failing tests**

```go
// internal/requirements/prepare_test.go
// shopChange: the PM halves shipping for Silver members no longer, but quarters
// it; Orders' sheet still halves. o substitutes a working clone of Orders.
func shopChange(t *testing.T) (s *testrepo.Shop, ordersWork string, o Options) {
	s = testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	ordersWork = testrepo.Clone(t, s.Orders)
	fe := sources.NewFetcher(t.TempDir()).WithSubstitute(&sources.Substitute{URL: filepath.ToSlash(s.Orders), Root: ordersWork})
	return s, ordersWork, Options{Flow: filepath.Join(s.Product, "sheets", "flows", "shop"), Dir: s.Product, Base: "main", Fetcher: fe}
}

func TestDecideOpensThenClosesOnTheDraft(t *testing.T) {
	_, work, o := shopChange(t)
	p, err := Prepare(o)
	if err != nil { t.Fatal(err) }
	q := find(p.Decide(nil).Requirements, Compute, "Orders team", "shipping_cost")
	if q == nil || q.Status != StatusOpen || len(q.Cases) == 0 { t.Fatalf("got %+v", q) }
	file := filepath.Join(work, "sheets", "specs", "orders", "sheet.toml")
	ov := flow.Overlay{flow.OverlayKey(file): []byte(strings.Replace(read(t, file), "fee / 2", "fee / 4", 1))}
	if q := find(p.Decide(ov).Requirements, Compute, "Orders team", "shipping_cost"); q.Status != StatusDone { t.Fatalf("with the draft: %+v", q) }
	if q := find(p.Decide(nil).Requirements, Compute, "Orders team", "shipping_cost"); q.Status != StatusOpen { t.Fatal("Decide changed the prepared state") }
}

func TestRunEqualsPrepareDecide(t *testing.T) { /* same statuses, notes and case counts for every item */ }
```

```go
// internal/agree/chain_test.go
func TestReportInputsAndSheets(t *testing.T) {
	pm, teams := shop(t, nil)
	r := Run(pm, teams, Options{Mode: Isolated})
	for _, c := range r.Checks { if _, ok := r.Inputs[c.Case]; !ok { t.Fatalf("no input for %s", c.Case) } }
	if len(r.Sheets["Orders team"]) != 1 || !strings.HasSuffix(filepath.ToSlash(r.Sheets["Orders team"][0]), "orders/specs/orders") { t.Fatalf("%v", r.Sheets) }
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/requirements ./internal/agree -run "Decide|PrepareDecide|InputsAndSheets"`
Expected: build failure (`Prepare`, `Inputs`, `Sheets` undefined).

- [ ] **Step 3: Implement the split and the two report fields** as the Interfaces block says.

- [ ] **Step 4: Run the packages**

Run: `go test ./internal/requirements ./internal/agree ./cmd/dsheets`
Expected: PASS (existing `Run` tests unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/requirements internal/agree
git commit -m "requirements: prepare a change once and decide it on any overlay; agree reports case inputs and team sheets"
```

---

### Task 4: `requirements` prints each team's editor line

**Files:**
- Modify: `internal/requirements/run.go` (`Options.Branch`, editor lines in `Decide`), `internal/requirements/output.go` (`writeTeam`, JSON), `internal/requirements/apply.go` (record and pull request description), `cmd/dsheets/requirementscmd.go` (nothing to add unless the flag below is wanted; see Step 3)
- Test: `internal/requirements/output_test.go`, `internal/requirements/apply_test.go`, `internal/requirements/prepare_test.go`

**Interfaces:**
- Consumes: Task 3's `Report.Sheets`, `Prepared`.
- Produces:
  - `Options.Branch string`: the PM's branch the lines name. When empty and `Head` is empty, it is the current branch (`git symbolic-ref --short -q HEAD`); a detached head gives no lines.
  - `Result.Editor map[string]string` (`json:"editor,omitempty"`): team → `dsheets serve --edit <sheet> --flow <alias>:<flow>@<branch>`.
    - `<sheet>` is the team's first sheet in `Report.Sheets`, relative to its repository root (`sources.RepoRoot`), slash-separated; the literal `<sheet>` when none was found.
    - `<alias>` is the PM repository's registry alias (`SelfAlias`), and `<flow>` the flow folder relative to that alias's root.
    - There are no lines when the alias, the flow's place under it, or the branch is unknown.

- [ ] **Step 1: Write the failing tests**

```go
func TestEditorLines(t *testing.T) {
	s, _, o := shopChange(t)   // product is on branch quarter
	o.Base = "main"
	res, err := Run(o)
	if err != nil { t.Fatal(err) }
	want := "dsheets serve --edit sheets/specs/orders --flow product:flows/shop@quarter"
	if res.Editor["Orders team"] != want { t.Fatalf("got %q", res.Editor["Orders team"]) }
	var b strings.Builder
	res.WriteMarkdown(&b)
	if !strings.Contains(b.String(), "open in the editor: "+want) { t.Fatal(b.String()) }
	testrepo.Git(t, s.Product, "switch", "--detach")
	if res, _ := Run(o); len(res.Editor) != 0 { t.Fatalf("detached head gave %v", res.Editor) }
}

// apply_test.go
func TestApplyRecordsEditorLine(t *testing.T) {
	// schemaResult(...) with Editor{"Payments team": "dsheets serve --edit sheets/specs/pay --flow product:flows/x@b"}
	// → the record file and Applied.Description both contain "Open in the editor: `dsheets serve --edit sheets/specs/pay --flow product:flows/x@b`"
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/requirements -run "EditorLine"`
Expected: FAIL (`Editor` undefined).

- [ ] **Step 3: Implement**

`writeTeam` prints `  open in the editor: <line>` right after the team's header line. `WriteFolder` gets it through `writeTeam`. The JSON gets it through the field. The record and description of `--apply` add `- Open in the editor: \`<line>\`` for each team of the repository's items that has a line. No new command flag: the branch comes from the working tree, or from `Head` when it names a branch. Use `revs` to test whether `Head` names a branch, and set `Options.Branch` from it in `cmdRequirements`.

- [ ] **Step 4: Run the package and the command tests**

Run: `go test ./internal/requirements ./cmd/dsheets`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/requirements cmd/dsheets
git commit -m "requirements: print and record the editor line for each team"
```

---

### Task 5: Opening the PM's change from an owner's repository

**Files:**
- Create: `internal/requirements/open.go`
- Test: `internal/requirements/open_test.go`

**Interfaces:**
- Consumes: Task 2's `FetchChange`, Task 3's `Prepare`.
- Produces:
  - `func ParseChangeRef(ref string) (alias, path, branch string, err error)`. It parses `alias:path@branch`. Its errors are `--flow names the PM's repository by its registry alias: alias:path@branch` and `--flow needs @<branch>, the PM's branch with the change`.
  - `func OpenChange(top, ref string, fe *sources.Fetcher) (*Prepared, error)`. It looks up the alias through `sources.NewResolver(top, nil, fe).Lookup` and calls `FetchChange`. Then it runs `Prepare` with `Flow` = `<Root>/<path>`, `Dir` = `Root`, `Base`/`Head` from the change, `Branch` = branch, and a fetcher that substitutes `top` for the repository whose URL is `sources.OriginURL(top)`. With no origin there is no substitute.
  - `func (p *Prepared) TeamsOf(top string) (teams []string, why string)`: the PM flow's step teams whose `sources.TeamAliases` include the alias of `top`'s origin, in step order. When there are none, `why` says why: no origin remote, not in the registry, or in the registry but in no team of this flow.

- [ ] **Step 1: Write the failing tests**

```go
func TestParseChangeRef(t *testing.T) {
	// "product:flows/shop@quarter" → "product", "flows/shop", "quarter"
	// "flows/shop@quarter" → alias error; "product:flows/shop" → branch error; "product:@x" → path error
}

func TestOpenChange(t *testing.T) {
	s := testrepo.NewShop(t)
	s.Change(t, s.Product, "quarter", [3]string{"sheets/specs/shop/sheet.toml", "shipping_fee / 2", "shipping_fee / 4"})
	work := testrepo.Clone(t, s.Orders)
	p, err := OpenChange(work, "product:flows/shop@quarter", sources.NewFetcher(t.TempDir()))
	if err != nil { t.Fatal(err) }
	if teams, why := p.TeamsOf(work); !slices.Equal(teams, []string{"Orders team"}) { t.Fatalf("%v %s", teams, why) }
	file := filepath.Join(work, "sheets", "specs", "orders", "sheet.toml")
	ov := flow.Overlay{flow.OverlayKey(file): []byte(strings.Replace(read(t, file), "fee / 2", "fee / 4", 1))}
	if q := find(p.Decide(ov).Requirements, Compute, "Orders team", "shipping_cost"); q.Status != StatusDone { t.Fatalf("%+v", q) }
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/requirements -run "ChangeRef|OpenChange"`
Expected: build failure.

- [ ] **Step 3: Implement `open.go`** as the Interfaces block says.

- [ ] **Step 4: Run the package**

Run: `go test ./internal/requirements`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/requirements
git commit -m "requirements: open a PM's change by alias:path@branch from an owner's repository"
```

---

### Task 6: Saving a sheet saves the flows it needs (T-31)

**Files:**
- Create: `internal/editor/sheetflows.go`
- Modify: `internal/editor/editor.go` (`SheetResult.Flows`, `sheet()` writes), `internal/editor/flowedit.go` (extract the version raise)
- Test: `internal/editor/sheetflows_test.go`

**Interfaces:**
- Consumes: Task 1's sheet overlay in `AnalyzeWith`.
- Produces:
  - `type FlowFix struct{ File string \`json:"file"\`; Removed []string \`json:"removed"\` }` plus unexported new content and token.
  - `SheetResult.Flows []FlowFix` (`json:"flows,omitempty"`), set by both `CheckSheet` and `SaveSheet`.
  - `func (ed *Editor) flowFixes(sheetFile string, content []byte, after *sheet.Sheet) (fixes []FlowFix, problems []Problem, err error)`. It covers every `flow.FindDirs(ed.Root)` flow whose `sheet` has no alias and is the same folder as `sheetFile` (`os.SameFile` on the folders). The fix removes the ids that `after` does not derive (missing, or an input) from each stage's `computes` and `callee_computes` and from `[output] computes`, writing through `FlowDraft.Apply` (`StagesFromDoc`/`OutputFromDoc` with the ids filtered). It raises each changed flow's version once per commit. Each fixed flow is analysed with an overlay of the sheet content and the fixed flow content. The flow errors it shows that the unedited flow (analysed from disk) did not show come back as `Problem{New: true, Diagnostic: {Severity: Error, Code: "flow", File: <flow.toml>, Message: ...}}`.
  - `func (ed *Editor) DraftOverlay(d SheetDraft) (ov flow.Overlay, eng *eval.Engine, reason string, err error)`: the draft's sheet content and its flow fixes keyed by `flow.OverlayKey`, with the draft's engine. `reason` is set when the draft cannot be evaluated (as `DraftEngine` gives it). Task 8 uses it.
  - `func (ed *Editor) raiseVersion(path string, before int, content []byte, kind tomledit.Kind) ([]byte, bool, error)`: the "raise once per commit" code that `prepare` and `flowRun` share today, moved here, with both using it.

- [ ] **Step 1: Write the failing tests** (`flowWorkspace` holds `flows/checkout-to-payment`, which pins `free_shipping` in `computes`, and `callee_computes = ["shipping_fee"]` with `carries = { fee = "shipping_fee" }`)

```go
func TestSaveSheetDropsStaleComputes(t *testing.T) {
	dir, ed := flowWorkspace(t)
	d := inputDraft(t, ed, "free_shipping", "request.order.freeShipping", "boolean") // free_shipping becomes an input row
	res, err := ed.SaveSheet(d)
	if err != nil || !res.Saved { t.Fatalf("%v %+v", err, res) }
	want := []FlowFix{{File: "flows/checkout-to-payment/flow.toml", Removed: []string{"free_shipping"}}}
	if !reflect.DeepEqual(res.Flows, want) { t.Fatalf("flows %+v", res.Flows) }
	if strings.Contains(onDisk(t, dir, want[0].File), `"free_shipping"]`) { t.Fatal("computes still names free_shipping") }
	f, _ := flow.Load(filepath.Join(dir, "flows", "checkout-to-payment"))
	if a := flow.Analyze(f, sources.NewResolver(f.Dir, f.Sources, ed.fetcher())); len(a.Errors) > 0 { t.Fatalf("%v", a.Errors) }
}

func TestCheckSheetReportsFlowFixWithoutWriting(t *testing.T) { /* same draft through CheckSheet: same Flows, flow.toml unchanged on disk */ }

func TestSaveSheetRefusesAFlowErrorItCannotFix(t *testing.T) {
	// The draft deletes shipping_fee and gives shipping_cost the formula "free_shipping ? 0 : 5":
	// callee_computes is fixed, but carries fee = "shipping_fee" is left.
	// → !res.Saved; a Problem with Code "flow", New true, File "flows/checkout-to-payment/flow.toml",
	//   Message containing "shipping_fee"; neither file changed on disk.
}

func TestSaveSheetWithoutFlowsIsUnchanged(t *testing.T) { /* workspace(t, true): res.Flows empty, saves as before */ }
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/editor -run "StaleComputes|FlowFix|CannotFix|WithoutFlows"`
Expected: FAIL (`Flows` undefined).

- [ ] **Step 3: Implement** as the Interfaces block says. In `sheet()`:
  - After `prepare`, call `flowFixes` with `p.content` and `p.after`, and skip it when `p.after` is nil.
  - Append its problems to `p.res.Problems` so `hasNew` refuses the save.
  - Add the flow writes to `ws` in the same `Store.Save` as the sheet and `cases.toml`, and return their tokens.

- [ ] **Step 4: Run the package**

Run: `go test ./internal/editor ./cmd/dsheets`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/editor
git commit -m "editor: saving a sheet also unpins rows it no longer derives from this repository's flows (T-31)"
```

---

### Task 7: The Requirements view

**Files:**
- Create: `internal/editor/requirements.go`
- Modify: `internal/requirements/check.go` (export `SameField`), `internal/requirements/output.go` (export `Shown`)
- Test: `internal/editor/requirements_test.go`

**Interfaces:**
- Consumes: Task 3's `Result`, `Report.Inputs`, `Prepared.Analysis`; Task 5's `TeamsOf`.
- Produces:

```go
// ReqView is what the Requirements panel shows.
type ReqView struct {
	Flow  string    `json:"flow"`  // the --flow reference
	Base  string    `json:"base"`
	Head  string    `json:"head"`
	Teams []string  `json:"teams"` // this repository's teams in the flow
	Why   string    `json:"why,omitempty"` // why Teams is empty
	Items []ReqItem `json:"items"`
}
type ReqItem struct {
	ID, Kind, Team, What, Rule, Status, Note string // json: lower-case names
	Mine  bool      `json:"mine"`
	Via   string    `json:"via,omitempty"`   // another team's item: "the call to <stage>"
	Cases []ReqCase `json:"cases,omitempty"`
	Rows  []string  `json:"rows,omitempty"`  // rows of this sheet producing its fields, in sheet order
}
type ReqCase struct {
	Case, Input, Field, Status, Expected, Actual, Note string // Input: "k = v, ..." sorted by key; Expected/Actual via requirements.Shown
}
func RequirementsView(ref string, res *requirements.Result, pm *flow.Analysis, teams []string, why string, draft *eval.Engine) *ReqView
```

Selection and order:
- Mine: every item whose `Team` is in `teams`.
- Others (`Mine` false): Compute (not `Remove`) and Send items of team T, for each PM call stage whose callee step's team is in `teams` and whose caller is T, when `Row` or one of its PM ancestors is carried (`CarryOf`) on that stage's request or response boundary. `Via` names the stage. With `teams` empty, every item is listed as not mine.
- Order: mine first, then others. Within each group, open, then undocumented, then done, then the requirements order.

Rows: for mine, take the rows of `draft` that send a field of the item's cases, or the item's own `Field` (an own send for output and handoff roles, a call send to the stage for request), plus the row whose id matches `Row` (`flow.Norm`). Add their derived ancestors from `draft.Graph`, leave out inputs, and keep sheet order. For others, take the rows that send any field of the via stage's response, with their derived ancestors. A nil `draft` gives no rows.

- [ ] **Step 1: Write the failing tests**

```go
func TestRequirementsViewOwnItem(t *testing.T) {
	v := view(t, "orders", nil) // helper: shop, branch quarter, OpenChange from a clone of repo, Decide(overlay), RequirementsView with TeamsOf and the draft engine
	it := item(t, v, "Compute", "shipping_cost")
	if !it.Mine || it.Status != "open" || len(it.Cases) == 0 || it.Cases[0].Expected == "" || !strings.Contains(it.Cases[0].Input, "member") { t.Fatalf("%+v", it) }
	if !slices.Equal(it.Rows, []string{"shipping_cost"}) { t.Fatalf("rows %v", it.Rows) }
}

func TestRequirementsViewDraftTurnsGreen(t *testing.T) {
	v := view(t, "orders", [][2]string{{"fee / 2", "fee / 4"}})
	if it := item(t, v, "Compute", "shipping_cost"); it.Status != "done" || len(it.Cases) != 0 { t.Fatalf("%+v", it) }
}

func TestRequirementsViewNeighbour(t *testing.T) {
	v := view(t, "ship", nil)
	it := item(t, v, "Compute", "shipping_cost")
	if it.Mine || it.Team != "Orders team" || it.Via != "the call to shipping-quote" { t.Fatalf("%+v", it) }
}

func TestRequirementsViewUnknownTeam(t *testing.T) {
	// a clone whose origin is removed: Teams empty, Why mentions "origin", every item Mine == false
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/editor -run RequirementsView`
Expected: build failure.

- [ ] **Step 3: Implement `requirements.go`** as the Interfaces block says.

- [ ] **Step 4: Run the packages**

Run: `go test ./internal/editor ./internal/requirements`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/editor internal/requirements
git commit -m "editor: the Requirements view: this team's items, neighbours' items through calls, failing cases and producing rows"
```

---

### Task 8: `serve --edit --flow` and the requirements endpoint

**Files:**
- Modify: `cmd/dsheets/main.go` (`cmdServe` flag), `cmd/dsheets/edit.go` (state, endpoint)
- Create: `cmd/dsheets/editreqs.go`
- Test: `cmd/dsheets/editreqs_test.go`

**Interfaces:**
- Consumes: Tasks 5 to 7.
- Produces:
  - Flag `--flow alias:path@branch` on `serve`. Without `--edit`, or when the argument is a flow, it is a usage error: `--flow needs --edit on a sheet`.
  - `type reqState struct{ mu sync.Mutex; ref, top string; fe *sources.Fetcher; prep *requirements.Prepared; err error; teams []string; why string }` on `editServer.reqs`, nil without `--flow`.
  - `func (es *editServer) openRequirements(ref string, fe *sources.Fetcher)` is called at start. It stores the error rather than failing, and prints `reading <ref>...` to serve's output.
  - `stateJSON` adds `"requirements": {"flow": ref}` when set.
  - `POST /edit/requirements` (guarded) takes the body `{"draft": SheetDraft, "refresh": bool}` and answers 200 with `{"view": ReqView} | {"stale": reason} | {"error": message}`:
    - `refresh` opens the change again.
    - With no prepared change, it answers `error`.
    - A draft token conflict, or a draft that cannot be evaluated (`DraftOverlay` gives a reason), answers `stale`.
    - Otherwise it answers `view` = `RequirementsView(ref, prep.Decide(ov), prep.Analysis(), teams, why, eng)`.
    - Requests are served one at a time (`reqState.mu`).

- [ ] **Step 1: Write the failing tests**

```go
func TestEditRequirementsEndpoint(t *testing.T) {
	// shop with branch quarter; es := newEditServer(<orders clone>/sheets/specs/orders); es.openRequirements("product:flows/shop@quarter", fetcher)
	// GET /edit/state → requirements.flow == the ref
	// POST /edit/requirements with the unchanged draft → view, item shipping_cost open
	// POST with the draft formula "tier == \"Silver\" ? fee / 4 : fee" → that item done
}
func TestEditRequirementsStaleDraft(t *testing.T) { /* draft formula "fee /" → {"stale": ...}, no "view" */ }
func TestEditRequirementsOpenError(t *testing.T) { /* ref with unknown branch: state still served, POST → {"error": contains "nope"}; /edit/check still works */ }
func TestServeFlowNeedsEditOnSheet(t *testing.T) { /* cmdServe([]string{"--flow", "p:f@b", dir}) → error "--flow needs --edit on a sheet" */ }
func TestEditRequirementsRefusesCrossSite(t *testing.T) { /* Origin http://evil.example → 403, as TestEditEndpointsRefuseCrossSite */ }
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/dsheets -run "EditRequirements|FlowNeedsEdit"`
Expected: FAIL.

- [ ] **Step 3: Implement** as the Interfaces block says. Use serve's own fetcher (MaxAge one minute) for `OpenChange`.

- [ ] **Step 4: Run the command tests and time one rerun**

Run: `go test ./cmd/dsheets`
Expected: PASS. Then, in `TestEditRequirementsEndpoint`, log how long the second POST takes. If a warm rerun takes over one second, cache the PM's cases in `Prepared`: add `agree.Options.Cases`, generated once in `Prepare`. If it is under one second, change nothing.

- [ ] **Step 5: Commit**

```bash
git add cmd/dsheets
git commit -m "serve: --flow opens the PM's change; /edit/requirements reruns its cases on the draft"
```

---

### Task 9: The panel in the page

**Files:**
- Modify: `internal/render/editor.js`, `internal/render/editor.css`
- Test: `internal/render/render_test.go` (only if it asserts on editor assets); browser check below

**Interfaces:**
- Consumes: `S.requirements` and the `/edit/requirements` answer from Task 8.

What it does:
- A bar button `Requirements · N open` (N counts mine with status open; `Requirements` while loading) when `S.requirements` is set; it opens the side panel on `#requirements`, as `Edit title` opens `#title`.
- `checkRequirements(refresh)` posts the draft after every `check()` answer and once on load. It keeps its own sequence number and drops older answers. It stores the view, or keeps the last view and shows the `stale` reason above it, or shows the `error` with **Fetch again** (`refresh: true`).
- The panel shows:
  - a heading, the `--flow` reference in `<code>`, and Fetch again;
  - the base and head;
  - two lists: "Your items" (with `teams` named) and "Other teams' items through your calls" (each with `via`), or `why` when there are no teams;
  - for each item, a button row with id, kind, status badge (`open`, `cannot tell` for undocumented, `done`) and `what`. Done items are folded under "N done".
- Expanding an item shows its `rule` in `<pre>`, its `note`, a cases table (Case, Input, Field, Expected, Got, and the note in a row of its own when present), and row chips. A chip opens that row; the row panel then shows a `← Requirements` link back.
- The grid marks the expanded item's rows with `tr.ed-req-row`.
- When an item of mine goes from open to done between two answers, the bar says `R-xxxxxx is done with this draft` (muted; nothing is saved yet).
- After a save, the bar lists the flow fixes: `Also saved flows/…/flow.toml: removed free_shipping from computes`. A refused save lists the flow problems as it lists sheet problems.
- CSS: reuse the editor's existing color tokens and badge styles. Light and dark both legible. At 390 px the cases table scrolls inside the panel, not the page.

- [ ] **Step 1: Implement the panel** in `editor.js` and its styles in `editor.css`.

- [ ] **Step 2: Syntax check**

Run: `node --check internal/render/editor.js`
Expected: no output.

- [ ] **Step 3: Browser check on localhost**

Run the shop fixture through serve. Build the repositories with a small Go test or script using `testrepo`, then:
`go run ./cmd/dsheets serve --edit <orders clone>/sheets/specs/orders --flow product:flows/shop@quarter`

Open `http://localhost:8080/`. Check that the panel lists `shipping_cost` open with its failing cases. Change the formula to `fee / 4`, and the item turns done without saving. Save, and the bar reports the save. Check at 390 px wide and in dark mode. If the Playwright MCP is unavailable, ask the developer to look.

- [ ] **Step 4: Run the render and command tests**

Run: `go test ./internal/render ./cmd/dsheets`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/render
git commit -m "editor: the Requirements panel"
```

---

### Task 10: Docs, the Acme replay, and closing T-31

**Files:**
- Modify: `docs/editor.md` (Requirements panel; single save of sheet and flows), `docs/requirements.md` (editor line; `--flow`), `docs/flows.md` (one line: the editor unpins rows a sheet edit stops deriving)

- [ ] **Step 1: Write the docs.** Use `requirements` output from the shop fixture as the example; no insurance or customer terms.

- [ ] **Step 2: Full test run**

Run: `git checkout -- specs/ && go test ./... && go run ./cmd/dsheets fmt --check`
Expected: every package `ok`; fmt lists nothing.

- [ ] **Step 3: Acme replay** (the developer resets the sandbox: `! rm -rf /d/pm-test && cd /d/projects/acme-corp && bash setup.sh`)

Follow `acme-corp/WALKTHROUGH.md` to the step where the PM's `move-free-shipping` branch exists, then:
1. In the PM's product repository, `dsheets requirements` prints an `open in the editor:` line for the Orders team.
2. In the Orders repository, run that line, adding `--addr localhost:8080`. The panel lists the free_shipping items.
3. In the sheet editor, turn `free_shipping` into an input and save. The bar reports `removed free_shipping from computes` in Orders' flow, the flow page opens without errors, and the items turn done. This is the T-31 check.

- [ ] **Step 4: Commit**

```bash
git add docs
git commit -m "docs: the Requirements panel, editor lines and the single save of sheet and flows"
```

- [ ] **Step 5: Record in Hopback**: T-37 completed; T-31 completed, citing the Acme step 3 result.
