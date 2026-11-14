# Requirements done-check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `dsheets requirements` closes Compute, Send, Receive and Carry items when the owning team's own sheets on its registry branch show the work, lists what dsheets cannot tell as "not documented", and prints the rule with each Compute item.

**Architecture:** `Run` compares base and head as today, then a new `Check` step reads each owner's sheets (fetched through the registry, as `impact` does) and sets a status and note on every requirement. Output hides done items and groups the rest. Every item carries structured `Row`, `Field` and `Remove` so nothing parses `Subject`.

**Tech Stack:** Go (stdlib only), the existing `internal/sources`, `internal/sheet`, `internal/flow` packages.

**Spec:** `docs/superpowers/specs/2026-11-14-requirements-done-check-design.md`

## Global Constraints

- Statuses are exactly `open`, `done`, `undocumented`.
- Exit status of `requirements` is unchanged (0 for any list, empty or not).
- New flags: `--no-check` (skip the check; every item stays `open`) and `--show-done` (also list done items). The check is on by default and honours `--offline`.
- Registry `.gitmodules` gains an optional `team` key per submodule; fallback is the alias equal to the team name's first word, lowercased; otherwise no owner.
- Schema items keep today's behaviour and ids; `--apply` is unchanged.
- No code is read; only sheets. A team with no sheet is `undocumented`, never `done`.
- Compute (remove) is `done` only in an owner that has at least one sheet.
- Rows and fields match by row id first, then by the name rule `slice` uses (case and separators ignored).

## Review Focus

- No registry, or an owner that cannot be fetched: items are `undocumented` with a note; the command still exits 0 and prints the rest.
- Owner has the row but as an input (not derived) for a Compute: stays `open` with a note, never `done`.
- Owner's derived row differs from the PM's only in whitespace: counts as the same rule.
- Everything done: output is empty (no count line), exit 0.
- `--no-check`: output equals today's, every status `open`.

---

### Task 1: Structured fields and the rule text on requirements

**Files:**
- Modify: `internal/requirements/model.go`, `internal/requirements/compare.go`
- Create: `internal/requirements/rule.go`, `internal/requirements/rule_test.go`
- Test: `internal/requirements/compare_test.go`

**Interfaces:**
- Produces: `Requirement` gains `Row string` (the end-to-end sheet row id), `Field string` (the interface field, Send and Receive only), `Remove bool` (a "stop computing" Compute), `Status string`, `Note string`, `Rule string` (all `json:",omitempty"`). Constants `StatusOpen`, `StatusDone`, `StatusUndocumented`. `func RuleText(r *sheet.Row) string` returns the canonical rule, "" for input rows.

- [ ] **Step 1: Write failing tests** in `rule_test.go` for `RuleText`, parsing rows with `sheet.Parse`:
  - formula row `free_shipping ? 0 : shipping_fee` gives `formula: free_shipping ? 0 : shipping_fee`.
  - the `free_shipping` table (hit policy first; rules `>= 100`/`-`/true, `-`/`Gold`/true, `-`/`-`/false) gives exactly
    ```
    table, first match:
      subtotal >= 100, loyalty_tier any -> true
      subtotal any, loyalty_tier Gold -> true
      subtotal any, loyalty_tier any -> false
    ```
    (hit policy `unique` prints `table, unique:`).
  - a lookup row gives `lookup: from db.shipping_zones, key country, values_file data/shipping_zones.csv`; inline values add `values: Standard=0, Silver=0.05, Gold=0.1` in `InlineOrder`.
  - each reject adds `reject when <when> -> <code>` as a last line.
  - an input row gives `""`.
  - runs of whitespace in a formula collapse to one space.
  In `compare_test.go`: `TestComputeCarriesRule` (`addHandling` pair: the `handling_fee` Compute has `Rule == "formula: shipping_fee * 0.1"`; a moved row keeps its rule; a remove Compute has `Rule == ""`) and `TestStructuredFields` (the `handlingFee` Send and Receive have `Field == "handlingFee"` and `Row == "handling_fee"`; a remove has `Remove == true` and `Row` without the ` (remove)` suffix).
