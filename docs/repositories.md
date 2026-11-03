# Other teams' repositories

Each team keeps its sheets, flows and interfaces in its own git repository
and only ever writes there. Anything cross-team (a flow that passes through
another team's interface, a product manager's end-to-end flow, a branch off
another team's topic) reads the other team's files and never writes back.

A **registry** lists every team repository. It lets any repository refer to
another team's files by a short name, and it tells a team, in its own pull
request, which other teams' flows its change would break.

## The registry

The registry is an ordinary git repository holding one git submodule per
team repository. dsheets reads only its `.gitmodules`:

```ini
[submodule "shipping"]
    path    = shipping
    url     = https://github.com/example/shipping-service.git
    branch  = main
    dsheets = sheets            # the folder dsheets reads
[submodule "orders"]
    path    = orders
    url     = https://github.com/example/orders-sheets.git
```

| Key | Meaning |
|---|---|
| name (`"shipping"`) | The alias other repositories use: `shipping:interfaces/quote-response.toml` |
| `url` | Where to fetch the repository. A relative URL (`../orders-sheets.git`) is relative to the registry's own URL. |
| `branch` | The branch to follow. Without it, the repository's default branch. |
| `dsheets` | The folder inside the repository that holds its sheets. The alias means that folder. Leave it out when the whole repository is sheets. |

Adding a team:

```sh
git submodule add https://github.com/example/billing-service.git billing
git config -f .gitmodules submodule.billing.branch main
git config -f .gitmodules submodule.billing.dsheets sheets
git commit -am "Register billing"
```

dsheets always uses the tip of the branch, not the commit the submodule
records, so nobody has to bump the links for dsheets to see the latest. A
scheduled job or Dependabot can bump them to keep the git host's view
current; that is optional.

People browse the registry on the git host, or clone it with
`--recurse-submodules` to have every team's files locally.

Registering is the one rule every team follows: a repository that is not in
the registry is invisible to `dsheets impact`.

## Using it from a team repository

The `dsheets.toml` at the repository root names the registry:

```toml
registry = { git = "https://github.com/example/dsheets-registry.git" }   # optional: ref = "main"
```

From then on every alias in the registry works anywhere in the repository:

| Where | Example |
|---|---|
| An interface in `flow.toml` | `interface = "shipping:interfaces/quote-response.toml"` |
| The end-to-end `sheet` | `sheet = "product:specs/order-checkout"` |
| A flow on the command line | `dsheets slice orders:flows/checkout-to-payment` |
| `--with` | `dsheets slice flows/loyalty-report --with orders:flows/checkout-to-payment` |
| Several flows drawn as one grid | `dsheets serve orders:flows/checkout-to-payment shipping:flows/quote` |

A file read from another repository resolves its own references with that
repository's `dsheets.toml`, so a flow in Orders that reads a flow in
Shipping, which reads Payments, works without any extra setup.

The registry repository can hold cross-team flows itself. Its own
`dsheets.toml` says `registry = { path = "." }`, so its flows use its own
submodule list.

A plain path into a submodule folder (`sheet = "../shipping/sheets/specs/x"`)
would read the commit the submodule records, not the branch, so `slice`
warns and suggests the alias instead (`shipping:specs/x`).

## `[sources]`

`[sources]` declares aliases by hand. With a registry, most flows need none.
It remains for three cases:

```toml
[sources]
shipping = { git = "https://github.com/example/shipping-service.git", ref = "v3.2.0", dir = "sheets" }
external = { git = "https://git.example.org/other-org/contracts.git" }
orders   = { path = "../../../orders-service", dir = "sheets" }
```

- **Pinning.** `ref` names a tag or commit, freezing that team for this flow
  (during a release, say) while the registry keeps following the branch.
  Without `ref`, a source follows the repository's default branch; a branch
  name follows that branch.
- **Repositories outside the registry**, such as another organisation's
  contracts.
- **A local checkout** (`path`), to try a flow against another team's change
  that is not pushed yet.

`dir` means the same as the registry's `dsheets` key.

`[sources]` can be written in a flow's `flow.toml` (for that flow) or in the
repository-root `dsheets.toml` (for every flow in the repository). When an
alias is defined more than once, the closest wins: the flow's own, then the
repository's, then the registry.

`registry` and `[sources]` are read only from the `dsheets.toml` at the
repository root. Display settings keep their own rule (the nearest
`dsheets.toml`).

## Fetching

- dsheets fetches only each source's folder and only its latest commit: a
  shallow, sparse fetch. A team's service code and history are never
  downloaded.
- A source that follows a branch is fetched once per command, so every run
  sees the current tip. A pinned tag or commit is fetched once and then
  read from the cache.
- `dsheets serve` fetches at startup and again on a reload once the last
  fetch is a minute old.
- Clones live in `.dsheets/sources/` at the root of the repository where the
  command runs (git-ignored). Git uses your normal credentials.
- `--offline` (on `slice`, `render`, `serve` and `impact`) uses only what is
  cached. `dsheets fetch <flow>` fills the cache ahead of an offline run.
- The text report names each git source and the commit it resolved to
  (`sources: shipping@a1b2c3d`), so a failure can be reproduced.

## `dsheets impact`

Run in the producing team's pull request, from its checkout:

```sh
dsheets impact                    # this working tree against its base branch
dsheets impact --as shipping      # when the origin remote is not the registry's URL
dsheets impact --out impact.html  # also write a page with each affected flow drawn
dsheets impact --json
```

It finds this repository in the registry by its `origin` remote, fetches
every other registered repository's sheets folder, and finds every flow
that reads a file from this repository. Each of those flows is checked
twice: against the base branch and against the working tree. Only what the
change introduces is reported:

```
quote-request (Shipping team) no longer carries zone
  orders:flows/checkout-to-payment, stage shipping-quote: needed by Shipping fee (Shipping team); ask Shipping team to add it

Pinned, not affected yet
  orders:flows/pinned pins shipping to v1

1 new problem in 1 checked flow
```

The report also lists problems the change **resolves**, flows that **pin**
this repository to a tag or commit (not affected until that team moves the
pin), and anything **not checked**: a repository that could not be fetched,
or a flow that already fails at the base branch.

| Exit code | Meaning |
|---|---|
| 0 | Nothing new breaks, and every consumer was checked |
| 1 | Something new breaks |
| 2 | Usage error |
| 3 | Nothing new breaks among what was checked, but some consumers could not be checked |

Whether a code blocks a merge is up to your CI and merge rules.

This repository's own flows are not part of `impact`; its own CI checks
them with `slice`. Branch and feed links (`--with`) exist only on the command
line, so `impact` cannot see them; the consuming team's CI is the backstop.

## CI

Both jobs need read access to every registered repository. The default CI
token usually covers only the repository itself, so give the job a
read-only token (here `DSHEETS_READ_TOKEN`) and let git use it.

**Producer** (every repository whose files others read):

```yaml
name: dsheets impact
on: pull_request
permissions:
  contents: read
jobs:
  impact:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version: stable
      - run: go install github.com/NandavardhanRadhakrishnan/DerivationSheets/cmd/dsheets@latest
      - run: git config --global url."https://x-access-token:${{ secrets.DSHEETS_READ_TOKEN }}@github.com/".insteadOf "https://github.com/"
      - uses: actions/cache@v4
        with:
          path: .dsheets/sources
          key: dsheets-${{ github.run_id }}
          restore-keys: dsheets-
      - run: dsheets impact --base ${{ github.base_ref }} --out impact.html
      - if: always()
        uses: actions/upload-artifact@v4
        with:
          name: impact
          path: impact.html
```

**Consumer** (every repository with flows):

```yaml
name: dsheets flows
on:
  push:
  pull_request:
  schedule:
    - cron: "0 6 * * *"   # catches producers that merged without impact
permissions:
  contents: read
jobs:
  slice:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version: stable
      - run: go install github.com/NandavardhanRadhakrishnan/DerivationSheets/cmd/dsheets@latest
      - run: git config --global url."https://x-access-token:${{ secrets.DSHEETS_READ_TOKEN }}@github.com/".insteadOf "https://github.com/"
      - run: for f in sheets/flows/*/; do dsheets slice "$f" --strict; done
```

Both fail on any non-zero exit code by default; to treat code 3 as a
warning, wrap the command (`dsheets impact || [ $? -eq 3 ]`).
