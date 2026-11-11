package diff

import (
	"reflect"
	"strings"
	"testing"
)

const baseFlow = `flow = "f"
version = 1
sheet = "."

[input]
interface = "in.toml"

[[stage]]
id = "ship"
kind = "call"
caller = "Orders"
computes = ["zone", "fee"]
callee = "Shipping"
callee_computes = ["rate"]

  [stage.request]
  interface = "req.toml"
  carries = { zip = "zone" }

  [stage.response]
  interface = "resp.toml"
  carries = { fee = "rate" }

[output]
interface = "out.toml"
producer = "Orders"
computes = ["total"]
`

func flowChange(t *testing.T, base, head string) FlowChange {
	t.Helper()
	var b, h []byte
	if base != "" {
		b = []byte(base)
	}
	if head != "" {
		h = []byte(head)
	}
	fc, err := DiffFlow(b, h)
	if err != nil {
		t.Fatal(err)
	}
	return fc
}

func TestDiffFlowCarriesAddedDropped(t *testing.T) {
	head := replace(t, baseFlow, `carries = { zip = "zone" }`, `carries = {}`)
	head = replace(t, head, `carries = { fee = "rate" }`, `carries = { fee = "rate", tax = "tax" }`)
	fc := flowChange(t, baseFlow, head)
	want := []BoundaryChange{
		{Where: "stage ship request", Dropped: []string{"zip"}},
		{Where: "stage ship response", Added: []string{"tax"}},
	}
	if fc.Name != "f" || !reflect.DeepEqual(fc.Boundaries, want) {
		t.Fatalf("got %+v", fc)
	}
	if len(fc.Computes) != 0 || len(fc.Stages) != 0 {
		t.Fatalf("unexpected: %+v", fc)
	}
}

func TestDiffFlowRewiredAndInterface(t *testing.T) {
	head := replace(t, baseFlow, `carries = { fee = "rate" }`, `carries = { fee = "rate2" }`)
	head = replace(t, head, `interface = "out.toml"`, `interface = "out2.toml"`)
	fc := flowChange(t, baseFlow, head)
	want := []BoundaryChange{
		{Where: "stage ship response", Rewired: []FieldChange{{Path: "fee", Before: "rate", After: "rate2"}}},
		{Where: "output", Interface: FieldChange{Path: "interface", Before: "out.toml", After: "out2.toml"}},
	}
	if !reflect.DeepEqual(fc.Boundaries, want) {
		t.Fatalf("got %+v", fc.Boundaries)
	}
}

func TestDiffFlowComputeMoved(t *testing.T) {
	head := replace(t, baseFlow, `computes = ["zone", "fee"]`, `computes = ["zone"]`)
	head = replace(t, head, `callee_computes = ["rate"]`, `callee_computes = ["rate", "fee"]`)
	fc := flowChange(t, baseFlow, head)
	want := []ComputeChange{{Row: "fee", Before: "Orders", After: "Shipping"}}
	if !reflect.DeepEqual(fc.Computes, want) {
		t.Fatalf("got %+v", fc.Computes)
	}
	if len(fc.Boundaries) != 0 {
		t.Fatalf("boundaries: %+v", fc.Boundaries)
	}
}

func TestDiffFlowStageAdded(t *testing.T) {
	head := replace(t, baseFlow, "[output]", "[[stage]]\nid = \"notify\"\nkind = \"handoff\"\ninterface = \"n.toml\"\nproducer = \"Orders\"\ncomputes = [\"audit\"]\n\n[output]")
	fc := flowChange(t, baseFlow, head)
	wantStages := []FieldChange{{Path: "stage notify", After: "handoff by Orders"}}
	wantComputes := []ComputeChange{{Row: "audit", After: "Orders"}}
	if !reflect.DeepEqual(fc.Stages, wantStages) || !reflect.DeepEqual(fc.Computes, wantComputes) {
		t.Fatalf("got %+v", fc)
	}
}

