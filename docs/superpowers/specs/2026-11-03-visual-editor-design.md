# Visual editor

Status: agreed in conversation on 3 November 2026; not built. Backlog item 6.

## Why

Today every change to a sheet, a flow or an interface means editing TOML by
hand. Product managers should not have to, and developers would rather not
either, unless an LLM is writing the TOML for them. The editor puts editing
on the pages `dsheets serve` already draws, so people change a sheet where
they read it.

There is one editor for everyone. There are no separate modes or permissions
for product managers and developers; anyone can do everything below.

## Goals

The everyday changes, without touching TOML:

- On a sheet: add, change and remove rows. The common case is adding an
  input or an output field to an existing transformation, which often means
  adding a derived row, a send, and the input rows it needs, in one sitting.
- On a flow: slice the end-to-end sheet across teams. Pin a row to a team's
  step, fix a missing field on an interface this repository owns, map a field
  to a row, and add or change a stage.
- Writes keep the files canonical and keep comments and formatting.
- On save, the sheet is validated and its cases regenerated.

## Non-goals (for now)

- **Creating a sheet or a flow from scratch.** That needs onboarding:
  importing OpenAPI into interfaces, reading existing code to find
  transformations, and so on. It is a separate piece of work. The editor's
  code should not assume a file already exists in ways that make adding
  creation later a rewrite.
- **Editing lookup data in CSV files** (`values_file`). Shown read-only.
- **Editing cases.** Expected values marked `"?"` stay `"?"` and are filled
  in by hand in `cases.toml`. A later version will let the editor edit cases.
- **Editing another team's files.** Anything reached through an `alias:`
  source (interfaces, an end-to-end sheet in another repository) is
  read-only. dsheets never writes to another team's repository.
- **Git.** The editor writes files in the working tree. It does not commit,
  branch, push or open pull requests.
- **Running flow checks on save.** Saving a sheet does not re-slice the flows
  that use it; people run that themselves, or CI does.
- **Renaming.** A row's or stage's ID can be edited only until the row or
  stage is first saved; after that it is locked. IDs are stable across
  versions (`docs/format.md`) and appear in formulas, flows, cases, evidence
  and other teams' flows, so renaming is its own piece of work.
- **Creating interface files.** A new stage picks from interface files that
  already exist. Making a new one belongs with onboarding.
- **Sheet settings.** `request_schema`, `target_schema` and `[display]` are
  not editable. The title is.
- **Turning a change into work for developers.** Saying what code has to
  change so it matches an edited sheet is a separate project, to be designed
  next (see "Related work").

## Anti-goals

- An invalid sheet on disk. Save is refused while validation reports an
  error.
- Rewriting a whole file to change one row. A save must leave every untouched
  row, comment and blank line byte-for-byte as it was, so the git diff shows
  only what changed.
- An editor that feels like a simplified tool. Developers who know the format
  must find it faster than the TOML, not slower.

## Constraints and assumptions

- People run it from a local git checkout, on localhost, as `serve` does
  today. This is a working assumption, not final: hosting it elsewhere will
  need a different approach. So the code that reads and writes files sits
  behind one small interface (load a file with a token identifying its
  version; save it only if the token still matches), and nothing else in the
  editor touches the filesystem directly.
- Go and plain HTML, CSS and JavaScript, like the rest of dsheets. No
  JavaScript build step and no framework, in keeping with the existing
  pages (`internal/render/sheet.js`).
- `github.com/pelletier/go-toml/v2` does not keep comments, so it cannot be
  used to write files. `internal/sheet/positions.go` already maps rows and
  keys to source lines; writing works at the level of text spans from that
  map (see "Writing TOML").

## How it looks

### Sheet: the page plus a side panel

The sheet page reads exactly as it does today: the row grid, rules, sends
and evidence. Selecting a row, or **+ Add row**, opens a form in a panel on
the right. The sheet updates live as you type, so the row's grid dots, rule
and send column show the edit before it is saved. A bar at the top shows how
many changes are unsaved, with **Save** and **Discard**.

The panel has:

- **Name, ID, description.**
- **Rule kind:** Input, Formula, Table, Lookup, or Text (a plain-language
  rule). Switching kind warns before throwing the old rule away.
- **For an input:** source (`request.`, `context.`, `db.`, `config.`,
  `call.`), type, usage, `when`, default.
- **Formula:** typed by hand, as CEL. Typing suggests row IDs and functions.
  A row may use rows anywhere on the sheet, as the format allows today; only
  rows that would make a cycle are shown as not usable. `uses` fills itself
  from the formula. Every case shows its result as you type (or why there is
  none, such as "rejected earlier"). The cases shown are the ones the draft
  would generate: `cases.Generate` runs on the draft in memory when typing
  pauses, so the preview is what Save will write, and a new input row has
  values in it straight away.
- **Decision table:** an editable grid. Columns come from rows you pick;
  cells take conditions such as `>= 2`, `-`, or values. Add, remove and
  reorder rules and condition columns; choose the hit policy. Gaps and
  overlaps are flagged as you edit.
- **Lookup:** `from` and `key` as a form. Inline `values` are editable; values
  from a CSV file are shown read-only, with the file's path.
- **Send:** field, usage R or S, `when` and `otherwise` for S, and a value
  map. Calls' sends (`send.target`) are edited the same way.
- **Rejects:** a list of `when`, code and message.
- **Open questions:** a list of text.

A name a formula, table or `uses` refers to that is not a row yet is flagged
in the panel with a one-click **add it as a request input**, which opens
that new row so the source path and type can be filled in. This is what
makes blocking invalid saves bearable.

### Flow: the page plus the same panel

