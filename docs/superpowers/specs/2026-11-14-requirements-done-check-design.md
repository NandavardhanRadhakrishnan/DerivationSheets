# Requirements: closing items from the owners' sheets (T-30)

Status: awaiting approval. Nothing is built.

## Intent and why

`dsheets requirements <flow>` lists what each team's code has to do for a flow
change. Today only Schema items ever close: once the owner's interface on its
default branch declares the field, the item drops out. Compute, Send, Receive
and Carry items stay until the product manager's flow itself merges, even when
every team has finished and their own `slice` is clean. The docs say "when it
prints nothing, the flow can merge", which therefore cannot happen from the
teams' work. The product manager wants to watch items close as teams merge.

Found in the T-27 Acme walkthrough (`acme-corp/WALKTHROUGH.md`, steps 15 to
22). The same walkthrough showed that Compute items name the row but not the
rule, and that the rule exists in only two places while work is in flight
(the owner's current sheet and the PM's unmerged branch).

## The developer's decisions

- **The signal is the owner's own sheet on its default branch.** If a team has
  updated its sheet and merged to the branch the registry follows, its code is
  assumed merged and working. dsheets never reads code.
- **A finished item drops out of the list**, as Schema items do. An empty list
  means the flow can merge.
- **A team with no sheet shows "not documented"**, kept apart from open. It does
  not block the flow merge; the PM decides.
- **Approach A alone:** read the owner's sheets and look for the evidence. No
  record files, no re-analysis of the owner's flow, no opt-out marker.
- **The rule travels with the item.** A Compute item carries the rule from the
  PM's end-to-end sheet (formula, table, lookup or rejects), so the owner needs
  not open the PM's branch.
- **No release-order guard.** Merged is not released; ordering between teams is
  a release concern, not dsheets'. `needs` links stay as they are.

## Goals

- Run `requirements` again after owners merge, and see only what is left.
- Say honestly what dsheets cannot tell (not documented), instead of showing it
  as open forever or marking it done.
- Give each Compute item enough to implement without opening the PM's branch.

## Non-goals and anti-goals

- Reading, running or testing code, or checking that code matches the sheet.
- A second mechanism for teams without sheets (record files, marks, opt-outs).
- Ordering work between teams, or refusing merges.
- Wrongly marking an item done. When dsheets cannot match, the item stays open
  or shows as not documented; it never closes on a guess.

## Approaches considered

- **A. Read the owner's sheets on its default branch and match evidence
  (chosen).** Needs a sheet, not a flow. Heuristic, but its failure mode is
  the accepted "not documented" state.
- **B. Re-run the full flow analysis on the owner's own flow.** More precise,
  but only for teams that have a flow; sheet-only teams could never be checked.
- **C. Teams list finished requirement ids in a record file.** Reliable and
  needs no matching, but depends on a person editing a file and ignores the
  sheet, which is the signal chosen.

## Design

### When an item is done

Evidence is looked for in every `sheet.toml` under the owner's dsheets folder,
read at the tip of the branch the registry follows (the same shallow, sparse
fetch `impact` and `slice` use; `--offline` uses the cache).

| Kind | Done when the owner's sheets contain |
|---|---|
| Schema | unchanged: the interface declares the field |
| Compute (add, move, change) | a derived row for the value whose rule equals the rule in the PM's head sheet |
| Compute (remove) | no derived row for the value, in an owner that has at least one sheet |
| Send | a row with a send to that field |
| Receive | an input row reading that field |
| Carry (pass on) | the row present in the owner's sheet, and, where the value crosses the next boundary, its send (the Send item covers the send) |
| Carry (keep across a call) | the row present in the owner's sheet |

A derived row that exists but whose rule differs from the PM's is **open**, with
a note: "the sheet has `free_shipping` but its rule differs from the flow's".
Rules are compared with the normalisation `dsheets diff` already uses for rows.

Rows and fields match by row id first, then by the field-name rule `slice`
uses (case and separators ignored), so `free_shipping` matches `freeShipping`.

### Which repository is the owner

Schema items already name their repository. For the other kinds the item has
only a team name ("Cart team"). Map it in this order:

1. an optional `team` key on the registry entry in `.gitmodules`
   (`team = "Cart team"`);
2. otherwise the alias equal to the team name's first word, lowercased
   (`cart`);
3. otherwise no owner: the item is not documented.

The flow's own repository is never the owner of another team's items, even
when both are the same team in the flow.

### The three states

| State | Meaning | Listed | Blocks the flow merge |
|---|---|---|---|
| open | the owner has sheets, the evidence is missing or differs | yes | yes |
| done | the evidence is there | no (counted) | no |
| not documented | no owner repository, or the owner has no sheets | yes, under its own heading | no, the PM decides |

An item is **never** done for a team with no sheet, and the Compute (remove)
rule above needs at least one sheet, so an interface-only team cannot show a
stop as done.

### Output

```
Cart team: checkout-to-payment (base e3172ab)
 (none open)

Orders team: checkout-to-payment (base e3172ab)
 R-062d10  Receive  read `freeShipping` in order-placed   open: no input row reads freeShipping

Not documented (dsheets cannot tell; no sheets): Payments team
 R-a7bfb2  Schema ...

3 of 5 done
```

- Groups with nothing open are left out; if every team is done, the output is
  empty and the exit status is unchanged (0).
- The `N of M done` line appears only when something is done.
- Compute items print the rule under the item, indented:
  ```
   R-65e0e5  Compute  compute `free_shipping`
             rule: table, first match
               subtotal >= 100            -> true
               loyalty_tier = Gold        -> true
               otherwise                  -> false
  ```
- `--json` gains `status` ("open", "done" or "undocumented"), `note` and
  `rule` per requirement, and the Markdown `--out` files follow the same
  rules. `--all` keeps its meaning (label and formatting changes); a new
  `--show-done` lists done items too, for people who want the record.
- The check runs by default, since `requirements` already fetches every
  registered repository. `--no-check` skips it and gives today's behaviour.

### What the rule text is

Taken from the row in the PM's head sheet: the formula, or the table with its
hit policy and rules, or the lookup (`from`, key, inline values, or the values
file's path), plus any reject rules. A row that is moved but unchanged still
carries its rule: the new owner needs it. For "change how computed" items the
existing before/after stays and the full head rule is added.

## Constraints

- Git-native; only the registry, the cache and the fetcher already used.
- dsheets still writes only under `--apply`.
- No change to ids, to the Schema close behaviour, or to `--apply`.
- Teams document at different depths; interfaces stay the minimum. This design
  adds no requirement that a team have a sheet.

## What is left to the builder

- Reuse the row comparison from `internal/diff` and the name matching from
  `internal/flow`; do not write a second copy of either.
- Where the check lives (a new file in `internal/requirements`, after
  `Compare` and `locate`), and how `Run` exposes the status.
- Exact wording of the notes, and the layout of the rule block, within the
  sketches above.
- Documentation: `docs/requirements.md` (workflow step 5, the states, the
  `team` key, the new flags) and `docs/repositories.md` (the `team` key).

## My calls, for you to check

- Missing owner and "no sheets" both map to not documented.
- Compute (remove) needs at least one sheet in the owner, so an absent row in
  a sheet-less team is not read as a stop.
- The `team` registry key with first-word fallback.
- The check is on by default, with `--no-check` and `--show-done`.
- A derived row with a different rule stays open with a note, never done.
- Done items are counted but not listed.
