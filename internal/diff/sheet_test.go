package diff

import (
	"reflect"
	"strings"
	"testing"
)

const baseSheet = `sheet = "demo"
version = 1
title = "Demo"

[[row]]
id = "net"
name = "Net"
formula = "a - b"

  [row.send]
  field = "netAmount"
  usage = "R"

[[row]]
id = "tier"
name = "Tier"
uses = ["member", "years"]

  [row.table]
  columns = ["member", "years", "=>"]
  rows = [
    [false, "< 2", "Standard"],
    [true, ">= 2", "Gold"],
  ]

[[row]]
id = "zone"
name = "Zone"
formula = "country"

  [[row.reject]]
  when = "zone == \"NONE\""
  code = "NOT_SERVED"
  message = "We do not ship there"
`

func change(t *testing.T, base, head string) SheetChange {
	t.Helper()
	var b, h []byte
	if base != "" {
		b = []byte(base)
	}
	if head != "" {
		h = []byte(head)
	}
	sc, err := DiffSheet(b, h)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func replace(t *testing.T, s, old, new string) string {
	t.Helper()
	i := indexOf(s, old)
	if i < 0 {
		t.Fatalf("%q not in test sheet", old)
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDiffSheetFormulaChange(t *testing.T) {
	sc := change(t, baseSheet, replace(t, baseSheet, `formula = "a - b"`, `formula = "a - c"`))
	want := []RowChange{{ID: "net", Status: Changed, Fields: []FieldChange{{Path: "formula", Before: "a - b", After: "a - c"}}}}
	if sc.Status != Changed || sc.Name != "demo" || !reflect.DeepEqual(sc.Rows, want) {
		t.Fatalf("got %+v", sc)
	}
}

func TestDiffSheetTableRule(t *testing.T) {
	sc := change(t, baseSheet, replace(t, baseSheet, `[true, ">= 2", "Gold"]`, `[true, ">= 2", "Platinum"]`))
	want := []RowChange{{ID: "tier", Status: Changed, Fields: []FieldChange{{Path: "table.rows[true,>= 2]", Before: "Gold", After: "Platinum"}}}}
	if !reflect.DeepEqual(sc.Rows, want) {
		t.Fatalf("got %+v", sc.Rows)
	}
}

func TestDiffSheetTableReorderIsNoChange(t *testing.T) {
	head := replace(t, baseSheet, "    [false, \"< 2\", \"Standard\"],\n    [true, \">= 2\", \"Gold\"],", "    [true, \">= 2\", \"Gold\"],\n    [false, \"< 2\", \"Standard\"],")
	sc := change(t, baseSheet, head)
	if len(sc.Rows) != 0 {
		t.Fatalf("reorder reported: %+v", sc.Rows)
	}
}

func TestDiffSheetTableRuleAddedAndRemoved(t *testing.T) {
	head := replace(t, baseSheet, `    [true, ">= 2", "Gold"],`, `    [true, ">= 5", "Platinum"],`)
	sc := change(t, baseSheet, head)
	want := []FieldChange{
		{Path: "table.rows[true,>= 5]", After: "[true, >= 5, Platinum]"},
		{Path: "table.rows[true,>= 2]", Before: "[true, >= 2, Gold]"},
	}
	if len(sc.Rows) != 1 || !reflect.DeepEqual(sc.Rows[0].Fields, want) {
		t.Fatalf("got %+v", sc.Rows)
	}
}

func TestDiffSheetRejectByCode(t *testing.T) {
	sc := change(t, baseSheet, replace(t, baseSheet, "We do not ship there", "Not served"))
	want := []RowChange{{ID: "zone", Status: Changed, Fields: []FieldChange{{Path: "reject[NOT_SERVED].message", Before: "We do not ship there", After: "Not served"}}}}
	if !reflect.DeepEqual(sc.Rows, want) {
		t.Fatalf("got %+v", sc.Rows)
	}
}

func TestDiffSheetRowsAddedRemoved(t *testing.T) {
	head := baseSheet + "\n[[row]]\nid = \"extra\"\nname = \"Extra\"\nformula = \"1\"\n"
	sc := change(t, baseSheet, head)
	if len(sc.Rows) != 1 || sc.Rows[0].ID != "extra" || sc.Rows[0].Status != Added {
		t.Fatalf("added: %+v", sc.Rows)
	}
	sc = change(t, head, baseSheet)
	if len(sc.Rows) != 1 || sc.Rows[0].ID != "extra" || sc.Rows[0].Status != Removed {
		t.Fatalf("removed: %+v", sc.Rows)
	}
	var logic []string
	for _, f := range sc.Rows[0].Fields {
		if !f.Label {
			logic = append(logic, f.Path)
		}
	}
	if !reflect.DeepEqual(logic, []string{"formula"}) {
		t.Fatalf("removed row's logic fields = %v", logic)
	}
}

func TestDiffSheetLabelOnly(t *testing.T) {
	sc := change(t, baseSheet, replace(t, baseSheet, `name = "Net"`, `name = "Net amount"`))
	want := []RowChange{{ID: "net", Status: Changed, Fields: []FieldChange{{Path: "name", Before: "Net", After: "Net amount", Label: true}}}}
	if !reflect.DeepEqual(sc.Rows, want) {
		t.Fatalf("got %+v", sc.Rows)
	}
}

func TestDiffSheetTitleIsLabel(t *testing.T) {
	sc := change(t, baseSheet, replace(t, baseSheet, `title = "Demo"`, `title = "Demo sheet"`))
	want := []FieldChange{{Path: "title", Before: "Demo", After: "Demo sheet", Label: true}}
	if !reflect.DeepEqual(sc.Meta, want) {
		t.Fatalf("got %+v", sc.Meta)
	}
}

func TestDiffSheetCommentOnly(t *testing.T) {
	sc := change(t, baseSheet, "# a new comment\n"+baseSheet)
	if len(sc.Rows) != 0 || len(sc.Meta) != 0 || !sc.TextOnly {
		t.Fatalf("got %+v", sc)
	}
}

func TestDiffSheetWholeAddedAndRemoved(t *testing.T) {
	sc := change(t, "", baseSheet)
	if sc.Status != Added || len(sc.Rows) != 3 {
		t.Fatalf("added: %+v", sc)
	}
	for _, r := range sc.Rows {
		if r.Status != Added || len(r.Fields) == 0 {
			t.Fatalf("row %+v", r)
		}
	}
	sc = change(t, baseSheet, "")
	if sc.Status != Removed || len(sc.Rows) != 3 || sc.Rows[0].Status != Removed {
		t.Fatalf("removed: %+v", sc)
	}
}

func TestDiffSheetUnparseable(t *testing.T) {
	sc := change(t, baseSheet, "this is = = not toml")
	if sc.Unparseable != "head" || len(sc.Rows) != 0 {
		t.Fatalf("got %+v", sc)
	}
	sc = change(t, "[[[", baseSheet)
	if sc.Unparseable != "base" {
		t.Fatalf("got %+v", sc)
	}
}

func TestDiffSheetDuplicateIDAddedIsNotAChangeToTheOriginal(t *testing.T) {
	head := baseSheet + "\n[[row]]\nid = \"net\"\nformula = \"a * 9\"\n"
	sc := change(t, baseSheet, head)
	if len(sc.Rows) != 1 || sc.Rows[0].ID != "net#2" || sc.Rows[0].Status != Added {
		t.Fatalf("got %+v", sc.Rows)
	}
}

func TestDiffSheetExistingDuplicateIDsAreStable(t *testing.T) {
	dup := baseSheet + "\n[[row]]\nid = \"net\"\nformula = \"a * 9\"\n"
	sc := change(t, dup, "# comment\n"+dup)
	if len(sc.Rows) != 0 {
		t.Fatalf("phantom change: %+v", sc.Rows)
	}
}

func TestDiffSheetDisplayIsLabel(t *testing.T) {
	base := "sheet = \"d\"\n\n[display]\nwidth = 1\n"
	sc := change(t, base, strings.Replace(base, "width = 1", "width = 2", 1))
	want := []FieldChange{{Path: "display.width", Before: "1", After: "2", Label: true}}
	if !reflect.DeepEqual(sc.Meta, want) {
		t.Fatalf("got %+v", sc.Meta)
	}
}

func TestDiffSheetSlugRenameIsShown(t *testing.T) {
	base := "sheet = \"a\"\nversion = 1\n"
	sc := change(t, base, strings.Replace(base, `"a"`, `"b"`, 1))
	want := []FieldChange{{Path: "sheet", Before: "a", After: "b"}}
	if !reflect.DeepEqual(sc.Meta, want) {
		t.Fatalf("got %+v", sc.Meta)
	}
}
