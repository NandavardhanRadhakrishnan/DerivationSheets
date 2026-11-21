# Boundary agreement: keeping the PM's intent and the teams' sheets in step (T-32)

Status: approved by the developer, 2026-11-21. Nothing is built.

## Intent and why

When a product manager changes a flow, each owning team has to bring its own
files in line: its flow (`computes`, pins), its sheet (rows), its interface
and source schema. Today nothing tells them the steps or their order, and the
wrong order breaks the flow editor (T-31). That was the starting point of
T-32, found in the T-27 Acme walkthrough (`acme-corp/WALKTHROUGH.md`, steps
9 to 21).

The brainstorm found a deeper cause. The same logic is written at two
levels:

- the **PM's end-to-end sheet**, written as intent (A → D), at whatever detail
  the PM likes;
- each **team's own sheet**, written at implementation detail (A → B → C → D).

In the Acme sandbox the PM happened to write at the teams' detail, so the
`free_shipping` table was identical in both and the problem stayed hidden. In
a real organisation the PM writes "Gold members and orders over 100 ship
free", and the teams add the steps in between. The two levels are linked only
by a shared row id, so they drift, and the T-30 done-check, which compares
rule text row by row, could never close work written at a different
granularity.

Planning happens top down (the PM decides first), while what runs is bottom
up (the team sheets mirror the code). Neither level can be derived from the
other while a change is in flight. So dsheets should not try to keep the two
levels textually the same; it should check that they **behave** the same
where it matters: at the boundaries between teams.

## Vocabulary

| Term | Meaning |
|---|---|
| **Sheet** | One derivation: input rows, derived rows (formula, table, lookup, text) and sends. Says how each value is produced and where it goes; nothing about teams. |
| **PM's sheet** | The end-to-end sheet a flow points at (`sheet =` in `flow.toml`). Intent, at any detail. |
| **Team sheet** | A sheet in a team's own repository. The team's real logic. A team may have several. |
| **Flow** | The slicing of the PM's sheet across teams: the boundaries (stages) and which team answers for each PM row. No logic. |
| **Team flow** | A team's own small flow (for example `orders-checkout`), naming its incoming and outgoing interfaces and its sheet. |
| **Interface** | One boundary's contract (API request or response, topic, event): fields, types, required. Usually imported from a source schema. |
| **Boundary value** | The value of an interface field at a boundary, for one case. |
| **Answering team** | The team accountable for a PM row's value where it leaves that team's boundary. The flow's pin records it. Example: Orders answers for `shipping_cost` (sent as `shippingCost` on `payment-request`), even when Shipping does part of the work. |
| **PM's cases** | `cases.toml` generated from the PM's sheet by `dsheets cases`: inputs plus expected boundary values. |

## The developer's decisions

- **Agree on behaviour at the boundaries, never on rule text.** A team sheet
  may be more detailed than the PM's sheet. Agreement is proven by running the
  PM's cases.
- **Every file owns one thing.** PM's sheet: intent and cases. Flow: boundaries
  and who answers for each PM row; no logic. Team sheets: all real logic,
  including how a coarse PM row is split between teams. Interfaces: what
  crosses each boundary.
- **A pin means "this team answers for this value at its boundary"**, not
  "this team computes every step of it".
- **The slice meeting stays short.** It decides which team answers for each
  changed PM row, and new fields on the outer boundaries. Nobody edits another
  team's sheet in the meeting.
- **How to split the work is settled between the answering team and the
  neighbour it needs**, not by the PM and not in the flow. Example: for
  "Silver members pay half shipping", Orders (answering for `shippingCost`)
  and Shipping decide whether Orders halves the fee (I1) or Shipping does (I2).
  A new field between them goes through an interface change, as
  `dsheets impact` already handles.
- **A PM row at an intermediate boundary is binding.** When the PM's sheet has
  a row that the flow carries on a boundary between two teams, that boundary
  value is part of the contract, as an outer one is. In Acme the PM's sheet
  has a separate `shipping_fee` row carried on `quote-response.fee`, so the
  PM's sheet as written fixes I1: Shipping returns the full rate. If the teams
  want I2, they ask the PM to change the sheet (make the row coarser, or move
  the halving into `shipping_fee`); until then the item stays open. The teams
  choose the split freely only where the PM's sheet has no row at the
  boundary between them.
- **Each team edits only its own sheet, in its own pull request.**
- **Sheet first is the default path; code first is supported and gets the
  same checks.** Either order, by hand or with AI.
- **A change is done when both checks pass:** the team's sheet passes `verify`
  against its code (sheet matches code), and the chained team sheets pass the
  PM's cases at the boundaries (code matches intent). In code-first work the
  second check is what catches a sheet that faithfully documents a bug.
- **The PM writes logic, not only English, for rows whose value crosses a
  boundary.** English (`text`) rows are allowed elsewhere; their expected
  values are filled in by hand (`"?"`, as today). Hand-written PM examples are
  a separate, low-priority task (T-33).
