package requirements

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shippingResult() *Result {
	schema := Requirement{ID: "R-3a1f", Kind: Schema, Team: "Shipping team", What: "add `surcharge` (number, required) to quote-response.toml", Subject: "quote-response:surcharge"}
	comp := Requirement{ID: "R-91c2", Kind: Compute, Team: "Shipping team", Subject: "shipping_fee", What: "change how `shipping_fee` is computed",
		Before: "subtotal * 0.05", After: "subtotal * 0.05 + surcharge", Needs: []string{"R-3a1f"}}
	send := Requirement{ID: "R-77e0", Kind: Send, Team: "Shipping team", Subject: "response@quote:surcharge", What: "send `surcharge` in quote-response, from `shipping_fee`", Needs: []string{"R-91c2"}}
	read := Requirement{ID: "R-0b0b", Kind: Receive, Team: "Orders team", Subject: "response@quote:surcharge", What: "read `surcharge` in quote-response", Needs: []string{"R-77e0"}}
	return &Result{Flow: "checkout-to-payment", Base: "502c48a", Head: "abc1234", Requirements: []Requirement{schema, comp, send, read}}
}

func TestMarkdownLayout(t *testing.T) {
	var buf bytes.Buffer
	if err := shippingResult().WriteMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	want := "Orders team: checkout-to-payment (base 502c48a)\n" +
		" R-0b0b  Receive  read `surcharge` in quote-response   needs R-77e0\n" +
		"\n" +
		"Shipping team: checkout-to-payment (base 502c48a)\n" +
		" R-3a1f  Schema   add `surcharge` (number, required) to quote-response.toml\n" +
		" R-91c2  Compute  shipping_fee: subtotal * 0.05 -> subtotal * 0.05 + surcharge   needs R-3a1f\n" +
		" R-77e0  Send     send `surcharge` in quote-response, from `shipping_fee`   needs R-91c2\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestMarkdownMarksDefaultPlacement(t *testing.T) {
	r := &Result{Flow: "f", Base: "b", Requirements: []Requirement{{ID: "R-1111", Kind: Compute, Team: "T", Subject: "x", What: "compute `x`", Placed: "default"}}}
	var buf bytes.Buffer
	if err := r.WriteMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "placed by default") {
		t.Errorf("a defaulted placement should say so:\n%s", buf.String())
	}
}

func TestMarkdownEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := (&Result{Flow: "f", Base: "b"}).WriteMarkdown(&buf); err != nil || buf.Len() != 0 {
		t.Fatalf("want no output, got %q, %v", buf.String(), err)
	}
}

func TestJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := shippingResult().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Requirements) != 4 || back.Requirements[1].Needs[0] != "R-3a1f" {
		t.Errorf("round trip lost data: %+v", back)
	}
	if strings.Contains(buf.String(), `"before": ""`) || strings.Contains(buf.String(), `"edit"`) {
		t.Errorf("empty fields should be omitted:\n%s", buf.String())
	}
	buf.Reset()
	if err := (&Result{Flow: "f"}).WriteJSON(&buf); err != nil || !strings.Contains(buf.String(), `"requirements": []`) {
		t.Errorf("an empty result prints an empty list, got %q, %v", buf.String(), err)
	}
}

func TestWriteFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if err := shippingResult().WriteFolder(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orders-team.md", "orders-team.json", "shipping-team.md", "shipping-team.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "orders-team.json"))
	if strings.Contains(string(data), "R-3a1f") {
		t.Errorf("a team's file holds only that team's requirements:\n%s", data)
	}
}

func statusResult() *Result {
	rule := "table, first match:\n  subtotal >= 100, loyalty_tier any -> true\n  subtotal any, loyalty_tier any -> false"
	return &Result{Flow: "f", Base: "b0b0b0b", Requirements: []Requirement{
		{ID: "R-0001", Kind: Compute, Team: "Cart team", Subject: "free_shipping", What: "compute `free_shipping`", Rule: rule, Status: StatusDone},
		{ID: "R-0002", Kind: Send, Team: "Cart team", Subject: "s", What: "send `freeShipping` in order-placed", Status: StatusDone},
		{ID: "R-0003", Kind: Compute, Team: "Orders team", Subject: "free_shipping (remove)", What: "stop computing `free_shipping`", Status: StatusDone},
		{ID: "R-0004", Kind: Receive, Team: "Orders team", Subject: "r", What: "read `freeShipping` in order-placed", Status: StatusOpen, Note: "no input row in orders's sheets reads `freeShipping`"},
		{ID: "R-0005", Kind: Compute, Team: "Orders team", Subject: "gift", What: "compute `gift`", Rule: rule, Status: StatusOpen},
		{ID: "R-0006", Kind: Send, Team: "Payments team", Subject: "p", What: "send `giftWrapFee`", Status: StatusUndocumented, Note: "no sheets in payments"},
	}}
}

func TestMarkdownHidesDoneAndGroupsTheRest(t *testing.T) {
	var buf bytes.Buffer
	if err := statusResult().WriteMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	want := "Orders team: f (base b0b0b0b)\n" +
		" R-0004  Receive  read `freeShipping` in order-placed   open: no input row in orders's sheets reads `freeShipping`\n" +
		" R-0005  Compute  compute `gift`\n" +
		"            table, first match:\n" +
		"              subtotal >= 100, loyalty_tier any -> true\n" +
		"              subtotal any, loyalty_tier any -> false\n" +
		"\n" +
		"Not documented (dsheets cannot tell):\n" +
		"Payments team: f (base b0b0b0b)\n" +
		" R-0006  Send     send `giftWrapFee`   no sheets in payments\n" +
		"\n" +
		"3 of 6 done\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestMarkdownShowDone(t *testing.T) {
	r := statusResult()
	r.ShowDone = true
	var buf bytes.Buffer
	if err := r.WriteMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Cart team: f") || !strings.Contains(out, " R-0002  Send     send `freeShipping` in order-placed   done\n") {
		t.Errorf("done items missing:\n%s", out)
	}
}

func TestMarkdownAllDoneIsEmpty(t *testing.T) {
	r := &Result{Flow: "f", Requirements: []Requirement{{ID: "R-0001", Kind: Send, Team: "T", What: "x", Status: StatusDone}}}
	var buf bytes.Buffer
	if err := r.WriteMarkdown(&buf); err != nil || buf.String() != "" {
		t.Errorf("got %q, %v; want nothing", buf.String(), err)
	}
}

func TestJSONHasStatusNoteAndRule(t *testing.T) {
	var buf bytes.Buffer
	if err := statusResult().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Requirements) != 6 || back.Requirements[0].Status != StatusDone || back.Requirements[3].Note == "" || back.Requirements[0].Rule == "" {
		t.Errorf("round trip lost data: %+v", back.Requirements)
	}
}

func TestWriteFolderFollowsTheListing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reqs")
	if err := statusResult().WriteFolder(dir); err != nil {
		t.Fatal(err)
	}
	orders, err := os.ReadFile(filepath.Join(dir, "orders-team.md"))
	if err != nil || strings.Contains(string(orders), "R-0003") || !strings.Contains(string(orders), "R-0004") {
		t.Errorf("orders-team.md: %v\n%s", err, orders)
	}
	pay, err := os.ReadFile(filepath.Join(dir, "payments-team.md"))
	if err != nil || !strings.Contains(string(pay), "Not documented") {
		t.Errorf("payments-team.md: %v\n%s", err, pay)
	}
	if _, err := os.Stat(filepath.Join(dir, "cart-team.md")); err != nil {
		t.Errorf("a team with only done items still gets a (summary) file: %v", err)
	}
}
