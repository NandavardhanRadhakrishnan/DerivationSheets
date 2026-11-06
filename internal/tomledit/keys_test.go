package tomledit

import (
	"strings"
	"testing"
)

func parseText(t *testing.T, text string) *Doc {
	t.Helper()
	d, err := Parse([]byte(text), nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDottedKeysKeepSpelling(t *testing.T) {
	d := parseText(t, "[[row]]\nid = \"a\"\na.\"b c\" = 1\n")
	d.Tables[0].Set("id", "b")
	d.Tables[0].Set("x.y", int64(3))
	m := decodeTOML(t, []byte(render(t, d)))
	row := m["row"].([]any)[0].(map[string]any)
	if a, _ := row["a"].(map[string]any); a == nil || a["b c"] != int64(1) {
		t.Errorf("a.\"b c\" read back as %v", row)
	}
	if x, _ := row["x"].(map[string]any); x == nil || x["y"] != int64(3) {
		t.Errorf("new dotted key x.y read back as %v", row)
	}
}

func TestQuotedTableName(t *testing.T) {
	for _, text := range []string{"[\"a.b\"]\nx = 1\n", "[t]\nx = 1\n\n  [t.\"c.d\"]\ny = 1\n"} {
		d := parseText(t, text)
		d.Tables[0].Set("x", int64(2))
		if len(d.Tables[0].Subs) > 0 {
			d.Tables[0].Subs[0].Set("y", int64(2))
		}
		out := render(t, d)
		m := decodeTOML(t, []byte(out))
		if strings.HasPrefix(text, "[\"a.b\"]") {
			if tb, _ := m["a.b"].(map[string]any); tb == nil || tb["x"] != int64(2) {
				t.Errorf("table \"a.b\" read back as %v\n%s", m, out)
			}
			continue
		}
		tb, _ := m["t"].(map[string]any)
		if sub, _ := tb["c.d"].(map[string]any); sub == nil || sub["y"] != int64(2) {
			t.Errorf("sub-table \"c.d\" read back as %v\n%s", m, out)
		}
	}
}

func TestInlineDottedKeys(t *testing.T) {
	d := parseText(t, "m = { a.b = 1, \"c.d\" = 2, e = 3 }\n")
	in := d.Get("m").Value.(Inline)
	in[2].Value = int64(4)
	m := decodeTOML(t, []byte(render(t, d)))["m"].(map[string]any)
	if a, _ := m["a"].(map[string]any); a == nil || a["b"] != int64(1) || m["c.d"] != int64(2) || m["e"] != int64(4) {
		t.Errorf("m read back as %v", m)
	}
}

func TestMultilineArrayKeepsComments(t *testing.T) {
	d := parseText(t, "[[row]]\nid = \"a\"\nuses = [\n  \"x\", # why x\n  # y matters\n  \"y\",\n]\n")
	d.Tables[0].Set("uses", []any{"x", "y", "z"})
	out := render(t, d)
	for _, want := range []string{"# why x", "# y matters", `"z"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost %q:\n%s", want, out)
		}
	}
}

func TestNewInlineKeysAreLiteral(t *testing.T) {
	d := parseText(t, "m = { a = 1 }\n")
	d.Get("m").Value = Inline{{Name: "0.05", Value: "x"}, {Name: "a", Value: int64(2)}}
	m := decodeTOML(t, []byte(render(t, d)))["m"].(map[string]any)
	if m["0.05"] != "x" || m["a"] != int64(2) {
		t.Errorf("m read back as %v", m)
	}
}
