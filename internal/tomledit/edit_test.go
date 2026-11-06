package tomledit

import (
	"strings"
	"testing"
)

const orderCheckout = "../../specs/order-checkout/sheet.toml"

// replaceOnce replaces old, which must occur exactly once in s.
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if n := strings.Count(s, old); n != 1 {
		t.Fatalf("%q occurs %d times", old, n)
	}
	return strings.Replace(s, old, new, 1)
}

// render writes d and checks the result is valid TOML.
func render(t *testing.T, d *Doc) string {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decodeTOML(t, out)
	return string(out)
}

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("first difference at line %d:\n got: %q\nwant: %q\n\nfull output:\n%s", i+1, g, w, got)
			}
		}
		t.Fatalf("outputs differ only in line endings:\n%q", got)
	}
}

func index(d *Doc, t *Table) int {
	for i, x := range d.Tables {
		if x == t {
			return i
		}
	}
	return -1
}

const zoneBlock = "[[row]]\n" +
	"id     = \"zone\"\n" +
	"name   = \"Shipping zone\"\n" +
	"uses   = [\"country\"]\n" +
	"lookup = { from = \"db.shipping_zones\", key = [\"country\"] }\n" +
	"values_file = \"data/shipping_zones.csv\"\n" +
	"\n" +
	"  [[row.reject]]\n" +
	"  when    = \"zone == \\\"NONE\\\"\"\n" +
	"  code    = \"DESTINATION_NOT_SERVED\"\n" +
	"  message = \"We do not ship to this country\"\n"

const zoneComment = "# Zones come from the carrier's list\n"

const couponRow = "\n[[row]]\n" +
	"id     = \"coupon\"\n" +
	"name   = \"Coupon code\"\n" +
	"source = \"request.order.coupon\"\n" +
	"type   = \"string\"\n"

func coupon() *Table {
	return &Table{Name: "row", Array: true, Keys: []*Key{
		{Name: "id", Value: "coupon"},
		{Name: "name", Value: "Coupon code"},
		{Name: "source", Value: "request.order.coupon"},
		{Name: "type", Value: "string"},
	}}
}

const subtotalSend = "  field = \"subtotal\"\n  usage = \"R\"\n"

func TestEditSameValueIsNoChange(t *testing.T) {
	orig := string(lf(readFile(t, orderCheckout)))
	d := mustParse(t, []byte(orig))
	d.Find("row", "id", "discount").Set("formula", "round(subtotal * discount_rate)")
	expect(t, render(t, d), orig)
}

func changeFormula(t *testing.T, orig string) (string, string) {
	d := mustParse(t, []byte(orig))
	d.Find("row", "id", "discount").Set("formula", "round(subtotal * discount_rate * 100) / 100")
	return render(t, d), replaceOnce(t, string(lf([]byte(orig))),
		`formula = "round(subtotal * discount_rate)"`, `formula = "round(subtotal * discount_rate * 100) / 100"`)
}

func TestEditChangeKey(t *testing.T) {
	got, want := changeFormula(t, string(lf(readFile(t, orderCheckout))))
	expect(t, got, want)
}

func TestEditAddUnalignedKey(t *testing.T) {
	orig := string(lf(readFile(t, orderCheckout)))
	d := mustParse(t, []byte(orig))
	d.Find("row", "id", "zone").Set("description", "Where the parcel goes")
	expect(t, render(t, d), replaceOnce(t, orig, "name   = \"Shipping zone\"\n",
		"name   = \"Shipping zone\"\ndescription = \"Where the parcel goes\"\n"))
}

func insertCoupon(t *testing.T, orig string) (string, string) {
	d := mustParse(t, []byte(orig))
	d.Insert(index(d, d.Find("row", "id", "subtotal"))+1, coupon())
	return render(t, d), replaceOnce(t, string(lf([]byte(orig))), subtotalSend, subtotalSend+couponRow)
}

func TestEditInsertRow(t *testing.T) {
	got, want := insertCoupon(t, string(lf(readFile(t, orderCheckout))))
	expect(t, got, want)
	if !strings.Contains(got, couponRow+"\n# ---------- From context and config ----------\n") {
		t.Fatal("new row not at the end of its section")
	}
}

func TestEditDeleteRow(t *testing.T) {
	orig := string(lf(readFile(t, orderCheckout)))
	d := mustParse(t, []byte(orig))
	if !d.Remove(d.Find("row", "id", "customs_value")) {
		t.Fatal("Remove found nothing")
	}
	block := "\n[[row]]\n" +
		"id      = \"customs_value\"\n" +
		"name    = \"Customs value\"\n" +
		"uses    = [\"subtotal\", \"discount\", \"zone\"]\n" +
		"formula = \"subtotal - discount\"\n" +
		"\n" +
		"  [row.send]\n" +
		"  field     = \"customsValue\"\n" +
		"  usage     = \"S\"\n" +
		"  when      = \"zone != \\\"DOMESTIC\\\"\"\n" +
		"  otherwise = \"do-not-send\"\n"
	expect(t, render(t, d), replaceOnce(t, orig, block, ""))
}

