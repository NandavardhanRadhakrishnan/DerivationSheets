# Row-level diff (`dsheets diff`) design

Backlog: T-5. Shares its comparison with T-6 (implementation work per team),
which is brainstormed separately afterwards.

## Intent

A reviewer opening a change to a sheet or a flow should see what the change
means, not which TOML lines moved: which rows were added, removed or
altered, and for flows which fields and computations changed for each team.
`dsheets diff` produces that from two git revisions.

## Goals

- Git-native. The command knows only git revisions. It never calls a CI
  platform API, so it works the same under GitHub, Azure DevOps, GitLab,
  Jenkins or a laptop. Platforms post its output from one step in the
  user's own pipeline.
- Output is markdown (readable as plain text) on stdout, and `--json` for
  tooling. T-6 builds on the structured form.
- Sheets first, flows second, in the same pull request.

## Non-goals

- No platform integrations, no PR-comment posting.
- No CI gating. It exits 0 whenever it produces a diff; non-zero only for
  real errors (unknown ref, unreadable file, no default branch found).
  (Developer decision.)
- CSV lookup data is never compared. (Developer decision.)
- Impact on other teams' consumers is out of scope; that belongs to T-6.
  Only the repo being diffed is considered.
- The git diff driver (`.gitattributes`) is a later step, after the diff
  works.

## Usage

`dsheets diff [base]..[head]`

- With no arguments, compares the merge-base of the default branch and
  `HEAD` against `HEAD`, which is what a pull request shows.
- Default branch (my call on the fallback order): `origin/HEAD` if set, then
  `origin/main`, `origin/master`, then local `main`, `master`. If none exist,
  fail with a message asking for an explicit base. An explicit base always
  wins. `origin/HEAD` is often unset (it is in this repo), so the fallbacks
  matter.
- `internal/impact` already takes a `Base` option ("the branch the registry
  follows" when empty) and passes it to git as a ref; it has no git
  default-branch lookup of its own. The new resolver should live where both
  commands can call it. Open for the plan: when a registry is configured,
  does `dsheets diff` use the registry's followed branch (as `impact` does)
  before falling back to the chain above? (My suggestion: yes, so the two
  commands never disagree.)
- Both sides are read from git objects, never from the working tree.
  `store.Committed(path)` cannot do this: it only runs `git show HEAD:./path`.
  This work adds a revision-aware reader to `internal/store` (read a file at
  a revision, list the sheets and flows present at a revision, e.g. with
  `git show <rev>:path` and `git ls-tree`). T-6 reuses it.
- Flags: `--all` includes label and style changes; `--cases` includes
  `cases.toml`; `--json` for structured output.

## What a sheet diff shows

- Rows are matched by `id`. A changed `id` is one row removed and one added.
- Added and removed rows are listed individually. This includes every row of
  a workflow folder that was added or deleted as a whole. (Developer
  decision.)
- A row that exists on both sides lists each changed logic field with its
  before and after values. Logic fields: `formula`, `source`, `type`,
  `send`, and the other behaviour-bearing fields; the builder confirms the
  full list from `tomledit` and the schema. (Developer decision: before and
  after values are required.)
- Nested parts (`[row.send]`, `[row.table]` and its ranges, each
  `[[row.reject]]` entry) are broken down to the exact part that changed,
  not shown as a whole block before and after: for example "reject entry 2:
  message changed" or "table range 100-200 became 100-250".
  (Developer decision.) How entries inside a list are matched (by a key
  where one exists, otherwise by position) is left to the builder.
- By default only logic changes appear. Label changes (`name`, `title`),
  comments and layout appear only with `--all`, in the way `git diff -w`
  hides whitespace changes. (Developer decision.)
- `cases.toml` changes appear only with `--cases`. CSVs and other data files
  are never shown.
- A sheet with no logic change is not listed.
- Sheets are matched by their `sheet` name, not folder path, so moving a
  folder is not reported as delete plus add. (My call.)
- `base..head` means the same as in git: both revisions are compared
  directly. Only the no-argument form uses the merge-base. (My call.)

## What a flow diff shows

Grouped by team, where "team" means a repo (developer decision; for the time
being, only the team that made the change, i.e. the repo being diffed).
Per team, two things (developer decision):

- fields added or dropped at its boundaries and interfaces;
- changes in who computes what, e.g. a field the caller computed that the
  callee now computes.

Where the data lives (from `internal/flow`): handoff and call stages
(`KindHandoff`, `KindCall`), `StageSpec.Owner()` (the team that computes the
rows before the stage), and for calls `Caller`, `Callee` and
`callee_computes`. The builder maps these onto the diff and reuses
`impact.Finding`'s vocabulary ("input", "output", "stage <id>") where it
fits. If a field dropped here is consumed by another team, that consequence
is T-6's job.

## Constraints

- Go, existing stack. Reuse `tomledit` to parse both sides into rows; do not
  write a second parser. Reading a revision is new code in `internal/store`
  (see Usage), not a reuse of `Committed`.
- Put the comparison in a package that T-6 can call (returns structured
  changes; markdown and JSON are renderers over it). My call.
- Teams' files live next to application code, so only files dsheets already
  discovers are read. The command must not look at unrelated TOML.

## Left to the builder

- Exact markdown layout, as long as added, removed and changed are
  scannable and before/after are on adjacent lines.
- JSON schema.
- Behaviour on a file that fails to parse on one side (suggest: report the
  file as unparseable on that side rather than failing the whole diff).
- Tests, including a repo with a stubbed default branch.

## Later

- `.gitattributes` diff driver, scoped by filename or folder, never
  `*.toml`; falls back to plain text diff when the file cannot be parsed or
  `dsheets` is missing. Only after the command works.