func TestDiffFlowStageOwnerChanged(t *testing.T) {
	head := replace(t, baseFlow, `caller = "Orders"`, `caller = "Orders team"`)
	fc := flowChange(t, baseFlow, head)
	want := FieldChange{Path: "stage ship.caller", Before: "Orders", After: "Orders team"}
	if len(fc.Stages) != 1 || fc.Stages[0] != want {
		t.Fatalf("got %+v", fc.Stages)
	}
}

func TestDiffFlowNoChange(t *testing.T) {
	fc := flowChange(t, baseFlow, baseFlow)
	if len(fc.Stages)+len(fc.Boundaries)+len(fc.Computes) != 0 || fc.TextOnly {
		t.Fatalf("got %+v", fc)
	}
}

func TestDiffFlowCommentOnly(t *testing.T) {
	fc := flowChange(t, baseFlow, "# note\n"+baseFlow)
	if !fc.TextOnly || len(fc.Stages)+len(fc.Boundaries)+len(fc.Computes) != 0 {
		t.Fatalf("got %+v", fc)
	}
}

func TestDiffFlowWholeAdded(t *testing.T) {
	fc := flowChange(t, "", baseFlow)
	if fc.Status != Added || len(fc.Stages) != 1 || fc.Stages[0].Path != "stage ship" || !strings.Contains(fc.Stages[0].After, "call") {
		t.Fatalf("got %+v", fc)
	}
	if len(fc.Boundaries) != 0 {
		t.Fatalf("a flow added whole lists no boundary changes: %+v", fc.Boundaries)
	}
	rows := map[string]string{}
	for _, c := range fc.Computes {
		rows[c.Row] = c.After
		if c.Before != "" {
			t.Fatalf("added flow has a before: %+v", c)
		}
	}
	want := map[string]string{"zone": "Orders", "fee": "Orders", "rate": "Shipping", "total": "Orders"}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("computes = %v", rows)
	}
}

func TestDiffFlowUnparseable(t *testing.T) {
	fc := flowChange(t, baseFlow, "[[[ nope")
	if fc.Unparseable != "head" {
		t.Fatalf("got %+v", fc)
	}
}

const baseIface = `interface = "quote"
title = "Quote"
owner = "Shipping"

[[field]]
name = "zip"
type = "string"
required = true
description = "Postcode"

[[field]]
name = "weight"
type = "number"
`

func TestDiffInterfaceFields(t *testing.T) {
	head := replace(t, baseIface, "required = true", "required = false")
	head = replace(t, head, `type = "number"`, `type = "string"`)
	head += "\n[[field]]\nname = \"city\"\ntype = \"string\"\nrequired = true\n"
	ic, err := DiffInterface([]byte(baseIface), []byte(head))
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldChange{
		{Path: "field[zip].required", Before: "true", After: "false"},
		{Path: "field[weight].type", Before: "number", After: "string"},
		{Path: "field city", After: "string, required"},
	}
	if ic.Name != "quote" || !reflect.DeepEqual(ic.Fields, want) {
		t.Fatalf("got %+v", ic)
	}
}

func TestDiffInterfaceRemovedAndLabel(t *testing.T) {
	head := replace(t, baseIface, "\n[[field]]\nname = \"weight\"\ntype = \"number\"\n", "")
	head = replace(t, head, `description = "Postcode"`, `description = "Zip code"`)
	ic, err := DiffInterface([]byte(baseIface), []byte(head))
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldChange{
		{Path: "field[zip].description", Before: "Postcode", After: "Zip code", Label: true},
		{Path: "field weight", Before: "number"},
	}
	if !reflect.DeepEqual(ic.Fields, want) {
		t.Fatalf("got %+v", ic.Fields)
	}
}

func TestDiffInterfaceUnknownKeyIsNotUnparseable(t *testing.T) {
	head := "extra = 1\n" + baseIface
	ic, err := DiffInterface([]byte(baseIface), []byte(head))
	if err != nil || ic.Unparseable != "" {
		t.Fatalf("got %+v, %v", ic, err)
	}
}
