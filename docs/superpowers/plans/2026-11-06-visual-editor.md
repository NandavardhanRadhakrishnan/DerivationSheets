# Visual editor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let anyone edit sheets, flows and interfaces on the pages `dsheets serve --edit` draws, with saves that are validated, version-bumped, regenerate cases, and leave every untouched row, comment and blank line byte-for-byte as it was.

**Architecture:** Two foundations with no UI (milestone 1): `internal/store`, the only code that reads and writes the edited files (load with a content token, save only if the token still matches, answer what is in the last commit), and `internal/tomledit`, which splits a TOML file into spans (each top-level key; each top-level table or `[[array]]` element with its sub-tables), remembers each span's original text, and on write re-renders only spans whose content changed, in canonical form. On top of them, `internal/editor` turns drafts sent by the browser into new file content, validates it against the files as they were, and saves through the store; `serve --edit` exposes it as JSON endpoints, and plain JavaScript panels on the existing pages drive it.

**Tech Stack:** Go 1.27; `github.com/pelletier/go-toml/v2` (decoding) and its `unstable` parser (byte ranges, read-only); git CLI through `os/exec`; plain HTML, CSS and JavaScript embedded with `go:embed`.

**Spec:** `docs/superpowers/specs/2026-11-03-visual-editor-design.md`

**Milestones.** Tasks 1–4 are milestone 1 (backlog T-1), 5–9 milestone 2 (T-2), 10–12 milestone 3 (T-3), 13–16 milestone 4 (T-4). Each milestone ships on its own. Tasks 5–16 are written against the interfaces tasks 1–4 define; the session that starts each later milestone first checks its tasks against the code as it then is.

**Decisions this plan makes** (the spec leaves them to the builder, or this plan departs from it):

- **Spans come from go-toml's `unstable` parser, not from `internal/sheet/positions.go`.** `positions.go` knows only sheets and finds lines with regular expressions; flows and interfaces need the same, and multi-line strings and arrays need a real parser. The `unstable` parser gives exact byte ranges for keys, values and table headers. It only reads; it never writes. `positions.go` stays as it is for validation line numbers.
- **The store answers with the committed file content** (`Committed`), and `store.CommittedVersion` reads `version` from it. That is "the version in the last commit", and the same call serves row-level diffs (T-5) and change-to-work (T-6) later.
- **The browser sends drafts of whole rows** (and, for flows, a list of edit operations); the server applies them to the parsed file, re-validates everything it writes, and renders previews with the existing `render` and `eval` code. The JavaScript holds form state only.
- **Canonical form is layout, not value text.** A re-rendered span gets canonical key order, alignment and indentation, but each value whose content did not change keeps its text as written (`0.10` stays `0.10`, a hand-aligned table keeps its spacing). A value is written in canonical text only when it changed.

## Global Constraints

- `dsheets serve` stays read-only without `--edit`, and its pages are byte-identical to today's when `--edit` is not given.
- "Nothing else in the editor touches the filesystem directly": the editor reads and writes the files it edits only through `store.Store`. Loaders keep reading supporting files as today (`data/` CSVs, `data/config.toml`, `dsheets.toml`, files reached through `alias:` sources).
- go-toml never writes files (it drops comments). Every write of a sheet, flow or interface goes through `tomledit`.
- A save must leave every untouched row, comment and blank line byte-for-byte as it was, including line endings (CRLF or LF) and whether the file ends with a newline.
- dsheets never writes to another team's repository: anything reached through an `alias:` source is read-only; the store refuses paths under `.dsheets/` and inside nested git checkouts.
- A save is refused only for **errors the edit introduces**: errors not among those the same files had before the edit, compared by code, row and message. Errors already there are shown and do not block. Warnings never block.
- `version` of a sheet or flow goes up by one at most once per commit: only when the file's version equals the committed version. A file with no committed version is not raised. Interface `version` is left alone.
- Row and stage IDs can be edited only until first saved.
- No git operations other than reading `HEAD` (`git show`, `git rev-parse`). No commits, branches or pushes.
- Plain HTML, CSS and JavaScript; no build step, no framework, no new Go dependencies.
- Copy uses plain neutral words ("team", "target", "row"), no insurance jargon; examples use order-checkout.

## Review Focus

1. **Files checked out with CRLF.** This repository's working tree is CRLF on Windows (`core.autocrlf=true`; every example file on disk has `\r\n`). Unchanged spans must round-trip byte-for-byte, and new or changed lines must use `\r\n` in a CRLF file. Tests: Task 2 (`TestRoundTripExamples` CRLF variant), Task 4 (`TestEditCRLF`).
2. **A section heading with no blank line before the first row of its section** (`# ---------- Derived ----------` directly above `[[row]]`). By the "directly above" rule it would belong to the row and vanish when the row is deleted. A leading comment block stops at a line matching `^#\s*-{3,}`: that line and the lines above it are free text. Test: Task 4 (`TestEditSectionHeadingStays`).
3. **A browser bug that leaves a row out of the draft's order.** It must be refused, never read as a deletion; deletions are explicit. Test: Task 6 (`TestApplyRefusesSilentDrop`).
4. **Another web page POSTing to the localhost editor** (cross-site request forgery against `localhost:8080`). Edit endpoints accept only `application/json` from the same origin. Test: Task 8 (`TestEditEndpointsRefuseCrossSite`).
5. **A path from the browser that escapes the repository or points into another team's checkout** (`../`, an absolute path, `.dsheets/sources/...`, a submodule). The store refuses it. Test: Task 1 (`TestPathsConfined`).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/store/store.go` (create) | `Store` interface, `Local` (working tree plus git `HEAD`), tokens, path confinement, `CommittedVersion` |
| `internal/tomledit/doc.go` (create) | `Doc`, `Table`, `Key`, `Inline`, `Raw`; `Parse` (spans, comments, free text); accessors |
| `internal/tomledit/value.go` (create) | decoding AST values to the model, value equality, canonical value text |
| `internal/tomledit/layout.go` (create) | `Layout`, `TableLayout`, `Sheet`, `Flow`, `Interface`, `LayoutFor` |
| `internal/tomledit/write.go` (create) | `Bytes` (changed spans only), `Format` (every span), span rendering, comment keeping |
| `internal/cases/regen.go` (create), `internal/cases/file.go`, `cmd/dsheets/main.go` (modify) | cases regeneration as a library, shared by `dsheets cases` and the editor |
| `internal/editor/rowdata.go` (create) | `RowData` ↔ `tomledit.Table` |
| `internal/editor/sheetdraft.go` (create) | `SheetDraft`, `Apply`, default positions |
| `internal/editor/editor.go` (create) | `Editor`: check and save pipelines for sheets (later flows) |
| `internal/editor/preview.go` (create) | live preview: draft page fragment and per-case results |
| `internal/editor/flowdraft.go` (create) | `FlowDraft` operations on `flow.toml` and interface files |
| `cmd/dsheets/edit.go` (create), `cmd/dsheets/main.go` (modify) | `serve --edit`, edit endpoints, same-origin checks |
| `internal/render/editor.js`, `internal/render/editor.css` (create), `internal/render/render.go`, `sheet.html.tmpl` (modify) | the sheet side panel |
| `internal/flowrender/flowedit.js` (create), `internal/flowrender/*` (modify) | the flow side panel |
| `internal/flow/flow.go`, `internal/flow/analyze.go`, `internal/iface/iface.go`, `cmd/dsheets/flowcmd.go` (modify) | parsing from bytes, analysis overlay, `local` fields, import merge |
| `schema/interface.schema.json`, `docs/flows.md`, `docs/format.md`, `README.md`, `BACKLOG.md` (modify) | `local`, canonical layout, `serve --edit` |

