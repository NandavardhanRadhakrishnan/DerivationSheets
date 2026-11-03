# Cross-repository references and impact checks

Status: agreed in conversation on 3 November 2026; not built.

## Why

Each team keeps its sheets, flows and interfaces in its own git repository
and writes only there. Cross-team work (a flow that passes through another
team's interface, a product manager's end-to-end flow, a branch off another
team's topic) must be able to read another team's files.

Two things go wrong without more support:

1. Today only interfaces can come from another repository. A flow's
   end-to-end `sheet`, the flows given to `--with`, and the flows drawn in a
   combined grid must be local paths.
2. A producing team cannot see who consumes its files. Not every signature
   change gets a meeting with every team; a team may change a field
   believing nobody reads it. The team must learn, **in its own pull
   request, before merging**, which other teams' flows the change breaks.

## Goals

- Any reference in dsheets can point at a file in another team's repository:
  interfaces, the end-to-end sheet, `--with` flows, and flow folders given to
  `render` and `serve`.
- References follow a branch (normally `main`) by default, so a change
  merged by the owning team is picked up by everyone on their next run,
  manual or CI.
- A registry repository lists every team repository. Its git submodules are
  the list; there is no second registry format to keep in sync.
- A new command, `dsheets impact`, run in the producing team's pull request,
  reports which flows in other repositories the change breaks.
- Whatever fails is visible: the report and the rendered page name the
  commit each source resolved to.

## Non-goals

- dsheets never writes to another team's repository.
- dsheets does not decide whether a failing impact check blocks a merge. It
  reports and sets the exit code; the organisation's CI and merge rules
  decide.
- No copies of other teams' files are committed anywhere (no subtrees).
- No hosted service: everything runs from git and the CLI.

## Anti-goals

- A design that needs someone to bump a pin every time a team merges.
- An impact report that repeats problems that already existed before the
  pull request. Teams would learn to ignore it.

## The registry repository

An ordinary git repository whose content is one git submodule per team
repository:

```ini
# .gitmodules in the registry repository
[submodule "shipping"]
    path    = shipping
    url     = https://github.com/example/shipping-service.git
    branch  = main
    dsheets = sheets            # the folder dsheets reads
[submodule "orders"]
    path    = orders
    url     = https://github.com/example/orders-sheets.git
```

- The submodule **name** is the alias: `shipping:interfaces/quote-response.toml`.
- `branch` is the branch dsheets follows; without it, the repository's
  default branch.
- `dsheets` names the folder inside the repository that holds its sheets,
  flows and interfaces. An alias means that folder: with the entry above,
  `shipping:interfaces/quote-response.toml` is
  `sheets/interfaces/quote-response.toml` in Shipping's repository. A
  repository that holds only sheets leaves the key out, and the alias means
  its root. Git ignores keys it does not know, so the key does not disturb
  normal submodule use.
- People browse the registry on the git host, or clone it with
  `--recurse-submodules` to have every team's files locally.
- dsheets reads only `.gitmodules`. It ignores the commit each submodule
  records and always uses the tip of the named branch, so nobody has to bump
  the links for dsheets to see the latest. A scheduled job or Dependabot
  (which supports submodules) may bump them so the git host's view stays
  current; that is optional.
- A submodule links a whole repository; git cannot link a folder. The
  `dsheets` key is how the registry narrows it to the sheets folder, and
  dsheets downloads only that folder (see Fetching).
- The registry repository may hold flows of its own (for example product
  managers' end-to-end flows). Its own `dsheets.toml` says
  `registry = { path = "." }`, so its flows resolve aliases through its own
  `.gitmodules`. A `.gitmodules` file alone never makes a repository the
  registry, because team repositories may have unrelated submodules. Where
  cross-team flows live is up to the organisation: any registered
  repository works.
- A plain path into a submodule folder (`sheet = "../shipping/specs/x"`)
  would read the recorded commit, not the branch, and `impact` would not
  see it as a reference to `shipping`. `slice` and `validate` warn on any
  plain path that lands inside a submodule of the current repository and
  suggest the alias form (`shipping:specs/x`).
- Registering is the one rule every team must follow: a repository that is
  not in the registry is invisible to `impact`.

## How a team repository uses the registry

The `dsheets.toml` at the **repository root** names it. `registry` and
`[sources]` are repository-wide and are read only from the root file;
display settings keep their current rule (the nearest `dsheets.toml` wins),
so a subfolder file with only `[display]` never hides the registry. The
project-file loader must accept these new keys, which today it rejects.

```toml
registry = { git = "https://github.com/example/dsheets-registry.git" }   # optional: branch = "main"
```

From then on every alias in the registry can be used in that repository
without declaring it.

`[sources]` stays, at two levels, for pins and for repositories outside the
registry (an external organisation, a local checkout):

```toml
# dsheets.toml: for every flow in the repository
[sources]
external = { git = "https://git.example.org/other-org/contracts.git" }

# flow.toml: for this flow only
[sources]
shipping = { git = "https://github.com/example/shipping-service.git", ref = "v3.2.0", dir = "sheets" }  # frozen for a release
```

A git source in `[sources]` takes `dir`, with the same meaning as the
registry's `dsheets` key: the folder the alias means, default the root.

What `[sources]` is for once the registry exists:

- **Pinning**: freeze a team at a tag or commit for one flow, or for the
  whole repository, while the registry keeps following the branch.
- **Repositories outside the registry**: an external organisation's
  contracts, for example.
- **A local checkout** (`path`): try a flow against another team's change
  that is not pushed yet.

Alias lookup order: the flow's `[sources]`, then the repository's
`dsheets.toml` `[sources]`, then the registry. The first match wins.

`ref` becomes optional for git sources. Without it, the source follows the
repository's default branch. A tag or commit pins it. A branch name follows
that branch. This replaces today's "a git source needs ref" error.

## References

One form everywhere: `alias:path`, path relative to the root of the aliased
repository. A reference without an alias stays a path relative to the file
that contains it (or to the working directory, on the command line).

Newly accepted:

| Where | Example |
|---|---|
| `sheet` in `flow.toml` | `sheet = "product:specs/order-checkout"` |
| `slice --with` | `dsheets slice flows/loyalty-report --with orders:flows/checkout-to-payment` |
| `render` / `serve` flow arguments | `dsheets serve orders:flows/checkout-to-payment shipping:flows/quote` |
| `fetch` | `dsheets fetch orders:flows/checkout-to-payment` |

Interface references already accept it.

On the command line, aliases resolve through the `dsheets.toml` of the
current working directory's repository.

**Nested references.** A file loaded from another repository resolves its own
references with that repository's configuration (its `dsheets.toml`, its
registry, its flows' `[sources]`), exactly as it would at home. In practice
the registry is shared, so aliases mean the same thing everywhere. All
clones go into the cache of the repository where the command was run, never
into a cached clone.

## Fetching

- **Only the sheets folder, only the latest commit.** A git source is
  fetched shallow (one commit, no history) and sparse (only the files under
  its `dsheets` / `dir` folder). Other files in the repository, such as
  service code, are never downloaded. The exact git commands (for example
  `clone --depth 1 --filter=blob:none --sparse` and `sparse-checkout set`,
  or `fetch --depth 1 origin <commit>` for a pinned commit) are left to the
  builder, as long as no full clone is ever made.
- **Only what is needed.** Ordinary commands fetch only the repositories
  their references name, lazily. `impact` is the one command that fetches
  every registered repository's sheets folder, because it must look for
  consumers.
- CI may cache `.dsheets/sources/` between runs, so a later run only
  fetches what changed; the CI examples show this.
- Every command that resolves a git source fetches it first, at most once
  per alias per run, so a run always sees the current tip of a followed
  branch. Pinned tags and commits are fetched only when missing locally.
- `--offline` uses whatever is cached and never touches the network; a
  missing clone is an error, as today.
- `dsheets fetch` stays, to fill the cache ahead of an offline run.
- `serve` fetches at startup, then on a page reload only when its last
  fetch is more than a minute old, so editing and reloading stays fast and
  the page never goes stale for long. The resolver's fetch state lives for
  the server's lifetime.
- Clones live in `.dsheets/sources/` at the repository root (git-ignored),
  as today. Git uses the caller's credentials.
- The text report, `--json` output and rendered pages name each source and
  the commit it resolved to (`shipping@a1b2c3d`), so a failure can be
  reproduced. This exists for interfaces today; it extends to every kind of
  reference.

## `dsheets impact`

Run in the producing team's pull request, from its checkout:

```sh
dsheets impact                 # this repository's working tree against its main branch
dsheets impact --as shipping   # when the alias cannot be found from the git remote
dsheets impact --json
dsheets impact --out impact.html
```

What it does:

1. Find this repository's alias: the registry submodule whose URL matches
   this repository's `origin` remote (URLs compared after normalising
   scheme, `.git` suffix and `git@host:` vs `https://host/`), or `--as`.
2. Fetch every registered repository at its followed branch.
3. Find every flow (`flow.toml`) in every **other** registered repository,
   including the registry itself, that references a file in this
   repository: its `sheet`, its input, output and stage interfaces, and its
   `[sources]`. Direct references only; each flow names every file it
   reads. This repository's own flows are not part of `impact`; its own CI
   checks them with `slice`, and a flow being drafted in a pull request is
   expected to have open problems.
4. Check each of those flows twice, as `slice --strict` would: once with
   this repository resolved to its base branch (the "before"), once with
   this repository resolved to the local working tree (the "after").
5. Report only what is **new** in "after": missing fields, fields that no
   longer fit, ordering errors, and references that no longer resolve (a
   renamed or deleted file). Problems already present before are not
   repeated.
6. Also list, as information, problems the change **resolves**, and flows
   that reference this repository through a pin (`ref = "v3.2.0"`) and are
   therefore not affected until that team moves the pin.

A registered repository that cannot be fetched, or a consumer flow that
cannot be loaded at its base branch, is listed by name in the report under
**Not checked**, and the check continues with the rest. A flow that loads at
the base branch but not with this change is a new break, not "not checked".

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Nothing new breaks, and every consumer was checked |
| 1 | Something new breaks (takes precedence over 3) |
| 2 | Usage error |
| 3 | Nothing new breaks among what was checked, but some consumers could not be checked |

What CI does with each code is the organisation's choice; the examples
fail on 1 and 3.

**Not covered: branch and feed links.** A `--with` relationship exists only
on the command line, so `impact` cannot see it. Consumer CI that runs
`slice --with` is the backstop. Recording `with` in `flow.toml` is possible
later.

The text report groups by what changed, then by the consuming team and
flow, naming the boundary, the field and the rows that read it:

```
quote-response (Shipping team) drops fee
  orders:flows/checkout-to-payment, stage shipping-quote: needed by Shipping cost (Orders team)
```

`--json` gives the same as data (`--json` is new for `impact` only). `--out`
writes one self-contained page:
the new problems grouped by consuming team, then each affected flow drawn
with `--check` against the pull request.

## CI

Documentation ships two GitHub Actions examples:

- **Producer** (every repository with interfaces or sheets others read): on
  pull request, run `dsheets impact`.
- **Consumer** (every repository with flows): on push, on pull request, and
  on a schedule, run `dsheets slice --strict` on its flows. This is the
  backstop that catches what `impact` cannot (an unregistered producer, a
  merge that skipped the check).

Both need read access to every registered repository. The default CI token
usually covers only the repository itself, so the examples show a read-only
token or app installation configured for git (`url.<base>.insteadOf` or a
credential helper).

## Decided in conversation

- No mandatory central repository for references; the registry exists to
  make consumers known to producers before they merge.
- The registry is a repository of git submodules, chosen over a separate
  registry file.
- Sources follow `main` (or the default branch) by default; pins stay
  possible.
- Producers see breakage in their own pull request; consumer CI is the
  backstop.
- Blocking or warning is the organisation's CI policy, not dsheets'.
- Only each team's sheets folder is fetched; CI never clones whole
  repositories.

## Calls made by Claude (open to change)

- Submodule name is the alias; the recorded submodule commit is ignored.
- A `dsheets` key in `.gitmodules` (and `dir` in `[sources]`) names the
  sheets folder; aliases mean that folder; fetches are shallow and sparse.
- Alias lookup order: flow `[sources]`, repository `[sources]`, registry.
- `ref` optional, meaning the default branch.
- `impact` reports only new problems, plus resolved ones and pinned flows
  as information.
- `impact` checks direct references only, and only other repositories'
  flows. Branch and feed links (`--with`) are not covered; consumer CI is
  the backstop.
- Unreachable repositories are listed as "Not checked" with exit code 3.
- The registry marks itself with `registry = { path = "." }`.
- Plain paths into submodule folders get a warning suggesting the alias.
- `registry` and `[sources]` come only from the repository-root
  `dsheets.toml`.
- Every run fetches followed branches once per alias; `--offline` opts out.
  `serve` refetches on reload at most once a minute.
- Clones always go into the invoking repository's cache.

## Left to the builder

- How the "before" checkout of this repository is obtained (a cached clone
  of `origin` at the base branch, or `git worktree` from the local
  repository); `--base <ref>` to override the base branch.
- How findings are keyed to compare before and after (boundary, field and
  kind of problem is the natural key).
- How `flow.toml` files are found in a repository (walk the tree, skipping
  `.git` and `.dsheets`).
- Error wording, and whether `impact` checks registered repositories in
  parallel.
- Updating `docs/flows.md`, the README and the JSON Schemas for the new
  `ref` rule, `registry`, and repository-level `[sources]`.
