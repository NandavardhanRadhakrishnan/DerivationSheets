# Backlog

What is left, as of 2 November 2026. Phase 1 of the original handoff is
done, and so are flows: slicing, interface import, git sources, piece
export, branch and feed checks, and the combined grid.

Items are grouped by where they come from. "Suggested order" at the end is
a recommendation, not a commitment.

## Gaps in what is built

1. **Calls in cases and verify.** `cases` and `verify` compare only a
   sheet's own payload. What a piece sends to a call (`[[row.send]]` with
   `target`) and reads back (`call.<stage>.<field>`) gets no generated
   cases and no evidence. `eval` already returns the call payloads.
2. **Evidence per piece in a flow.** Evidence is recorded per sheet.
   There is no view of evidence per piece or per team inside a flow, or in
   the combined grid.
3. **Lists ("for each item").** Arrays import as a single `array` field;
   there is no way to derive or send one value per item. This is still an
   open question from the handoff; the likely shape is a group of rows
   with a shared "for each" label, as tax forms handle repeated
   dependents.
4. **Module path.** The Go module is `derivationsheets`, so
   `go install github.com/NandavardhanRadhakrishnan/DerivationSheets/cmd/dsheets@latest`
   does not work. Rename the module to the repository path.
5. **CI.** No GitHub Actions job runs `go test`, `dsheets validate` on the
   specs, or `dsheets slice --strict` on the flows.

## Phase 2 from the handoff (not started)

6. **Visual editor.** On top of `dsheets serve`, so non-technical people
   never edit TOML. It must read and write canonical TOML and keep
   comments and formatting. Needs a design conversation first.
7. **Formatter.** A canonical `taplo fmt` configuration (or a `dsheets
   fmt`) and a pre-commit hook.
8. **Diffs for pull requests.** Show changes as row-level sheet
   differences rather than text diffs; for flows, as boundary and
   interface differences (fields added or dropped, who computes what).
9. **Drafting rows from natural language** with an LLM. Ambiguities must
   become `open_questions`, never guesses.
10. **DMN XML export**, for viewing in existing DMN tools. Export only,
    never execution. Only matters if FEEL or DMN tooling becomes
    important; CEL stays the expression language until then.

## Phase 3 from the handoff (not started)

Documenting existing code without changing it.

11. **Draft sheets from existing code**, including SQL strings and config
    keys, proposed by an LLM.
12. **Production evidence from OpenTelemetry traces**, configured at the
    process level, with payloads redacted before storage. The evidence
    model already has a `trace` run kind; nothing ingests traces yet.
13. **Database and config snapshots** on a schedule, with a diff view
    between snapshots.
14. **Replay captured requests** through the evaluator and compare with
    the payloads actually sent, to verify inferred rows.

## Known limits, by design for now

- Matching interface fields to rows by name is a heuristic. Listing
  `carries` explicitly avoids wrong matches; ambiguous matches are warned
  about.
- Upstream origins found by the branch trace are guesses, labelled as
  such, until the owning team maps the field in its own sheet.
- In a combined grid, a value a shared interface does not carry yet shows
  as separate rows on each side until the field exists or is declared on
  both sides.
- Enum checks need the row's possible values (tables, lookups, booleans);
  type checks need a known type (typed inputs, tables, lookups).

## Suggested order

1. Items 4 and 5: small, and they make the repository usable by others.
2. Item 1: closes the testing gap that piece export opened.
3. Item 8: PR diffs make flows reviewable in a normal team workflow.
4. Item 6: the largest remaining piece; design it before building.
