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
