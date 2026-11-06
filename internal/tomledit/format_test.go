package tomledit

import (
	"bytes"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func mustFormat(t *testing.T, d *Doc) []byte {
	t.Helper()
	out, err := d.Format()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeTOML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return m
}

// canonicalFiles are example files already in canonical form.
func canonicalFiles(t *testing.T) []string {
	t.Helper()
	files := []string{
		"../../specs/order-checkout/sheet.toml",
		"../../flows/loyalty-report/sheet.toml",
		"../../flows/mobile-orders/sheet.toml",
	}
	for _, pattern := range []string{"../../flows/*/flow.toml", "../../interfaces/*/*.toml"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	return files
}

func TestFormatCanonicalFiles(t *testing.T) {
	for _, file := range canonicalFiles(t) {
		for name, in := range map[string][]byte{"as is": readFile(t, file), "lf": lf(readFile(t, file)), "crlf": crlf(readFile(t, file))} {
			if out := mustFormat(t, mustParse(t, in)); !bytes.Equal(out, in) {
				t.Errorf("%s (%s): Format changed a canonical file:\n%s", file, name, out)
			}
		}
	}
}

func TestFormatKeepsMeaning(t *testing.T) {
	for _, file := range examples(t) {
		in := readFile(t, file)
		out := mustFormat(t, mustParse(t, in))
		if want, got := decodeTOML(t, in), decodeTOML(t, out); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Format changed the meaning:\n%s", file, out)
		}
	}
}

func TestFormatAlignsSend(t *testing.T) {
	out := string(lf(mustFormat(t, mustParse(t, readFile(t, "../../examples/pieces/2-orders-team/sheet.toml")))))
	want := "  [[row.send]]\n" +
		"  field     = \"customsValue\"\n" +
		"  usage     = \"S\"\n" +
		"  when      = \"zone != \\\"DOMESTIC\\\"\"\n" +
		"  otherwise = \"do-not-send\"\n"
	if !strings.Contains(out, want) {
		t.Fatalf("customs_value send not aligned:\n%s", out)
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{"a\"b\\c", `"a\"b\\c"`},
		{"tab\there", `"tab\there"`},
		{"\x01", `"\u0001"`},
		{"é", `"é"`},
		{"one\ntwo", "\"\"\"\none\ntwo\"\"\""},
		{int64(3), "3"},
		{3, "3"},
		{0.05, "0.05"},
		{float64(2), "2"},
		{1e21, "1e+21"},
		{math.Inf(-1), "-inf"},
		{math.NaN(), "nan"},
		{true, "true"},
		{[]any{}, "[]"},
		{[]any{"a", 1.5}, `["a", 1.5]`},
		{Inline{}, "{}"},
		{Inline{{Name: "Standard", Value: "STD"}, {Name: "Silver", Value: "SLV"}}, `{ Standard = "STD", Silver = "SLV" }`},
		{Inline{{Name: "a b", Value: int64(1)}}, `{ "a b" = 1 }`},
		{Raw("1979-05-27"), "1979-05-27"},
	}
	for _, c := range cases {
		got, err := FormatValue(c.v, "\n")
		if err != nil || got != c.want {
			t.Errorf("FormatValue(%#v) = %q, %v; want %q", c.v, got, err, c.want)
		}
	}
	for _, bad := range []any{map[string]any{}, nil} {
		if _, err := FormatValue(bad, "\n"); err == nil {
			t.Errorf("FormatValue(%#v) accepted", bad)
		}
	}
}

func TestFormatMultilineStringCRLF(t *testing.T) {
	d := mustParse(t, []byte("sheet = \"s\"\r\n\r\n[[row]]\r\nid = \"a\"\r\n"))
	d.Tables[0].Keys = append(d.Tables[0].Keys, &Key{Name: "text", Value: "line one\nline two"})
	out := mustFormat(t, d)
	if !bytes.Contains(out, []byte("text = \"\"\"\r\nline one\r\nline two\"\"\"\r\n")) {
		t.Fatalf("multi-line string:\n%q", out)
	}
	if got := mustParse(t, out).Tables[0].Get("text").Value; got != "line one\nline two" {
		t.Fatalf("decoded back as %q", got)
	}
}

func TestFormatTableRows(t *testing.T) {
	sheet := readFile(t, "../../specs/order-checkout/sheet.toml")
	lines := strings.SplitAfter(string(lf(sheet)), "\n")
	want := strings.Join(lines[90:98], "")
	d := mustParse(t, []byte("sheet = \"s\"\n\n[[row]]\nid = \"a\"\n"))
	d.Tables[0].Subs = append(d.Tables[0].Subs, &Table{Name: "table", Keys: []*Key{
		{Name: "columns", Value: []any{"is_member", "customer_years", "=>"}},
		{Name: "rows", Value: []any{
			[]any{false, "< 2", "Standard"},
			[]any{false, ">= 2", "Silver"},
			[]any{true, "< 2", "Silver"},
			[]any{true, ">= 2", "Gold"},
		}},
	}})
	got := string(mustFormat(t, d))
	if got != "sheet = \"s\"\n\n[[row]]\nid = \"a\"\n\n"+want {
		t.Fatalf("got:\n%s\nwant table:\n%s", got, want)
	}
}
