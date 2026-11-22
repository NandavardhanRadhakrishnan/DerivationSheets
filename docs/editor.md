# Editing sheet.toml in VS Code

`schema/sheet.schema.json` gives autocompletion, hover docs and inline errors
for `sheet.toml` in any editor that uses Taplo. `schema/flow.schema.json` and
`schema/interface.schema.json` do the same for `flow.toml` and interface
files; `.taplo.toml` attaches all three, and imported interfaces get a
`#:schema` line automatically.

1. Install **Even Better TOML** (`tamasfe.even-better-toml`). It bundles Taplo.
2. Associate the schema, in either of two ways:
   - **Per file:** put a schema directive on the first line of `sheet.toml`.
     The path is relative to the file:
     ```toml
     #:schema ../../schema/sheet.schema.json
     ```
     The fixture `specs/order-checkout/sheet.toml` does this.
   - **Per repository:** the `.taplo.toml` at the repository root attaches the
     schema to `specs/**/sheet.toml`. The extension picks it up automatically.
3. Reload the window. Typing `[[row]]` and then a key offers completions, and an
   unknown key or a bad `usage` is underlined.

The schema catches shape errors only: unknown keys, wrong types, and
malformed IDs and codes. Cross-row checks such as undefined references,
cycles, table gaps and unused rows need `dsheets validate`. Every editor error
also appears in `validate`, which embeds the same schema.

## Formatting

`dsheets fmt [--check] [path...]` puts every `sheet.toml`, `flow.toml` and
interface file under the given paths (default: the current folder) in the
canonical layout the editor writes: key order, alignment and indentation.
Values keep their text as written, and comments are kept. `cases.toml`,
data files and hidden folders are skipped. `--check` writes nothing, lists
the files that would change and exits 1.

A pre-commit hook in `.githooks/pre-commit` runs `dsheets fmt --check` on the
staged `.toml` files. Turn it on once per clone with
`git config core.hooksPath .githooks`.

## Serving the editor

`dsheets serve --edit <sheet>` opens the sheet in the browser editor, on
localhost. Saving writes the sheet and, in the same save, the flows (below).

```
dsheets serve --edit sheets/specs/orders
dsheets serve --edit sheets/specs/orders --flow product:flows/shop@quarter
```

`--flow <alias>:<path>@<branch>` opens the product manager's change. It
fetches that repository with history, sparse to the alias folder, and compares
the branch with its merge-base. The alias and the `@branch` are both
required, and `--flow` needs `--edit` on a sheet; otherwise it is the usage
error `--flow needs --edit on a sheet`. If the change cannot be read, serve
still starts, and the panel shows the error with Fetch again. The line to run
is printed by `dsheets requirements` (see `requirements.md`).

## The Requirements panel

The bar button `Requirements · N open` opens the panel. It lists this
repository's team's items: open, then cannot tell, then done (folded). Other
teams' Compute and Send items that pass through a call this team answers show
under "Other teams' items through your calls". When the repository's team
cannot be told, the button says `Requirements · team unknown` and every item
shows under "Other teams' items", with the reason. The team is found from the
origin remote: every registry alias of that repository counts.

An expanded item shows the product manager's rule, the note, the failing
cases (Case, Field, Expected, Got, Input) and chips for the rows of this sheet
that produce the fields. A row chip opens the row, and the row has a
`← Requirements` link back.

As you edit, the product manager's cases rerun against the draft through the
teams' sheets, in a separate request after each check. Until the answer for
the latest edit comes back, the button says `Requirements · checking…`.
Items turn done without saving, and the bar says `R-xxxxxx is done with this
draft`.

An item is never shown done on a guess. When the cases cannot run on the
draft, the button says `Requirements · not checked` and the panel dims the
last result with the reason. That happens when:

- the draft, or the sheet as saved, has an error: a sheet with errors leaves
  the team's flow out of the cases;
- the sheet changed on disk since the draft was started, or does not parse;
- no flow of this repository's team was matched with the sheet being edited;
- the repository has no origin remote, or the registry pins it to a tag or
  commit: the cases then read the registry's copy, not your working tree.

Fetch again re-reads the product manager's branch. If that fails after the
change was read once, the panel keeps the items as last read and says that
Fetch again failed.

Example, from the shop fixture: the product manager changes `shipping_cost`
from halving to quartering Silver's fee on branch `quarter`. In Orders, the
panel lists the open Compute item for `shipping_cost`, with the failing cases.
Change the formula in the editor and the item turns done before you save. In
Shipping, the same Orders item shows under "Other teams' items through your
calls", marked "Orders team, through the call to shipping-quote": a draft of
Shipping's `fee` changes what the item's cases get.

## One save for the sheet and the flows

Saving a sheet, with or without `--flow`, also removes the rows the draft no
longer derives from `computes` and `callee_computes` in this repository's
flows whose `sheet` is this sheet. Everything is written in one save, and the
bar says `Also saved flows/shop/flow.toml: removed free_shipping from computes`.

If a flow error the edit introduces is still left, the save is refused and
the flow problems are listed. For example, deleting a row that a `carries`
entry names.
