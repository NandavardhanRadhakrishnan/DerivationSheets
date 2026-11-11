# Row-level diff (`dsheets diff`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `dsheets diff [base]..[head]` prints what changed between two git revisions as row-level sheet differences and, for flows, as boundary and compute differences.

**Architecture:** A revision-aware git reader in `internal/store` supplies file contents at any revision. A new pure package `internal/diff` decodes the two TOML sides into generic maps and compares them (sheets by row `id`, nested parts by exact path; flows by stage, boundary and "who computes row X"), producing a structured `Report`. `cmd/dsheets/diffcmd.go` renders it as markdown or JSON. The structured `Report` is what T-6 will build on.

**Tech Stack:** Go (module `github.com/NandavardhanRadhakrishnan/DerivationSheets`), `github.com/pelletier/go-toml/v2`, `git` on PATH, existing `internal/flow` and `internal/iface` parsers.

**Spec:** `docs/superpowers/specs/2026-11-11-row-level-diff-design.md`

## Global Constraints

- Git-native: only git revisions and `git` subprocesses; no CI-platform API.
- Reads both sides from git objects, never the working tree. `store.Committed` stays as is (HEAD only).
- Exit 0 whenever a diff is produced (including "no differences"); non-zero only for real errors (unknown ref, no git repo, no default branch found). Never a CI-gating flag.
- CSV and other data files are never compared. `cases.toml` only with `--cases`.
- Logic changes only by default; label/style changes only with `--all`.
- Rows matched by `id`; sheets matched by their `sheet` name (folder moves are not delete+add).
- Before and after values shown; nested parts broken down to the exact path (e.g. `send.field`, `table.rows[true,>= 2]`), never the whole block.
- "Team" = a repo: the diff covers only the repo being diffed. Other teams' consumers are T-6.
- Read only files that changed between the revisions; never parse unrelated TOML.
- Windows is a supported platform: slash-separated repo-relative paths in all output; tests must not depend on `/tmp`.
- No second TOML parser: decode with `go-toml/v2` (already the project's parser); reuse `flow.Parse` and `iface.Parse`.

## Review Focus

- A change to a non-dsheets `.toml` (e.g. `Cargo.toml`) is ignored and never errors.
- A file that fails to parse on one side is reported as unparseable on that side; the rest of the diff still prints.
- A comment-only or whitespace-only edit to `sheet.toml` shows nothing by default; with `--all` it shows "formatting or comments only".
- Reordering rows or table rules with no content change shows nothing.
- Moving a sheet folder (same `sheet` name) shows as the sheet's changes, not delete+add.
- A base with no default branch discoverable gives an error that says to pass an explicit base.
- Two revisions with no dsheets differences print "No sheet or flow differences." and exit 0.

---

### Task 1: Revision-aware git reader

**Files:**
- Create: `internal/store/revs.go`
- Test: `internal/store/revs_test.go` (reuse the `run`, `write` helpers in `store_test.go`)

**Interfaces:**
- Produces:
  - `var ErrNotInRev = errors.New("not in that revision")`
  - `type Revs struct{ top string }` — `top` is the repository's top-level folder.
  - `func NewRevs(dir string) (*Revs, error)` — `dir` inside a git working tree; error otherwise.
  - `func (r *Revs) Top() string` — absolute top-level folder.
  - `func (r *Revs) Resolve(rev string) (sha string, err error)` — `git rev-parse --verify <rev>^{commit}`.
  - `func (r *Revs) MergeBase(a, b string) (string, error)`
  - `func (r *Revs) DefaultBranch() (string, error)` — first that resolves of `origin/HEAD`, `origin/main`, `origin/master`, `main`, `master`; else an error whose text contains `pass an explicit base`.
  - `type Change struct{ Status byte; Path string }` — `Status` is `'A'`, `'D'` or `'M'`; `Path` slash-separated, relative to `top`.
  - `func (r *Revs) Changed(base, head string) ([]Change, error)` — `git diff --name-status --no-renames base head` (two-dot: direct comparison).
  - `func (r *Revs) ReadAt(rev, path string) ([]byte, error)` — `git show rev:path`; missing path returns an error matching `ErrNotInRev`.

- [ ] **Step 1: Write failing tests** in `revs_test.go` against a temp repo (`t.TempDir()`, `git init -b main`, commits made with the `run` helper): `TestRevsReadAt` (content at first and second commit; missing path `errors.Is(err, ErrNotInRev)`), `TestRevsChanged` (an added, a deleted and a modified file give statuses `A`, `D`, `M`; a moved file gives `D` plus `A`), `TestRevsMergeBase` (branch off `main`, commit on both; merge-base equals the fork commit), `TestRevsDefaultBranchFallback` (repo with local `main` and no remote → `"main"`; repo whose only branch is `trunk` → error containing `pass an explicit base`), `TestRevsNotGit` (plain temp dir → error).
- [ ] **Step 2: Run** `go test ./internal/store -run Revs` — expect FAIL (undefined).
- [ ] **Step 3: Implement** the API above in `revs.go` with `exec.Command("git", "-C", top, ...)`; for `origin/HEAD`, try `git rev-parse --verify --quiet refs/remotes/origin/HEAD^{commit}`.
- [ ] **Step 4: Run** `go test ./internal/store` — expect PASS (existing store tests included).
- [ ] **Step 5: Commit** `git add internal/store && git commit -m "Read files and changes at any git revision"`

### Task 2: Generic value comparison and sheet diff

**Files:**
- Create: `internal/diff/model.go`, `internal/diff/values.go`, `internal/diff/sheet.go`
- Test: `internal/diff/values_test.go`, `internal/diff/sheet_test.go`

**Interfaces:**
- Produces (`model.go`):
  - `type Status string` with consts `Added`, `Removed`, `Changed` = `"added"`, `"removed"`, `"changed"`.
  - `type FieldChange struct{ Path, Before, After string; Label bool }` — `Path` like `formula` or `table.rows[true,>= 2]`; `Before` empty for additions and `After` empty for removals; `Label` marks label/style fields; values formatted with `fmt.Sprint`-style TOML-ish text (strings unquoted unless empty).
  - `type RowChange struct{ ID string; Status Status; Fields []FieldChange }`
  - `type SheetChange struct{ Name, Path, OldPath string; Status Status; Meta []FieldChange; Rows []RowChange; TextOnly bool; Unparseable string }` — `Name` is the `sheet` slug; `OldPath` set only when the folder moved; `TextOnly` means decoded content equal but bytes differ; `Unparseable` is `"base"`, `"head"` or `""`.
- Produces (`values.go`): `func diffMaps(path string, before, after map[string]any, key listKeyFunc) []FieldChange` and `type listKeyFunc func(path string, elem any, parent map[string]any) (key string, ok bool)`. Maps compare per key recursively (`path.key`); scalar and scalar-list values compare by `reflect.DeepEqual` after normalizing integers/floats (`1` equals `1.0`); a list of maps or of lists is matched by the key function (elements it has no key for are matched by position, path `name[i]`), added/removed elements become single changes with `Before`/`After` holding the element text.
- Produces (`sheet.go`): `func DiffSheet(base, head []byte) (SheetChange, error)` — `base` or `head` may be `nil` for added/removed sheets (all rows listed individually as added/removed with their fields); unparseable sides are reported via `Unparseable`, not an error. Top-level `title` is a label; `version`, `request_schema`, `target_schema` are logic. Row labels: `name`, `description`, `open_questions`. All other row keys are logic. Row-list keys: `table.rows` keyed by the cells before the result column (all but the last element of each rule; use the text of each cell joined with `,`, e.g. `true,>= 2`); `reject` keyed by `code`; `send` when a list keyed by `target` + `field`; all else by position.

- [ ] **Step 1: Write failing tests** (inline TOML strings, taken from the shapes in `specs/order-checkout/sheet.toml`):
  - `TestDiffMapsNested`: `send.field` change gives one `FieldChange{Path:"send.field", Before:"salesTax", After:"taxAmount"}`; `send.map` with one key changed gives only `send.map.Gold`.
  - `TestDiffSheetFormulaChange`: formula edit gives `RowChange{ID:"total", Status:Changed}` with one logic field.
  - `TestDiffSheetTableRule`: changing one table result gives path `table.rows[true,>= 2]` with before `Gold`, after `Platinum`; reordering rules alone gives no `Rows`.
  - `TestDiffSheetRejectByCode`: changing a reject `message` gives `reject[DESTINATION_NOT_SERVED].message`.
  - `TestDiffSheetRowsAddedRemoved`: a row added and a row removed each listed individually.
  - `TestDiffSheetLabelOnly`: a `name` edit appears with `Label:true`.
  - `TestDiffSheetCommentOnly`: base and head differ only in a comment → no rows, `TextOnly:true`.
  - `TestDiffSheetWholeAddedAndRemoved`: `base == nil` lists every head row as `Added` (each with its fields); `head == nil` lists every row as `Removed`.
  - `TestDiffSheetUnparseable`: invalid TOML in `head` → `Unparseable:"head"`, no error.
- [ ] **Step 2: Run** `go test ./internal/diff` — expect FAIL.
- [ ] **Step 3: Implement** `model.go`, `values.go`, `sheet.go`. Decode with `toml.Unmarshal` into `map[string]any`; `rows` come from `raw["row"].([]any)`; match by `id`; `Rows` sorted in head order with removed rows after.
- [ ] **Step 4: Run** `go test ./internal/diff` — expect PASS.
- [ ] **Step 5: Commit** `git add internal/diff && git commit -m "Compare two versions of a sheet row by row"`

### Task 3: Flow and interface diff

**Files:**
- Create: `internal/diff/flow.go`
- Test: `internal/diff/flow_test.go`

**Interfaces:**
- Consumes: `flow.Parse(dir, file string, text []byte) (*flow.Flow, error)`; `iface.Parse(path string, data []byte) (*iface.Interface, error)`; `FieldChange`, `Status` from Task 2.
- Produces (`flow.go`):
  - `type BoundaryChange struct{ Where string; Interface FieldChange; Added, Dropped []string; Rewired []FieldChange }` — `Where` is `"input"`, `"output"` or `"stage <id> <role>"` where role is `handoff`, `request` or `response` (same wording as `impact.Finding.Where`); `Interface` has a non-empty `Path` only when the boundary's interface reference changed; `Added`/`Dropped` are carried field names; `Rewired` is a field whose carried row changed (`Path` = field, `Before`/`After` = row IDs).
  - `type ComputeChange struct{ Row, Before, After string }` — who computes a row; `Before`/`After` are team names as written in the flow (`producer`, `caller`, `callee`), empty when nobody does.
  - `type FlowChange struct{ Name, Path string; Status Status; Stages []FieldChange; Boundaries []BoundaryChange; Computes []ComputeChange; TextOnly bool; Unparseable string }` — `Stages` lists stages added, removed, or with `kind`/owner changes (by stage ID).
  - `type InterfaceChange struct{ Name, Path string; Status Status; Meta []FieldChange; Fields []FieldChange; TextOnly bool; Unparseable string }` — `Fields` per field `name`: added, removed, or `type`/`required`/`enum` changed (`description` is a label).
  - `func DiffFlow(base, head []byte) (FlowChange, error)` and `func DiffInterface(base, head []byte) (InterfaceChange, error)` — `nil` sides allowed as in `DiffSheet`.
- "Who computes row X" is built per side from: each stage's `Computes` → `Owner()`; each call stage's `CalleeComputes` → `Callee`; `Output.Computes` → `Output.Producer`.

- [ ] **Step 1: Write failing tests** with small inline `flow.toml` and interface TOML modeled on `examples/walkthrough/*/flow.toml` (copy one and trim): `TestDiffFlowCarriesAddedDropped` (a field added and one dropped on a handoff), `TestDiffFlowComputeMoved` (row removed from caller's `computes` and added to `callee_computes` → `ComputeChange{Row, Before:<caller>, After:<callee>}`), `TestDiffFlowStageAdded`, `TestDiffFlowNoChange` (identical → empty change), `TestDiffInterfaceFields` (field added, one `required` flipped, one `type` changed), `TestDiffFlowUnparseable`.
- [ ] **Step 2: Run** `go test ./internal/diff -run 'Flow|Interface'` — expect FAIL.
- [ ] **Step 3: Implement** in `flow.go`. `flow.Parse` problems (schema errors) are ignored here: only the parsed fields are compared. Match stages by `ID`.
- [ ] **Step 4: Run** `go test ./internal/diff` — expect PASS.
- [ ] **Step 5: Commit** `git add internal/diff && git commit -m "Compare flows and interfaces: boundaries and who computes what"`

### Task 4: Run: discover changed files and assemble the report

**Files:**
- Create: `internal/diff/run.go`
- Test: `internal/diff/run_test.go` (temp git repos like Task 1; copy the `run`/`write` helpers locally)

**Interfaces:**
- Consumes: `store.Revs` (Task 1), `DiffSheet`, `DiffFlow`, `DiffInterface` (Tasks 2–3).
- Produces:
  - `type Options struct{ Dir string; Base, Head string; All, Cases bool }` — empty `Base` means merge-base of `DefaultBranch()` and `Head`; empty `Head` means `HEAD`.
  - `type CasesChange struct{ Sheet, Path string; Status Status; Cases []FieldChange }` — only filled with `Options.Cases`; cases listed by their `id` key.
  - `type Report struct{ Repo, Base, Head string; Sheets []SheetChange; Flows []FlowChange; Interfaces []InterfaceChange; Cases []CasesChange }` — `Base`/`Head` are the resolved short SHAs.
  - `func Run(o Options) (*Report, error)`.
- Behavior: `Revs.Changed(base, head)`; only `.toml` files are considered. A file is a sheet if its name is `sheet.toml`, a flow if `flow.toml`, `cases.toml` is handled only with `Cases`; any other `.toml` is read at both revisions and is an interface only if it has a top-level `interface` key — otherwise ignored silently. Files under hidden folders (any path part starting with `.`) and `data/` folders are ignored. Sheets pair up across revisions by `sheet` slug (a deleted and an added `sheet.toml` with the same slug become one `SheetChange` with `OldPath` set); same for flows by `flow` slug. Results sorted by path. Drop sheets, flows and interfaces that have no changes, unless `TextOnly` or `Unparseable` is set.
- `Repo` is the base name of `Revs.Top()`.

- [ ] **Step 1: Write failing tests**: `TestRunSheetChange` (two commits editing a formula; `Report.Sheets` has one row change), `TestRunIgnoresOtherToml` (a `Cargo.toml` change adds nothing and does not error), `TestRunFolderMove` (`git mv` of a sheet folder plus a formula edit → one `SheetChange` with `OldPath`), `TestRunNewSheetListsRows`, `TestRunDeletedSheet`, `TestRunCsvIgnored` (CSV under `data/` change → empty report), `TestRunCasesFlag` (a `cases.toml` change is absent without `Cases` and present with it), `TestRunDefaultBase` (feature branch off `main`, no args → compares against `main`'s fork point, not working tree), `TestRunBadRef` (error mentions the ref).
- [ ] **Step 2: Run** `go test ./internal/diff -run Run` — expect FAIL.
- [ ] **Step 3: Implement** `Run`. Read each side with `ReadAt`, treating `ErrNotInRev` as `nil`.
- [ ] **Step 4: Run** `go test ./internal/diff` — expect PASS.
- [ ] **Step 5: Commit** `git add internal/diff && git commit -m "Run a diff between two revisions"`

### Task 5: Output and the `dsheets diff` command

**Files:**
- Create: `internal/diff/render.go`, `cmd/dsheets/diffcmd.go`, `docs/diff.md`
- Modify: `cmd/dsheets/main.go:66` (add the command to `commands`), `BACKLOG.md` (item 8), `README.md` (list the command, wherever commands are listed)
- Test: `internal/diff/render_test.go`, `cmd/dsheets/diffcmd_test.go`

**Interfaces:**
- Consumes: `diff.Run`, `diff.Report`.
- Produces:
  - `func (r *Report) WriteMarkdown(w io.Writer, all bool) error` — header `## dsheets diff <Repo>: <Base>..<Head>`; sections per sheet, flow, interface; per row, `Before → After` on adjacent lines in a fenced or indented block; label changes shown only when `all`; `TextOnly` files shown only when `all` as "formatting or comments only"; empty report prints `No sheet or flow differences.`
  - `func (r *Report) WriteJSON(w io.Writer) error` — indented JSON of the `Report` struct (add `json` tags in `model.go`/`flow.go`/`run.go` as part of this task, snake_case, `omitempty` for empty lists).
  - `func cmdDiff(args []string, out, errw io.Writer) error` — usage `[base]..[head] [--all] [--cases] [--json]`. Positional forms: none; `base`; `base..head`; `base..` and `..head`. `..` splits on the first `..`; three dots are an error (usage). Uses `os.Getwd()` as `Options.Dir`. Returns nil whenever a report was written.

- [ ] **Step 1: Write failing tests**: `TestWriteMarkdownRowChange` (golden string for one changed row with two logic fields and one label field: label absent with `all=false`, present with `all=true`), `TestWriteMarkdownEmpty`, `TestWriteJSONRoundTrip` (marshal then unmarshal equals report), `TestCmdDiffExitsZeroWithChanges` (temp repo; `cmdDiff` returns nil and output contains the changed row id), `TestCmdDiffBadRef` (returns non-nil error, not `errFailed`), `TestCmdDiffArgForms` (`main..HEAD`, `main`, `main..`).
- [ ] **Step 2: Run** `go test ./internal/diff ./cmd/dsheets -run 'Markdown|JSON|CmdDiff'` — expect FAIL.
- [ ] **Step 3: Implement** the renderers, `cmdDiff` with `newFlags("diff", "[base]..[head] [--all] [--cases] [--json]", errw)` and `parse` as in `diffcmd`'s neighbors (`fmtcmd.go`), register it in `commands` ("show what changed between two git revisions as sheet rows and flow boundaries"), write `docs/diff.md` (usage, defaults, flags, what is and is not compared, a CI snippet that pipes `dsheets diff` markdown into a job summary or PR comment without naming a platform API, and a "Later: git diff driver, scoped by folder or filename, never `*.toml`" note), and tick item 8 in `BACKLOG.md`.
- [ ] **Step 4: Run** `git checkout -- specs/` then `go vet ./... && go test ./...` — expect PASS. Run `go run ./cmd/dsheets diff` and `go run ./cmd/dsheets fmt --check` in the repo; both succeed.
- [ ] **Step 5: Commit** `git add internal/diff cmd/dsheets docs/diff.md BACKLOG.md README.md && git commit -m "Add dsheets diff"`

---

## Self-review

- **Spec coverage:** git-native and revision reader (T1); default base fallback (T1, T4); no-arg merge-base (T4); `base..head` semantics (T5); row matching by id, nested exact paths, before/after, label vs logic, whole sheets listed, `cases` flag, CSV excluded, sheet match by name, unparseable side (T2, T4); flows per repo with fields and who-computes (T3); markdown and JSON (T5); always exit 0 (T5); structured report for T-6 (`Report`); diff driver left for later (docs note).
- **Types:** `FieldChange`, `Status`, `SheetChange`, `FlowChange`, `InterfaceChange`, `Report` are defined once and used with the same names in later tasks.
- **Left open on purpose:** whether `dsheets diff` should prefer the registry's followed branch (as `impact` does) before the git default-branch chain. This plan implements only the git chain; the registry preference is a small follow-up once decided.
