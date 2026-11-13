# `dsheets diff`

Shows what changed between two git revisions as sheet rows and flow
boundaries, instead of lines of TOML. It reads both sides from git, so it
works the same under any CI platform and on a laptop; it never calls a
platform API.

```
dsheets diff                  # this branch against the default branch (what a pull request shows)
dsheets diff main..HEAD       # two revisions, compared directly
dsheets diff v1.2             # v1.2 against HEAD
dsheets diff --json           # the same report as JSON
```

Flags: `--all` adds label and formatting changes, `--cases` adds
`cases.toml`, `--json` prints JSON, `--color auto|always|never` controls color. It exits 0 whenever it produced a
report, including "no differences"; it exits non-zero only for real errors
(an unknown revision, no git repository, no default branch found). It never
fails a build because of what changed.

## How it prints

Each change is a line starting with `-` (before) or `+` (after), under the
row it belongs to, as `git diff` shows them:

```diff
  customs_value (changed)
- formula: subtotal - discount
+ formula: subtotal - (discount * 0.9)
```

- **In a terminal** the lines print as they are, `-` in red and `+` in
  green. `--color never` or the `NO_COLOR` environment variable turns the
  color off; `--color always` keeps it when piping, for example into
  `less -R`.
- **Anywhere else** (a pipe, a file, a CI step) the output is markdown with
  each section in a fenced `diff` block. GitHub, GitLab and Azure DevOps
  draw those blocks with red and green lines, so posting the output as a
  comment or summary needs no extra step.

## Which revisions

With no argument the base is the merge-base of the base branch and `HEAD`.

The base branch is the one the registry follows for this repository, the
same branch `dsheets impact` compares with, so the two never disagree. It is
found as `origin/<branch>`, or `<branch>` if there is no such remote branch.
The repository's alias is found from its `origin` remote; `--as` names it
when that does not work, and `--offline` uses the registry as already
fetched. If a registry is configured but cannot be read, or follows a branch
this repository does not have, diff stops and asks for an explicit base
instead of guessing.

With no registry, or when the registry follows the remote's default branch,
the base branch is the first that exists of `origin/HEAD`, `origin/main`,
`origin/master`, `main` and `master`; `origin/HEAD` is often unset (it is when
a remote was added after `git init`), so the others matter. If none exists,
pass a base.

With `base..head`, both revisions are compared directly, exactly as
`git diff base head` does, and the registry is not consulted. Uncommitted
edits are never included.

## What is compared

Only the files that changed between the two revisions are read.

- **Sheets** (`sheet.toml`), matched by their `sheet` name, so moving a
  folder shows as that sheet's changes, not a deletion and an addition.
  Rows are matched by `id`. A row that exists on both sides lists each
  changed part with its before and after values, down to the exact part:
  `send.field`, `table.rows[true,>= 2]`, `reject[NOT_SERVED].message`.
  Table rules are matched by their input cells, so reordering rules is not
  a change. An added or deleted sheet lists every row.
- **Flows** (`flow.toml`): stages added, removed or with a new kind or team;
  for each boundary (input, each stage's handoff, request and response, the
  output) the fields it now carries, no longer carries, or now fills from a
  different row, and a changed interface; and **who computes what**: every
  row whose computing team changed.
- **Interfaces** (a `.toml` file with a top-level `interface` key): fields
  added, removed, or with a changed type, `required` or `enum`.
- The report covers the repository being diffed. What a change means for
  other teams' consumers is not part of it.

By default only changes to behaviour appear. Names, titles, descriptions,
`[display]` settings and comments are label and formatting changes, shown
with `--all`, much as `git diff -w` hides whitespace.

A flow's own settings are compared too: the sheet it reads, its `sources`
(including a pinned `ref`), the output's producer, and a renamed `flow` or
`sheet`. A row id that appears twice is told apart by occurrence (`tax#2`).

Never compared: CSV and other data files, hidden folders, TOML files that
are not sheets, flows or interfaces (`Cargo.toml` and the like), and
`cases.toml` unless `--cases` is given. A file that is not valid TOML on one
side is reported as such, and the rest of the report still prints.

## In CI

Run `dsheets diff` in a step of your own pipeline and post its markdown
wherever your platform shows text: a pull-request comment, a job summary
or a build artifact. For example, writing it to a file and attaching it:

```
dsheets diff > dsheets-diff.md
```

dsheets does not need to know which platform runs it. A shallow clone must
fetch enough history for the base, for example `fetch-depth: 0` on GitHub
Actions.

## Later

A git diff driver (`.gitattributes`) so that plain `git diff` and
`git log -p` show row-level changes locally. It will be scoped by filename
or folder and never by `*.toml`, and fall back to the text diff for any file
it cannot read.