func TestEditDeleteRowWithComment(t *testing.T) {
	in := replaceOnce(t, string(lf(readFile(t, orderCheckout))), zoneBlock, zoneComment+zoneBlock)
	d := mustParse(t, []byte(in))
	d.Remove(d.Find("row", "id", "zone"))
	got := render(t, d)
	expect(t, got, replaceOnce(t, in, "\n"+zoneComment+zoneBlock, ""))
	if !strings.Contains(got, "# ---------- Derived ----------") {
		t.Fatal("section heading removed")
	}
}

func TestEditSectionHeadingStays(t *testing.T) {
	in := replaceOnce(t, string(lf(readFile(t, orderCheckout))),
		"# ---------- Derived ----------\n\n[[row]]", "# ---------- Derived ----------\n[[row]]")
	d := mustParse(t, []byte(in))
	d.Remove(d.Find("row", "id", "customer_years"))
	block := "[[row]]\n" +
		"id      = \"customer_years\"\n" +
		"name    = \"Years as customer\"\n" +
		"uses    = [\"signup_date\", \"request_time\"]\n" +
		"formula = \"years_between(signup_date, request_time)\"\n"
	got := render(t, d)
	expect(t, got, replaceOnce(t, in, block, ""))
	if !strings.Contains(got, "# ---------- Derived ----------\n\n[[row]]\nid     = \"zone\"") {
		t.Fatal("heading not above zone")
	}
}

func TestEditMoveRowWithComment(t *testing.T) {
	in := replaceOnce(t, string(lf(readFile(t, orderCheckout))), zoneBlock, zoneComment+zoneBlock)
	d := mustParse(t, []byte(in))
	zone := d.Find("row", "id", "zone")
	d.Remove(zone)
	d.Insert(index(d, d.Find("row", "id", "discount"))+1, zone)
	discountSend := "  field = \"discountAmount\"\n  usage = \"R\"\n"
	want := replaceOnce(t, in, "\n"+zoneComment+zoneBlock, "")
	want = replaceOnce(t, want, discountSend, discountSend+"\n"+zoneComment+zoneBlock)
	expect(t, render(t, d), want)
}

func TestEditCommentsInChangedSpan(t *testing.T) {
	in := replaceOnce(t, string(lf(readFile(t, orderCheckout))),
		"uses    = [\"subtotal\", \"discount_rate\"]\nformula = \"round(subtotal * discount_rate)\"",
		"uses    = [\"subtotal\", \"discount_rate\"]  # both inputs\n# rounded to whole cents\nformula = \"round(subtotal * discount_rate)\"")
	d := mustParse(t, []byte(in))
	row := d.Find("row", "id", "discount")
	row.Set("formula", "round(subtotal * discount_rate * 100) / 100")
	want := replaceOnce(t, in, `formula = "round(subtotal * discount_rate)"`, `formula = "round(subtotal * discount_rate * 100) / 100"`)
	expect(t, render(t, d), want)
	row.Delete("uses")
	expect(t, render(t, d), replaceOnce(t, want, "uses    = [\"subtotal\", \"discount_rate\"]  # both inputs\n", ""))
}

func TestEditTableRuleComments(t *testing.T) {
	in := replaceOnce(t, string(lf(readFile(t, orderCheckout))),
		"    [true,  \"< 2\",  \"Silver\"],\n    [true,  \">= 2\", \"Gold\"],\n",
		"    # members\n    [true,  \"< 2\",  \"Silver\"],\n    [true,  \">= 2\", \"Gold\"],  # top tier\n")
	d := mustParse(t, []byte(in))
	rows := d.Find("row", "id", "loyalty_tier").Sub("table")[0].Get("rows")
	rules := rows.Value.([]any)
	rules[0] = []any{false, "< 2", "Basic"}
	want := replaceOnce(t, in, "    [false, \"< 2\",  \"Standard\"],\n", "    [false, \"< 2\",  \"Basic\"],\n")
	expect(t, render(t, d), want)
	rows.Value = []any{rules[0], rules[1], rules[3]}
	expect(t, render(t, d), replaceOnce(t, want, "    # members\n    [true,  \"< 2\",  \"Silver\"],\n", ""))
}

func TestEditVersionAndTitle(t *testing.T) {
	orig := string(lf(readFile(t, orderCheckout)))
	d := mustParse(t, []byte(orig))
	d.Set("version", int64(2))
	want := replaceOnce(t, orig, "version = 1\n", "version = 2\n")
	expect(t, render(t, d), want)
	d.Set("title", "Checkout")
	expect(t, render(t, d), replaceOnce(t, want, "title   = \"Online order checkout\"", "title   = \"Checkout\""))
}

