package tomledit

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// decode turns a value node into the model: string, bool, int64, float64,
// []any, Inline, or Raw for dates and times.
func decode(n *unstable.Node, data []byte) (any, error) {
	switch n.Kind {
	case unstable.String:
		return strings.ReplaceAll(string(n.Data), "\r\n", "\n"), nil
	case unstable.Bool:
		return string(n.Data) == "true", nil
	case unstable.Integer:
		return strconv.ParseInt(string(n.Data), 0, 64)
	case unstable.Float:
		s := strings.ReplaceAll(string(n.Data), "_", "")
		switch strings.TrimLeft(s, "+-") {
		case "inf":
			if strings.HasPrefix(s, "-") {
				return math.Inf(-1), nil
			}
			return math.Inf(1), nil
		case "nan":
			return math.NaN(), nil
		}
		return strconv.ParseFloat(s, 64)
	case unstable.LocalDate, unstable.LocalTime, unstable.LocalDateTime, unstable.DateTime:
		return Raw(data[n.Raw.Offset : n.Raw.Offset+n.Raw.Length]), nil
	case unstable.Array:
		out := []any{}
		it := n.Children()
		for it.Next() {
			v, err := decode(it.Node(), data)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case unstable.InlineTable:
		out := Inline{}
		it := n.Children()
		for it.Next() {
			kv := it.Node()
			v, err := decode(kv.Value(), data)
			if err != nil {
				return nil, err
			}
			name := keyName(kv)
			out = append(out, &Key{Name: name, Value: v, text: keyText(kv, data), textFor: name})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unexpected TOML value of kind %s", n.Kind)
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

// formatKey writes one part of a key, quoted when it is not bare.
func formatKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return quote(k, "")
}

// quote writes a basic string; a string with a newline becomes a multi-line
// basic string whose lines end with nl.
func quote(s, nl string) string {
	multi := nl != "" && strings.Contains(s, "\n")
	var b strings.Builder
	if multi {
		b.WriteString(`"""` + nl)
	} else {
		b.WriteByte('"')
	}
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n' && multi:
			b.WriteString(nl)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	if multi {
		b.WriteString(`"""`)
	} else {
		b.WriteByte('"')
	}
	return b.String()
}

func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case f == math.Trunc(f) && math.Abs(f) < 1<<53:
		return strconv.FormatInt(int64(f), 10)
	}
	if s := strconv.FormatFloat(f, 'f', -1, 64); strings.Contains(s, ".") {
		return s
	}
	return strconv.FormatFloat(f, 'e', -1, 64)
}

// FormatValue writes v as canonical TOML value text. nl is the line ending
// for multi-line strings.
func FormatValue(v any, nl string) (string, error) {
	switch x := v.(type) {
	case string:
		return quote(x, nl), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return formatFloat(x), nil
	case Raw:
		return string(x), nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			s, err := FormatValue(e, nl)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case Inline:
		if len(x) == 0 {
			return "{}", nil
		}
		parts := make([]string, len(x))
		for i, k := range x {
			if k == nil {
				return "", fmt.Errorf("inline table with a missing key")
			}
			s, err := FormatValue(k.Value, nl)
			if err != nil {
				return "", fmt.Errorf("%s: %w", k.Name, err)
			}
			parts[i] = k.writtenInline() + " = " + s
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}
	return "", fmt.Errorf("cannot write a value of type %T", v)
}

// formatDotted writes a key name with each "."-separated part quoted as
// needed: x.y is the key y in the table x.
func formatDotted(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = formatKey(p)
	}
	return strings.Join(parts, ".")
}

// keyText returns a key node's parts as written, quotes included, joined
// with ".".
func keyText(n *unstable.Node, data []byte) string {
	var parts []string
	it := n.Key()
	for it.Next() {
		r := it.Node().Raw
		parts = append(parts, string(data[r.Offset:r.Offset+r.Length]))
	}
	return strings.Join(parts, ".")
}

// keyName joins the parts of a key node's key with ".".
func keyName(n *unstable.Node) string {
	var parts []string
	it := n.Key()
	for it.Next() {
		parts = append(parts, string(it.Node().Data))
	}
	return strings.Join(parts, ".")
}

// copyValue deep-copies a model value, so the original content of a span
// survives edits made in place.
func copyValue(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = copyValue(e)
		}
		return out
	case Inline:
		out := make(Inline, len(x))
		for i, k := range x {
			out[i] = &Key{Name: k.Name, Value: copyValue(k.Value), text: k.text, textFor: k.textFor}
		}
		return out
	}
	return v
}

// number returns v as a float64 when it is a number.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// equal compares model values; numbers compare by value, so int64(0)
// equals 0.0.
func equal(a, b any) bool {
	if x, ok := number(a); ok {
		y, ok := number(b)
		return ok && (x == y || math.IsNaN(x) && math.IsNaN(y))
	}
	switch x := a.(type) {
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case Inline:
		y, ok := b.(Inline)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i].Name != y[i].Name || !equal(x[i].Value, y[i].Value) {
				return false
			}
		}
		return true
	case string, bool, Raw:
		return a == b
	}
	return false
}
