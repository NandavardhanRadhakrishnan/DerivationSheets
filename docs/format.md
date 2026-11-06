# Sheet format

A workflow folder holds `sheet.toml` (authored), `cases.toml` (generated, then
committed), and an optional `data/` folder of design-time values. TOML is
used because indentation carries no meaning: indenting `[row.send]` and
other sub-blocks is purely cosmetic.

## Top-level keys

| Key | Required | Meaning |
|---|---|---|
| `sheet` | yes | Slug for the workflow, such as `order-checkout` |
| `version` | yes | Integer, incremented on every reviewed change. Evidence is keyed by it. |
| `title` | no | Display title |
| `request_schema` | no | Path to the incoming request's JSON Schema |
| `target_schema` | no | Schema of the request sent to the target: a JSON Schema path, or a JSON/YAML OpenAPI file with a pointer, e.g. `billing.yaml#/components/schemas/Invoice`. When set, `validate` checks every `send.field` against it, and checks that every required field is sent. |

## Display

An optional `[display]` table controls presentation only. It never affects
evaluation, and it overrides the project-wide `[display]` in `dsheets.toml`.

| Key | Meaning |
|---|---|
| `evidence` | Initial state of the rendered page's evidence switch: `"shown"` (default) or `"hidden"` |
| `[display.columns]` | Header labels: `number`, `value`, `rule`, `send`, `evidence` |
| `[display.sections]` | Section titles: `request`, `context`, `data`, `derived` |

```toml
[display]
evidence = "hidden"

[display.columns]
send = "Sent to billing as"
```

`dsheets.toml` takes the same `[display]` table. At the repository root it
can also name a `registry` and declare `[sources]`; see
[repositories.md](repositories.md). `validate` reports unknown keys in either
file with their line.

## Rows

Each `[[row]]` is one value. **Input rows** have a `source` and no rule.
**Derived rows** have `uses` and exactly one of `formula`, `table`, `lookup`
or `text`.

