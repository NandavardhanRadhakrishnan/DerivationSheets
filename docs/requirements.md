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
dsheets requirements flows/checkout-to-payment --show-done # also list what the owners' sheets already show as done
dsheets requirements flows/checkout-to-payment --no-check  # do not read the owners' sheets: every item is open
```

The default base is the one `dsheets diff` uses: the branch the registry
follows for this repository, at the point your branch left it. Without a
range, the head is your working tree. `--all` adds label and formatting
changes, as in `diff`. A flow with no change prints nothing and exits 0.

## What it prints

```
Shipping team: checkout-to-payment (base 502c48a)
 R-3a1f9c  Schema   add `surcharge` (number, required) to quote-response.toml
 R-91c2e4  Compute  shipping_fee: subtotal * 0.05 -> subtotal * 0.05 + surcharge   needs R-3a1f9c
 R-77e0b1  Send     send `surcharge` in quote-response, from `shipping_fee`   needs R-91c2e4
```

An open Compute or Send that the product manager's cases fail lists up to
five of them under it, then how many more:

```
Orders team: checkout-to-payment (base 7c3ac06)
 R-4bbf66  Compute  shipping_cost: free_shipping ? 0 : shipping_fee -> free_shipping ? 0 : shipping_fee * 2   open: 4 of 6 cases differ
            formula: free_shipping ? 0 : shipping_fee * 2
           case loyalty-tier-silver      shippingCost  expected 18, got 27
           case loyalty-tier-silver-2    shippingCost  expected 18, got 27
           case isolate-customer-years   shippingCost  expected 18, got 27
           case isolate-zone             shippingCost  expected 36, got 54
```

One group per team, in dependency order. Each requirement has a stable id,
a kind, what to do, why (the row or boundary it comes from) and what it
needs. The ids come from the kind, team, flow and subject, so a tracker item
that holds `R-3a1f9c` stays linked through every rerun.

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

A Compute item prints the rule from the product manager's sheet under it (the
formula, the decision table with its hit policy, or the lookup, and any reject
rules), so the owning team can implement it without opening the flow's branch.
`--json` carries the same text as `rule`.

## Done, open and not documented

Each rerun also reads the owning team's own sheets, on the branch the registry
follows for it, and decides every item:

| State | Meaning | Listed | Blocks the flow merge |
|---|---|---|---|
| done | the owner's sheets show the work | no, counted (`3 of 5 done`) | no |
| open | the owner has sheets and the evidence is missing or differs | yes | yes |
| not documented | dsheets cannot tell: no repository for the team in the registry, or it has no sheets, or it could not be read, or the PM's cases cannot be told on the team's sheet | yes, under `Not documented (dsheets cannot tell):` | no, the PM decides |

What counts as evidence, looked for in every `sheet.toml` under the owner's
dsheets folder:

| Kind | Done when the owner's sheets contain |
|---|---|
| Schema | the interface declares the field (as before) |
| Compute | every PM case agrees at the team's boundary (see `slice --cases` in [flows.md](flows.md#checking-the-teams-sheets)); a team with no own flow for the boundary falls back to the rule text: a derived row for the value whose rule equals the flow's |
| Compute (stop) | no derived row for the value, in an owner that has at least one sheet |
| Send | a row that sends the field, and no PM case differs on that field at the boundary |
| Receive | an input row that reads the field |
| Carry | the row, in the owner's sheet |

For a Compute, the cases are the product manager's generated cases (`dsheets
cases`), run through the team's own flow and sheet with the end-to-end values
fed in at its incoming handoff, as `slice --cases` does in its isolated mode.
The item is decided on the fields that carry the row:

- every case agrees: done, however the team's rule is written;
- some case differs: open, with `N of M cases differ` and the cases listed;
- some case cannot be told (an input the team's sheet lacks, a sheet that
  fails, a call that is not answered): not documented, with the reason.

A row no field carries (such as `discount_rate`) is judged on the fields of
the rows it feeds, and the note says `judged on <fields>, which other rows
also feed`, since a difference there may come from another row.

When the team has sheets but no own flow that takes the boundary's
interface in and gives the next one out, or no field carries the row or
anything it feeds, the rule text decides and the note ends with `checked by
rule text: <why>`. Then a derived row that exists with a different rule
stays open, with a note. The comparison covers every field of the row except
its labels (name, description, open questions) and its send, as `dsheets
diff` does, so a changed reject message or a changed `uses` counts as a
different rule.

Matching is by where the value sits, not by name alone:

- Compute, Compute (stop) and Carry find the owner's row by id (case and
  separators ignored: `free_shipping`).
- Send needs a send of the field to the same place: a call request is filled by
  a send to that stage, anything else by a send to the sheet's own target.
- Receive needs an input row whose source is the request (or, for a call's
  response, `call.<stage>.<field>`). A value read from config, a database or
  the context is not the field.
- Field names match ignoring case and separators. When only one name is a
  dotted path, its last part may stand for it (`country` and
  `shipping.country`); two paths must match whole.

An item is never closed on a guess. A "stop computing" item stays out of done
while any of the owner's sheets could not be read, because a missing row might
be in the sheet that failed; it shows as not documented, naming the file. Items
owned by the repository the flow itself is in are also not documented: its
sheets on the followed branch are the flow's own base.

A team is found in the registry by an optional `team = "Cart team"` key on its
entry (every entry with that key is read, so a team can own several
repositories), or else by the alias equal to the first word of the team's name
(`cart`) when that entry names no other team. See [repositories.md](repositories.md). A team that keeps only
interfaces has no sheets to read, so its Compute, Send, Receive and Carry
items show as not documented; its Schema items still close.

`--json` lists every item, done ones included, with `status` and `note`, and
on an open or not documented Compute or Send the `cases` behind it: each
case's id, boundary, field, row, team, status, expected and actual value.
`--no-check` skips all of this, and `--offline` uses only what is fetched.

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
5. Rerun `requirements` to see what is left. Items drop out as each owner's
   sheets on the followed branch show the work. When nothing is open, the
   flow can merge; items listed as not documented are the PM's call.

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
