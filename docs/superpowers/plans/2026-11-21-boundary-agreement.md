# Boundary Agreement Implementation Plan (steps 1 to 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Check team sheets against the PM's sheet by running the PM's cases through the teams' own flows and comparing values at the boundaries, and close `requirements` items on that evidence instead of rule text.

**Architecture:** A new package `internal/agree` builds the PM's expected boundary values from its sheet (generated cases, evaluated), splits the PM's flow into team segments, finds each team's own flow for its segment by interface name, evaluates the team sheets (resolving calls by evaluating the callee's sheet) and reports one check per case and boundary field. `dsheets slice --cases` prints the end-to-end report; `requirements` uses the isolated report for its done-check.

**Tech Stack:** Go (standard library), existing packages `internal/eval`, `internal/cases`, `internal/flow`, `internal/sources`, `internal/requirements`.

**Spec:** `docs/superpowers/specs/2026-11-21-boundary-agreement-design.md` (sections 1 to 5 and the delivery order's steps 1 to 3). Section 6, the editor's Requirements panel and the `--flow` pointer, is a separate plan.

## Global Constraints

- Never close an item on a guess (D-22): anything dsheets cannot evaluate is "cannot tell", which maps to `undocumented`, never `done`.
- A PM row carried on any boundary, outer or between two teams, is binding (D-23).
- Values compare with `sheet.Equal`, as `verify` does.
- Interfaces are matched between the PM's flow and a team's own flow by the interface's declared name (`iface.Interface.Name`), as `flow.Connect` does; file paths differ between repositories. (This refines the spec's "matched by file".)
- Owner repositories are read through the registry and fetcher `requirements` already uses, on the branch the registry follows; `--offline` and `--no-check` keep their meaning.
- The PM's expected values come from evaluating the PM's sheet at head for generated cases (`cases.Generate`), not from its committed `cases.toml`, which is read only for hand-filled `"?"` values matched by case id.
- Restore `specs/` before running tests or merging (`git checkout -- specs/`).
- Windows worktree: some files are CRLF in the worktree; run `gofmt -l` on changed files before each commit.

## Plan decisions (not in the spec; flagged to the developer)

- **Fallback for teams without a matching own flow.** The spec says such a stage is "cannot tell". A team that keeps sheets but no flow would then lose the T-30 rule-text check it has today (`TestRunClosesAnItemFromTheOwnersSheet`). This plan keeps the T-30 structural result for an item when its team has no matching own flow, and adds the note `checked by rule text: no flow of <team> joins <in> to <out>`.
- **Receive and Carry stay structural only.** A Receive item's boundary field is produced by another team; gating it on that team's values would make one team's item depend on another's work. Send items add the value check on their own field.

## Review Focus

- **Send maps.** A row sent with `map` (`{ Gold = "GLD" }`) puts the code on the wire. Expected and fed values must be wire values; comparing row values would report every mapped field as differs. Test: `TestWireValueUsesTheSendMap` (Task 2).
- **Dotted field names.** `customer.signupDate` is nested in payloads. Reading and feeding must use paths. Test: `TestDottedFieldIsFedAndCompared` (Task 4).
- **A team's call stage has another id than the PM's.** Orders' own flow may call the stage `quote` where the PM's says `shipping-quote`; sources read `call.quote.fee`. Test: `TestTeamStageIdDiffers` (Task 4).
- **A PM case that rejects.** No field after the reject is compared; the team that owns the reject's row must reject with the same code. Test: `TestRejectMustMatch` (Task 4).
- **Numbers that are equal but typed differently** (`4` and `4.0`, int from TOML and float from CEL). Test: `TestNumbersCompareByValue` (Task 4).

---

## File structure

| File | Responsibility |
|---|---|
| `internal/eval/eval.go` | add `Pending` input marker and exported `SetPath` |
| `internal/flow/analyze.go` | add `(*Boundary).RowOf(field) string` |
| `internal/flow/find.go` (new) | `FindDirs(root)`, moved from `internal/impact` |
| `internal/sources/registry.go` | `TeamAliases(team, reg)`, moved from `internal/requirements` |
| `internal/agree/agree.go` (new) | types, `Run` |
| `internal/agree/expect.go` (new) | PM cases and wire values |
| `internal/agree/segment.go` (new) | PM segments, matching team flows |
| `internal/agree/chain.go` (new) | evaluating a segment, checks |
| `internal/agree/registry.go` (new) | `RegistryTeams`: team flows from the registry |
| `internal/agree/testdata/shop/...` (new) | fixture: PM flow and three team flows |
| `internal/agree/*_test.go` (new) | tests |
| `cmd/dsheets/flowcmd.go` | `slice --cases` |
| `internal/requirements/agree.go` (new) | apply an agree report to requirements |
| `internal/requirements/model.go`, `run.go`, `output.go` | `Cases` on items, wiring, printing |
| `docs/flows.md`, `docs/requirements.md`, `README.md` | docs |

