# `dsheets requirements`: from a flow change to per-team work

Status: draft for approval (T-6). Brainstormed 2026-11-13.

## Intent

Teams own interfaces (API requests and responses, Kafka topics) in their
own repositories. Flows that cross teams live in the registry repository or a
team repository. A product manager changes a flow's input and output, meets
the teams concerned, and uses `dsheets slice` to settle who computes, sends
and carries what. When they agree, the PM commits the flow.

Today nothing turns that agreement into work. This design adds a command that
compares the flow before and after and writes, per team, the list of things
that team's code has to do, in a logical order, so a team lead can turn it
into work items. It also makes the interface edits those items imply,
in the owner's repository, ready for the owner's own pull request.

It works the same whether the flow was edited in the editor, by an LLM or by
hand, because it only reads git. It has no dependency on a CI or tracker
platform.

## Goals

- One flat, ordered list of atomic requirements per team for a flow change.
- Each requirement says what to do, why (the flow row or boundary it comes
  from), and what it needs (other requirements), so a developer who missed
  the meeting can act on it.
- Markdown for people, JSON for scripts that create tracker items.
- Interface edits written into the owner's repository on a branch, so the
  owner's development pull request holds both the contract change and the
  code, and a reviewer can check one against the other.
- Re-runnable: as owner pull requests merge, the list shrinks. An empty list
  means the flow can merge.

## Non-goals

- Grouping requirements into tickets. How many items a team makes is the
  team lead's or PM's call.
- Creating tickets or opening pull requests. dsheets never calls a platform
  API. The only network write it makes is `git push` of the `--apply`
  branch, through the developer's own git credentials.
- Ordering teams against each other or flagging cross-team coordination. The
  `needs` links sort items within the output; they are informational.
- Checking that code matches the sheet. The reviewer does that.
- Merging many repositories into a single abstraction. A team and a
  repository are treated as the same here; several repositories per team
  comes later.

## Anti-goals

- Writing into another team's repository without being asked. Only `--apply`
  writes, only into the clones it names or makes, and only a new branch: it
  never touches an existing branch, never force-pushes, and never pushes to
  the default branch.
- Reporting noise. Label and formatting changes stay out, as in `diff`.
- A report that cannot be recomputed. Requirements must come from git state,
  not from a stored file that can drift.

## The workflow

1. Teams and the PM agree a change in a meeting, using `slice`. The PM
   commits the flow on a branch named by their own convention and opens a
   draft pull request. That is the agreement.
2. `dsheets requirements <flow>` compares the flow on that branch with the
   default base (the same base `diff` uses) and prints the requirements per
   team. The PM posts it on the pull request.
3. `dsheets requirements <flow> --apply` finds the owner repositories with
   Schema items, makes a sparse shallow clone of each repository's dsheets
   folder under `.dsheets/work/<alias>` (or uses a clone passed with
   `--apply alias=path`), cuts a new branch from the tip of the branch the
   registry names for that repository (its default branch when the registry
   gives none), edits the interface file with `tomledit`, commits, and pushes
   the branch to that repository's remote. The branch is named
   `dsheets/<flow>` unless `--branch alias=name` says otherwise. The
   developer renames it to their team's convention and starts the
   implementation on it, and one pull request in that repository holds both.
   `--no-push` stops after the local commit. dsheets does not open the pull
   request.
4. Each owner pull request merges when ready. The PM's flow pull request
   merges last. Until then it reports missing fields, which is the unfinished
   work. Interface changes arrive on the default branch, which every alias
   already follows, so no branch lookup in other repositories is needed.
5. Rerunning `requirements` shows what is left. When it prints nothing, the
   flow can merge.

## Requirement kinds