- **Assume every team in a flow has a sheet.**
- **Driving the team's code from dsheets through a team-supplied adapter is a
  separate task, T-34**, done right after this one. This design uses
  `verify` as it is.

## Goals

- An owner can see, for each requirement, exactly which boundary values are
  wrong for which cases, and fix their own sheet until they are right.
- Requirements close when the teams' work behaves as the PM intended, even
  when a team's sheet is more detailed than the PM's.
- A change that spans a team's sheet and flow cannot be saved in an order that
  breaks either (T-31).
- The slice meeting gets no longer than it is today.

## Non-goals and anti-goals

- Making the two levels textually identical, or generating one from the other.
- Putting logic in the flow.
- Letting one team edit another team's sheet, in the meeting or through a tool.
- Running or testing code in this task (that is `verify` today and T-34 next).
- Editing source schemas (`.avsc`, OpenAPI, JSON Schema).
- Closing an item on a guess. When dsheets cannot evaluate a value, the item
  shows "cannot tell" (as "not documented" does today), never done.

## Approaches considered

- **A. One editor updates flow, sheet and schema together** (developer's first
  idea). Fixes the order problem, but keeps both levels holding the same logic
  as text, so drift and the granularity problem remain.
- **B. Developers write only code; the sheet is generated from it** (developer's
  second idea). Removes double writing, but reverses the planning direction,
  needs T-12 first, and a generated sheet documents bugs as truth.
- **C. Keep rule-text matching and add tooling to copy the PM's rule into team
  sheets.** Smallest change, but forces team sheets to the PM's granularity.
- **D. Boundary agreement checked by the PM's cases (chosen).** The levels may
  differ in detail; only boundary values must agree. Keeps the meeting short,
  keeps logic out of the flow, works for sheet-first and code-first. Costs a
  new chained evaluator and depends on the PM writing computable rules where
  values cross boundaries.

## The design

### 1. Case runner

New capability: run the PM's cases through the team sheets along a flow and
compare boundary values.

**Where the expected values come from (my call).** The runner does not read
the PM's `cases.toml`. It generates the case inputs from the PM's sheet at
head (as `dsheets cases` does) and evaluates the PM's sheet for each case,
which gives the value of every PM row, so expected values are known at every
boundary, not only at the sheet's own output. A committed `cases.toml` is
used only for its hand-filled `"?"` values, matched by case id; when it is
missing or stale, rows that need those values are "cannot tell".

**Evaluating one team's stage.** Find the team's sheet for the stage (section
2) and evaluate it:

- `request.<path>` inputs read the incoming interface's payload;
- `call.<stage>.<field>` inputs read the callee's response (below);
- `context.`, `config.` and `db.` inputs take the value of the PM's input row
  with the same source when there is one; a lookup uses its design-time
  values;
- the sheet's sends build the outgoing payload, and its sends to a call stage
  build that call's request.

**Two modes (my call).** Feeding each stage from the stage before it would make
one mistake upstream keep every item downstream open, and would make items
close only in upstream-first merge order (in the walkthrough, Orders merged
before Cart). So the runner has two modes:

| Mode | Each team's stage is fed | Used by |
|---|---|---|
| **Isolated** | The PM's expected values at its incoming handoff boundary (or the flow's input), for every field that carries a PM row. A call's response always comes from evaluating the callee's sheet on the request this team sent, never from the PM's values, so a caller is judged together with the callee it really talks to; the response's own fields are still compared (below). | The done-check and the editor: a team is judged on its own sheet, whatever upstream has merged |
| **End to end** | The previous stage's output, from the flow's input to its output | `dsheets slice <flow> --cases`: the whole chain, to show the result once everything has merged |

**Comparing.** At every boundary, for every field that the flow analysis says
carries a PM row, compare the team's value with the PM's value for that row
in the same case. This includes boundaries between two teams, such as a call's
request and response: a PM row carried there is binding. A field that carries
no PM row is not compared; a wrong value in it surfaces at the next compared
field (if the PM wrote `shipping_cost` as one coarse row and both Orders and
Shipping halve the fee, `payment-request.shippingCost` is wrong).

**Rejects (my call).** A PM case that rejects agrees when the chain rejects
with the same code; which team rejects does not matter end to end. In
isolated mode, a reject belongs to the team its row is placed with, and that
team's stage must produce it. A team that rejects where the PM's case does
not, or with another code, differs. When a stage rejects, later fields of
that case are not compared. A `MISSING_INPUT` in isolated mode means the team
reads a field its incoming boundary does not carry, and is reported as
differs, naming the field.

Results per case and boundary field: **agrees**, **differs** (expected, got),
or **cannot tell** (with the reason: a text rule, a lookup with no design-time
value, a team input with no counterpart, no team sheet found).

### 2. Finding a team's sheet for a stage

A team's sheet does not declare its interfaces (none of the Acme sheets sets
`request_schema` or `target_schema`). The team's own flow does: it names the
sheet and its incoming and outgoing interfaces. So the runner reads, in the
owner repository on the branch the registry follows, every team flow, and
picks the one whose input interface and output interface match the boundary
pair of this stage in the PM's flow. That flow's `sheet` is the team sheet.