---

### Task 1: Groundwork in eval, flow and sources

**Files:**
- Modify: `internal/eval/eval.go` (input handling at `:222-265`, `setPath` at `:568`)
- Modify: `internal/flow/analyze.go` (next to `CarryOf`, `:109`)
- Create: `internal/flow/find.go`; Modify: `internal/impact/impact.go` (delete `flowDirs`, `:275-291`, call `flow.FindDirs`)
- Modify: `internal/sources/registry.go`; `internal/requirements/check.go` (delete `ownerAliases`, `:65-85`, call `sources.TeamAliases`)
- Test: `internal/eval/eval_test.go`, `internal/flow/flow_test.go`, `internal/sources/registry_test.go`; move `TestOwnerAliases` from `internal/requirements/check_test.go` to `internal/sources/registry_test.go` as `TestTeamAliases`

**Interfaces:**
- Produces:
  - `type Pending struct{ Reason string }` in `eval`: an `Input` value meaning "not known yet". The row's status becomes `StatusUnknown` with `Detail = Reason`; no `MISSING_INPUT` reject; rows depending on it are unknown and not sent.
  - `func SetPath(m map[string]any, path string, v any)` in `eval` (exported wrapper of `setPath`).
  - `func (b *Boundary) RowOf(field string) string` in `flow`: the row ID the field carries, `""` when none.
  - `func FindDirs(root string) []string` in `flow`: every folder under root holding `flow.toml`, skipping `.git` and `.dsheets`, sorted.
  - `func TeamAliases(team string, reg map[string]Source) []string` in `sources`: same behaviour as today's `ownerAliases`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/eval/eval_test.go
func TestPendingInputIsUnknownNotMissing(t *testing.T) {
	// sheet: fee from call.quote.fee (number); cost = fee * 2, sent as cost;
	// weight from request.weight, sent to target quote as weight.
	e := mustEngine(t, pendingSheet)
	res := e.Evaluate(Input{"weight": 3, "fee": Pending{Reason: "call quote not made yet"}})
	if res.Reject != nil {
		t.Fatalf("pending is not missing, got reject %+v", res.Reject)
	}
	if res.Status["fee"] != StatusUnknown || res.Status["cost"] != StatusUnknown {
		t.Errorf("status = %v", res.Status)
	}
	if res.CallPayloads["quote"]["weight"] != 3 {
		t.Errorf("the call request is still built, got %v", res.CallPayloads)
	}
	if _, sent := res.Payload["cost"]; sent {
		t.Error("an unknown value is not sent")
	}
}

// internal/flow/flow_test.go
func TestRowOf(t *testing.T) {
	a := example(t, nil)
	// the example flow's shipping-quote response carries fee = shipping_fee
	for _, b := range a.Boundaries {
		if b.Role == RoleResponse && b.RowOf("fee") != "shipping_fee" {
			t.Errorf("RowOf(fee) = %q", b.RowOf("fee"))
		}
	}
	if a.Boundaries[0].RowOf("no-such-field") != "" {
		t.Error("an uncarried field has no row")
	}
}

func TestFindDirs(t *testing.T) // two flow.toml folders and one under .dsheets: returns the two, sorted
```

Use whatever helper the eval tests already use to build an engine from TOML text in place of `mustEngine`, if one exists.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/eval ./internal/flow ./internal/sources -run 'Pending|RowOf|FindDirs|TeamAliases'`
Expected: FAIL, undefined `Pending`, `RowOf`, `FindDirs`, `TeamAliases`.

- [ ] **Step 3: Implement the five additions and the two moves**

In `input()`, check for `Pending` before the `in[r.ID]` value is coerced. `RowOf` searches `b.carry` (row → `*Carry`) for `Carry.Field.Name == field`. Moves are cut and paste; callers switch to the new names.

