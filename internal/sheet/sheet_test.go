package sheet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCells(t *testing.T) {
	cases := []struct {
		raw   any
		text  bool
		value any
		want  bool
	}{
		{">= 46", false, 46.0, true},
		{">= 46", false, 45.0, false},
		{"-", false, nil, true},
		{"High", false, "High", true},
		{"!= High", false, "Low", true},
		{true, false, true, true},
		{"[18..35]", true, 35.0, true},
		{"(300000..500000]", true, 300000.0, false},
		{"(300000..500000]", true, 500000.0, true},
		{"24", true, 24.0, true},
	}
	for _, c := range cases {
		cell, err := ParseCell(c.raw, c.text, true)
		if err != nil {
			t.Fatalf("%v: %v", c.raw, err)
		}
		if got := cell.Match(c.value); got != c.want {
			t.Errorf("%v matches %v = %v, want %v", c.raw, c.value, got, c.want)
		}
	}
	if _, err := ParseCell("[1..5]", false, false); err == nil {
		t.Error("range accepted in a decision table")
	}
	if _, err := ParseCell("[9..5]", true, true); err == nil {
		t.Error("inverted range accepted")
	}
}

func TestPositions(t *testing.T) {
	text := `sheet = "x"
version = 1

[[row]]
id = "a"
uses = ["b"]

  [row.table]
  columns = ["b", "=>"]
  rows = [
    [true, 1],
    [false, 2],
  ]

  [[row.reject]]
  when = "a > 1"

  [row.send]
  field = "f"
`
	p := ScanPositions("x", text)
	if len(p.Rows) != 1 || p.Rows[0].Line != 4 {
		t.Fatalf("rows = %+v", p.Rows)
	}
	checks := map[string]int{"id": 5, "uses": 6, "table.columns": 9, "table.rows": 10, "reject.0.when": 16, "send.field": 19}
	for k, want := range checks {
		if got := p.RowLine(0, k); got != want {
			t.Errorf("%s at line %d, want %d", k, got, want)
		}
	}
	if got := p.Rows[0].TableRules; len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Errorf("table rule lines = %v", got)
	}
}

func TestLookupCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rates.csv")
	os.WriteFile(path, []byte("plan,age,value\nGOLD,[0..35],100\nGOLD,[36..65],200\n"), 0o644)
	d, issues := ReadLookupCSV(path, []string{"plan", "age"})
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if len(d.Entries) != 2 || d.Entries[1].Line != 3 || !Equal(d.Entries[1].Value, 200.0) {
		t.Fatalf("entries = %+v", d.Entries)
	}
	_, issues = ReadLookupCSV(path, []string{"age", "plan"})
	if len(issues) == 0 {
		t.Error("columns out of key order not reported")
	}
}
