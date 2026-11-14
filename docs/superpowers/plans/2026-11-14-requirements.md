# `dsheets requirements` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `dsheets requirements <flow>` lists, per team, the ordered requirements a flow change implies; `--apply` writes the interface edits to a new branch in each owner repository.

**Architecture:** A new package `internal/requirements` compares `flow.Analyze` at the base revision with `flow.Analyze` at head (base materialised from git with a new `store.Revs.Export`, repository substituted as `impact` does) and maps differing cells to five kinds. `diff.DiffSheet` supplies before/after text for Compute. Output (markdown, JSON, per-team folder) lives in the same package. `--apply` lives in `internal/requirements/apply.go` and shells out to git; interface edits use `internal/tomledit`.

**Tech Stack:** Go, existing packages `flow`, `diff`, `store`, `sources`, `iface`, `tomledit`; git CLI. Tests build temp git repos as `internal/diff/run_test.go` does.

**Spec:** `docs/superpowers/specs/2026-11-13-requirements-design.md`

## Global Constraints

- Command name `requirements`, taking a flow argument like `slice` (`alias:path` or a folder).
- Default base is the one `diff` uses: `impact.FollowedBranch`, then merge-base with head (reuse `diff` base resolution; do not reimplement).
- Label and formatting changes excluded; `--all` includes them, as in `diff`.
- Requirement ids stable: derived from kind, team, flow and subject; format `R-` plus 4 hex chars of a SHA-1.
- Kinds: Compute, Send, Receive, Carry, Schema. Order: Schema, Compute, Carry, Send, Receive; stable within each.
- A Compute for a row no stage lists in `computes` is marked `placed: default`; a step with no team is "No team named".
- dsheets writes only in its own repo except `--apply`: new branches only, never force-push, never touch an existing or default branch, refuse if the branch exists on the remote.
- `--apply` writes interface (Schema) edits only, via `internal/tomledit` (untouched lines byte-for-byte).
- Commit trailers `Dsheets-Requirement: <id>` (one per id) and `Dsheets-Change: <label>`; record file `<dsheets folder>/changes/<label-slug>.md`.
- Neutral terminology: "target", generic examples (order-checkout); no insurance jargon.
- Before running tests or merging: `git checkout -- specs/` (editor tryouts dirty fixtures).

## Review Focus

- A flow with no change: prints nothing, exit 0 (not an empty heading).
- Base flow does not exist (new flow): every requirement is an addition; no panic on nil base analysis.
- Base or head analysis has errors: report the errors, exit 1, no partial list.
- A sheet used by no flow: output says so rather than silence.
- Interface file already declares the field: no Schema item.
- `--apply` twice: second run refuses (branch exists remotely or locally) and changes nothing.
- Windows paths and CRLF interface files survive the tomledit edit.

---

### Task 1: Export a revision and compare analyses' inputs

**Files:**
- Modify: `internal/store/revs.go`
- Test: `internal/store/revs_test.go`

**Interfaces:**
- Produces: `func (r *Revs) Export(rev, dest string) error` writes the whole tree of `rev` under `dest` (via `git archive rev | tar -x`, or `git worktree`-free equivalent that works on Windows: `git archive --format=tar` piped into `archive/tar` in Go).

- [ ] **Step 1: Write failing test `TestExport`**: commit file `a/b.txt` = "one", change to "two" in a second commit; `Export(firstRev, dir)` yields `dir/a/b.txt` containing "one"; unknown rev returns an error.
- [ ] **Step 2:** Run `go test ./internal/store -run TestExport` → FAIL (undefined).
- [ ] **Step 3: Implement `Export`** with `git -C top archive --format=tar rev` and Go's `archive/tar`; reject entries escaping `dest`.
- [ ] **Step 4:** Run same test → PASS.
- [ ] **Step 5:** `git add internal/store && git commit -m "store: export a revision's tree"`.

### Task 2: Requirement model, ids and ordering

**Files:**
- Create: `internal/requirements/model.go`, `internal/requirements/model_test.go`