- [ ] **Step 4: Run the whole suite**

Run: `git checkout -- specs/ && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/eval internal/flow internal/sources internal/impact internal/requirements
git commit -m "eval: pending inputs; flow: RowOf, FindDirs; sources: TeamAliases"
```

---

### Task 2: The fixture and the PM's expected values

**Files:**
- Create: `internal/agree/testdata/shop/` (below), `internal/agree/agree.go`, `internal/agree/expect.go`, `internal/agree/expect_test.go`, `internal/agree/helpers_test.go`

**The fixture.** Every path is local and relative, with no git and no aliases. Folder layout:

```
shop/interfaces/{checkout-request,order-placed,quote-request,quote-response,payment-request}.toml
shop/product/specs/shop/sheet.toml   shop/product/flows/shop/flow.toml      (PM)
shop/cart/specs/cart/sheet.toml      shop/cart/flows/cart/flow.toml
shop/orders/specs/orders/sheet.toml  shop/orders/flows/orders/flow.toml
shop/shipping/specs/ship/sheet.toml  shop/shipping/flows/ship/flow.toml
```

Interface fields, all required:
- `checkout-request`: `member` boolean, `weight` number
- `order-placed`: `tier` string, `weight` number
- `quote-request`: `weight` number
- `quote-response`: `fee` number
- `payment-request`: `shippingCost` number

PM sheet `shop`:
- inputs `member` (`request.member`, boolean) and `weight` (`request.weight`, number);
- `tier = member ? "Silver" : "Standard"`;
- `shipping_fee = weight * 2`, with a reject `weight > 30`, code `TOO_HEAVY`;
- `shipping_cost = tier == "Silver" ? shipping_fee / 2 : shipping_fee`, sent as `shippingCost`.

PM flow `shop`:
- input `checkout-request`;
- handoff `order-placed`, producer `Cart team`, computes `["tier"]`;
- call `shipping-quote`, caller `Orders team`, callee `Shipping team`, `callee_computes = ["shipping_fee"]`; request `quote-request`, response `quote-response` with `carries = { fee = "shipping_fee" }`;
- output `payment-request`, producer `Orders team`, computes `["shipping_cost"]`.

Team sheets and flows:
- **Cart.** Inputs `member`, `weight` from the request. `tier` as the PM's, sent as `tier`; `weight` sent as `weight`. Flow: input `checkout-request`, output `order-placed`, producer `Cart team`.
- **Orders.** Inputs `tier` and `weight` from the request. `weight` sent to target `quote` as `weight`. `fee` from `call.quote.fee`. `shipping_cost = tier == "Silver" ? fee / 2 : fee`, sent as `shippingCost`. Flow: input `order-placed`, call stage `quote` (caller Orders, callee Shipping, request `quote-request`, response `quote-response`), output `payment-request`.
- **Shipping.** Input `weight` from the request. `fee = weight * 2` with the `TOO_HEAVY` reject, sent as `fee`. Flow: input `quote-request`, output `quote-response`, producer `Shipping team`.

All four flows must pass `dsheets slice` with no errors.

**Interfaces:**
- Consumes: Task 1.
- Produces (in `agree.go`; types are used by every later task):

```go
type Mode int
const (
	Isolated Mode = iota // each team fed the PM's values at its incoming handoff
	EndToEnd             // each team fed the previous team's output
)

const (
	Agrees     = "agrees"
	Differs    = "differs"
	CannotTell = "cannot-tell"
)

// Check is one compared value: a boundary field, or a reject, in one case.
type Check struct {
	Case     string `json:"case"`
	Boundary int    `json:"-"`         // index in the PM analysis's Boundaries
	Role     string `json:"role"`      // flow.Role*
	Stage    string `json:"stage"`     // the PM's stage id
	Iface    string `json:"interface"` // interface name
	Field    string `json:"field"`     // interface field, or "reject"
	Row      string `json:"row,omitempty"` // the PM row the field carries
	Team     string `json:"team"`      // team whose step produces the value
	Status   string `json:"status"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Note     string `json:"note,omitempty"`
}

type Report struct {
	Flow   string
	Mode   Mode
	Cases  int
	Checks []Check
	Notes  []string // what could not be read, once each
}

