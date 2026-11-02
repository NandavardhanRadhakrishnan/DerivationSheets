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

`taplo fmt` formatting and a pre-commit hook are planned for Phase 2.
