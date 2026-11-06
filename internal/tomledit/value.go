package tomledit

import (
	"fmt"
	"math"
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
			out = append(out, &Key{Name: keyName(kv), Value: v})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unexpected TOML value of kind %s", n.Kind)
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
			out[i] = &Key{Name: k.Name, Value: copyValue(k.Value)}
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