// TeamFlows returns the analysed own flows of a team, and notes on what
// could not be read.
type TeamFlows func(team string) (flows []*flow.Analysis, notes []string)

type Options struct {
	Mode    Mode
	RefTime time.Time   // generated cases' reference time; zero means cases.DefaultReferenceTime
	Filled  *cases.File // the PM's committed cases.toml, for "?" values; may be nil
}
```

- In `expect.go`:
  - `type pmCase struct{ ID string; Res *eval.Result; Filled map[string]any }`. `Filled` holds the field → value pairs from `Options.Filled` for this case id, excluding `"?"`.
  - `func pmCases(pm *flow.Analysis, o Options) []pmCase`
  - `func wire(pm *flow.Analysis, c pmCase, b *flow.Boundary, field string) (v any, ok bool)`:
    - when the field carries no PM row (`RowOf == ""`), `ok` is false;
    - when the PM row has a send whose field normalises equal to `field` (an own send on the output boundary, or a call send to `b.StageID` on a request boundary), it returns the sent value from `Res.Payload` or `Res.CallPayloads`, so the send map is applied;
    - otherwise it returns `Res.Values[row]` when the status is not unknown;
    - otherwise `Filled[field]`;
    - otherwise `ok` is false.
- In `helpers_test.go`:
  - `func shop(t *testing.T, patch map[string]string) (pm *flow.Analysis, teams TeamFlows)`. It copies `testdata/shop` to a temp folder, replaces whole files by relative path from `patch`, analyses the PM flow, and serves teams by mapping `"Cart team"` to `cart/flows/cart`, and likewise for Orders and Shipping.
  - `func pmCaseWith(t *testing.T, pm *flow.Analysis, kv ...any) pmCase`: the first generated case whose input has these row values; fails the test when there is none.
  - `func boundary(pm *flow.Analysis, role string) *flow.Boundary`: the first boundary with that role.

- [ ] **Step 1: Write the failing tests**

```go
func TestPMCasesCoverBothTiers(t *testing.T) {
	pm, _ := shop(t, nil)
	cs := pmCases(pm, Options{})
	tiers := map[any]bool{}
	for _, c := range cs {
		tiers[c.Res.Values["tier"]] = true
	}
	if !tiers["Silver"] || !tiers["Standard"] {
		t.Errorf("generated cases miss a tier: %v", tiers)
	}
}

func TestWireValueAtTheCallResponse(t *testing.T) {
	pm, _ := shop(t, nil)
	c := pmCaseWith(t, pm, "member", false, "weight", 4) // helper: first case matching these inputs
	if v, ok := wire(pm, c, boundary(pm, flow.RoleResponse), "fee"); !ok || !sheet.Equal(v, 8) {
		t.Errorf("fee = %v, %v", v, ok)
	}
	if _, ok := wire(pm, c, boundary(pm, flow.RoleOutput), "notAField"); ok {
		t.Error("a field carrying no PM row is not compared")
	}
}

func TestWireValueUsesTheSendMap(t *testing.T) {
	// patch the PM sheet: shipping_cost's send gets map = { "4" = "FOUR", ... } or a
	// tier send to the output with map = { Silver = "SLV", Standard = "STD" } and
	// payment-request gains a tier field; the expected wire value is "SLV", not "Silver"
}

