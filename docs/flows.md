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


## Exporting each team's piece

```sh
dsheets slice flows/checkout-to-payment --export examples/pieces
```

writes each team's part as a flow and sheet of its own, which the team can
take into its repository:

| Piece | Receives | Calls | Produces |
|---|---|---|---|
| `1-storefront-team` | `checkout-request` | | `order-placed` |
| `2-orders-team` | `order-placed` | `shipping-quote` (Shipping team) | `payment-request` |
| `3-shipping-team` | `quote-request` | | `quote-response` |

A caller's piece spans its calls, so it says exactly what the team calls
and with what: the `call` stage in its `flow.toml`, sends with
`target = "shipping-quote"` for the request, and inputs from
`call.shipping-quote.<field>` for the response. Values a boundary does not
carry yet arrive with an open question naming the proposed field. Data
files the piece uses are copied with it. `slice --export` checks every
piece on its own after writing it.

## Branching off, and feeding in

The reverse of slicing: add a new consumer or a new producer to an
interface that already exists. Write it as a flow of its own, with the
existing interface as its input (a branch) or its output (a feed), and
check it against the flows already using that interface:

```sh
dsheets slice flows/loyalty-report --with flows/checkout-to-payment   # branch off order-placed
dsheets slice flows/mobile-orders  --with flows/checkout-to-payment   # feed into order-placed
```

- **Branch.** Each value the branch needs that the interface lacks is traced
  upstream through every flow that produces the interface. Names differ
  between services, so the trace looks for the value by name in the
  producer's sheet and in the interfaces before it, and labels what it finds
  as a guess: *probable origin: Member (is_member, received as
  customer.isMember; matched by name, please confirm); add isMember to
  order-placed*. Only the owning team can confirm it, by mapping the field
  in its own sheet.
- **Feed.** The new producer must produce every field the existing
  consumers actually read from the interface, which is more precise than
  the schema's `required` list: *loyaltyTier: flow checkout-to-payment
  reads it: needed by Discount rate (Orders team), Free shipping (Orders
  team), the output*.

`--with` can be repeated. Flows meet only where they reference the same
interface.

## One grid for several flows

```sh
dsheets render examples/pieces/* --out pieces.html
dsheets serve  flows/checkout-to-payment flows/loyalty-report flows/mobile-orders --check
```

Several flow folders draw as one table. A flow whose input is another's
output continues it; a flow from a call's request to its response fills
that call's callee step; a branch or a feed is drawn after, its first column
marked *Branch from …* or *Feeds …*. Values line up across flows through
the fields of the interfaces they share, including fields still being
agreed. The exported pieces of a flow combine back into the flow's own
layout. With `--check`, each flow is also checked against the others.

## Not yet

- Lists ("for each item"): arrays are imported as a single `array` field.