- [ ] **Step 2: Run** `go test ./internal/requirements -run "RuleText|ComputeCarriesRule|StructuredFields"`; expect FAIL (undefined).
- [ ] **Step 3: Implement** `RuleText` in `rule.go` (one rule kind per row, as `Row.Rule()` says; table cell `"-"` prints `any`, other cells print their `Raw` text). In `compare.go` set `Row`, `Field`, `Remove` at every `c.add(Requirement{...})` site and `Rule: RuleText(row)` on non-remove Computes from the head row. Do not change `Subject` or ids.
- [ ] **Step 4: Run** the same command plus `go test ./internal/requirements`; expect PASS.
- [ ] **Step 5: Commit** `requirements: structured fields and the rule text on items`.

### Task 2: The registry `team` key and exported name matching

**Files:**
- Modify: `internal/sources/sources.go` (add `Team string` to `Source`), `internal/sources/registry.go` (`module.team`, parse key `team`, set on `Source`), `internal/flow/analyze.go`
- Test: `internal/sources/registry_test.go`, `internal/flow/flow_test.go`

**Interfaces:**
- Produces: `Source.Team`; `func flow.Norm(s string) string` and `func flow.NameMatch(field string, r *sheet.Row) bool` exported wrappers over the existing `norm` and `nameMatch`.

- [ ] **Step 1: Write failing tests:** `TestParseGitmodulesTeam` (a submodule with `team = "Cart team"` parses to `Source.Team == "Cart team"`; one without it has `""`); `TestNameMatch` (a row with id `free_shipping` matches field `freeShipping`; an input row `request.order.weightKg` matches `weightKg`; `zone` does not match `freeShipping`).
- [ ] **Step 2: Run** `go test ./internal/sources ./internal/flow -run "Team|NameMatch"`; expect FAIL.
- [ ] **Step 3: Implement** as listed; wrappers only, no behaviour change.
- [ ] **Step 4: Run** `go test ./internal/sources ./internal/flow`; expect PASS.
- [ ] **Step 5: Commit** `sources: team key in the registry; flow: export name matching`.

### Task 3: The check

**Files:**
- Create: `internal/requirements/check.go`, `internal/requirements/check_test.go`

**Interfaces:**
- Consumes: Task 1 fields and `RuleText`; Task 2 `Source.Team`, `flow.Norm`, `flow.NameMatch`; `sheet.Load`.
- Produces: `type fetcher interface{ Fetch(src sources.Source) (root, rev string, err error) }` (satisfied by `*sources.Fetcher`); `func Check(rs []Requirement, reg map[string]sources.Source, fe fetcher)` sets `Status` and `Note` on every requirement in place; `func ownerAlias(team string, reg map[string]sources.Source) (string, bool)`.

- [ ] **Step 1: Write failing tests** with a fake `fetcher` mapping alias to a temp folder of sheets (no git), covering:
  - `ownerAlias`: `team` key wins; else first word (`"Cart team"` finds alias `cart`); else false.
  - Compute add: owner has a derived row `free_shipping` with the same rule text as `q.Rule` -> `done`; same id but different rule -> `open`, note contains `differs`; row present only as an input -> `open`; absent -> `open`.
  - Compute remove: owner with a sheet and no derived `free_shipping` -> `done`; owner still deriving it -> `open`, note contains `still`.
  - Send: a row whose `Send.Field` equals `freeShipping` (also in `CallSends`) -> `done`; none -> `open`.
  - Receive: an input row where `flow.NameMatch(field, row)` -> `done`; none -> `open`.
  - Carry: a row whose id matches `q.Row` -> `done`.
  - no owner alias -> `undocumented`, note contains `registry`; owner folder with no `sheet.toml` -> `undocumented`, note contains `no sheets`; fetch error -> `undocumented`, note carries the error.
  - Schema items are left `open`.
  - whitespace-only formula difference counts as the same rule.
- [ ] **Step 2: Run** `go test ./internal/requirements -run Check`; expect FAIL.
- [ ] **Step 3: Implement** `Check`: group requirements by owner alias, fetch each owner once, find every `sheet.toml` under its root (skip `.git` and `.dsheets`, as `flowDirs` in `internal/impact` does), load them with `sheet.Load`, then apply the evidence rules of the spec's table. Compare rules as `RuleText` strings with whitespace runs collapsed.
- [ ] **Step 4: Run** `go test ./internal/requirements -run Check`; expect PASS.
- [ ] **Step 5: Commit** `requirements: close items from the owners' sheets`.