func TestFilledValueStandsInForATextRow(t *testing.T) {
	// patch shipping_cost to a text rule; Options.Filled has case <id>: shippingCost = 4;
	// wire returns 4 for that case and ok=false for a case with no filled value
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/agree`
Expected: FAIL, package has no Go files or undefined names.

- [ ] **Step 3: Write the fixture, `agree.go` types and `expect.go`**

Check the fixture first: `go run ./cmd/dsheets slice internal/agree/testdata/shop/product/flows/shop`, and the same for each team flow, must print no errors.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agree`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agree
git commit -m "agree: the PM's expected boundary values"
```

---

### Task 3: Segments and finding the team's own flow

**Files:**
- Create: `internal/agree/segment.go`, `internal/agree/segment_test.go`, `internal/agree/registry.go`

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces:

```go
// segment is the stretch of the PM's flow one team's own flow must cover.
type segment struct {
	Team    string
	In, Out int   // PM boundary indexes; for a callee: its request and response
	Calls   []int // request boundary indexes of calls made inside the segment
	Steps   []int // PM step indexes of the team's steps in the segment
	Callee  bool
}

func segments(pm *flow.Analysis) []segment

// match is a team flow found for a segment.
type match struct {
	A       *flow.Analysis
	StageOf map[int]string // PM request boundary index -> the team flow's call stage id
}

// findFlow picks the one team flow whose first and last boundaries' interface
// names equal the segment's In and Out, and which has, for every call in the
// segment, a request boundary with the same interface name. note says why
// none was found, or names the candidates when several match.
func findFlow(pm *flow.Analysis, sg segment, flows []*flow.Analysis) (m *match, note string)

// RegistryTeams serves the team flows of every repository the registry
// lists for a team (sources.TeamAliases), fetched on the branch the registry
// follows, analysed with flow.Analyze; self is skipped.
func RegistryTeams(reg map[string]sources.Source, fe *sources.Fetcher, self string) TeamFlows
```

How `segments` builds them: step `k` of a PM analysis sits between boundaries `k` and `k+1`.

- A callee step gives its own segment, `{In: k, Out: k+1, Callee: true}`.
- A step with `KeepsFrom == -1` starts a new segment with `In = k`.
- A step with `KeepsFrom >= 0` joins the segment that holds step `KeepsFrom`.
- After each non-callee step, if boundary `k+1` is a request, append it to `Calls`; otherwise set `Out = k+1`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSegmentsOfShop(t *testing.T) {
	pm, _ := shop(t, nil)
	got := segments(pm)
	// want three: Cart {In:0 Out:1}; Orders {In:1 Out:last, Calls:[request]}; Shipping callee {request, response}
	if len(got) != 3 || got[1].Team != "Orders team" || len(got[1].Calls) != 1 || !got[2].Callee {
		t.Fatalf("segments = %+v", got)
	}
}

func TestFindFlowByInterfaceName(t *testing.T) {
	pm, teams := shop(t, nil)
	fl, _ := teams("Orders team")
	m, note := findFlow(pm, segments(pm)[1], fl)
	if m == nil || m.StageOf[segments(pm)[1].Calls[0]] != "quote" {
		t.Fatalf("match = %+v, note %q", m, note)
	}
}

func TestFindFlowNoneAndSeveral(t *testing.T) {
	// none: the Cart flow's output changed to quote-request -> m == nil, note names order-placed
	// several: the same Cart flow passed twice -> m == nil, note names both flow folders
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/agree -run 'Segments|FindFlow'`
Expected: FAIL, undefined.

- [ ] **Step 3: Implement `segments`, `findFlow`, `RegistryTeams`**

`RegistryTeams` caches analyses per alias. For each folder from `flow.FindDirs(root)` it calls `flow.Load` and `flow.Analyze` with `sources.NewResolver(f.Dir, f.Sources, fe)`. Flows with analysis errors are left out and noted. The tests here don't exercise it; Task 6 does, through `requirements.Run`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agree`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agree
git commit -m "agree: team segments of a flow, and the team flow for each"
```

---

### Task 4: Running the cases

**Files:**
- Create: `internal/agree/chain.go`, `internal/agree/chain_test.go`

**Interfaces:**
- Consumes: Tasks 1 to 3.
- Produces: `func Run(pm *flow.Analysis, teams TeamFlows, o Options) *Report` (in `agree.go`).

**What `Run` does:**

1. **Cases.** Compute `pmCases` and `segments`. Find each segment's flow once, through `teams(sg.Team)` and `findFlow`.
2. **Isolated mode.** For each segment and each case:
   - When the PM case rejects at a row placed in a step before `sg.Steps[0]` (`pm.Place[row]`), skip the case: there is nothing to feed.
   - The incoming payload is built from `wire` for every field of boundary `In`, using `eval.SetPath`.
3. **End-to-end mode.** Segments run in order. The incoming payload is the previous non-callee segment's outgoing payload. Callee segments run only through their caller.
4. **Evaluating a segment's team sheet.** Use the matched flow's `Engine`. Inputs, by the team row's source:
   - `request.<path>`: from the incoming payload.
   - `call.<id>.<field>`: from a resolved response, or `eval.Pending` until resolved.
   - `context.`, `config.` and `db.`: the PM result's value of the PM input row with the same `Source` string, when there is one.

   Calls are resolved in passes, up to `len(Calls)+1`:
   - Evaluate the sheet.
   - For each unresolved call whose request (`CallPayloads[StageOf[req]]`) is complete, evaluate the callee's segment on that request. Its output payload becomes the response.
   - Repeat until no call is left to resolve.
5. **Checks.** Emit them in the segment run of the team that produces the boundary:
   - every field of `Out` and of each `Calls` request boundary (the caller produces those);
   - for a callee segment, its response;
   - only fields with `wire` ok.

   Values are read with `eval.GetPath`. Status is `Agrees` (by `sheet.Equal`), `Differs`, or `CannotTell` when the team value is unknown, with the reason from the team step's `Detail`.
6. **Rejects.**
   - When the PM case rejects at a row placed in one of this segment's `Steps`, emit a `Field: "reject"` check: `Expected` is the code, `Actual` is the team's code or `"(none)"`.
   - When the team rejects and the PM doesn't, emit `Differs` with `Expected: "(none)"`.
   - A team `MISSING_INPUT` gets the note `reads <source>, which <iface> does not carry`.
   - After any reject, no field checks for that case in that segment.
   - A callee reject inside a caller's run ends that run with no checks, because the callee's own segment reports it.
7. **No flow found.** Emit one `CannotTell` check per expected field of the segment, with the `findFlow` note and `Team` set.

- [ ] **Step 1: Write the failing tests**

```go
func countBy(r *Report, team, field, status string) int // helper

func TestShopAgrees(t *testing.T) {
	for _, mode := range []Mode{Isolated, EndToEnd} {
		pm, teams := shop(t, nil)
		r := Run(pm, teams, Options{Mode: mode})
		for _, c := range r.Checks {
			if c.Status != Agrees {
				t.Errorf("mode %v: %+v", mode, c)
			}
		}
		if countBy(r, "Orders team", "shippingCost", Agrees) == 0 || countBy(r, "Shipping team", "fee", Agrees) == 0 {
			t.Errorf("mode %v: missing checks: %+v", mode, r.Checks)
		}
	}
}

func TestLowercaseSilverDiffersOnOrders(t *testing.T) {
	pm, teams := shop(t, map[string]string{"orders/specs/orders/sheet.toml": ordersWith(`tier == "silver" ? fee / 2 : fee`)})
	r := Run(pm, teams, Options{Mode: Isolated})
	if countBy(r, "Orders team", "shippingCost", Differs) == 0 {
		t.Fatalf("want a differing shippingCost: %+v", r.Checks)
	}
	if countBy(r, "Cart team", "", Differs)+countBy(r, "Shipping team", "", Differs) != 0 {
		t.Error("only Orders is wrong")
	}
}

func TestSplitAgainstABindingRow(t *testing.T) {
	// I2: Shipping halves for Silver (needs tier on quote-request: patch interface, Orders sends tier to quote,
	// Shipping reads request.tier, fee = tier == "Silver" ? weight : weight * 2; Orders shipping_cost = fee).
	// Want: Shipping fee Differs on Silver cases (D-23: the PM's shipping_fee is binding); Orders shippingCost Agrees.
}

func TestTeamStageIdDiffers(t *testing.T)        // the fixture already uses "quote" vs "shipping-quote": a call check exists and agrees
func TestRejectMustMatch(t *testing.T)           // Shipping's reject code patched to HEAVY: one reject check, Team "Shipping team", Differs, Expected "TOO_HEAVY"; no shippingCost check for that case
func TestNumbersCompareByValue(t *testing.T)     // Orders: shipping_cost = fee * 1.0 when not Silver: still Agrees
func TestDottedFieldIsFedAndCompared(t *testing.T) // order-placed field renamed to order.tier in interface, Cart send and Orders source: agrees
func TestNoTeamFlowIsCannotTell(t *testing.T)    // teams("Cart team") returns nothing: Cart's checks CannotTell with a note naming order-placed; no Agrees for Cart
func TestTextRuleIsCannotTell(t *testing.T)      // Orders' shipping_cost becomes a text rule: its shippingCost checks are CannotTell
func TestEndToEndPassesTheChainAlong(t *testing.T) // Cart's tier patched to always "Standard": EndToEnd reports Differs at order-placed tier (Cart) AND at shippingCost (Orders); Isolated reports only Cart
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/agree -run 'Shop|Silver|Split|Stage|Reject|Numbers|Dotted|NoTeam|Text|EndToEnd'`
Expected: FAIL, undefined `Run`.

- [ ] **Step 3: Implement `Run` and its helpers in `chain.go`**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agree`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agree
git commit -m "agree: run the PM's cases through the team sheets"
```

---

### Task 5: `dsheets slice --cases`, and the meaning of a pin in the docs

**Files:**
- Modify: `cmd/dsheets/flowcmd.go` (`cmdSlice`, `:215-276`)
- Test: `cmd/dsheets/flowcmd_test.go`
- Modify: `docs/flows.md` ("Who computes what", `:145-154`; add a "Checking the teams' sheets" section), `README.md` (the `slice` row of the command table)

**Interfaces:**
- Consumes: `agree.Run`, `agree.RegistryTeams`, `sources.NewResolver(...).Registered()`.
- Produces:
  - **The flag.** `--cases` runs `agree.Run` in `EndToEnd` mode after the analysis prints. It reads the PM's `cases.toml` from `a.SheetDir` when the file exists.
  - **The output.** It prints one block per team, in segment order:

    ```
    Cases (12, end to end):
      Cart team      order-placed     12 agree
      Shipping team  quote-response    1 differs
        case silver-heavy   fee  expected 31, got 15.5
      Orders team    payment-request  11 agree, 1 cannot tell
        case ...  shippingCost  cannot tell: fee depends on a value that cannot be computed
    ```

    Lines are listed for differs and cannot-tell only, with notes printed once per team, followed by the report's `Notes`.
  - **Exit status.** With `--strict`, any `Differs` exits 1.
  - **The registry.** It comes from the flow's repository. Without a registry, the line `no registry: team flows cannot be found` is printed and every check is cannot tell.

- [ ] **Step 1: Write the failing test**

`TestSliceCases` builds a git repository holding the PM's part of the `internal/agree/testdata/shop` fixture plus a registry. It uses the pattern of `TestRunClosesAnItemFromTheOwnersSheet` in `internal/requirements/run_test.go`: each team folder becomes its own git repository, listed in `.gitmodules` with `team = "<Team> team"` and `dsheets = .`. The PM flow refers to team interfaces by alias. The test runs `slice <flow> --cases`, asserts the output contains `Cases (` and `Orders team`, and that no line contains `differs`. It then patches the Orders repository to lower-case `silver`, commits, reruns with `--strict`, and asserts the error is `errFailed` and the output contains `shippingCost  expected`.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./cmd/dsheets -run TestSliceCases`
Expected: FAIL, flag provided but not defined: `-cases`.

- [ ] **Step 3: Implement the flag and printing; update the docs**

In `docs/flows.md`, the "Who computes what" section says that a row listed in `computes` means the step's team answers for the value at its boundary. A team's own sheet may compute it in more steps, or split it with a team it calls. The new section explains `--cases`, the two modes in one line each, and D-23 (a PM row carried on a boundary between two teams is binding). In `README.md`, add `--cases` to the `slice` row.

- [ ] **Step 4: Run the tests**

Run: `git checkout -- specs/ && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/dsheets docs/flows.md README.md
git commit -m "slice --cases: run the PM's cases through the team sheets"
```

---

### Task 6: `requirements` closes items by cases

**Files:**
- Create: `internal/requirements/agree.go`, `internal/requirements/agree_test.go`
- Modify: `internal/requirements/model.go` (add `Cases`), `run.go` (`Run`, after `Check` at `:123-126`), `output.go` (`line`, `:31-57`)
- Modify: `docs/requirements.md` (the "Done, open and not documented" tables, `:58-113`)
- Test: `internal/requirements/run_test.go`, `internal/requirements/output_test.go`

**Interfaces:**
- Consumes: `agree.Run` (Isolated), `agree.RegistryTeams`, `agree.Check`. Items are matched to checks by `Check.Team` and `Check.Row`; the unexported segments aren't needed.
- Produces:
  - **The new field.** `Requirement.Cases []agree.Check` with `json:"cases,omitempty"`. It holds the failing and cannot-tell checks behind an open or undocumented item.
  - **The function.** `func applyCases(rs []Requirement, pm *flow.Analysis, rep *agree.Report)`, called from `Run` unless `NoCheck`. It skips items already `undocumented` by `Check`, and items owned by the flow's own repository.
  - **Compute (add or change).** The relevant checks are those with `Row == q.Row` and `Team == q.Team`. When there are none, use the checks with `Team == q.Team` whose `Row` is a descendant of `q.Row` in `pm.Engine.Graph` (through `Users`), and note `judged on <fields>, which other rows also feed`.
    - Any `Differs`: `open`, with the note `N of M cases differ`.
    - Otherwise any `CannotTell`: `undocumented`, with the first check's note.
    - All `Agrees`: `done`.
    - No relevant checks, or every relevant check is cannot tell only because no team flow was found: keep `Check`'s structural result, and append `checked by rule text: <note>`.
  - **Send.** When `Check` said done but a check on the same field at `q.Role` and `q.Stage` differs, set it `open` with those cases.
  - **Compute (stop), Receive, Carry, Schema:** unchanged.
  - **Printing.** `line()` prints up to 5 failing cases under an open item, then `… and N more`:

    ```
               case silver-domestic   shippingCost  expected 4, got 8
    ```

- [ ] **Step 1: Write the failing tests**

```go
// agree_test.go, with hand-built agree.Report values (no fixture needed)
func TestComputeClosesWhenCasesAgreeDespiteRuleText(t *testing.T)    // Check said open ("rule differs"); checks for Row shipping_cost, Team Orders all agree -> done
func TestComputeOpenListsDifferingCases(t *testing.T)               // one Differs -> open, len(Cases)==1, note "1 of 3 cases differ"
func TestComputeCannotTellIsUndocumented(t *testing.T)
func TestComputeWithoutTeamFlowKeepsRuleText(t *testing.T)         // all CannotTell with a no-flow note -> structural status kept, note has "checked by rule text"
func TestUnsentRowJudgedOnDescendants(t *testing.T)                 // q.Row discount_rate, checks on Row discount (a user of discount_rate) for the team
func TestSendOpensOnDifferingField(t *testing.T)
func TestUndocumentedIsLeftAlone(t *testing.T)

// output_test.go
func TestLinePrintsFailingCases(t *testing.T) // 7 failing cases: 5 lines then "… and 2 more"
```

In `run_test.go`, `TestRunClosesAnItemFromTheOwnersSheet` must still pass unchanged, through the rule-text fallback. Add `TestRunClosesComputeByCases`: the owner repository gets a team flow plus a sheet whose rule is written differently from the PM's but gives the same values (`shipping_fee + 0` in place of `shipping_fee`). The item is expected to be `done`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/requirements -run 'Compute|Send|Undocumented|LinePrints|ClosesComputeByCases'`
Expected: FAIL, undefined `applyCases` and `Cases`.

- [ ] **Step 3: Implement `applyCases`, the wiring in `Run` and the printing; update `docs/requirements.md`**

In `docs/requirements.md`:
- The Compute row of the evidence table becomes "every PM case agrees at the team's boundary (see `slice --cases`); a team with no own flow for the boundary falls back to the rule text".
- Add the failing-case lines to "What it prints".
- Add `cases` to the `--json` sentence.

- [ ] **Step 4: Run everything**

Run: `git checkout -- specs/ && gofmt -l ./internal ./cmd && go test ./...`
Expected: no gofmt output; PASS.

- [ ] **Step 5: Check against the Acme sandbox**

Reset the sandbox (stop running editors, then `bash setup.sh` in `D:/projects/acme-corp`), build, then in `work/product` run:

```bash
go build -o /d/projects/acme-corp/.bin/dsheets.exe ./cmd/dsheets
```
```bash
/d/projects/acme-corp/.bin/dsheets.exe slice sheets/flows/checkout-to-payment --cases
```

Expected: no `differs`.

Then replay the walkthrough's steps 2 to 6 (`WALKTHROUGH.md`) and run `requirements ... main..HEAD`. Expected: Cart's Compute item `R-65e0e5` is open. `order-placed` doesn't carry `freeShipping` until Orders' Schema item merges, so there is no boundary check yet, and the item falls back to rule text (`no row free_shipping`). After replaying steps 9 to 21 the list is empty, as in the walkthrough's T-30 replay.

Record anything unexpected in the handoff; don't fix it in this task.

- [ ] **Step 6: Commit**

```bash
git add internal/requirements docs/requirements.md
git commit -m "requirements: close Compute and Send items by the PM's cases"
```
