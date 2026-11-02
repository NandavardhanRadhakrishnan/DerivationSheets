# Flows: slicing a derivation across teams

A single derivation sheet describes one transformation. Real flows usually
cross several teams: an API call here, a Kafka topic there, each owned by
someone else. A **flow** lets everyone agree on those boundaries:

1. **The product manager** writes the end-to-end derivation as an ordinary
   sheet: the input, the output, and how each output value is derived. It
   says nothing about who computes what.
2. **Each team** brings the interfaces it already has (imported from
   OpenAPI, Avro or JSON Schema) and adds its boundary to the flow as a
   stage, naming the rows it computes.
3. **`dsheets slice`** places every row in a team's step, works out which
   values must cross each boundary, and compares that with what each
   interface carries. The report is the agenda for the meeting: fields to
   add, fields that don't fit, fields nobody uses.

```sh
dsheets render flows/checkout-to-payment --out flow.html          # the flow, drawn left to right
dsheets serve  flows/checkout-to-payment                          # same view on localhost
dsheets slice  flows/checkout-to-payment                          # boundary report, for review
dsheets render flows/checkout-to-payment --check --out flow.html  # the view with the review marked
```

## Interfaces

An interface is any boundary a flow passes through: an API request or
response, a Kafka topic, an event, a file. Each is saved as its own TOML
file, so any flow can bring it in.

```toml
interface = "order-placed"
kind      = "kafka-topic"     # api-request, api-response, kafka-topic, event, file, other
owner     = "Orders team"
status    = "existing"        # existing: missing fields become requests to the owner
                              # proposed: being designed in this flow

[[field]]
name     = "weightKg"
type     = "number"           # string, number, boolean, date, datetime, object, array
required = false
```

Nested fields use dotted names (`customer.signupDate`). Don't write these by
hand when a schema exists; import it:

```sh
# OpenAPI: a request or response body, or any schema by pointer
dsheets interface import shipping.openapi.yaml --operation getShippingQuote --part request \
    --name quote-request --owner "Shipping team" --out interfaces/shipping/quote-request.toml
dsheets interface import shipping.openapi.yaml --operation getShippingQuote --part response \
    --name quote-response --owner "Shipping team" --out interfaces/shipping/quote-response.toml

# Avro, for Kafka topics: union with null means optional
dsheets interface import order-placed.avsc --owner "Orders team" --out interfaces/orders/order-placed.toml

# JSON Schema
dsheets interface import payment-request.schema.json --kind api-request --status proposed \
    --owner "Payments team" --out interfaces/payments/payment-request.toml
```

The file records where it was imported from; import again to refresh it.

## flow.toml

```toml
flow    = "checkout-to-payment"
version = 1
sheet   = "../../specs/order-checkout"   # the end-to-end sheet

[sources]                               # other teams' repositories, by alias
shipping = { git = "https://github.com/example/shipping-contracts.git", ref = "v3.2.0" }

[input]
interface = "../../interfaces/storefront/checkout-request.toml"

[[stage]]                               # a topic: the next team takes over
kind      = "handoff"
interface = "../../interfaces/orders/order-placed.toml"
producer  = "Storefront team"
computes  = ["customer_years", "loyalty_tier"]

[[stage]]                               # an API call: the caller keeps its state
id       = "shipping-quote"
kind     = "call"
caller   = "Orders team"
computes = ["zone", "discount_rate", "discount", "free_shipping"]
callee   = "Shipping team"
callee_computes = ["shipping_fee"]

  [stage.request]
  interface = "shipping:quote-request.toml"

  [stage.response]
  interface = "shipping:quote-response.toml"
  carries   = { fee = "shipping_fee" }

[output]
interface = "../../interfaces/payments/payment-request.toml"
producer  = "Orders team"
computes  = ["shipping_cost", "customs_value", "net_amount", "sales_tax", "total"]
```

### Two kinds of stage

| Kind | Use for | What must cross it |
|---|---|---|
| `handoff` | Kafka topics, events, fire-and-forget calls: the next team takes over | Every value anyone after it needs |
| `call` | Request and response: the caller waits and carries on | The request carries what the callee needs; the response carries what the callee computed. The caller keeps everything else. |

After a call, the caller continues, so the next stage's producer (or the
output's) must be the caller, or left out.

### Which row each field carries

Fields are matched to rows by name, ignoring case and separators:
`weightKg` matches the row `weight_kg` or a source `request.order.weightKg`.
The input matches request paths exactly; the output matches the sheet's
send fields exactly. When names differ, say so with `carries`:

```toml
carries = { fee = "shipping_fee" }    # field = row id
```

### Who computes what

Rows listed in `computes` (or `callee_computes`) are placed in that step.
Any other derived row goes in the latest step that can still serve every
row that uses it and every boundary that carries it; callees only compute
what they are given. Context, database and config inputs belong to the
step that reads them and never cross a boundary.

`slice` reports an error when a row is placed after something that needs
it.

## Interfaces in other teams' repositories

Each team keeps its interfaces in its own repository. A flow declares those
repositories once under `[sources]` and refers to files as `alias:path`:

```toml
[sources]
shipping = { git = "https://github.com/example/shipping-contracts.git", ref = "v3.2.0" }
orders   = { path = "../../../orders-contracts" }   # a local checkout
```

- `ref` is required for git sources and should be a tag or commit, so
  everyone in the meeting sees the same version. The report shows the
  resolved commit.
- Clones are cached in `.dsheets/sources/` at the repository root
  (git-ignored). `dsheets fetch <flow>` updates them; `slice --offline`
  never touches the network.
- Git uses your normal credentials, so private repositories work as they do
  for `git clone`.

## Reading the report and the view

| Mark | Meaning |
|---|---|
| ✓ | The value must cross here, and the interface carries it |
| ✕ | The value must cross here, but the interface has no field for it. For an existing interface this is a request to its owner. On the output, also a required field nothing produces. |
| ⚠ | The field exists but doesn't fit: optional when always needed, a different type, or values outside its enum |
| ○ | Carried but not needed after this point, or not used by the flow at all |

`render` and `serve` draw the flow left to right: interfaces in shaded
columns, each team's step between them. Each row follows one value: where
it enters, where it is computed (with its rule), and every field that
carries it (→ passes through, ↷ kept by the caller across a call). By
default that is all the page shows; it is a picture of the flow.

`--check` adds the review: ✕ and ⚠ marked in the table, counts in the
headers, a **To agree** list grouping every ✕ and ⚠ by the team that owns
the interface, and the fields nobody uses. `slice --out` always writes the
checked view.

`slice --strict` exits 1 while anything is missing or doesn't fit, so a CI
job can hold a flow until every team has agreed.


## Not yet

- Writing each team's step out as its own derivation sheet. A caller's step
  spans a call, so its sheet needs inputs from a response and sends to two
  targets. That needs a `call.*` source and per-target sends in the sheet
  format, which is a format change to agree first.
- Lists ("for each item"): arrays are imported as a single `array` field.
