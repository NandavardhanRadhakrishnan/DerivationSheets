package tomledit

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// examples lists every TOML file under the repository's example folders.
func examples(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, root := range []string{"specs", "flows", "interfaces", "examples"} {
		err := filepath.WalkDir(filepath.Join("..", "..", root), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".toml") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 20 {
		t.Fatalf("found only %d example files", len(files))
	}
	return files
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func lf(data []byte) []byte   { return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")) }
func crlf(data []byte) []byte { return bytes.ReplaceAll(lf(data), []byte("\n"), []byte("\r\n")) }

func noFinalNewline(data []byte) []byte {
	if bytes.HasSuffix(data, []byte("\r\n")) {
		return data[:len(data)-2]
	}
	return bytes.TrimSuffix(data, []byte("\n"))
}

func mustParse(t *testing.T, data []byte) *Doc {
	t.Helper()
	d, err := Parse(data, LayoutFor(data))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRoundTripExamples(t *testing.T) {
	for _, file := range examples(t) {
		orig := readFile(t, file)
		for name, in := range map[string][]byte{"as is": orig, "lf": lf(orig), "crlf": crlf(orig), "no final newline": noFinalNewline(orig)} {
			d, err := Parse(in, LayoutFor(in))
			if err != nil {
				t.Errorf("%s (%s): %v", file, name, err)
				continue
			}
			out, err := d.Bytes()
			if err != nil {
				t.Errorf("%s (%s): Bytes: %v", file, name, err)
				continue
			}
			if !bytes.Equal(out, in) {
				t.Errorf("%s (%s): round trip changed the file:\n%s", file, name, out)
			}
		}
	}
}

func TestParseSheet(t *testing.T) {
	d := mustParse(t, readFile(t, "../../specs/order-checkout/sheet.toml"))
	var top []string
	for _, k := range d.Top {
		top = append(top, k.Name)
	}
	if !reflect.DeepEqual(top, []string{"sheet", "version", "title"}) {
		t.Fatalf("top keys = %v", top)
	}
	if v := d.Get("version").Value; v != int64(1) {
		t.Fatalf("version = %#v", v)
	}
	if len(d.Tables) != 20 {
		t.Fatalf("%d tables", len(d.Tables))
	}
	for _, tb := range d.Tables {
		if tb.Name != "row" || !tb.Array {
			t.Fatalf("table %q array=%v", tb.Name, tb.Array)
		}
	}
	tier := d.Find("row", "id", "loyalty_tier")
	rows, ok := tier.Sub("table")[0].Get("rows").Value.([]any)
	if !ok || len(rows) != 4 {
		t.Fatalf("loyalty_tier rows = %#v", tier.Sub("table")[0].Get("rows").Value)
	}
	if !reflect.DeepEqual(rows[0], []any{false, "< 2", "Standard"}) {
		t.Fatalf("first rule = %#v", rows[0])
	}
	want := Inline{{Name: "Standard", Value: int64(0)}, {Name: "Silver", Value: 0.05}, {Name: "Gold", Value: 0.1}}
	if got := d.Find("row", "id", "discount_rate").Get("values").Value; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v", got)
	}
	if d.Find("row", "id", "customs_value").Sub("send")[0].Array {
		t.Fatal("customs_value send parsed as [[row.send]]")
	}
	if d.Find("row", "id", "nothing") != nil {
		t.Fatal("Find returned a table for a missing row")
	}
}

func TestParseFlow(t *testing.T) {
	d := mustParse(t, readFile(t, "../../flows/checkout-to-payment/flow.toml"))
	var names []string
	for _, tb := range d.Tables {
		names = append(names, tb.Name)
	}
	if !reflect.DeepEqual(names, []string{"sources", "input", "stage", "stage", "output"}) {
		t.Fatalf("tables = %v", names)
	}
	if v := d.Tables[3].Sub("request")[0].Get("interface").Value; v != "shipping:quote-request.toml" {
		t.Fatalf("request interface = %#v", v)
	}
	want := Inline{{Name: "path", Value: "../../interfaces/shipping"}}
	if got := d.Tables[0].Get("shipping").Value; !reflect.DeepEqual(got, want) {
		t.Fatalf("shipping = %#v", got)
	}
}

func TestParseValues(t *testing.T) {
	d := mustParse(t, []byte("a = 0x1F\nb = 1_000\nc = -inf\nd = 1979-05-27\ne = 'lit'\nf = \"\"\"\none\r\ntwo\"\"\"\ng = []\nh = {}\n[t . \"x y\"]\nk = 1\n"))
	cases := map[string]any{"a": int64(31), "b": int64(1000), "d": Raw("1979-05-27"), "e": "lit", "f": "one\ntwo", "g": []any{}, "h": Inline{}}
	for k, want := range cases {
		if got := d.Get(k).Value; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", k, got, want)
		}
	}
	if c, _ := d.Get("c").Value.(float64); c > -1e308 {
		t.Errorf("c = %#v", d.Get("c").Value)
	}
	if d.Tables[0].Name != "t.x y" {
		t.Errorf("table name = %q", d.Tables[0].Name)
	}
}

func TestParseComments(t *testing.T) {
	flow := mustParse(t, readFile(t, "../../flows/checkout-to-payment/flow.toml"))
	stage := flow.Tables[2].orig
	if !strings.HasPrefix(flow.lines[stage.start], "# 1. The storefront publishes") {
		t.Fatalf("first stage starts at %q", flow.lines[stage.start])
	}
	sheet := mustParse(t, readFile(t, "../../specs/order-checkout/sheet.toml"))
	for _, sg := range sheet.segs {
		if sg.free {
			continue
		}
		for _, l := range sheet.lines[sg.start:sg.end] {
			if strings.Contains(l, "---------- Derived ----------") {
				t.Fatalf("section heading inside a span starting %q", sheet.lines[sg.start])
			}
		}
	}
	first := sheet.segs[0]
	if first.free || first.key != "sheet" || !strings.HasPrefix(sheet.lines[first.start], "#:schema") {
		t.Fatalf("first segment = %+v", first)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("a = \n"), nil); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte("a = 1\na = 2\n"), nil); err == nil {
		t.Fatal("duplicate key accepted")
	}
}