### Task 4: Wire the check into `Run`

**Files:**
- Modify: `internal/requirements/run.go`, `internal/requirements/model.go`
- Test: `internal/requirements/run_test.go`

**Interfaces:**
- Consumes: `Check`.
- Produces: `Options.NoCheck bool`; `Result.ShowDone bool` (`json:"-"`); `func (r *Result) Counts() (done, total int)`.

- [ ] **Step 1: Write failing tests:** `TestRunNoRegistryIsUndocumented` (a formula change in `fixtureRepo` with no registry: the Compute item has `Status == StatusUndocumented` and a note naming the registry; `Run` returns no error); `TestRunNoCheckLeavesOpen` (same change with `NoCheck`: every item `StatusOpen`).
- [ ] **Step 2: Run** them; expect FAIL.
- [ ] **Step 3: Implement:** in `Run`, set `Status = StatusOpen` on every item, then unless `o.NoCheck` load the registry (`sources.NewResolver(top, nil, fe).Registered()`, treating a missing registry as an empty map) and call `Check(rs, reg, fe)` after `locate`. Do not special-case this repository's own alias.
- [ ] **Step 4: Run** `go test ./internal/requirements`; expect PASS.
- [ ] **Step 5: Commit** `requirements: run the done-check`.

### Task 5: Output and flags

**Files:**
- Modify: `internal/requirements/output.go`, `cmd/dsheets/requirementscmd.go`
- Test: `internal/requirements/output_test.go`, `cmd/dsheets/` (the existing requirements command test file)

**Interfaces:**
- Consumes: statuses, `Rule`, `Note`, `Counts`.
- Produces: flags `--no-check`, `--show-done` on the command.

- [ ] **Step 1: Write failing tests** on a hand-built `Result`:
  - `done` items are not printed; `open` items print with `   open: <note>` when a note exists; a Compute item with a `Rule` prints each rule line indented by 12 spaces under the item.
  - `undocumented` items print after all teams under the heading `Not documented (dsheets cannot tell):`, grouped by team, each with its note.
  - the line `3 of 5 done` prints last when something is done and something remains; when nothing is open or undocumented the output is empty (no count line).
  - `ShowDone` prints done items marked `done`.
  - JSON includes every item with `status`, `note`, `rule`; `WriteFolder` files follow the markdown rules.
  - command test: `--no-check` gives today's output; `--show-done` is accepted and lists done items.
- [ ] **Step 2: Run** `go test ./internal/requirements ./cmd/dsheets -run "Requirements|Markdown|JSON|Folder"`; expect FAIL.
- [ ] **Step 3: Implement** in `output.go`'s `byTeam`/`line`/`writeTeam`/`WriteMarkdown`; add the flags and `Options.NoCheck`/`Result.ShowDone` plumbing in `requirementscmd.go`, and update the usage string.
- [ ] **Step 4: Run** `go test ./...` (slow, ~3 minutes; run it in the background); expect all PASS.
- [ ] **Step 5: Commit** `requirements: print only what is left`.

### Task 6: Docs, sandbox retest, Hopback

**Files:**
- Modify: `docs/requirements.md` (workflow step 5, the three states, the rule under Compute items, `--no-check`, `--show-done`), `docs/repositories.md` (the `team` key), `README.md` (the `requirements` row), `D:/projects/acme-corp/README.md` and `WALKTHROUGH.md`
- Modify: Hopback

- [ ] **Step 1: Edit the docs** so they match the behaviour, including that only sheets are read and a sheet-less team shows as not documented.
- [ ] **Step 2: Retest in the Acme sandbox** (`bash setup.sh`, then repeat the free-shipping walkthrough through the editors or `scenarios/2a` to `2c`): after Orders' merge only Orders' stop-computing and Receive are still listed or closed as their sheet says; after Cart's merge every item closes and `requirements` prints nothing before the flow merges. Add `team = "Cart team"`-style keys to the sandbox `.gitmodules` only if the first-word fallback fails.
- [ ] **Step 3: Update Hopback:** `hopback task update T-30 --status completed` with a note of what shipped, and state the branch is uncommitted or committed as the developer decides.
- [ ] **Step 4: Commit** `docs: requirements done-check`.
