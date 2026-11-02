package iface

import (
	"path/filepath"
	"testing"
)

var examples = filepath.Join("..", "..", "interfaces")

func fields(in *Interface) map[string]Field {
	out := map[string]Field{}
	for _, f := range in.Fields {
		out[f.Name] = f
	}
	return out
}

func TestImportOpenAPIRequest(t *testing.T) {
	in, err := Import(filepath.Join(examples, "storefront", "storefront.openapi.yaml"), ImportOptions{Operation: "placeOrder", Part: "request"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields(in)
	if in.Kind != "api-request" || in.Name != "place-order" {
		t.Errorf("kind %q name %q", in.Kind, in.Name)
	}
	checks := []struct {
		name     string
		typ      string
		required bool
	}{
		{"customer", "object", true},
		{"customer.signupDate", "date", true},
		{"customer.id", "string", false},
		{"shipping.method", "string", true},
		{"order.giftMessage", "string", false},
	}
	for _, c := range checks {
		got, ok := f[c.name]
		if !ok || got.Type != c.typ || got.Required != c.required {
			t.Errorf("%s = %+v, want type %s required %v", c.name, got, c.typ, c.required)
		}
	}
	if len(f["shipping.method"].Enum) != 2 {
		t.Errorf("enum not imported: %+v", f["shipping.method"])
	}
}

func TestImportOpenAPIResponse(t *testing.T) {
	in, err := Import(filepath.Join(examples, "shipping", "shipping.openapi.yaml"), ImportOptions{Operation: "getShippingQuote", Part: "response"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields(in)
	if in.Kind != "api-response" || !f["fee"].Required || f["estimatedDays"].Required || f["estimatedDays"].Type != "number" {
		t.Errorf("response fields = %+v", in.Fields)
	}
	if _, err := Import(filepath.Join(examples, "shipping", "shipping.openapi.yaml"), ImportOptions{Operation: "nope"}); err == nil {
		t.Error("unknown operation accepted")
	}
}

func TestImportAvro(t *testing.T) {
	in, err := Import(filepath.Join(examples, "orders", "order-placed.avsc"), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := fields(in)
	if in.Kind != "kafka-topic" || in.Name != "order-placed" {
		t.Errorf("kind %q name %q", in.Kind, in.Name)
	}
	if f["weightKg"].Required || !f["subtotal"].Required || f["placedAt"].Type != "datetime" || len(f["loyaltyTier"].Enum) != 3 {
		t.Errorf("fields = %+v", in.Fields)
	}
}

func TestImportJSONSchema(t *testing.T) {
	in, err := Import(filepath.Join(examples, "payments", "payment-request.schema.json"), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := fields(in)
	if !f["currency"].Required || f["customsValue"].Required || len(in.Fields) != 11 {
		t.Errorf("fields = %+v", in.Fields)
	}
}

func TestWriteLoad(t *testing.T) {
	in, err := Import(filepath.Join(examples, "orders", "order-placed.avsc"), ImportOptions{Owner: "Orders team"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.toml")
	if err := Write(path, in, ""); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Owner != "Orders team" || len(back.Fields) != len(in.Fields) || back.Field("weightKg").Required {
		t.Errorf("round trip lost data: %+v", back)
	}
}