---

## Milestone 1: store and comment-preserving writer (T-1)

### Task 1: File store

**Files:**
- Create: `internal/store/store.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces:
  ```go
  // File is a loaded file and the token of the version loaded.
  type File struct {
      Path  string // slash-separated, relative to the store root
      Data  []byte
      Token string
  }
  // Write asks to replace Path with Data if it still has Token.
  // Token "" means the file must not exist yet (creating it).
  type Write struct {
      Path  string
      Data  []byte
      Token string
  }
  var (
      ErrConflict     = errors.New("changed on disk since it was loaded")
      ErrNotCommitted = errors.New("not in the last commit")
      ErrOutside      = errors.New("outside the repository")
      ErrReadOnly     = errors.New("belongs to another repository")
  )
  // ConflictError lists every path whose token no longer matches; errors.Is(err, ErrConflict).
  type ConflictError struct{ Paths []string }

  type Store interface {
      Load(path string) (File, error)              // missing file: errors.Is(err, fs.ErrNotExist)
      Save(ws ...Write) (tokens []string, err error) // every token checked before anything is written
      Committed(path string) ([]byte, error)       // content in the last commit, or ErrNotCommitted
  }
  type Local struct{ /* root, mutex */ }
  func NewLocal(root string) (*Local, error)       // root made absolute; must be a directory
  func Token(data []byte) string                   // "sha256:" + lowercase hex
  // CommittedVersion reads the integer top-level `version` of the committed file.
  // ok is false when the file is not committed or has no integer version.
  func CommittedVersion(s Store, path string) (version int, ok bool, err error)
  ```

- [ ] **Step 1: Write the failing tests**

A helper `repo(t)` makes a temp folder, `git init -q`, sets `user.name`/`user.email` locally, writes `a/sheet.toml` with `version = 1\n`, commits.

```go
func TestLoadSave(t *testing.T)        // Load("a/sheet.toml").Token == Token(data); Save({path, "version = 2\n", tok}) returns
                                       // [Token("version = 2\n")]; file on disk is "version = 2\n"
func TestSaveConflict(t *testing.T)    // Load, then os.WriteFile the same file "x = 1\n"; Save with old token →
                                       // errors.Is(err, ErrConflict), err.(*ConflictError).Paths == ["a/sheet.toml"]; disk still "x = 1\n"
func TestSaveAllOrNothing(t *testing.T) // Save(w1 valid, w2 stale token) → ErrConflict; neither file changed
func TestSaveCreate(t *testing.T)      // Save({"a/new.toml", data, ""}) creates it; again with "" → ErrConflict
func TestPathsConfined(t *testing.T)   // Load and Save of "../x.toml" and an absolute path → ErrOutside;
                                       // Save of ".dsheets/sources/s/sheet.toml" → ErrReadOnly; a folder "team" with
                                       // its own `git init` → Save("team/sheet.toml") → ErrReadOnly; Load("team/sheet.toml") ok
func TestCommitted(t *testing.T)       // change a/sheet.toml on disk → Committed returns "version = 1\n";
                                       // untracked a/new.toml → ErrNotCommitted; NewLocal(non-repo tmp).Committed → ErrNotCommitted
func TestCommittedVersion(t *testing.T) // a/sheet.toml → (1, true, nil); untracked file → (0, false, nil);
                                       // committed file without version → (0, false, nil)
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/store/ -v`
Expected: FAIL (package has no non-test files / undefined: NewLocal)

- [ ] **Step 3: Implement `internal/store/store.go`**

Paths: reject absolute paths and any cleaned path starting with `..` (`ErrOutside`); `Save` also rejects a first segment `.dsheets` or `.git`, and any folder strictly between the root and the file that contains a `.git` entry, file or folder (`ErrReadOnly`). `Save` holds a mutex, loads every target, compares tokens, collects all mismatches into one `ConflictError`, and only then writes each file through a temp file in the same folder and `os.Rename`, keeping the existing file's mode (0o644 for new files). `Committed` runs `git -C <root> show HEAD:./<path>`; a non-zero exit becomes `ErrNotCommitted` (wrapping git's message), a missing git binary is returned as is. `CommittedVersion` decodes with `toml.Unmarshal` into `map[string]any` and accepts `int64` only.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add the file store the editor reads and writes through"
```

---

### Task 2: Parse a TOML file into spans

**Files:**
- Create: `internal/tomledit/doc.go`, `internal/tomledit/value.go`, `internal/tomledit/write.go` (only `Bytes` for an unchanged document in this task), `internal/tomledit/layout.go` (types and `LayoutFor` only; values in Task 3), `internal/tomledit/doc_test.go`

**Interfaces:**
- Produces:
  ```go
  // Doc is a TOML file split into spans: each top-level key, and each top-level
  // table or [[array]] element together with its sub-tables. Change Top and
  // Tables, then Bytes writes the file back, rewriting only changed spans.
  type Doc struct {
      Top    []*Key
      Tables []*Table
      // unexported: original lines with their terminators, line ending, final
      // newline, segments (span or free text), layout
  }
  type Key struct {
      Name  string // dotted keys joined with "."; quoted parts unquoted
      Value any    // string, bool, int64, float64, []any, Inline or Raw
  }
  type Inline []*Key // an inline table, in written order
  type Raw string    // value text written as is: dates and times
  type Table struct {
      Name  string // relative to the parent: "row", "stage", "field", "display"; subs "send", "reject", "table", "request"
      Array bool   // written [[...]]
      Keys  []*Key
      Subs  []*Table
      // unexported: origin (original span and a deep copy of its parsed content)
  }
  type Layout struct{ Tables map[string]TableLayout } // filled in Task 3
  type TableLayout struct{ Order, Unaligned, Multiline, Subs []string }
  var Sheet, Flow, Interface *Layout
  // LayoutFor picks a layout by the file's first top-level key: sheet, flow or interface; nil otherwise.
  func LayoutFor(data []byte) *Layout

  func Parse(data []byte, l *Layout) (*Doc, error)
  func (d *Doc) Bytes() ([]byte, error)
  func (d *Doc) Get(name string) *Key
  func (d *Doc) Find(table, key string, v any) *Table // first top-level table named table whose key equals v
  func (t *Table) Get(name string) *Key
  func (t *Table) Sub(name string) []*Table
  ```

- [ ] **Step 1: Write the failing tests**