Rules for the first version (my call):

- **The PM's stages define the boundaries that are compared.** A team flow may
  span several PM stages: Orders' flow covers both the `shipping-quote` call
  and the output. Interfaces are matched by file, not by stage id, so a call
  stage may have a different id in the team flow.
- **A call in a team flow that the PM's flow does not show** (say Orders
  calling a Pricing service) makes the fields that depend on its response
  "cannot tell". Walking the teams' own call graph is later work.
- **Exactly one team flow must match.** None or several makes the stage's
  values "cannot tell", naming the boundary and the candidates.

### 3. Done-check by cases (changes T-30)

| Kind | Closes today (T-30) | Closes under this design |
|---|---|---|
| Compute | A derived row with the same rule text | In isolated mode, every PM case agrees on the fields that carry the row itself. A row carried by no field (such as `discount_rate`) is judged on the nearest compared fields it feeds at the answering team's outgoing boundary, and the note says other rows also feed them |
| Compute (stop) | No derived row for the value | Unchanged |
| Send, Receive, Carry | Structural evidence in the owner's sheet | Unchanged, plus the boundary fields involved agree on every case |
| Schema | The interface declares the field | Unchanged |

Statuses stay `done`, `open`, `undocumented`; "cannot tell" from the runner
maps to `undocumented` with the reason as the note. The rule text stays on
Compute items as a hint.

### 4. `requirements` output

An open Compute, Send or Receive item lists its failing cases under it:

```
Orders team
 R-91c2e4  Compute  shipping_cost: half the rate for Silver
           case silver-domestic   shippingCost  expected 4, got 8
           case silver-intl       shippingCost  expected 6, got 12
```

`--json` carries the same as `cases: [{id, field, expected, actual}]`.

### 5. Meaning of a pin

`docs/flows.md` and `slice` change from "rows listed in `computes` are placed
in that step" to "the team answers for these values at its boundary". A team
sheet may contain rows the PM's sheet does not have; that is never a
finding. Placement of PM rows in `slice` is unchanged.

### 6. Requirement-driven editing (the owner's view)

In `serve --edit` on a team sheet, a **Requirements** panel lists the open
items for this team.

Where the items come from (my call): the PM's change lives on a branch of the
PM's repository until it merges last, and nothing in the owner's repository
points at it. So the owner names it: `serve --edit <sheet> --flow
<alias>:<flow path>@<branch>`. `requirements` prints that line for each team,
ready to paste, and `--apply` writes it into its record file. A neighbour
that does part of the work for another team's item (Shipping, in an I2 split)
opens the same line and sees the items whose fields pass through its stage,
marked as another team's.

Selecting an item shows:

- the PM's rule, as a hint;
- the failing cases at this team's boundary (input, expected, got);
- the rows of this sheet that produce those fields.

As the owner edits, the cases rerun against the draft through the chain, and
turn green. Saving writes the sheet, and the team flow when the change needs
it (for example removing a stale `computes` entry when a derived row becomes
an input), in one save. This removes the order problem of T-31.

### 7. Delivery order

1. Chained case runner and finding a team's sheet (sections 1, 2), with
   `slice --cases`.
2. Done-check by cases and the new `requirements` output (sections 3, 4).
3. Meaning of a pin: docs and `slice` (section 5).
4. Requirement-driven editing (section 6).
5. Later, separately: AI drafting of the sheet edit from a requirement (T-10)
   or from a code diff (T-12).

## Constraints

- Go, as the rest of dsheets; the evaluator in `internal/eval` and the flow
  analysis in `internal/flow` are reused.
- Owner repositories are read with the registry and fetch code `requirements`
  already uses (`internal/sources`), on the branch the registry follows;
  `--offline` and `--no-check` keep their meaning.
- Values compare with `sheet.Equal`, as `verify` does.
- Never close an item on a guess (decision D-22).

## Limits

- Only computable values can be compared. A `text` row, or a database lookup
  without design-time values (D-15), makes the fields that depend on it
  "cannot tell".
- Untyped derived rows (T-28) weaken the comparison: a team table that returns
  the text `"False"` where the PM's returns `false` shows as differs, which is
  right, but nothing tells the team why until rows can declare a type.
- The PM's cases cover what `dsheets cases` generates from the PM's sheet. An
  English-only PM row gets no case of its own until T-33.

## Related

- T-28 (typed derived rows), T-30 (done-check, changed here), T-31 (edit
  order, solved by section 6), T-33 (hand-written PM examples), T-34 (test
  middleware), T-10 and T-12 (AI drafting), D-15 (database-backed lookups),
  D-21 and D-22 (the T-30 design and its matching rules).

## Left to the builder

- The exact flag and output names (`slice --cases`, the JSON shape), within
  the style of the existing commands.
- How the Requirements panel looks in the editor, within the editor's existing
  layout.
- Performance work (caching fetched sheets, evaluating cases in parallel).