func TestEditInsertTopKey(t *testing.T) {
	orig := string(lf(readFile(t, "../../interfaces/shipping/quote-response.toml")))
	d := mustParse(t, []byte(orig))
	d.Set("version", "2")
	expect(t, render(t, d), replaceOnce(t, orig, "status      = \"existing\"\n", "status      = \"existing\"\nversion     = \"2\"\n"))
	d.Delete("version")
	expect(t, render(t, d), orig)
}

func TestEditFlowPin(t *testing.T) {
	orig := string(lf(readFile(t, "../../flows/checkout-to-payment/flow.toml")))
	d := mustParse(t, []byte(orig))
	k := d.Tables[3].Get("computes")
	k.Value = append(k.Value.([]any), "shipping_cost")
	expect(t, render(t, d), replaceOnce(t, orig,
		`computes = ["zone", "discount_rate", "discount", "free_shipping"]`,
		`computes = ["zone", "discount_rate", "discount", "free_shipping", "shipping_cost"]`))
}

const shippingMethodField = "\n[[field]]\n" +
	"name     = \"shippingMethod\"\n" +
	"type     = \"string\"\n" +
	"required = false\n"

func shippingMethod() *Table {
	return &Table{Name: "field", Array: true, Keys: []*Key{
		{Name: "name", Value: "shippingMethod"}, {Name: "type", Value: "string"}, {Name: "required", Value: false},
	}}
}

func TestEditInterfaceAddField(t *testing.T) {
	orig := string(lf(readFile(t, "../../interfaces/orders/order-placed.toml")))
	d := mustParse(t, []byte(orig))
	d.Insert(len(d.Tables), shippingMethod())
	expect(t, render(t, d), orig+shippingMethodField)
}

func TestEditCRLF(t *testing.T) {
	in := string(crlf(readFile(t, orderCheckout)))
	got, want := changeFormula(t, in)
	expect(t, got, string(crlf([]byte(want))))
	got, want = insertCoupon(t, in)
	expect(t, got, string(crlf([]byte(want))))
}

func TestEditNoFinalNewline(t *testing.T) {
	in := string(noFinalNewline(lf(readFile(t, "../../interfaces/orders/order-placed.toml"))))
	d := mustParse(t, []byte(in))
	d.Insert(len(d.Tables), shippingMethod())
	expect(t, render(t, d), in+"\n"+strings.TrimSuffix(shippingMethodField, "\n"))

	d = mustParse(t, []byte(in))
	d.Remove(d.Tables[len(d.Tables)-1])
	last := "\n\n[[field]]\n" +
		"name     = \"loyaltyTier\"\n" +
		"type     = \"string\"\n" +
		"required = true\n" +
		"enum     = [\"STANDARD\", \"SILVER\", \"GOLD\"]"
	if !strings.HasSuffix(in, last) {
		t.Fatalf("unexpected fixture end:\n%s", in)
	}
	expect(t, render(t, d), strings.TrimSuffix(in, last))
}

func TestEditSendBecomesArray(t *testing.T) {
	orig := string(lf(readFile(t, orderCheckout)))
	d := mustParse(t, []byte(orig))
	row := d.Find("row", "id", "customs_value")
	row.Sub("send")[0].Array = true
	row.Subs = append(row.Subs, &Table{Name: "send", Array: true, Keys: []*Key{
		{Name: "field", Value: "customsValue"}, {Name: "usage", Value: "R"}, {Name: "target", Value: "shipping-quote"},
	}})
	keys := "  field     = \"customsValue\"\n" +
		"  usage     = \"S\"\n" +
		"  when      = \"zone != \\\"DOMESTIC\\\"\"\n" +
		"  otherwise = \"do-not-send\"\n"
	expect(t, render(t, d), replaceOnce(t, orig, "  [row.send]\n"+keys,
		"  [[row.send]]\n"+keys+"\n  [[row.send]]\n  target = \"shipping-quote\"\n  field  = \"customsValue\"\n  usage  = \"R\"\n"))
}

func TestEditClone(t *testing.T) {
	orig := string(lf(readFile(t, orderCheckout)))
	d := mustParse(t, []byte(orig))
	c := d.Find("row", "id", "total").Clone()
	c.Set("id", "total_copy")
	d.Insert(len(d.Tables), c)
	got := render(t, d)
	if !strings.HasPrefix(got, orig) || !strings.Contains(got[len(orig):], "id      = \"total_copy\"") {
		t.Fatalf("clone not appended as a new row:\n%s", got[len(orig)-40:])
	}
	if d.Find("row", "id", "total") == nil {
		t.Fatal("Clone changed the original")
	}
}

func TestEditUnsupportedValue(t *testing.T) {
	d := mustParse(t, lf(readFile(t, orderCheckout)))
	d.Find("row", "id", "total").Set("x", map[string]any{})
	if _, err := d.Bytes(); err == nil || !strings.Contains(err.Error(), "x") {
		t.Fatalf("err = %v", err)
	}
}