**Interfaces:**
- Produces:
  - `type Kind string` with consts `Schema, Compute, Carry, Send, Receive` (values "Schema" etc.).
  - `type Requirement struct { ID string; Kind Kind; Team, Repo, Flow, Subject, What, Before, After, Why string; Placed string; Needs []string; File string }` with JSON tags in snake_case, empty fields omitted. `File` is set only on Schema (interface file path relative to its repo's top).
  - `func MakeID(kind Kind, team, flow, subject string) string`
  - `func Sort(rs []Requirement)` orders by kind rank (Schema, Compute, Carry, Send, Receive), then team, then subject.

- [ ] **Step 1: Write failing tests** `TestMakeIDStable` (same inputs → same `R-xxxx`, differing subject → differing id, matches `^R-[0-9a-f]{4}$`) and `TestSortOrder` (given one of each kind in reverse, result order is Schema, Compute, Carry, Send, Receive).
- [ ] **Step 2:** Run `go test ./internal/requirements` → FAIL.
- [ ] **Step 3: Implement** the types, `MakeID` (SHA-1 of `kind|team|flow|subject`, first 4 hex), `Sort` (`sort.SliceStable`).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit "requirements: model, ids and order".

### Task 3: Compare two analyses

**Files:**
- Create: `internal/requirements/compare.go`, `internal/requirements/compare_test.go`
- Consumes: `flow.Analysis` fields `Place`, `Steps`, `Boundaries` (`Items`, `Iface`, `CarryOf`), `Transit`; `diff.DiffSheet(base, head []byte) (SheetChange, error)`.

**Interfaces:**
- Produces: `func Compare(base, head *flow.Analysis, all bool) ([]Requirement, error)`; `base` may be nil (new flow). Both non-nil analyses must have no `Errors`; otherwise return an error naming the side and first error.
- Mapping (each decided from the analyses, keyed by row id and boundary):
  - **Compute:** a row whose team (`Steps[Place[row]].Team()`) differs, is new, or whose sheet row changed per `diff.DiffSheet` of the two `Sheet.File` contents (label changes dropped unless `all`) → Compute on the head team with `Before/After` from the row's FieldChanges; if the team changed, a Compute removal ("stop computing") on the base team. `Placed = "default"` when no step's pins listed the row (expose via a new exported method `(*Analysis) Pinned(row string) bool` in `internal/flow`).
  - **Send:** a boundary item that is Carried at head and not (same boundary, same field, same row) at base → Send on the producing step's team (the step before the boundary); `Needs` the row's Compute id, or the Carry id when the row is not computed by that team, else none (original input).
  - **Receive:** the same item → Receive on the consuming step's team (step after the boundary).
  - **Carry:** `Transit[step][row]` is `"kept"` or `"in"` at head and not at base, on that step's team, `Needs` the Send or Compute that provides the value.
  - **Schema:** a Missing item at head, not Missing at base (or a head Warn with `Fix`), on `Iface.Owner`; `What` like `add \`surcharge\` (number, required) to quote-response.toml`; `File` = interface file relative to its repo; the Send/Receive/Compute items for that field `Need` it.
- Why text: `why` names the flow row id or boundary (`Role`, `StageID`) it comes from.

- [ ] **Step 1: Write failing tests** with in-memory analyses built from the `examples`/`specs/order-checkout` style fixtures under `t.TempDir()` (write sheet, flow, interface files; analyse twice with one edit). Cases, each asserting kinds, teams and `Needs`:
  1. `TestCompareFormulaChange`: changing `shipping_fee` formula yields exactly one Compute with Before/After and no Send.
  2. `TestCompareNewFieldOnBoundary`: new row sent in a response whose interface lacks it yields Schema → Compute → Send → Receive, with Needs chain Schema ← Compute ← Send.
  3. `TestCompareLabelOnlyExcluded`: a description change yields no requirements; with `all=true` still none for flow cells (labels only appear in Compute `before/after`) — assert it yields one Compute with `Label` text.
  4. `TestCompareTeamMoved`: moving a row between steps' `computes` gives a Compute for the new team and a removal for the old.
  5. `TestCompareDefaultPlacement`: unpinned new derived row gives `Placed == "default"`.
  6. `TestCompareNilBase`: nil base turns every head cell into additions without panic.
  7. `TestCompareErrors`: analysis with `Errors` returns an error.
  8. `TestCompareNoChange`: identical analyses give an empty slice.
- [ ] **Step 2:** Run `go test ./internal/requirements -run TestCompare` → FAIL.
- [ ] **Step 3: Implement `Compare`** and `(*Analysis).Pinned` as specified; call `Sort` and fill `Needs` by id after all items are made. Approach: build maps from (boundary role+stage, field) and (step, row) at both sides, then iterate head cells not present at base.
- [ ] **Step 4:** Run → PASS; also `go test ./internal/flow`.
- [ ] **Step 5:** Commit "requirements: compare two analyses".

### Task 4: Run against git: base, head, default base

**Files:**
- Create: `internal/requirements/run.go`, `internal/requirements/run_test.go`
- Consumes: `store.Revs` (`NewRevs`, `Resolve`, `MergeBase`, `Export`, `Top`), `impact.FollowedBranch`, `sources.Fetcher.WithSubstitute`.
- Modify: `internal/diff/run.go` — export the base logic as `func ResolveBase(revs *store.Revs, base, head string, as string, fe *sources.Fetcher) (baseSHA, headSHA string, err error)` and make `Run` call it (behavior unchanged; `internal/diff` tests must still pass).

**Interfaces:**
- Produces:
  ```go
  type Options struct { Flow string; Dir string; Base, Head string; As string; All bool; Fetcher *sources.Fetcher }
  type Result struct { Flow, Base, Head string; Requirements []Requirement; Orphan bool /* a sheet used by no flow */ }
  func Run(o Options) (*Result, error)
  ```
  `Flow` is a folder (already resolved by the caller). Head analysis uses the working tree; base analysis exports the base commit to a temp dir, loads the flow at the same relative path, and substitutes this repository for its URL (as `impact.Run` does); a flow absent at base gives a nil base.

- [ ] **Step 1: Write failing tests** with a temp git repo (copy `specs`-style fixture, commit, branch, edit sheet, commit): `TestRunFormulaChange` (one Compute for the changed row, base = merge-base), `TestRunNewFlowNoBase` (flow added on branch → all additions), `TestRunNoChange` (empty result), `TestRunExplicitBase` (`Base` given).
- [ ] **Step 2:** Run `go test ./internal/requirements ./internal/diff` → FAIL.
- [ ] **Step 3: Implement** `ResolveBase` refactor and `Run`; remove the temp dir with `defer os.RemoveAll`.
- [ ] **Step 4:** Run both packages → PASS.
- [ ] **Step 5:** Commit "requirements: read the base and head of a flow from git".

### Task 5: Output (markdown, JSON, per-team folder)

**Files:**
- Create: `internal/requirements/output.go`, `internal/requirements/output_test.go`

**Interfaces:**
- Produces: `func (r *Result) WriteMarkdown(w io.Writer) error`, `func (r *Result) WriteJSON(w io.Writer) error`, `func (r *Result) WriteFolder(dir string) error`.
- Markdown format per spec: a `<Team>: <flow> (base <short sha>)` heading per team, then lines ` R-3a1f  Schema   add ... ` with `needs R-…` at the end. Empty result prints nothing. `Orphan` prints one line: `No flow uses this sheet, so there are no requirements.`
- JSON: `{ "flow", "base", "head", "requirements": [...] }`.
- Folder: `<dir>/<team-slug>.md` and `<dir>/<team-slug>.json`.

- [ ] **Step 1: Write failing tests** `TestMarkdownLayout` (exact lines for the spec's Shipping example), `TestMarkdownEmpty` (no output), `TestJSONShape` (round-trips, empty fields omitted), `TestWriteFolder` (two teams → four files).
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit "requirements: markdown, JSON and folder output".

### Task 6: The command

**Files:**
- Create: `cmd/dsheets/requirementscmd.go`, `cmd/dsheets/requirementscmd_test.go`
- Modify: `cmd/dsheets/main.go` command table (add after `impact`): `{"requirements", "<flow> [base]..[head] [--all] [--json] [--out dir] [--as alias] [--offline] [--apply ...]", "list what each team's code must do for a flow change, in order", cmdRequirements}`

**Interfaces:**
- Consumes: `argPath`, `newFetcher`, `newFlags`, `parse`, `requirements.Run`.
- Flags: `--all`, `--json`, `--out dir`, `--as`, `--offline`; positional 1 = flow, optional 2 = `base..head`. Exit 0 with nothing printed when no requirements; `errFailed` when an analysis has errors.

- [ ] **Step 1: Write failing tests** (temp repo, as `diffcmd_test.go`): `TestRequirementsPrints`, `TestRequirementsJSON`, `TestRequirementsEmptyIsQuiet`, `TestRequirementsUsage` (no args → `errUsage`).
- [ ] **Step 2:** Run `go test ./cmd/dsheets -run TestRequirements` → FAIL. **Step 3:** Implement. **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit "dsheets requirements".

### Task 7: `--apply` (interface edits on a branch)

**Files:**
- Create: `internal/requirements/apply.go`, `internal/requirements/apply_test.go`
- Modify: `cmd/dsheets/requirementscmd.go` (flags `--apply` (repeatable `alias=path` for existing clones), `--no-push`, `--branch alias=name` (repeatable), `--change label`)

**Interfaces:**
- Produces:
  ```go
  type ApplyOptions struct { Clones map[string]string; Branches map[string]string; Change string; NoPush bool; Work string /* .dsheets/work */; Registry map[string]sources.Source }
  type Applied struct { Repo, Branch, Dir string; IDs []string; Pushed bool; Description string }
  func Apply(r *Result, o ApplyOptions) ([]Applied, error)
  ```
- Behavior, per owner repo with Schema items:
  1. Clone: `git clone --depth 1 --filter=blob:none --sparse --branch <registry ref> <url> <Work/alias>`, then `git sparse-checkout set <dsheets folder>`; or use `Clones[alias]`. Refuse a clone with uncommitted changes.
  2. Branch: `git switch -c <Branches[alias] or dsheets/<flow>>` from the followed branch tip; refuse if the branch exists locally or (unless `NoPush`) on the remote (`git ls-remote --heads`).
  3. Edit: for each Schema item, add a `[[field]]` (name, type, required) to the interface file with `tomledit` (`Doc.Insert`), skipping a field already declared; write with `Doc.Bytes()`.
  4. Record file `changes/<label-slug>.md` under the dsheets folder listing change label, flow, base and ids.
  5. Commit with trailers; push `origin <branch>` unless `NoPush`.
  6. Print a pull-request description with the same ids.
  Never force-push; never switch to or commit on the default branch.

- [ ] **Step 1: Write failing tests** using a bare temp repo as the remote and a clone: `TestApplyAddsField` (field appended, other lines byte-identical, record file written, trailers present, branch pushed to bare remote), `TestApplyNoPush` (branch only local), `TestApplyRefusesExistingBranch` (second run errors, nothing changed), `TestApplySkipsDeclaredField`, `TestApplyUsesGivenClone`, `TestApplyCRLF` (CRLF interface keeps CRLF).
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit "requirements --apply: interface edits on a new branch".

### Task 8: Docs and hand-off checks

**Files:**
- Create: `docs/requirements.md` (workflow, kinds, output, `--apply`, linking, re-running; generic order-checkout example)
- Modify: `README.md` command list, `docs/flows.md` (one link line)

- [ ] **Step 1:** Write `docs/requirements.md` from the spec's workflow, kinds, output and linking sections (neutral terms, no insurance jargon).
- [ ] **Step 2:** `git checkout -- specs/ && go vet ./... && go test ./...` → all PASS.
- [ ] **Step 3:** Run `go run ./cmd/dsheets requirements flows/checkout-to-payment` in the repo and read the output for sense (show it to the developer; do not add features to present it).
- [ ] **Step 4:** Commit "docs: dsheets requirements".

---

**Self-review:** Spec coverage: kinds and ordering (T2, T3), base/head and default base (T4), output formats and `--out` (T5), flags and exit (T6), `--apply` with branch, trailers, record file, no-push, refusal (T7), docs (T8), orphan sheet (T4/T5). Types consistent: `Requirement`, `Result`, `Options`, `ApplyOptions` are defined once and reused. Anything the spec leaves to the builder (id shape, layout, wording) is fixed above.
