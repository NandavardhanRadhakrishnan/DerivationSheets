# `dsheets requirements`

Turns a change to a flow into the work each team's code has to do, in a
logical order, so a team lead can make work items from it. It compares the
flow as it was with the flow as it is now, using only git: it works the same
whether the flow was edited in the editor, by an LLM or by hand, and it never
calls a platform API.

```
dsheets requirements flows/checkout-to-payment             # this branch against the default branch
dsheets requirements flows/checkout-to-payment main..HEAD  # two revisions, compared directly
dsheets requirements flows/checkout-to-payment --json      # the same, for scripts that make tracker items
dsheets requirements flows/checkout-to-payment --out reqs  # reqs/<team>.md and reqs/<team>.json per team
```

The default base is the one `dsheets diff` uses: the branch the registry
follows for this repository, at the point your branch left it. Without a
range, the head is your working tree. `--all` adds label and formatting
changes, as in `diff`. A flow with no change prints nothing and exits 0.

## What it prints

```
Shipping team: checkout-to-payment (base 502c48a)
 R-3a1f  Schema   add `surcharge` (number, required) to quote-response.toml
 R-91c2  Compute  shipping_fee: subtotal * 0.05 -> subtotal * 0.05 + surcharge   needs R-3a1f
 R-77e0  Send     send `surcharge` in quote-response, from `shipping_fee`   needs R-91c2
```

One group per team, in dependency order. Each requirement has a stable id,
a kind, what to do, why (the row or boundary it comes from) and what it
needs. The ids come from the kind, team, flow and subject, so a tracker item
that holds `R-3a1f` stays linked through every rerun.

| Kind | Team | Means |
|---|---|---|
| Schema | the interface's owner | A field must be added or changed because the flow needs something the contract lacks. |
| Compute | the computing team | A row is added to the team's `computes`, or its formula, table or lookup changed, or the computing team changed (the old team gets a "stop computing"). |
| Carry | a team that holds a value it does not compute | The value arrives, must be kept (across a call, in the caller's own state) and used or forwarded later. |
| Send | the producer of a boundary | A boundary now carries a field, or fills it from a different row. |
| Receive | the consumer of a boundary | The consumer now gets a field it must read. |

A Compute marked `(placed by default: no stage names it)` is a derived row
that no stage lists in `computes`; analysis put it in the latest step able to
serve it. The product manager can pin it in the flow.

`dsheets` only compares what the flow and the interfaces say. It does not
check that code matches the sheet; the reviewer does that. It does not group
requirements into tickets, create tickets or open pull requests.

## Workflow

1. The teams and the product manager agree a change in a meeting, using
   `slice`. The PM commits the flow on a branch and opens a draft pull
   request. That is the agreement.
2. `dsheets requirements <flow>` prints the requirements per team. The PM
   posts it on the pull request.
3. `dsheets requirements <flow> --apply` writes the interface edits (below).
4. Each owner's pull request merges when ready. The flow's pull request merges
   last, so the default branch always describes what is live. Until then the
   flow reports missing fields, which is the unfinished work.
5. Rerun `requirements` to see what is left. When it prints nothing, the flow
   can merge.

## `--apply`

Writes the Schema items into the owner's repository, ready for the owner's own
pull request, which then holds both the contract change and the code.

```
dsheets requirements flows/checkout-to-payment --apply
dsheets requirements flows/checkout-to-payment --apply --no-push
dsheets requirements flows/checkout-to-payment --apply --branch payments=ab/handling-fee --change CHK-142
dsheets requirements flows/checkout-to-payment --apply --clone payments=../payments-service
```

For each repository with Schema items it:

- makes a shallow, sparse clone of just that repository's dsheets folder under
  `.dsheets/work/<alias>` (or uses `--clone alias=path`; a clone that already
  exists there is reused, and must have no uncommitted changes);
- cuts a **new branch** from the tip of the branch the registry names for the
  repository (its default branch when the registry names none), called
  `dsheets/<flow>` unless `--branch alias=name` says otherwise;
- edits the interface file with `internal/tomledit`, so untouched lines stay
  byte for byte as they were; a field added to an imported interface gets
  `local = true`, as the editor does, and the pull request description reminds
  you to change the source schema too;
- writes `<dsheets folder>/changes/<label>.md`, a record of the change label,
  the flow, the base compared with and the requirement ids;
- commits with the trailers `Dsheets-Requirement: R-…` (one per requirement)
  and `Dsheets-Change: <label>`; the label is `--change`, the flow's name by
  default, and can be your story or epic id;
- pushes the branch with your own git credentials, unless `--no-push`.

It prints a pull request description with the same ids for you to paste.
Rename the branch to your team's convention with `git branch -m` and start
the implementation on it.

`--apply` is the one thing `dsheets` does outside its own repository, and it
is deliberately narrow: only a new branch, never an existing one, never a
force-push, never the default branch. It checks every repository before
changing any, and refuses when the branch name already exists locally or on
the remote, so a rerun never overwrites. It writes interface edits only;
Compute, Send, Receive and Carry are code changes that belong to the team.
It needs push access to the owner repository; without it, use `--no-push` and
a fork. Renaming a pushed branch leaves the old name on the remote until you
delete it.

## Linking pull requests

Branch names follow each team's own convention and are never used to find
other repositories' work. Linking is for people and trackers: the stable
requirement ids, the commit trailers (found with `git log --grep`), and the
record file, which survives a squash merge that rewrites the message. Whether
a requirement is done is never read from a link; it is read from the list
shrinking as the owner pull requests merge.

## Limits

- A team and a repository are treated as the same here.
- A flow that fails to analyse at the base or at head reports the error and
  no list.
- A new field's type comes from the sheet (an input's type, the values of a
  table or lookup, or `number` for arithmetic on numbers). When it cannot be
  told, the Schema item says so and `--apply` refuses, before changing
  anything, rather than write a guess into another team's contract.
- The flow must be in the repository you run the command in, not in a fetched
  copy under `.dsheets/`.
