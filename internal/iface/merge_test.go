package iface

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func names(in *Interface) []string {
	var out []string
	for _, f := range in.Fields {
		out = append(out, f.Name)
	}
	return out
}

func TestMergeKeepsLocal(t *testing.T) {
	fresh := &Interface{Name: "order-placed", Fields: []Field{{Name: "orderId", Type: "string", Required: true}}}
	existing := &Interface{Name: "order-placed", Fields: []Field{
		{Name: "orderId", Type: "string", Required: true},
		{Name: "shippingMethod", Type: "string", Required: true, Local: true},
		{Name: "giftWrap", Type: "boolean", Local: true},
	}}
	merged, kept := Merge(fresh, existing)
	if want := []string{"orderId", "shippingMethod", "giftWrap"}; !reflect.DeepEqual(names(merged), want) {
		t.Errorf("fields %v, want %v", names(merged), want)
	}
	if want := []string{"shippingMethod", "giftWrap"}; !reflect.DeepEqual(kept, want) {
		t.Errorf("kept %v, want %v", kept, want)
	}
	if f := merged.Field("shippingMethod"); f == nil || !f.Local || !f.Required || f.Type != "string" {
		t.Errorf("shippingMethod = %+v", f)
	}
}

func TestMergeDropsMarkWhenSchemaHasIt(t *testing.T) {
	fresh := &Interface{Name: "order-placed", Fields: []Field{
		{Name: "orderId", Type: "string", Required: true},
		{Name: "shippingMethod", Type: "string", Required: false},
	}}
	existing := &Interface{Name: "order-placed", Fields: []Field{
		{Name: "shippingMethod", Type: "number", Required: true, Local: true},
	}}
	merged, kept := Merge(fresh, existing)
	if len(kept) != 0 {
		t.Errorf("kept %v, want none", kept)
	}
	f := merged.Field("shippingMethod")
	if f == nil || f.Local || f.Type != "string" || f.Required {
		t.Errorf("shippingMethod = %+v, want the schema's field without the mark", f)
	}
}

func TestMergeReplacesOthers(t *testing.T) {
	fresh := &Interface{Name: "order-placed", Fields: []Field{{Name: "orderId", Type: "string", Required: true}}}
	existing := &Interface{Name: "order-placed", Fields: []Field{
		{Name: "orderId", Type: "number"},
		{Name: "legacyCode", Type: "string"},
	}}
	merged, kept := Merge(fresh, existing)
	if len(kept) != 0 || !reflect.DeepEqual(names(merged), []string{"orderId"}) {
		t.Errorf("fields %v kept %v", names(merged), kept)
	}
	if merged.Field("orderId").Type != "string" {
		t.Errorf("orderId comes from the schema, got %+v", merged.Field("orderId"))
	}
}

func TestLocalRoundTrip(t *testing.T) {
	in := &Interface{Name: "order-placed", Kind: "kafka-topic", Imported: "order-placed.avsc", Fields: []Field{
		{Name: "loyaltyTier", Type: "string", Required: true, Enum: []string{"GOLD"}, Local: true},
	}}
	path := filepath.Join(t.TempDir(), "x.toml")
	if err := Write(path, in, ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "enum     = [\"GOLD\"]\nlocal    = true\n") {
		t.Errorf("local is written after enum:\n%s", data)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Fields[0].Local {
		t.Errorf("local lost on load: %+v", back.Fields[0])
	}
	if _, err := Parse(path, []byte("interface = \"x\"\n[[field]]\nname = \"a\"\nlocal = \"yes\"\n")); err == nil {
		t.Error("local must be a boolean")
	}
}
