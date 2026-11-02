package sheet

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// CellKind is the kind of a decision-table or lookup key cell.
type CellKind int

const (
	CellAny     CellKind = iota // "-"
	CellLiteral                 // a value that must be equal
	CellCompare                 // "<", "<=", ">", ">=", "!=" followed by a value
	CellRange                   // "[a..b]", "(a..b]", "]a..b[" and so on; lookup data only
)

// Cell is one input condition.
type Cell struct {
	Kind  CellKind
	Op    string // for CellCompare
	Value any    // for CellLiteral and CellCompare
	Lo    any    // for CellRange
	Hi    any
	LoInc bool
	HiInc bool
	Raw   string
}

// ParseCell parses a cell. TOML cells carry their own type; text cells (from
// CSV) are typed by their content. Ranges are only allowed when allowRange is
// set.
func ParseCell(raw any, fromText, allowRange bool) (Cell, error) {
	s, isString := raw.(string)
	if !isString {
		v := Normalize(raw)
		return Cell{Kind: CellLiteral, Value: v, Raw: FormatValue(v)}, nil
	}
	t := strings.TrimSpace(s)
	if t == "-" {
		return Cell{Kind: CellAny, Raw: "-"}, nil
	}
	for _, op := range []string{"<=", ">=", "!=", "<", ">"} {
		if strings.HasPrefix(t, op) {
			rest := strings.TrimSpace(t[len(op):])
			if rest == "" {
				return Cell{}, fmt.Errorf("comparison %q has no value", s)
			}
			return Cell{Kind: CellCompare, Op: op, Value: ParseText(rest), Raw: op + " " + rest}, nil
		}
	}
	if strings.Contains(t, "..") && len(t) > 2 && strings.ContainsAny(t[:1], "[(]") && strings.ContainsAny(t[len(t)-1:], "])[") {
		if !allowRange {
			return Cell{}, fmt.Errorf("range %q is only allowed in lookup data; use comparisons or split the rule", s)
		}
		lo, hi, ok := strings.Cut(t[1:len(t)-1], "..")
		if !ok {
			return Cell{}, fmt.Errorf("malformed range %q", s)
		}
		c := Cell{
			Kind:  CellRange,
			Lo:    ParseText(strings.TrimSpace(lo)),
			Hi:    ParseText(strings.TrimSpace(hi)),
			LoInc: t[0] == '[',
			HiInc: t[len(t)-1] == ']',
			Raw:   t,
		}
		if cmp, ok := Compare(c.Lo, c.Hi); !ok || cmp > 0 {
			return Cell{}, fmt.Errorf("range %q must have a lower bound below its upper bound, of the same type", s)
		}
		return c, nil
	}
	if fromText {
		v := ParseText(t)
		return Cell{Kind: CellLiteral, Value: v, Raw: FormatValue(v)}, nil
	}
	return Cell{Kind: CellLiteral, Value: s, Raw: s}, nil
}

// Match reports whether value satisfies the cell. A nil value only matches "-".
func (c Cell) Match(value any) bool {
	switch c.Kind {
	case CellAny:
		return true
	case CellLiteral:
		return Equal(value, c.Value)
	case CellCompare:
		if value == nil {
			return false
		}
		if c.Op == "!=" {
			return !Equal(value, c.Value)
		}
		cmp, ok := Compare(value, c.Value)
		if !ok {
			return false
		}
		switch c.Op {
		case "<":
			return cmp < 0
		case "<=":
			return cmp <= 0
		case ">":
			return cmp > 0
		case ">=":
			return cmp >= 0
		}
	case CellRange:
		lo, ok1 := Compare(value, c.Lo)
		hi, ok2 := Compare(value, c.Hi)
		if !ok1 || !ok2 {
			return false
		}
		return (lo > 0 || (c.LoInc && lo == 0)) && (hi < 0 || (c.HiInc && hi == 0))
	}
	return false
}

func (c Cell) String() string { return c.Raw }

// Normalize converts decoded TOML or JSON values to the sheet's value types:
// float64, bool, string, time.Time, nil, []any and map[string]any.
func Normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case toml.LocalDate:
		return time.Date(x.Year, time.Month(x.Month), x.Day, 0, 0, 0, 0, time.UTC)
	case toml.LocalDateTime:
		return time.Date(x.Year, time.Month(x.Month), x.Day, x.Hour, x.Minute, x.Second, x.Nanosecond, time.UTC)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = Normalize(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Normalize(e)
		}
		return out
	}
	return v
}

// ParseText types a text value: numbers, true/false, dates and datetimes are
// recognised; quoted text loses its quotes; anything else stays a string.
func ParseText(s string) any {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64); err == nil && !strings.ContainsAny(s, "xXnN") {
		return f
	}
	if t, ok := ParseTime(s); ok {
		return t
	}
	return s
}

// ParseTime parses a date (2006-01-02, as UTC midnight) or an RFC 3339
// datetime.
func ParseTime(s string) (time.Time, bool) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// Equal compares two sheet values. Numbers compare numerically, times by
// instant, and other values by their string form, so a TOML map key "true"
// equals the boolean true.
func Equal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if cmp, ok := Compare(a, b); ok {
		return cmp == 0
	}
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			return ab == bb
		}
	}
	return FormatValue(a) == FormatValue(b)
}

// Compare orders two numbers, two strings or two times.
func Compare(a, b any) (int, bool) {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		if !ok {
			return 0, false
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	case string:
		y, ok := b.(string)
		if !ok {
			return 0, false
		}
		return strings.Compare(x, y), true
	case time.Time:
		y, ok := b.(time.Time)
		if !ok {
			return 0, false
		}
		return x.Compare(y), true
	}
	return 0, false
}

// FormatValue renders a value compactly for keys, traces and display.
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatFloat(x, 'f', 0, 64)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	case time.Time:
		if x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0 && x.Location() == time.UTC {
			return x.Format("2006-01-02")
		}
		return x.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}