`examples(t)` lists every `*.toml` under `specs/`, `flows/`, `interfaces/` and `examples/` (paths from the test's folder, `../../`), including `cases.toml` and `data/config.toml`, parsed with `LayoutFor(data)`; `Parse` accepts a nil layout (no canonical order). In this task `Sheet`, `Flow` and `Interface` are declared as empty layouts.

```go
func TestRoundTripExamples(t *testing.T) // for each file, three inputs: as on disk; with every "\r\n" made "\n" and then
                                         // every "\n" made "\r\n"; with the final line terminator removed.
                                         // Parse then Bytes == input, byte for byte
func TestParseSheet(t *testing.T)        // specs/order-checkout/sheet.toml: Top names [sheet version title];
                                         // Get("version").Value == int64(1); 20 tables, all Name "row", Array true;
                                         // Find("row","id","loyalty_tier").Sub("table")[0].Get("rows").Value is []any of 4 []any,
                                         //   the first == []any{false, "< 2", "Standard"};
                                         // Find("row","id","discount_rate").Get("values").Value == Inline{{"Standard", int64(0)},
                                         //   {"Silver", 0.05}, {"Gold", 0.1}};
                                         // Find("row","id","customs_value").Sub("send")[0].Array == false
func TestParseFlow(t *testing.T)         // flows/checkout-to-payment/flow.toml: table names in order
                                         // [sources input stage stage output]; second stage .Sub("request")[0].Get("interface").Value
                                         // == "shipping:quote-request.toml"; Tables[0].Get("shipping").Value is Inline{{"path", "../../interfaces/shipping"}}
func TestParseComments(t *testing.T)     // white-box: in checkout-to-payment the first [[stage]] span starts on the
                                         // "# 1. The storefront publishes" line; in order-checkout no span contains
                                         // "# ---------- Derived ----------"; the "#:schema" line belongs to top key "sheet"
func TestParseErrors(t *testing.T)       // Parse("a = \n") error contains "1"; Parse("a = 1\na = 2\n") is an error
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/tomledit/ -v`
Expected: FAIL (undefined: Parse)

- [ ] **Step 3: Implement `Parse`, the value decoder and `Bytes` for an unchanged document**

Validate first with `toml.Unmarshal` into `map[string]any` and return its error (with its line) for invalid TOML. Then walk `unstable.Parser` expressions (`KeepComments` off). Line of a `KeyValue` = line of its `Raw` start; its last line = line of `Raw` end. Line of a `Table`/`ArrayTable` header = line of its first key part's `Raw` (header nodes have no range of their own). A header `[a.b]` is a sub-table of the current top-level table when that table's `Name` is `a`; otherwise it starts a top-level table named `a.b`. Each top-level span runs from its first line to the last line of its last expression. Lines between spans are blank or comment lines; the leading comments of a span are the comment lines directly above its first line with no blank line between (stopping at a line matching `^#\s*-{3,}`, Review Focus 2); the rest is free text. Keep the original lines with their own terminators so mixed endings survive. Values: `String` → node `Data` with `"\r\n"` made `"\n"`; `Integer` → `int64` (underscores, `0x`/`0o`/`0b`); `Float` → `float64` (`inf`, `nan`); `Bool`; `Array` → `[]any`; `InlineTable` → `Inline`; dates and times → `Raw` of their text. `Bytes` assembles the output from the segments (do not return the input buffer); in this task every span is unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tomledit/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tomledit
git commit -m "Split TOML files into spans that round-trip byte for byte"
```

---

### Task 3: Canonical layouts and `Format`

**Files:**
- Modify: `internal/tomledit/layout.go`, `internal/tomledit/value.go`, `internal/tomledit/write.go`
- Create: `internal/tomledit/format_test.go`
- Modify: `docs/format.md` (a short "Canonical layout" section: key order, alignment, two-space sub-tables, the editor and a future `fmt` share it)

**Interfaces:**
- Consumes: Task 2's model.
- Produces:
  ```go
  // Format writes every span in canonical form. Values keep their text as written;
  // comments and free text are kept.
  func (d *Doc) Format() ([]byte, error)
  // FormatValue writes v as canonical TOML value text. nl is the line ending for multi-line strings.
  func FormatValue(v any, nl string) (string, error)
  ```
  Layout values, by table path (`""` is the top level). Keys not in `Order` follow in their current order; keys not in `Unaligned` line up their `=` with the longest such key present; sub-tables follow `Subs`, others after them in their current order.
  ```go
  Sheet = &Layout{Tables: map[string]TableLayout{
      "":           {Order: []string{"sheet", "version", "title", "request_schema", "target_schema"}, Unaligned: []string{"request_schema", "target_schema"}},
      "row":        {Order: []string{"id", "name", "description", "source", "type", "usage", "when", "default", "uses", "formula", "lookup", "values", "values_file", "text", "open_questions"},
                     Unaligned: []string{"description", "values_file", "open_questions"}, Subs: []string{"table", "reject", "send"}},
      "row.table":  {Order: []string{"hit_policy", "columns", "rows"}, Unaligned: []string{"hit_policy", "columns", "rows"}, Multiline: []string{"rows"}},
      "row.reject": {Order: []string{"when", "code", "message"}},
      "row.send":   {Order: []string{"target", "field", "usage", "when", "otherwise", "map"}},
      "display":    {Order: []string{"evidence"}, Subs: []string{"columns", "sections"}},
      "display.columns":  {Order: []string{"number", "value", "rule", "send", "evidence"}},
      "display.sections": {Order: []string{"request", "context", "data", "derived"}},
  }}
  Flow = &Layout{Tables: map[string]TableLayout{
      "":               {Order: []string{"flow", "version", "title", "sheet"}},
      "input":          {Order: []string{"interface", "carries"}},
      "stage":          {Order: []string{"id", "kind", "interface", "carries", "producer", "caller", "computes", "callee", "callee_computes"},
                         Unaligned: []string{"callee_computes"}, Subs: []string{"request", "response"}},
      "stage.request":  {Order: []string{"interface", "carries"}},
      "stage.response": {Order: []string{"interface", "carries"}},
      "output":         {Order: []string{"interface", "carries", "producer", "computes"}},
  }}
  Interface = &Layout{Tables: map[string]TableLayout{
      "":      {Order: []string{"interface", "title", "kind", "owner", "status", "version", "imported", "description"}},
      "field": {Order: []string{"name", "type", "required", "enum", "description"}, Unaligned: []string{"description"}},
  }}
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestFormatCanonicalFiles(t *testing.T) // Format == input for specs/order-checkout/sheet.toml, flows/loyalty-report/sheet.toml,
                                            // flows/mobile-orders/sheet.toml, flows/*/flow.toml and interfaces/**/*.toml.
                                            // If one of them is not canonical by the rules above, leave it out with a
                                            // comment naming the line; never edit an example file to make a test pass.
func TestFormatKeepsMeaning(t *testing.T)   // every example file: toml.Unmarshal(Format(x)) deep-equals toml.Unmarshal(x)
func TestFormatAlignsSend(t *testing.T)     // examples/pieces/2-orders-team/sheet.toml, row customs_value: Format writes
                                            // "  field     = \"customsValue\"", "  usage     = \"S\"",
                                            // "  when      = \"zone != \\\"DOMESTIC\\\"\"", "  otherwise = \"do-not-send\""
func TestFormatValue(t *testing.T)          // table-driven, nl "\n":
    // "a\"b\\c" → `"a\"b\\c"`; "tab\there" → `"tab\there"`; "\x01" → `"\u0001"`; "é" → `"é"`;
    // "one\ntwo" → "\"\"\"\none\ntwo\"\"\"";
    // int64(3) → `3`; 3 (int) → `3`; 0.05 → `0.05`; float64(2) → `2`; 1e21 → `1e+21`; math.Inf(-1) → `-inf`; math.NaN() → `nan`;
    // true → `true`; []any{} → `[]`; []any{"a", 1.5} → `["a", 1.5]`; Inline{} → `{}`;
    // Inline{{"Standard", "STD"}, {"Silver", "SLV"}} → `{ Standard = "STD", Silver = "SLV" }`; Inline{{"a b", int64(1)}} → `{ "a b" = 1 }`;
    // Raw("1979-05-27") → `1979-05-27`; map[string]any{} → error; nil → error
func TestFormatTableRows(t *testing.T)      // a new row table (no origin) with columns ["is_member", "customer_years", "=>"] and the four
                                            // rules of loyalty_tier renders, at sub-table indent, exactly lines 91–98 of
                                            // specs/order-checkout/sheet.toml ("  [row.table]" through "  ]")
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/tomledit/ -run TestFormat -v`
Expected: FAIL (undefined: Format)

- [ ] **Step 3: Implement the layouts, `FormatValue` and span rendering**

Value text: basic strings escape `\`, `"` and control characters (`\b \t \n \f \r`, else `\uXXXX`); a string containing a newline is a multi-line basic string: `"""`, nl, the content with newlines written as nl and the same escapes, `"""`. Floats: integral and within ±2^53 → integer text; otherwise `strconv.FormatFloat(f, 'f', -1, 64)` when that contains a `.`, else `'e'` form; `inf`, `-inf`, `nan`. Keys are bare when they match `^[A-Za-z0-9_-]+$`, else quoted. A table span renders: its leading comments; the header (`[name]` or `[[name]]`, full dotted path for subs, trailing header comment kept), indented by the depth's indent (0 for top-level, two spaces per level below); its keys in layout order, each preceded by the comments that were directly above it in the original span and followed by its original trailing comment; then for each sub-table one blank line and the sub-table. A key whose value equals its original value keeps the original value text; otherwise `FormatValue`. A `Multiline` array value writes `[`, one element per line at indent + 2 with a trailing comma, then `]` at the key's indent; when every element is an array, cells line up: each cell except the last is written `text,` padded to the widest `text,` in its column, then one space (see `TestFormatTableRows`). A top-level key aligns with the other aligned top-level keys of its group: keys whose original lines were adjacent, plus keys inserted next to them.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tomledit/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tomledit docs/format.md
git commit -m "Write TOML spans in the canonical layout of the example files"
```

---

### Task 4: Edits that change only their spans

**Files:**
- Modify: `internal/tomledit/doc.go`, `internal/tomledit/write.go`
- Create: `internal/tomledit/edit_test.go`

**Interfaces:**
- Consumes: Tasks 2 and 3.
- Produces:
  ```go
  func (d *Doc) Set(name string, v any)  // change a top-level key, or insert it at its layout position among Top
  func (d *Doc) Delete(name string)
  func (d *Doc) Insert(i int, t *Table)  // into Tables at index i
  func (d *Doc) Remove(t *Table) bool
  func (t *Table) Set(name string, v any)
  func (t *Table) Delete(name string)
  func (t *Table) Clone() *Table         // deep copy with no origin: written as a new span
  ```
  Identity: a `*Table` from `Parse` is its original span wherever it moves in `Tables`; a table without an origin is new; an original table missing from `Tables` is deleted. Keys are matched by name, sub-tables by name and position among sub-tables of that name. A span is changed when its keys (as a set of name → value) or its sub-tables (in order, with `Name` and `Array`) differ from its original content; numbers compare by value (`int64(0)` equals `0.0`).

- [ ] **Step 1: Write the failing tests**

Each test works on a copy of the named file's bytes, applies the edit, and compares `Bytes()` with an expected text built from the original by exact string replacement. A helper `mustDecode(t, out)` asserts `toml.Unmarshal` succeeds on every output.

```go
func TestEditSameValueIsNoChange(t *testing.T) // order-checkout discount: Set("formula", "round(subtotal * discount_rate)") → Bytes == original
func TestEditChangeKey(t *testing.T)           // discount formula → "round(subtotal * discount_rate * 100) / 100": original with the line
                                               // `formula = "round(subtotal * discount_rate)"` replaced, nothing else
func TestEditAddUnalignedKey(t *testing.T)     // zone: Set("description", "Where the parcel goes") → `description = "Where the parcel goes"`
                                               // inserted after `name   = "Shipping zone"`; nothing else changes
func TestEditInsertRow(t *testing.T)           // new &Table{Name: "row", Array: true} with id "coupon", name "Coupon code",
                                               // source "request.order.coupon", type "string", inserted after subtotal →
                                               // "\n[[row]]\nid     = \"coupon\"\nname   = \"Coupon code\"\nsource = \"request.order.coupon\"\ntype   = \"string\"\n"
                                               // appears right after subtotal's send block, before the blank line and
                                               // "# ---------- From context and config ----------"
func TestEditDeleteRow(t *testing.T)           // Remove customs_value → its 11 lines and the blank line before them are gone; nothing else
func TestEditDeleteRowWithComment(t *testing.T) // file with "# Zones come from the carrier's list" directly above zone's [[row]]:
                                               // Remove zone → the comment goes too; "# ---------- Derived ----------" stays
func TestEditSectionHeadingStays(t *testing.T) // file with "# ---------- Derived ----------" directly above customer_years' [[row]]
                                               // (no blank line): Remove customer_years → the heading stays, above zone
func TestEditMoveRowWithComment(t *testing.T)  // same commented zone, moved in Tables to after discount → comment and row appear after
                                               // discount's send block, and are gone from their old place
func TestEditCommentsInChangedSpan(t *testing.T) // discount with "# rounded to whole cents" directly above formula and
                                               // `uses    = ["subtotal", "discount_rate"]  # both inputs`: change formula → both comments
                                               // kept in place; then Delete("uses") → "# both inputs" gone, "# rounded to whole cents" kept
func TestEditTableRuleComments(t *testing.T)   // loyalty_tier rows with "    # members" directly above rule 3 and "  # top tier" trailing
                                               // rule 4: change rule 1's result to "Basic" → rows re-aligned, both comments kept with
                                               // rules 3 and 4; then remove rule 3 → "# members" gone
func TestEditVersionAndTitle(t *testing.T)     // d.Set("version", int64(2)) → only `version = 1` → `version = 2`;
                                               // d.Set("title", "Checkout") → `title   = "Checkout"`
func TestEditFlowPin(t *testing.T)             // checkout-to-payment: append "shipping_cost" to the shipping-quote stage's computes →
                                               // only its computes line changes; its "# 2. The Orders team asks..." comment stays
func TestEditInterfaceAddField(t *testing.T)   // interfaces/orders/order-placed.toml: append a field table {name "shippingMethod",
                                               // type "string", required false} → original + "\n[[field]]\nname     = \"shippingMethod\"\n
                                               // type     = \"string\"\nrequired = false\n"
func TestEditCRLF(t *testing.T)                // TestEditChangeKey and TestEditInsertRow on the CRLF form: expected texts in CRLF too
func TestEditNoFinalNewline(t *testing.T)      // order-placed without its final newline: append a field → output has no final newline;
                                               // Remove the last field → no final newline
func TestEditSendBecomesArray(t *testing.T)    // customs_value: its send sub Array = true plus a second &Table{Name: "send", Array: true}
                                               // with target "shipping-quote" → two "  [[row.send]]" blocks, the first with the original keys
func TestEditUnsupportedValue(t *testing.T)    // row.Set("x", map[string]any{}) → Bytes returns an error naming "x"
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/tomledit/ -run TestEdit -v`
Expected: FAIL (undefined: (*Doc).Set, …)

- [ ] **Step 3: Implement the edit methods and `Bytes` for changed documents**

```
order    := Top then Tables (new order); each item has an original span or none
anchors  := the longest run of items with an original span whose original positions increase (LIS)
moved    := items with an original span that are not anchors
for each original segment, in file order:
    free text                    → write as is
    span deleted or moved        → drop it; if the line before it is blank and the line after it is blank or
                                   end of file, also drop the blank lines directly before it
    anchor                       → write its original lines if unchanged, else render it (Task 3)
                                   then write the new and moved items that follow it in order, up to the next anchor:
                                   a top-level key on the next line; a table as one blank line and the table
items before the first anchor    → written directly before the first anchor's first line (a table followed by one blank line)
```
New and changed lines use the file's line ending (that of its first line; `\n` for a one-line file); the final newline is as in the original. `Set` on a top-level key that is absent inserts it after the last present key that comes before it in the layout's `Order`, else first.

- [ ] **Step 4: Run all tomledit tests**

Run: `go test ./internal/tomledit/ -v`
Expected: PASS

- [ ] **Step 5: Run the whole suite and commit**

Run: `go test ./...` — Expected: PASS

```bash
git add internal/tomledit
git commit -m "Rewrite only the TOML spans an edit changes, keeping comments"
```

---

## Milestone 2: sheet editing and save (T-2)

### Task 5: Cases regeneration as a library

**Files:**
- Create: `internal/cases/regen.go`, `internal/cases/regen_test.go`
- Modify: `internal/cases/file.go` (`Read` = read + `ParseFile`; `Write` = `Format` + write), `cmd/dsheets/main.go` (`cmdCases` uses `Regenerate` and `Summary.Lines`)

**Interfaces:**
- Produces:
  ```go
  func ParseFile(data []byte) (*File, error)
  func Format(s *sheet.Sheet, inputOrder []*sheet.Row, refTime time.Time, cs []Case) []byte
  // Regenerate builds cases.toml content, keeping hand-written expected values from old (nil: none).
  func Regenerate(e *eval.Engine, old *File, ref time.Time) ([]byte, Summary)
  type Summary struct {
      Cases, Covered, Targets, Evaluated, Kept int
      Uncovered []Target
      FillIn    []string // the "case %q: fill in expected ..." lines
  }
  func (s Summary) Lines(path string) []string // exactly the lines `dsheets cases` prints today, in the same order
  // RefTime is old's reference time, or DefaultReferenceTime when old is nil or has none.
  func RefTime(old *File) (time.Time, error)
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestRegenerateIsIdempotent(t *testing.T) // order-checkout: old = Read(specs/order-checkout/cases.toml); Regenerate(e, old, RefTime(old))
                                              // == the file with "\r\n" made "\n"
func TestSummaryLines(t *testing.T)           // Lines("x/cases.toml")[0] starts "wrote x/cases.toml: " and matches today's wording
```
Existing `cmd/dsheets` tests for `cases` keep passing unchanged.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cases/ -run 'TestRegenerate|TestSummary' -v` — Expected: FAIL (undefined: Regenerate)

- [ ] **Step 3: Move the logic out of `cmdCases` into `regen.go`; `cmdCases` keeps its flags and output**

- [ ] **Step 4: Run** `go test ./internal/cases/ ./cmd/dsheets/ -v` — Expected: PASS

- [ ] **Step 5: Commit** — `git commit -m "Make cases regeneration a library call"`

---

### Task 6: Sheet drafts

**Files:**
- Create: `internal/editor/rowdata.go`, `internal/editor/sheetdraft.go`, `internal/editor/sheetdraft_test.go`

**Interfaces:**
- Consumes: `tomledit` (Tasks 2–4).
- Produces:
  ```go
  type Pair struct{ Key string; Value any }          // JSON: ["key", value]
  type TableData struct {
      HitPolicy string  `json:"hit_policy,omitempty"`
      Columns   []string `json:"columns"`
      Rows      [][]any  `json:"rows"`
  }
  type LookupData struct {
      From       string   `json:"from"`
      Key        []string `json:"key"`
      Values     []Pair   `json:"values,omitempty"`
      ValuesFile string   `json:"values_file,omitempty"`
  }
  type RejectData struct{ When, Code, Message string } // json: when, code, message
  type SendData struct{ Target, Field, Usage, When, Otherwise string; Map []Pair } // json: target, field, usage, when, otherwise, map
  type RowData struct {
      ID, Name, Description, Source, Type, Usage, When string // json: id, name, description, source, type, usage, when
      HasDefault bool `json:"has_default,omitempty"`
      Default    any  `json:"default,omitempty"`
      Uses          []string     `json:"uses,omitempty"`
      Formula       string       `json:"formula,omitempty"`
      Table         *TableData   `json:"table,omitempty"`
      Lookup        *LookupData  `json:"lookup,omitempty"`
      Text          string       `json:"text,omitempty"`
      Rejects       []RejectData `json:"rejects,omitempty"`
      Sends         []SendData   `json:"sends,omitempty"`
      OpenQuestions []string     `json:"open_questions,omitempty"`
  }
  func RowFromTable(t *tomledit.Table) RowData
  // ApplyRow changes t so it says r. One send without a target is written [row.send];
  // otherwise every send is [[row.send]].
  func ApplyRow(t *tomledit.Table, r RowData)

  type SheetDraft struct {
      File    string             `json:"file"`    // relative to the store root
      Token   string             `json:"token"`
      Title   *string            `json:"title,omitempty"`
      Order   []string           `json:"order,omitempty"`   // every row ID in the new order; nil keeps the order
      Rows    map[string]RowData `json:"rows,omitempty"`    // changed and new rows, by ID
      Deleted []string           `json:"deleted,omitempty"`
  }
  // Apply returns the content the draft describes.
  func (d SheetDraft) Apply(orig []byte) ([]byte, error)
  // DefaultPosition is the index in Tables for a new row with no position:
  // inputs after the last input of the same source kind (else after the last input, else first);
  // derived rows after the lowest row they use (else at the end).
  func DefaultPosition(doc *tomledit.Doc, r RowData) int
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestRowRoundTrip(t *testing.T)         // every row of every example sheet: ApplyRow(t, RowFromTable(t)) → Bytes unchanged
func TestApplyFormula(t *testing.T)         // order-checkout, Rows{"discount": formula changed} → only that line differs
func TestApplyDefaultPositions(t *testing.T) // new request input → after subtotal; new config input → after sales_tax_rate;
                                            // new derived row using ["subtotal", "discount"] → after discount
func TestApplyDelete(t *testing.T)          // Deleted ["customs_value"] → row gone
func TestApplyRefusesSilentDrop(t *testing.T) // Order missing "zone" and Deleted empty → error naming zone; nothing written
func TestApplyRefusesRename(t *testing.T)   // Rows{"discount": RowData{ID: "discount_amount", ...}} → error "row IDs cannot be renamed"
func TestApplySends(t *testing.T)           // one send → "[row.send]"; send plus call send → two "[[row.send]]"
func TestApplyTitle(t *testing.T)           // Title "Checkout" → only the title line changes
```

- [ ] **Step 2: Run** `go test ./internal/editor/ -v` — Expected: FAIL (undefined: SheetDraft)
- [ ] **Step 3: Implement `rowdata.go` and `sheetdraft.go`** (numbers from JSON arrive as `float64`; tomledit's numeric equality keeps `0` and `0.0` unchanged)
- [ ] **Step 4: Run** `go test ./internal/editor/ -v` — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -m "Turn sheet drafts into file content"`

---

### Task 7: Check and save pipeline for sheets

**Files:**
- Create: `internal/editor/editor.go`, `internal/editor/editor_test.go`

**Interfaces:**
- Consumes: `store.Store`, `store.CommittedVersion` (Task 1), `SheetDraft` (Task 6), `cases.Regenerate`, `cases.RefTime`, `cases.ParseFile` (Task 5), `sheet.Parse`, `validate.Validate`.
- Produces:
  ```go
  type Editor struct{ Store store.Store }
  type Problem struct {
      validate.Diagnostic
      New  bool `json:"new"`            // an error the edit introduces
      Rule int  `json:"rule,omitempty"` // decision-table rule, from 1 (Task 10)
  }
  type SheetResult struct {
      Saved         bool              `json:"saved"`
      Tokens        map[string]string `json:"tokens,omitempty"`
      Version       int               `json:"version"`
      VersionRaised bool              `json:"version_raised"`
      Problems      []Problem         `json:"problems"`
      Conflict      []string          `json:"conflict,omitempty"`
      Cases         []string          `json:"cases,omitempty"` // cases.Summary.Lines
  }
  func (ed *Editor) CheckSheet(d SheetDraft) (*SheetResult, error) // everything except writing
  func (ed *Editor) SaveSheet(d SheetDraft) (*SheetResult, error)
  ```
  Order: (1) load the sheet; token mismatch → `Conflict`, stop. (2) Apply. (3) Validate before and after; an after error whose (code, row, message) is not in the before errors (as a multiset) is `New`; any `New` → not saved. A TOML parse error after the edit is a `New` error with code `toml`. (4) Raise `version` to committed + 1 when it equals the committed version. (5) `Store.Save`. (6) Regenerate cases with `RefTime` of the existing `cases.toml`, save it with its token (`""` when missing), and report `Summary.Lines`.

- [ ] **Step 1: Write the failing tests** (each copies `specs/order-checkout` into a temp git repo with one commit)

```go
func TestSaveSheetChangesOnlyEdit(t *testing.T)  // discount formula changed → git diff of sheet.toml is that line and `version = 2`;
                                                 // Cases non-empty; cases.toml rewritten
func TestSaveSheetConflict(t *testing.T)         // file changed on disk after load → Saved false, Conflict ["sheet.toml"], disk untouched
func TestSaveSheetRefusesNewError(t *testing.T)  // formula "subtotal * coupon_rate" → Saved false; a Problem New true, Code "ref-undefined", Row "discount"
func TestSaveSheetKeepsOldErrors(t *testing.T)   // sheet committed with an existing error (a lookup row whose CSV overlaps) plus an unrelated
                                                 // valid edit → Saved true; that error present with New false
func TestSaveSheetVersionOncePerCommit(t *testing.T) // save → 2 and VersionRaised; save again → 2, VersionRaised false;
                                                 // commit; save → 3
func TestSaveSheetUncommitted(t *testing.T)      // folder not in git → version unchanged
func TestSaveSheetCasesKeepReferenceTime(t *testing.T) // cases.toml with reference_time "2026-01-01T00:00:00Z" and a hand-written expected
                                                 // value for a "?" → both still there after the save
func TestCheckSheetWritesNothing(t *testing.T)   // CheckSheet with a valid edit → Saved false, no Problems with New, files untouched
```

- [ ] **Step 2: Run** `go test ./internal/editor/ -run 'Sheet' -v` — Expected: FAIL
- [ ] **Step 3: Implement `editor.go`**
- [ ] **Step 4: Run** `go test ./internal/editor/ -v` — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -m "Validate, version and save sheet drafts"`

---

### Task 8: `serve --edit` endpoints

**Files:**
- Create: `cmd/dsheets/edit.go`, `cmd/dsheets/edit_test.go`
- Modify: `cmd/dsheets/main.go` (`--edit` flag on `serve`; `serveOptions.Edit *editor.Editor`; usage text)

**Interfaces:**
- Consumes: `editor.Editor`, `SheetDraft`, `RowFromTable` (Tasks 6–7), `store.NewLocal`.
- Produces (only with `--edit`; 404 otherwise):
  ```
  GET  /edit/state  → {"file", "token", "version", "title", "rows": [RowData], "ids": [all row IDs], "saved_ids": [IDs saved before, locked]}
  POST /edit/check  SheetDraft → SheetResult plus "uses": {rowID: [row IDs the formula names]} (expr.CEL.Identifiers that are row IDs)
                                 and "cycle": {rowID: [row IDs that would make a cycle if used]}
  POST /edit/save   SheetDraft → SheetResult
  ```
  The store root is `git rev-parse --show-toplevel` of the served folder, else the served folder. `--edit` is refused for an `alias:` argument and for an `--addr` whose host is not `localhost`, `127.0.0.1` or `::1`. POSTs need `Content-Type: application/json`, a body under 1 MB, and, when present, `Origin` equal to `http://` + Host and `Sec-Fetch-Site` equal to `same-origin`; otherwise 415 or 403.

- [ ] **Step 1: Write the failing tests** (`httptest` against `serveHandler`)

```go
func TestServeWithoutEditIsReadOnly(t *testing.T)    // GET /edit/state → 404; GET / has no "edit-state"
func TestEditState(t *testing.T)                     // 20 rows; rows[0].id "signup_date"; token == store.Token(file bytes)
func TestEditEndpointsRefuseCrossSite(t *testing.T)  // Origin "http://evil.example" → 403; Sec-Fetch-Site "cross-site" → 403;
                                                     // Content-Type "text/plain" → 415
func TestEditCheckUsesAndCycle(t *testing.T)         // draft discount formula "subtotal * discount_rate" → uses ["subtotal", "discount_rate"];
                                                     // cycle for "discount" includes "total"
func TestEditSaveRoundTrip(t *testing.T)             // save a title change → 200, saved true; GET / shows the new title
func TestEditRefusesAliasAndRemoteAddr(t *testing.T) // cmdServe with "--edit shipping:x" and with "--addr 0.0.0.0:0" → error
```

- [ ] **Step 2: Run** `go test ./cmd/dsheets/ -run Edit -v` — Expected: FAIL
- [ ] **Step 3: Implement `edit.go` and the flag**
- [ ] **Step 4: Run** `go test ./cmd/dsheets/ -v` — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -m "Add serve --edit and the sheet edit endpoints"`

---

### Task 9: Sheet side panel

**Files:**
- Create: `internal/render/editor.js`, `internal/render/editor.css`
- Modify: `internal/render/render.go` (`Page.Edit *EditView`; `type EditView struct{ State template.JS }`), `internal/render/sheet.html.tmpl` (when `.Edit` is set: `<script type="application/json" id="edit-state">`, the panel container, `editor.css`, `editor.js`), `cmd/dsheets/edit.go` (fill `Page.Edit`), `README.md` (`serve --edit`)

**Interfaces:**
- Consumes: the Task 8 endpoints.
- Panel behaviour (spec "Sheet: the page plus a side panel"): selecting a row or **+ Add row** opens the panel with name, ID (editable only for IDs not in `saved_ids`), description; rule kind Input, Formula or Text (Table and Lookup in milestone 3; their rows open read-only with "editing tables and lookups comes later"); input fields source (with `request.`, `context.`, `db.`, `config.`, `call.` choices), type, usage, `when`, default; formula text with suggestions of row IDs and CEL functions, rows in `cycle` greyed out, `uses` shown as filled from `/edit/check`; sends (field, usage R or S, `when` and `otherwise` for S, value map), including call sends with a target; rejects; open questions; unknown names flagged with **add it as a request input** (opens a new input row with that ID and `source = "request."`). A bar shows the number of unsaved changes with **Save** and **Discard**; `beforeunload` asks while there are unsaved changes. `/edit/check` runs 400 ms after typing stops; problems show against their rows, introduced ones in red. After a save the page reloads, keeping nothing unsaved, and shows "Saved. Version raised to N" when it was raised, plus the cases summary. A conflict says which file changed and offers **Reload**, keeping the draft visible.

- [ ] **Step 1: Write the failing test**

```go
func TestEditPageCarriesState(t *testing.T) // GET / with --edit contains `id="edit-state"` and the editor script; without --edit
                                            // the page bytes equal render.Write of the same page (read-only page unchanged)
```

- [ ] **Step 2: Run** `go test ./cmd/dsheets/ ./internal/render/ -v` — Expected: FAIL
- [ ] **Step 3: Implement the template hook, `editor.js` and `editor.css`** (follow `sheet.js` style: one IIFE, no dependencies)
- [ ] **Step 4: Run** `go test ./... ` — Expected: PASS
- [ ] **Step 5: Check it in a browser**

Run `go run ./cmd/dsheets serve --edit specs/order-checkout` in a temp copy of the repository, open `http://localhost:8080/`, then: change `discount`'s formula to `round(subtotal * discount_rate * 2)` → bar says 1 unsaved change; Save → "Version raised to 2", `git diff` shows only the formula and version lines plus `cases.toml`; type `subtotal * coupon_rate` → `coupon_rate` flagged with **add it as a request input**, Save refused with the error against `discount`; close the tab with unsaved edits → the browser asks first.

- [ ] **Step 6: Commit** — `git commit -m "Edit sheet rows in a side panel"`

---

## Milestone 3: tables, lookups and live preview (T-3)

### Task 10: Decision-table grid

**Files:**
- Modify: `internal/editor/editor.go` (`Problem.Rule` for `table-overlap`, `table-missing-case`, `table-cell`, `table-rule-width`: the 1-based rule whose line `Positions.TableRuleLine` gives), `internal/render/editor.js`, `internal/render/editor.css`, `internal/editor/editor_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestCheckTableOverlapNamesRule(t *testing.T) // loyalty_tier draft with rule 2 changed to [false, "< 2", "Silver"] →
                                                  // a Problem Code "table-overlap", Row "loyalty_tier", Rule 2
```

- [ ] **Step 2: Run** `go test ./internal/editor/ -run TestCheckTable -v` — Expected: FAIL
- [ ] **Step 3: Implement `Rule` and the grid**: columns picked from rows (the `=>` result column always last); cells take conditions (`>= 2`, `-`, values); add, remove and reorder rules and condition columns; hit policy `unique` or `first`; rules named by a problem are outlined. Switching rule kind asks "Replace the <kind> with a <kind>? The current rule will be removed." before discarding.
- [ ] **Step 4: Run** `go test ./internal/editor/ -v` — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -m "Edit decision tables in a grid"`

### Task 11: Lookup form

**Files:**
- Modify: `internal/render/editor.js`, `internal/editor/sheetdraft_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestApplyLookupInlineValues(t *testing.T) // discount_rate values Gold 0.10 → 0.12: only `values = {...}` changes, written
                                               // `{ Standard = 0, Silver = 0.05, Gold = 0.12 }`; zone's values_file kept as is
```

- [ ] **Step 2: Run** — Expected: FAIL only if `ApplyRow` mishandles lookups; otherwise it passes and pins behaviour
- [ ] **Step 3: Implement the form**: `from` and `key` fields; inline `values` as an editable key/value list; a `values_file` row shows its entries read-only with the file's path and "Edit the file to change these values".
- [ ] **Step 4: Run** `go test ./internal/editor/ -v` — Expected: PASS
- [ ] **Step 5: Commit** — `git commit -m "Edit lookups in the side panel"`

### Task 12: Live preview

**Files:**
- Create: `internal/editor/preview.go`, `internal/editor/preview_test.go`
- Modify: `cmd/dsheets/edit.go` (`POST /edit/preview`), `internal/render/render.go` (`WriteSheetTable(w io.Writer, p *Page) error`, the template's sheet table as its own named template), `internal/render/editor.js`

**Interfaces:**
- Produces:
  ```go
  type CaseResult struct {
      ID     string `json:"id"`
      Value  any    `json:"value,omitempty"`
      Reason string `json:"reason,omitempty"` // "rejected earlier (CODE)", "depends on a text rule", or the evaluation error
  }
  type Preview struct {
      SheetHTML string       `json:"sheet_html"` // the draft's sheet table, as render draws it
      Cases     []CaseResult `json:"cases"`      // for the selected row, from cases.Generate on the draft
      Problems  []Problem    `json:"problems"`
  }
  func (ed *Editor) PreviewSheet(d SheetDraft, row string) (*Preview, error)
  ```
  `POST /edit/preview` takes `{"draft": SheetDraft, "row": "<id>"}`. A draft that does not validate returns its problems and no `sheet_html`. Cases use `cases.RefTime` of the existing `cases.toml`.

- [ ] **Step 1: Write the failing tests**

```go
func TestPreviewFormula(t *testing.T)   // discount formula "subtotal * discount_rate * 2" → every case with a discount shows twice its saved value
func TestPreviewRejected(t *testing.T)  // a case whose zone is NONE → Reason "rejected earlier (DESTINATION_NOT_SERVED)"
func TestPreviewSheetHTML(t *testing.T) // SheetHTML contains the new formula as rendered and a grid dot for each used row
```

- [ ] **Step 2: Run** `go test ./internal/editor/ -run TestPreview -v` — Expected: FAIL
- [ ] **Step 3: Implement**; the page swaps the sheet table's HTML and the panel's case list 400 ms after typing stops.
- [ ] **Step 4: Run** `go test ./... ` — Expected: PASS
- [ ] **Step 5: Check it in a browser**: edit `discount`'s formula and watch its rule, grid dots and the case values change before saving.
- [ ] **Step 6: Commit** — `git commit -m "Preview sheet edits live, with every case's result"`

---

## Milestone 4: flows and interface re-import (T-4)

Re-planned on 8 November 2026 against the code after milestones 2 and 3, and D-16. Ships as one pull request.

Changes from the first version of tasks 13 to 16:

- **The flow draft carries whole stages, not operations** (as sheet drafts carry whole rows). Index shifts after inserting a stage cannot go wrong, and a stage left out of the list without being in `Deleted` is refused (Review Focus 3). Pins are the `computes` lists in the stages and the output; maps are their `carries`.
- **Marks re-run by re-rendering the flow page on the server**, as the sheet editor's preview does: `/edit/flow/check` returns the page and the panel's data per row (placement, steps it can go in, marks with the actions each allows).
- **The flow page always slices the sheet as saved on disk** (spec), so the overlay holds only `flow.toml` and interface files.
- **⚠ marks (D-16):** map to another field, plus "make required" (optional when always needed) and "change type" (type mismatch) on interface files in this repository. Enum mismatches stay read-only. Rowless ✕ marks (a required field nothing produces) stay in "To agree" only.
- **Serving a flow with `--edit`:** `/` is the flow page with the flow panel, `/sheet` the end-to-end sheet with the existing sheet panel when that sheet is in the repository and not reached through an alias. Sheet endpoints stay at `/edit/*`; flow endpoints are `/edit/flow/*`.

### Task 13: `local` fields and re-import merges

`iface.Field.Local`; `Load` reads `local`, `CheckShape` accepts it, `Write` writes `local    = true` after `enum`; `tomledit.Interface` orders `local` after `enum`; `schema/interface.schema.json` gains `field.local`; `docs/flows.md` gains "Fields added before the schema". `iface.Merge(fresh, existing) (merged, kept)`: fields from fresh; existing `local` fields fresh lacks are kept, marked, after fresh's fields; a `local` field fresh has loses the mark. `interface import` merges when `--out` exists and prints `kept <field>: not in <source> yet`.

Tests: `TestMergeKeepsLocal`, `TestMergeDropsMarkWhenSchemaHasIt`, `TestMergeReplacesOthers`, `TestLocalRoundTrip`, `TestImportKeepsLocal` (cmd).

### Task 14: Flows and interfaces from memory

`flow.Parse(dir, file string, data []byte)` (`Load` reads and calls it); `iface.Parse(path string, data []byte)`; `flow.Overlay map[string][]byte` by cleaned absolute path; `flow.AnalyzeWith(f, r, ov)` reads interface files from the overlay first (`Analyze(f, r) == AnalyzeWith(f, r, nil)`).

Tests: `TestAnalyzeOverlayInterface` (overlay order-placed with `shippingMethod` → the ✕ for shipping_method there is gone), `TestAnalyzeNilOverlay`.

### Task 15: Flow drafts, check and save

`internal/editor/flowdraft.go`, `internal/editor/flowedit.go`:

```go
type BoundaryData struct{ Interface string; Carries map[string]string }
type StageData struct {
    Orig *int // index in the file; nil for a new stage
    ID, Kind, Interface, Producer, Caller, Callee string
    Carries map[string]string
    Computes, CalleeComputes []string
    Request, Response *BoundaryData
}
type OutputData struct{ Producer string; Computes []string; Carries map[string]string }
type FieldEdit struct {
    Interface string // relative to the store root
    Name, Type string; Required bool
    Add bool // a new field; otherwise change the existing field's type and required
}
type FlowDraft struct {
    File, Token string
    Input   *BoundaryData // carries only
    Stages  []StageData   // nil keeps the stages; otherwise every stage, in order
    Deleted []int         // original indexes
    Output  *OutputData
    Fields  []FieldEdit
    Tokens  map[string]string // interface files the fields change, by path
}
func (ed *Editor) CheckFlow(d FlowDraft) (*FlowResult, error)
func (ed *Editor) SaveFlow(d FlowDraft) (*FlowResult, error)
```

A field added to an interface with `imported` is written with `local = true` and a warning naming the source schema; a fix to such an interface warns that re-importing undoes it. An interface reached through an `alias:` source, or outside the store root, refuses every edit. Errors: the flow's `Analysis.Errors` and interfaces' `CheckShape`, compared by message as a multiset, before and after. Version as for sheets. A save writes `flow.toml` and the interface files in one `Store.Save`.

Tests (temp git repo copy of `flows/`, `interfaces/`, `specs/`): `TestFlowUnchangedRoundTrip`, `TestPinWritesComputes`, `TestUnpin`, `TestAddFieldProposed`, `TestAddFieldImportedLocal`, `TestFixFieldRequired`, `TestAliasInterfaceReadOnly`, `TestMapWritesCarries`, `TestAddStage`, `TestDeleteStage`, `TestStageSilentDropRefused`, `TestFlowConflict`, `TestFlowIntroducedError`.

### Task 16: Flow side panel and serving a flow with `--edit`

`internal/flowrender`: `Page.Edit`; `data-row` on value rows, `data-step`/`data-boundary` on cells and headers, `data-stage` on stage headers; `flowedit.js`, `flowedit.css`. `cmd/dsheets/edit.go`: flow mode, `GET /edit/flow/state`, `POST /edit/flow/check`, `POST /edit/flow/save`, `/sheet`. Panel per spec "Flow: the page plus the same panel" and D-16; "+ stage" on every step that is not a callee's; selecting a stage header edits ID (until saved), kind, interfaces, producer, caller, callee, or deletes the stage.

Tests: `TestFlowEditState` (both stages; quote-request read-only), `TestFlowEditServesSheet`, `TestFlowEditRefusesCrossSite`. Then in a browser: pin `shipping_cost`, add `shippingMethod` to order-placed (warning, `local = true`), make weightKg required, add a stage, open the sheet from the row link and add a row, save both.