| Key | Applies to | Meaning |
|---|---|---|
| `id` | all | Unique, lowercase snake_case, stable across versions |
| `name` | all | Plain-language display name |
| `description` | all | Optional longer text |
| `source` | inputs | `request.<path>`, `context.<name>`, `db.<table>`, `config.<key>`, or `call.<stage>.<field>` (a field of a call's response, in a sheet that belongs to a flow) |
| `type` | inputs | `string`, `number`, `boolean`, `date` or `datetime` |
| `usage`, `when` | inputs | `R` (default): a missing value rejects the request with `MISSING_INPUT`. `S`: required only when `when` is true. |
| `default` | inputs | Used when the input is missing |
| `uses` | derived | Row IDs used by the rule, rejects and `send.when` |
| `formula` | derived | CEL expression |
| `[row.table]` | derived | Decision table |
| `lookup` | derived | `{ from = "db.x" or "config.x", key = [ids] }` |
| `values` / `values_file` | lookups | Design-time values: inline (one key only) or a CSV in `data/` |
| `text` | derived | Plain-language rule. Not computable, so it is reported as an open item. |
| `[[row.reject]]` | derived | `when`, `code`, `message`. Rejects the whole request. |
| `[row.send]` | any | `field`, `usage`, `when`, `otherwise`, `map`, and `target` for a call. Use `[[row.send]]` for several sends. |
| `open_questions` | any | Unresolved questions. `validate` reports them as warnings. |

### Sending

A row without `[row.send]` is not sent. `usage = "R"` means the field is
always required. `usage = "S"` means the field is situational, and needs:

- `when`: an expression stating when the field is required
- `otherwise`: `"do-not-send"` or `"sender-discretion"`

`field` may be dotted (`applicant.age`) to nest objects. `map` translates
values to the codes the target expects. Keys are the string forms of values, so
`{ true = "MANUAL", false = "STP" }` maps booleans. `validate` checks that the
map covers every value the row can produce, when those values are known.

### Calls made on the way

In a sheet that belongs to a flow with a `call` stage, a row can also be
sent in that call's request, and a field of its response can be read as an
input:

```toml
[[row]]
id     = "shipping_method"
source = "request.shippingMethod"

  [[row.send]]                 # to the sheet's own target
  field = "shippingMethod"
  usage = "R"

  [[row.send]]                 # in the shipping-quote call's request
  target = "shipping-quote"
  field  = "method"
  usage  = "R"

[[row]]
id     = "shipping_fee"
name   = "Shipping fee"
source = "call.shipping-quote.fee"   # from the call's response
```

A row has at most one send without `target`. `eval` prints what each call
is sent after the payload. `cases` writes the expected request of every
call (`expected_calls`), and `verify` compares it with the request tests
captured; a call response is an ordinary input in `input`, keyed by its row
ID, for the test to stub.

## Decision tables

```toml
  [row.table]
  hit_policy = "first"           # "unique" (default) or "first"
  columns = ["subtotal", "loyalty_tier", "=>"]
  rows = [
    [">= 100", "-",    true],
    ["-",      "Gold", true],
    ["-",      "-",    false],
  ]
```

A cell is one of:

- a literal, such as `true`, `"High"` or `46`
- `"-"`, meaning any value, including a missing one
- a comparison, such as `"< 18"`, `">= 46"` or `"!= High"`

The last cell is the result. Under `unique`, exactly one rule may match each
input; under `first`, the first match wins.

`validate` enumerates one representative value for every region the cells
distinguish, and reports any combination that no rule covers and, under
`unique`, any combination that two rules both match. Known domains are used
where available: booleans, the results of an upstream table, and lookup
values. For a string column with no known domain, "any other value" counts as
a case, so a catch-all `"-"` rule is needed.

## Lookups and data files

`values_file` points to a CSV. The header names the key columns in the order
of `lookup.key`, followed by one result column, conventionally `value`. Lines
starting with `#` are comments.

```csv
zone,shipping_method,weight_kg,value
DOMESTIC,STANDARD,[0..1],5
DOMESTIC,STANDARD,(1..5],9
DOMESTIC,STANDARD,(5..30],18
```

Key cells use the decision-table grammar plus ranges, which are allowed in
lookup data only:

| Range | Means |
|---|---|
| `[0..1]` | 0 ≤ x ≤ 1 |
| `(1..5]` | 1 < x ≤ 5 |
| `[0..18)` | 0 ≤ x < 18 (`]` and `[` may also mark open ends, as in DMN) |

`validate` reports entries that overlap (an error) and gaps inside the range
the data covers (a warning). Integer bounds that differ by one, as in
`[0..9]` and `[10..19]`, count as adjacent.

`data/config.toml` holds design-time config values. A row with source
`config.tax.sales_tax_rate` reads `[tax] sales_tax_rate = 0.08`. A config lookup with no
`values` reads the table at its key.

## Expressions

`formula`, `when`, `reject.when` and `send.when` use
[CEL](https://cel.dev). Identifiers are row IDs, and every identifier must
appear in the row's `uses`. Rejects and `send.when` may also refer to the row
itself. An input row's `when` may refer to other rows directly.

Sheets have a single number type, so every number is a CEL double: integer
literals are read as doubles (`x * 2` means `x * 2.0`). `date` and `datetime`
values are CEL timestamps.

| Function | Meaning |
|---|---|
| `round(x)` | Rounds to the nearest whole number, halves away from zero |
| `years_between(a, b)` | Whole years from a to b, counting a year only once its anniversary is reached |
| `today()` | The date of `context.request_time`. Never the real clock. Needs a row with that source. |

The evaluator sits behind `internal/expr.Engine`, so FEEL can replace CEL.

## cases.toml

Generated by `dsheets cases`; do not edit, except expected values marked
`"?"`. Those depend on text rules, and are kept when the file is regenerated.

```toml
[[case]]
id     = "loyalty-tier-gold"
covers = ["loyalty_tier:[true,>= 2]", "discount_rate:Gold", "free_shipping:[-,Gold]", "customs_value:send.when:true"]
input  = { signup_date = "2020-10-02", is_member = true, country = "CA", ... }
expected = { loyaltyTier = "GLD", discountAmount = 10, freeShipping = true, customsValue = 89, ... }
```

In a sheet that makes calls, each case that is not rejected also lists the
request every call should receive, one line per call stage:

```toml
expected_calls.shipping-quote = { method = "STANDARD", weightKg = 2, zone = "DOMESTIC" }
```

Coverage keys, which are also the branch keys used for evidence:

| Key | Branch |
|---|---|
| `row:[c1,c2]` | Decision-table rule, identified by its input cells |
| `row:reject:CODE` | Reject condition |
| `row:send.when:true` / `:false` | Both sides of a situational send |
| `row:send.STAGE.when:true` / `:false` | Both sides of a situational send to a call |
| `row:when:true` / `:false` | Both sides of a situational input |
| `row:KEY` | An inline lookup value, such as `discount_rate:Gold` |
| `row:isolation` | A pair of cases that differ only in inputs feeding this unsent row, so a wrong value in it would show in the payload or in a call. Rows sent to a call are checked directly and need no pair. |

Dates are placed relative to `reference_time`, which is fixed so the output
stays stable from run to run.

## Canonical layout

Indentation and alignment carry no meaning, but tools that write sheets,
flows and interfaces (the editor, and a future formatter) write them the
way `specs/order-checkout/sheet.toml` is written, so the git diff of a
change shows only what changed:

- Keys in a fixed order. A row: `id`, `name`, `description`, `source`,
  `type`, `usage`, `when`, `default`, `uses`, `formula`, `lookup`, `values`,
  `values_file`, `text`, `open_questions`; then `[row.table]`,
  `[[row.reject]]` and `[row.send]`. A send: `target`, `field`, `usage`,
  `when`, `otherwise`, `map`.
- The `=` of the keys in one table lines up with the longest key, except
  long ones that would push the rest too far: `description`, `values_file`
  and `open_questions` in a row, every key in `[row.table]`,
  `callee_computes` in a stage, `description` in an interface field.
- Sub-tables are indented two spaces and preceded by a blank line.
- A decision table's `rows` has one rule per line, cells lined up.
- Values keep the text they were written with (`0.10` stays `0.10`) until
  they change.

A tool that changes a file rewrites only the rows, stages, interface
fields and top-level keys the change touches. Comments directly above a row
move and are deleted with it; section comments such as
`# ---------- Derived ----------` and blank lines between rows stay where
they are.

## Evidence

Evidence is never stored in the sheet. `verify` writes one JSON file per run
to `.evidence/<sheet>/v<version>-<timestamp>-test.json` at the repository
root, and `render` merges in the newest run for the sheet's current version.

| State | Glyph | Meaning |
|---|---|---|
| Asserted | ◇ | No test or trace has checked it |
| Verified | ● n | All n relevant cases matched |
| Partly verified | ◐ n of m | Some table cells or branches were never exercised |
| Unexercised | ○ | No case took this path |
| Contradicted | ✕ n | n cases disagreed |
