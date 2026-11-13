package diff

import (
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func decode(t *testing.T, text string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := toml.Unmarshal([]byte(text), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDiffMapsNested(t *testing.T) {
	before := decode(t, "[send]\nfield = \"salesTax\"\nusage = \"R\"\nmap = { Gold = \"GLD\", Silver = \"SLV\" }\n")
	after := decode(t, "[send]\nfield = \"taxAmount\"\nusage = \"R\"\nmap = { Gold = \"GLD\", Silver = \"SIL\" }\n")
	got := diffMaps("", before, after, sheetListKey)
	want := []FieldChange{
		{Path: "send.field", Before: "salesTax", After: "taxAmount"},
		{Path: "send.map.Silver", Before: "SLV", After: "SIL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestDiffMapsNumbersAreEqualAcrossIntAndFloat(t *testing.T) {
	got := diffMaps("", decode(t, "x = 1\n"), decode(t, "x = 1.0\n"), sheetListKey)
	if len(got) != 0 {
		t.Fatalf("1 vs 1.0 reported as a change: %+v", got)
	}
}

func TestDiffMapsAddedAndRemovedKeys(t *testing.T) {
	got := diffMaps("", decode(t, "a = 1\n"), decode(t, "b = \"x\"\n"), sheetListKey)
	want := []FieldChange{
		{Path: "a", Before: "1"},
		{Path: "b", After: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestDiffMapsListWithoutKeyUsesPosition(t *testing.T) {
	got := diffMaps("", decode(t, "uses = [\"a\", \"b\"]\n"), decode(t, "uses = [\"a\", \"c\"]\n"), sheetListKey)
	want := []FieldChange{{Path: "uses", Before: "[a, b]", After: "[a, c]"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