| Kind | Team | Means |
|---|---|---|
| Compute | the computing team | A row is added to the team's `computes`, its formula, table or lookup changed, or the computing team changed (the old team gets a removal). Carries the before and after. |
| Send | the producer of a boundary | A boundary now carries a field, or fills it from a different row. Points at the source: a Compute, a Carry, or an original input. |
| Receive | the consumer of a boundary | The consumer now gets a field it must read. |
| Carry | a team that holds a value it does not compute | The value arrives, must be kept (across a call, in the caller's own state) and used or forwarded at a later boundary. |
| Schema | the interface's owner | A field must be added or changed because the flow needs something the contract lacks. Never appears alone. Names the repository and file, and is what `--apply` writes. |

A Send needs a value source but not always a new Compute. A Send or Carry
needs a Schema item only when the interface does not already declare the
field.

Requirements are ordered by their `needs`: Schema first, then Compute, then
Carry, Send and Receive, in a stable order within each. Within a team the
list reads as: add this field to the request schema, compute this value with
this formula, send it in this response.

## Output

```
Shipping team: checkout-to-payment (base 502c48a)
 R-3a1f  Schema   add `surcharge` (number, required) to quote-response.toml
 R-91c2  Compute  shipping_fee: subtotal * 0.05 -> subtotal * 0.05 + surcharge   needs R-3a1f
 R-77e0  Send     `surcharge` in quote-response, from shipping_fee                needs R-91c2
```

Each requirement has: `id` (stable, derived from kind, team, flow and
subject, so reruns keep the same ids), `kind`, `team`, `repo`, `flow`,
`subject`, `what`, `before`, `after`, `why`, `needs`. `--json` prints them
all; a folder form (`--out dir`) writes one markdown and one JSON file per
team.

## Linking the pull requests

Teams name branches their own way (initials, sprint, user story,
description), and the convention differs per team, so no name is shared
across repositories and nothing depends on one. Branch names are never used
to find other repositories' work: the flow merges last, after the owner
pull requests, so every alias is read from its followed branch.

Linking is for people and trackers, and it comes from the requirements
themselves:

- Requirement ids are stable (derived from kind, team, flow and subject), so
  a tracker item that holds `R-3a1f` stays linked through every rerun.
- `--apply` commits with git trailers: `Dsheets-Requirement: R-3a1f` (one
  per requirement the commit satisfies) and `Dsheets-Change: <label>`. The
  label is `--change <text>`, defaulting to the flow's name; the PM can set
  it to their story or epic id. Trailers survive any branch naming, show in
  the owner's pull request, and are found with `git log --grep`.
- Trailers and branch names do not survive a squash merge or a rewrite, and
  a platform's squash message is not under dsheets' control. File contents
  and the pull request do survive. So `--apply` also writes a small record
  file in the same commit, `<dsheets folder>/changes/<label-slug>.md`,
  listing the change label, the flow, the base it was compared with and the
  requirement ids that commit's edits satisfy. It travels with the diff
  through any merge style, and `git log -- changes/` finds it later. It is a
  traceability aid only: nothing reads it to decide what is done. `--apply`
  also prints a pull request description with the same ids, to paste.
- `--branch alias=name` (repeatable) sets each owner branch. A repository
  without one gets `dsheets/<flow>`, which the team can rename with
  `git branch -m` before pushing.

Whether a requirement is done is never read from a link. It is read from the
diff shrinking as owner pull requests merge.

## Approaches considered

- **A. Post-process `diff.Report`.** Cheapest. Rejected: `diff` sees only the
  repository being diffed, so it misses interface changes and sheet changes
  in other teams' repositories; it cannot tell a new Send from an existing
  one because it does not know where a value comes from; it has no Carry.
- **B. Analyse the flow at base and at head and compare the analyses
  (chosen).** `flow.Analyze` already resolves the whole flow across
  repositories and records who computes each row, how each field travels
  (passes through, kept across a call), what the interface lacks, and which
  team owns each step. `impact` already analyses base and head. Requirements
  are the cells that differ. `diff.Report` still supplies before and after
  text and the label filter.
- **C. Diff exported per-team pieces.** Natural grouping, but it repeats
  what the analysis gives and pieces are an export format, not a comparison
  format. Rejected.

For writing into other repositories: emit patches only (rejected as the only
mode, because it leaves a person retyping); apply into a clone (chosen);
open pull requests through a platform API (rejected: platform dependency).
For merge timing: the flow merges after development (chosen) so `main`
describes what is live and the diff stays recomputable.

## Constraints

- Git-native and platform-independent. The base is found as `diff` finds it.
  The base flow is read at the base revision with the revision-aware reader
  in `internal/store`.
- dsheets writes only in its own repository, except under `--apply`, which
  pushes a new branch to an owner repository. That needs the developer to
  have push access there; without it, `--no-push` and a fork are the way.
- Other repositories are read through the registry and its cache; no clones
  are needed to read.
- Interface edits go through `internal/tomledit` so untouched lines stay
  byte-for-byte as they were.

## Decisions as the developer made them

- Requirement count is not computed into tickets; teams decide.
- Cross-team ordering is out of scope; `needs` orders the output.
- Interface changes ride in the owner's development pull request, together
  with the code.
- The flow merges after development, not before.
- The flow pull request needs no view of unmerged interface branches: it
  merges after the owner pull requests.
- Teams document at different depths, and interfaces are the minimum every
  team has. `--apply` writes interface edits only. A team's own internal
  sheets or docs are updated by the team; dsheets neither writes them nor
  lists an item for them, and there is no per-team setting for it.
- Branch names follow each team's own convention and are never used for
  linking; linking survives squash through the record file and requirement
  ids.

## Left to the builder

- The comparison of two `flow.Analysis` values and the mapping of differing
  cells to kinds.
- The exact shape of ids, the markdown layout, and error wording.
- Sparse clone mechanics and branch handling in `--apply`.

## Calls I made (change them if wrong)

- Command name `requirements`, taking a flow like `slice` and `impact`.
- A fifth kind, Receive, for the consumer of a new field.
- Stable ids derived from content, so reruns and trackers line up.
- `--apply` writes interface (Schema) edits only. Compute, Send, Receive and
  Carry are code changes, which belong to the team.
- `--apply` pushes by default (the developer's decision), with `--no-push`
  as an escape; it refuses if the branch name already exists on the remote,
  so a rerun never overwrites. Renaming a pushed branch leaves the old name
  on the remote until the developer deletes it; this is their git step.
- `--apply` names branches from `--branch alias=name`, falling back to
  `dsheets/<flow>`; commits carry `Dsheets-Requirement` and `Dsheets-Change`
  trailers (see Linking).
- A derived row that no stage lists in `computes` is still placed by
  `flow.Analyze` in the latest step able to serve it, so it always has a
  team. Its Compute requirement is marked `placed: default`, meaning nobody
  named it in the flow, so the PM can pin it. A step with no team name is
  grouped under "No team named", as the analysis already labels it.
- Sparse shallow clone of just the dsheets folder, to avoid pulling a
  service's application code.
- A sheet used by several flows gives requirements per flow, through that
  flow's team assignment. A sheet used by no flow gives none, and the
  output says so.
- Label and formatting changes are excluded; `--all` adds them, as in `diff`.

## Open for the developer

None.