The flow page as `serve` draws it, with the review marks (✕, ⚠, ○) always on
in the editor. Selecting a value row opens the panel:

- **Computed by:** which step computes the row, and whether that was placed
  automatically or pinned. Every step a row can go in offers **compute
  here**, which pins it (writes `computes` or `callee_computes`). Unpinning
  returns it to automatic placement.
- **Crossing boundaries:** every ✕ or ⚠ for the row. For an interface file
  in this repository (any file not reached through an `alias:` source,
  whatever its `owner` or `status`): **add field** (name, type, required) to
  the interface file, or **map to an existing field** (writes `carries`). For
  an interface reached through an `alias:` source: read-only, and the item
  appears in that team's "To agree" list as today.
- **Imported interfaces.** When the file records `imported = ...`, adding a
  field is allowed, with a warning naming the source schema the field also
  belongs in. The field is written with `local = true`, and re-importing
  keeps it (see "Re-import merges").
- **+ stage** between any two columns, and selecting a stage header, opens
  the stage in the panel: kind (`handoff` or `call`), interfaces, producer,
  caller and callee.

The marks re-run as you edit, before saving.

The flow panel does not edit a row's rule. The row's name links to it on the
sheet page. The sheet page and the flow page are independent: each holds its
own unsaved edits, and the flow page always slices the sheet as saved on
disk. Adding a field and then slicing it means saving the sheet first, which
validation allows as soon as the row is complete.

### Re-import merges

`dsheets interface import` today rewrites the whole file from the schema. It
changes so that, when the output file exists:

- fields in the schema are written from the schema, as now;
- fields in the file marked `local = true` and absent from the schema are
  kept, still marked;
- a `local` field that the schema now has loses the mark, because the schema
  caught up;
- the command prints the fields it kept, so the owner sees what is still
  waiting to go into the schema.

`local` is added to `schema/interface.schema.json` and documented in
`docs/flows.md`. Fields without the mark are replaced or dropped from the
schema as today.

## Saving

1. The browser sends the edited content for every file it changed (the
   sheet, `flow.toml`, interface files).
2. The server checks that no file changed on disk since it was loaded (a
   `git pull`, another editor, an LLM). If one did, nothing is written; the
   page says which file changed and offers to reload, keeping the unsaved
   edits visible so they can be redone.
3. The server builds the new files in memory and validates them: the sheet
   with `validate`, a flow with `flow.Load` and `flow.Analyze`, an interface
   with its schema. An **error the edit introduces** refuses the whole save,
   and the page lists the errors against their rows. "Introduces" means it
   is not among the errors the same files had before the edit, compared by
   code, row and message. Errors that were already there, such as overlaps
   in a read-only CSV or a broken file reached through an `alias:`, are
   shown but do not block, or a sheet with one could never be saved.
   Warnings (such as open questions) never block. Saving a sheet does not
   analyse the flows that use it.
4. **Version.** A sheet's or flow's `version` goes up by one at most once
   per commit. If the file's version equals the version in the last commit,
   the first save raises it by one; later saves leave it alone. The page
   shows the version and says when a save raised it. The storage interface
   supplies "the version in the last commit", so a later non-git backend can
   answer differently. Interface `version` (from imports) is left alone.
5. Files are written.
6. For a sheet, cases are regenerated exactly as `dsheets cases` does:
   hand-written expected values are kept (`cases.Preserve`), and the
   reference time is the one already in `cases.toml`. The page reports the
   summary `cases` prints, including any expected values left as `"?"`.

Until a save succeeds, edits live only in the browser tab. Closing the tab
with unsaved edits asks first.

## Writing TOML

- Each row, stage, interface field and top-level key is a span of lines in
  the file. A save replaces only the spans that changed, inserts new ones,
  and removes deleted ones.
- A changed span is written in canonical form: the key order and alignment
  the example files use (`specs/order-checkout/sheet.toml`), sub-tables
  indented two spaces.
- Comments and blank lines outside changed spans are never touched. A
  comment block directly above a row belongs to that row: it moves and is
  deleted with it. Section comments such as `# ---------- Derived ----------`
  stay where they are.
- Comments inside a changed span are kept where the key they sit next to
  still exists.
- New rows go where the user put them; "+ Add row" without a position adds
  the row at the end of its section (inputs with inputs of the same source
  kind, derived rows after the row it uses that is lowest on the sheet).

## How it starts

My call: `dsheets serve` stays read-only, and editing is turned on with
`--edit` on the same command (`dsheets serve --edit specs/order-checkout`).
Serving a flow with `--edit` also makes its end-to-end sheet editable when
that sheet is in this repository: the flow page links to it, and both are
served by the same process, so adding a field on the sheet and slicing it in
the flow happens in one session.

## Related work

**From sheet change to implementation work.** When someone adds a field to a
sheet, a flow or an interface, the code that implements it has to change to
match. The next project turns the difference between the committed files and
the edited ones into requirements for whoever implements them: per team,
what to compute, send, carry or add to a schema. It works whether the change
came from the editor, an LLM or a hand edit, which is why it is not part of
the editor. It is close to backlog item 8 (row-level diffs for pull
requests); both start from the same comparison. To be brainstormed after
this design is agreed.

## Left to the builder

- How the browser and server exchange edits (whole-file content or edit
  operations), as long as the server re-validates everything it writes.
- How live preview is computed: re-rendering on the server per change, or
  sending the model to the page. The existing `eval` engine computes case
  results; reuse it.
- The exact canonical layout for keys not used in the example files.
- Tests: every save path round-trips the example sheets, flows and
  interfaces byte-for-byte when nothing changed, and changes only the
  expected spans when something did.
