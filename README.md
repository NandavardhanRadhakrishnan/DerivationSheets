# DerivationSheets

A derivation sheet documents how a service turns an incoming request into a
call to another system: which values are derived from which, which fields are
sent only under certain conditions, and where lookups come from. The target can
be anything a request is passed on to: another service in the same team,
another team's API, or an external system in a different organisation. The same document
serves as the specification at design time, as generated test cases during
build and test, and, with evidence, as a readable view in production.

The sheet is a specification and a view. It never runs in production, and
application code is never changed to make it documentable.

The working name lives in `internal/brand/brand.go`; the CLI is `dsheets`.

## Quick start

Requires Go 1.27 or later.

```sh
go build -o bin/dsheets ./cmd/dsheets

bin/dsheets validate specs/order-checkout
bin/dsheets cases    specs/order-checkout          # writes cases.toml
bin/dsheets eval     specs/order-checkout --case loyalty-tier-gold
bin/dsheets verify   specs/order-checkout --actual examples/results.json
bin/dsheets render   specs/order-checkout --out sheet.html
bin/dsheets serve    specs/order-checkout          # http://localhost:8080/
```

The example workflow, `specs/order-checkout`, prices an online order: a
loyalty tier from membership and years as a customer, a discount, free
shipping rules, a shipping fee looked up by zone, method and weight, a customs
value sent only for orders leaving the country, sales tax, and a reject for
destinations that are not served.

`examples/results.json` changes one output value (`shippingCost` in case
`isolate-zone`), so `verify` reports a contradiction and exits with code 1.
The rendered sheet then marks Shipping cost as contradicted. `examples/sheet.html`
is that render.

## Commands

| Command | What it does |
|---|---|
| `validate <workflow> [--json]` | Checks `sheet.toml` and its data. Reports errors and warnings with file and line. Exits 1 on errors. |
| `cases <workflow>` | Writes `cases.toml`: the smallest practical set of cases that covers every decision-table rule, reject, both sides of every situational `when`, and isolation pairs for rows that are not sent. |
| `eval <workflow> --case ID` | Evaluates one input and prints every row's value, which table rule or lookup entry fired, rejects, and send decisions. Inputs can also come from `--input` (values by row ID) or `--request` and `--context` (read by source path). `--json` prints the result and trace as JSON. |
| `verify <workflow> --actual results.json` | Compares the output payloads that tests captured with the expected values, then records evidence per row and per decision-table cell. Contradictions name the case and the field. Exits 1 on contradictions. |
| `render <workflow\|flow> --out page.html` | Writes one self-contained HTML file with the latest evidence for the sheet's version merged in. Works offline and prints on A4 or Letter landscape. `--evidence shown\|hidden` sets the initial state of the page's evidence switch; `--no-evidence` leaves evidence out entirely. For a flow, draws it left to right; `--check` adds the review (missing and unfit fields, and what each team has to agree). |
| `serve <workflow>` | Serves the same page on localhost, re-rendered on every request, so edits show on reload. Takes the same evidence flags. |
| `slice <flow> [--out flow.html]` | Places each row of a flow in a team's step and reports, for every boundary, which fields are carried, missing, unfit or unused. `--out` writes the view with the review marked. See [docs/flows.md](docs/flows.md). |
| `interface import <schema> --out <file>` | Imports an interface from OpenAPI (a request or response body), Avro or JSON Schema. |
| `fetch <flow>` | Clones or updates the other teams' repositories a flow refers to. |
| `schema [sheet\|flow\|interface]` | Prints a JSON Schema. |

Exit codes: 0 for success, 1 when a check fails, 2 for a usage error.

## Flows across teams

When a transformation crosses several teams (API calls, Kafka topics), a
**flow** slices the end-to-end sheet at each team's boundary. The product
manager writes the input, the output and the derivation; each team brings
its existing interfaces, imported from OpenAPI, Avro or JSON Schema, and
names the rows it computes. `render` draws the flow left to right, and
`dsheets slice` (or `render --check`) reports what every boundary must
carry and what each team has to change. Interfaces can live in each team's
own repository, pinned by git tag.

```sh
bin/dsheets slice  flows/checkout-to-payment
bin/dsheets render flows/checkout-to-payment --out flow.html
```

See [docs/flows.md](docs/flows.md); `examples/flow.html` is the example's view, and
`examples/walkthrough/` has the same flow at four steps of a review, each rendered on its own.

## Layout

```
specs/<workflow>/
  sheet.toml        authored specification
  cases.toml        generated, then committed
  data/             design-time values: lookup CSVs and config.toml
flows/<flow>/
  flow.toml         input, output and the stages between them
interfaces/<team>/  interface files, and the schemas they were imported from
schema/             JSON Schemas for sheet.toml, flow.toml and interfaces (also embedded in the binary)
.evidence/          git-ignored evidence runs, one JSON file per run
cmd/dsheets/        CLI
internal/           sheet model, validator, evaluator, case generator, evidence, renderer
```

See [docs/format.md](docs/format.md) for the file format and
[docs/editor.md](docs/editor.md) for VS Code setup.

## Display settings

Column headers, section titles, and whether evidence starts shown or hidden
are configurable. Settings layer in this order: built-in defaults, then a
project-wide `dsheets.toml` (the nearest one at or above the workflow, within
the repository), then the sheet's own `[display]` table, then command-line
flags.

```toml
# dsheets.toml at the repository root, or [display] inside a sheet.toml
[display]
evidence = "hidden"            # initial state of the evidence switch: "shown" (default) or "hidden"

[display.columns]
number   = "#"
value    = "Value"
rule     = "Rule"
send     = "Sent as"           # can name the target, e.g. "Sent to billing as"
evidence = "Evidence"

[display.sections]
request = "From the request"
context = "From context"
data    = "From database and config"
derived = "Derived"
```

Any subset can be given. The evidence switch in the page header is a plain
checkbox: it hides the Evidence column, the marks inside rules, the header
evidence line, and the Contradicted and Not exercised notes. It needs no
script, and a printed page shows whatever state is on screen.

## results.json

`verify` reads the actual output payloads, keyed by case ID:

```json
{
  "cases": {
    "loyalty-tier-gold":             { "payload": { "loyaltyTier": "GLD", "totalAmount": 96 } },
    "reject-destination-not-served": { "reject": "DESTINATION_NOT_SERVED" }
  }
}
```

The `"cases"` wrapper is optional. An entry whose only key is neither
`payload` nor `reject` is read as a bare payload.

## How evidence is judged

Each case is evaluated again to find which rows and branches it exercised.
Each row is then judged by the compared fields it influences:

- A row whose own sent field disagrees is contradicted.
- A row that is not sent is contradicted only when every field it feeds
  disagrees.
- Otherwise the row agrees, because a field downstream that matched vouches
  for it.

Branch evidence (table rules, rejects, `when` sides, inline lookup values)
follows the row's verdict in each case. A row is partly verified while some of
its branches have never been exercised.

## Status

Phase 1 is complete: schema, validator, evaluator, case generator, renderer,
verify. Phase 2 (visual editor, formatter, PR diffs, LLM drafting, DMN export)
and Phase 3 (documenting existing code from traces) are not started. The
evidence store is an interface (`internal/evidence.Store`), so trace runs can
move to a database or CI artifacts later.
