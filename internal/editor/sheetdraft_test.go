package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

const orderCheckout = "../../specs/order-checkout/sheet.toml"

var exampleSheets = []string{
	orderCheckout,
	"../../examples/pieces/1-storefront-team/sheet.toml",
	"../../examples/pieces/2-orders-team/sheet.toml",
	"../../examples/pieces/3-shipping-team/sheet.toml",
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// lf makes line endings "\n", so expected text can be written in Go.
func lf(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }

func rowOf(t *testing.T, data []byte, id string) RowData {
	t.Helper()
	d, err := tomledit.Parse(data, tomledit.Sheet)
	if err != nil {
		t.Fatal(err)
	}
	tb := d.Find("row", "id", id)
	if tb == nil {
		t.Fatalf("no row %s", id)
	}
	return RowFromTable(tb)
}

// changedLines returns the lines of b that are not in a, and of a not in b.
func changedLines(a, b string) (removed, added []string) {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	count := map[string]int{}
	for _, l := range al {
		count[l]++
	}
	for _, l := range bl {
		if count[l] > 0 {
			count[l]--
		} else {
			added = append(added, l)
		}
	}
	count = map[string]int{}
	for _, l := range bl {
		count[l]++
	}
	for _, l := range al {
		if count[l] > 0 {
			count[l]--
		} else {
			removed = append(removed, l)
		}
	}
	return removed, added
}

var idLine = regexp.MustCompile(`(?m)^id\s*=\s*"([^"]+)"`)

func rowIDs(text string) []string {
	var ids []string
	for _, m := range idLine.FindAllStringSubmatch(text, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func TestRowRoundTrip(t *testing.T) {
	for _, path := range exampleSheets {
		data := read(t, path)
		d, err := tomledit.Parse(data, tomledit.Sheet)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, tb := range d.Tables {
			if tb.Name != "row" {
				continue
			}
			r := RowFromTable(tb)
			// Through JSON too, as the browser sends it.
			js, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var back RowData
			if err := json.Unmarshal(js, &back); err != nil {
				t.Fatal(err)
			}
			ApplyRow(tb, back)
			n++
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(data) {
			_, added := changedLines(string(data), string(out))
			t.Errorf("%s: rewriting all %d rows changed the file: %q", path, n, added)
		}
	}
}

func TestApplyFormula(t *testing.T) {
	data := read(t, orderCheckout)
	r := rowOf(t, data, "discount")
	r.Formula = "round(subtotal * discount_rate * 2)"
	out, err := SheetDraft{Rows: map[string]RowData{"discount": r}}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	removed, added := changedLines(lf(data), lf(out))
	if !slices.Equal(removed, []string{`formula = "round(subtotal * discount_rate)"`}) ||
		!slices.Equal(added, []string{`formula = "round(subtotal * discount_rate * 2)"`}) {
		t.Errorf("removed %q, added %q", removed, added)
	}
	if strings.Count(string(out), "\r\n") != strings.Count(string(out), "\n") && strings.Contains(string(data), "\r\n") {
		t.Error("mixed line endings in a CRLF file")
	}
}

func TestApplyDefaultPositions(t *testing.T) {
	data := read(t, orderCheckout)
	cases := []struct {
		row   RowData
		after string
	}{
		{RowData{ID: "coupon", Name: "Coupon", Source: "request.order.coupon", Type: "string"}, "subtotal"},
		{RowData{ID: "handling_fee", Name: "Handling fee", Source: "config.pricing.handling_fee", Type: "number"}, "sales_tax_rate"},
		{RowData{ID: "net_of_discount", Name: "Net of discount", Uses: []string{"subtotal", "discount"}, Formula: "subtotal - discount"}, "discount"},
		{RowData{ID: "note", Name: "Note", Text: "Explained elsewhere."}, "total"},
	}
	for _, c := range cases {
		out, err := SheetDraft{Rows: map[string]RowData{c.row.ID: c.row}}.Apply(data)
		if err != nil {
			t.Fatal(err)
		}
		ids := rowIDs(lf(out))
		i := slices.Index(ids, c.row.ID)
		if i < 1 || ids[i-1] != c.after {
			t.Errorf("%s placed after %v; want after %s", c.row.ID, ids[max(i-1, 0)], c.after)
		}
		if _, err := tomledit.Parse(out, tomledit.Sheet); err != nil {
			t.Errorf("%s: output does not parse: %v", c.row.ID, err)
		}
	}
	// A new input lands above the heading of the next section.
	out, _ := SheetDraft{Rows: map[string]RowData{"coupon": cases[0].row}}.Apply(data)
	text := lf(out)
	if strings.Index(text, `id     = "coupon"`) > strings.Index(text, "# ---------- From context and config") {
		t.Error("new request input placed below the next section's heading")
	}
}

func TestApplyDelete(t *testing.T) {
	data := read(t, orderCheckout)
	out, err := SheetDraft{Deleted: []string{"customs_value"}}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(rowIDs(lf(out)), "customs_value") || strings.Contains(string(out), "customsValue") {
		t.Error("customs_value still there")
	}
	if _, err := (SheetDraft{Deleted: []string{"nope"}}).Apply(data); err == nil {
		t.Error("deleting an unknown row was accepted")
	}
}

func TestApplyRefusesSilentDrop(t *testing.T) {
	data := read(t, orderCheckout)
	order := slices.DeleteFunc(rowIDs(lf(data)), func(id string) bool { return id == "zone" })
	out, err := SheetDraft{Order: order}.Apply(data)
	if err == nil || !strings.Contains(err.Error(), "zone") {
		t.Fatalf("Apply = %d bytes, %v; want an error naming zone", len(out), err)
	}
}

func TestApplyOrder(t *testing.T) {
	data := read(t, orderCheckout)
	ids := rowIDs(lf(data))
	i, j := slices.Index(ids, "discount"), slices.Index(ids, "discount_rate")
	order := slices.Clone(ids)
	order[i], order[j] = order[j], order[i]
	out, err := SheetDraft{Order: order}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(lf(out)); !slices.Equal(got, order) {
		t.Errorf("order %v; want %v", got, order)
	}
	if _, err := (SheetDraft{Order: append(slices.Clone(ids), "ghost")}).Apply(data); err == nil {
		t.Error("an order naming an unknown row was accepted")
	}
	if _, err := (SheetDraft{Order: append(slices.Clone(ids), "zone")}).Apply(data); err == nil {
		t.Error("an order naming a row twice was accepted")
	}
}

func TestApplyRefusesRename(t *testing.T) {
	data := read(t, orderCheckout)
	r := rowOf(t, data, "discount")
	r.ID = "discount_amount"
	_, err := SheetDraft{Rows: map[string]RowData{"discount": r}}.Apply(data)
	if err == nil || !strings.Contains(err.Error(), "row IDs cannot be renamed") {
		t.Errorf("Apply error = %v", err)
	}
}

func TestApplySends(t *testing.T) {
	data := read(t, orderCheckout)
	r := rowOf(t, data, "weight_kg")
	r.Sends = []SendData{{Field: "weightKg", Usage: "R"}}
	out, err := SheetDraft{Rows: map[string]RowData{"weight_kg": r}}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	_, added := changedLines(lf(data), lf(out))
	if !slices.Contains(added, "  [row.send]") || slices.Contains(added, "  [[row.send]]") {
		t.Errorf("one send: added %q", added)
	}

	r.Sends = append(r.Sends, SendData{Target: "shipping-quote", Field: "parcel.weightKg", Usage: "R"})
	out, err = SheetDraft{Rows: map[string]RowData{"weight_kg": r}}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	_, added = changedLines(lf(data), lf(out))
	if n := strings.Count(strings.Join(added, "\n"), "  [[row.send]]"); n != 2 {
		t.Errorf("send plus call send: %d [[row.send]] headers in %q", n, added)
	}
	if got := rowOf(t, out, "weight_kg"); !slices.EqualFunc(got.Sends, r.Sends, func(a, b SendData) bool {
		return a.Target == b.Target && a.Field == b.Field && a.Usage == b.Usage
	}) {
		t.Errorf("sends read back as %+v", got.Sends)
	}

	// Back to one send: the call send is gone; the remaining send keeps its
	// [[row.send]] form, which the loader reads the same.
	r.Sends = r.Sends[:1]
	out, err = SheetDraft{Rows: map[string]RowData{"weight_kg": r}}.Apply(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowOf(t, out, "weight_kg").Sends; len(got) != 1 || got[0].Field != "weightKg" || strings.Contains(string(out), "shipping-quote") {
		t.Errorf("after removing the call send: %+v", got)
	}
}

func TestApplyRejectsAndQuestions(t *testing.T) {
	data := read(t, orderCheckout)
	r := rowOf(t, data, "zone")
	r.Rejects = append(r.Rejects, RejectData{When: `zone == "EMBARGO"`, Code: "EMBARGOED", Message: "Shipping is suspended"})
	r.OpenQuestions = []string{"Do islands count as domestic?"}
	out, err := SheetDraft{Rows: map[string]RowData{"zone": r}}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	got := rowOf(t, out, "zone")
	if len(got.Rejects) != 2 || got.Rejects[1].Code != "EMBARGOED" || !slices.Equal(got.OpenQuestions, r.OpenQuestions) {
		t.Errorf("read back %+v", got)
	}
	removed, _ := changedLines(lf(data), lf(out))
	if len(removed) != 0 {
		t.Errorf("adding a reject removed %q", removed)
	}
}

func TestApplyTitle(t *testing.T) {
	data := read(t, orderCheckout)
	title := "Checkout"
	out, err := SheetDraft{Title: &title}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	removed, added := changedLines(lf(data), lf(out))
	if !slices.Equal(removed, []string{`title   = "Online order checkout"`}) || !slices.Equal(added, []string{`title   = "Checkout"`}) {
		t.Errorf("removed %q, added %q", removed, added)
	}
}

func TestPairJSON(t *testing.T) {
	var ps []Pair
	if err := json.Unmarshal([]byte(`[["Gold", 0.1], ["Silver", "x"]]`), &ps); err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Key != "Gold" || ps[0].Value != 0.1 || ps[1].Value != "x" {
		t.Errorf("%+v", ps)
	}
	js, _ := json.Marshal(ps)
	if string(js) != `[["Gold",0.1],["Silver","x"]]` {
		t.Errorf("%s", js)
	}
	if err := json.Unmarshal([]byte(`[["a", 1, 2]]`), &ps); err == nil {
		t.Error("a three-element pair was accepted")
	}
}

func TestSendMapKeysWithDots(t *testing.T) {
	data := read(t, orderCheckout)
	r := rowOf(t, data, "discount_rate")
	r.Sends = []SendData{{Field: "discountRate", Usage: "R", Map: []Pair{{"0", "NONE"}, {"0.05", "FIVE"}, {"0.1", "TEN"}}}}
	out, err := SheetDraft{Rows: map[string]RowData{"discount_rate": r}}.Apply(data)
	if err != nil {
		t.Fatal(err)
	}
	got := rowOf(t, out, "discount_rate").Sends
	if len(got) != 1 || len(got[0].Map) != 3 || got[0].Map[1].Key != "0.05" || got[0].Map[1].Value != "FIVE" {
		t.Errorf("map read back as %+v\n%s", got, out)
	}
}
