package diff

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// listKeyFunc says how to match the elements of a list between two versions.
// path is the list's path, elem one element, parent the map holding the list.
// It returns the element's identity, and the value to compare once two
// elements with the same identity are paired (for a table rule, its result).
// ok is false when the list has no identity (elements are then matched by
// position).
type listKeyFunc func(path string, elem any, parent map[string]any) (key string, value any, ok bool)

// diffMaps lists every exact part that differs between two decoded TOML
// tables, in path order. Either side may be nil.
func diffMaps(path string, before, after map[string]any, key listKeyFunc) []FieldChange {
	seen := map[string]bool{}
	var names []string
	for k := range before {
		seen[k] = true
		names = append(names, k)
	}
	for k := range after {
		if !seen[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	var out []FieldChange
	for _, k := range names {
		out = append(out, diffValue(join(path, k), before[k], after[k], before, after, key)...)
	}
	return out
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

// diffValue compares one value; a nil side means the value is absent. bp and
// ap are the tables that hold the value on each side.
func diffValue(path string, b, a any, bp, ap map[string]any, key listKeyFunc) []FieldChange {
	bm, bIsMap := b.(map[string]any)
	am, aIsMap := a.(map[string]any)
	if (bIsMap || b == nil) && (aIsMap || a == nil) && (bIsMap || aIsMap) {
		return diffMaps(path, bm, am, key)
	}
	bl, bIsList := b.([]any)
	al, aIsList := a.([]any)
	if (bIsList || b == nil) && (aIsList || a == nil) && (bIsList || aIsList) {
		return diffLists(path, bl, al, bp, ap, key)
	}
	if equalValues(b, a) {
		return nil
	}
	return []FieldChange{{Path: path, Before: text(b), After: text(a)}}
}

func diffLists(path string, bl, al []any, bp, ap map[string]any, key listKeyFunc) []FieldChange {
	if !hasStructure(bl) && !hasStructure(al) {
		if equalValues(bl, al) {
			return nil
		}
		return []FieldChange{{Path: path, Before: listText(bl), After: listText(al)}}
	}
	bk, bok := keysOf(path, bl, bp, key)
	ak, aok := keysOf(path, al, ap, key)
	if bok && aok {
		return diffKeyed(path, bl, al, bk, ak, key)
	}
	var out []FieldChange
	for i := 0; i < len(bl) || i < len(al); i++ {
		p := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(bl):
			out = append(out, whole(p, nil, al[i], key)...)
		case i >= len(al):
			out = append(out, whole(p, bl[i], nil, key)...)
		default:
			out = append(out, diffValue(p, bl[i], al[i], nil, nil, key)...)
		}
	}
	return out
}

type keyed struct {
	key   string
	value any
	elem  any
}

// keysOf identifies every element of a list, or reports that it cannot
// (some element has no identity, or two share one).
func keysOf(path string, l []any, parent map[string]any, key listKeyFunc) ([]keyed, bool) {
	out := make([]keyed, 0, len(l))
	seen := map[string]bool{}
	for _, e := range l {
		k, v, ok := key(path, e, parent)
		if !ok || seen[k] {
			return nil, false
		}
		seen[k] = true
		out = append(out, keyed{k, v, e})
	}
	return out, true
}

func diffKeyed(path string, bl, al []any, bk, ak []keyed, key listKeyFunc) []FieldChange {
	before := map[string]keyed{}
	for _, k := range bk {
		before[k.key] = k
	}
	inAfter := map[string]bool{}
	var out []FieldChange
	for _, k := range ak {
		inAfter[k.key] = true
		p := path + "[" + k.key + "]"
		b, ok := before[k.key]
		if !ok {
			out = append(out, whole(p, nil, k.elem, key)...)
			continue
		}
		out = append(out, diffValue(p, b.value, k.value, nil, nil, key)...)
	}
	for _, k := range bk {
		if !inAfter[k.key] {
			out = append(out, whole(path+"["+k.key+"]", k.elem, nil, key)...)
		}
	}
	return out
}

// whole reports an element that exists on one side only: a table is broken
// into its parts, anything else shown whole.
func whole(path string, b, a any, key listKeyFunc) []FieldChange {
	if m, ok := b.(map[string]any); ok {
		return diffMaps(path, m, nil, key)
	}
	if m, ok := a.(map[string]any); ok {
		return diffMaps(path, nil, m, key)
	}
	return []FieldChange{{Path: path, Before: text(b), After: text(a)}}
}

func hasStructure(l []any) bool {
	for _, e := range l {
		switch e.(type) {
		case map[string]any, []any:
			return true
		}
	}
	return false
}

func listText(l []any) string {
	if l == nil {
		return ""
	}
	return text(l)
}

// text writes a decoded value as short readable text.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if x == "" {
			return `""`
		}
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = text(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		names := make([]string, 0, len(x))
		for k := range x {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, len(names))
		for i, k := range names {
			parts[i] = k + " = " + text(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

// equalValues compares decoded values, treating 1 and 1.0 as the same.
func equalValues(a, b any) bool { return reflect.DeepEqual(norm(a), norm(b)) }

func norm(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = norm(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = norm(e)
		}
		return out
	}
	return v
}

// sheetListKey says how the lists inside a sheet row are matched: table
// rules by their input cells, rejects by code, sends by target and field.
func sheetListKey(path string, elem any, parent map[string]any) (string, any, bool) {
	switch {
	case strings.HasSuffix(path, "table.rows"):
		rule, ok := elem.([]any)
		if !ok || len(rule) == 0 {
			return "", nil, false
		}
		cols, _ := parent["columns"].([]any)
		cells, result := rule, any(nil)
		if n := len(cols); n > 0 && cols[n-1] == "=>" && len(rule) == n {
			cells, result = rule[:n-1], rule[n-1]
		}
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = text(c)
		}
		return strings.Join(parts, ","), result, true
	case path == "reject" || strings.HasSuffix(path, ".reject"):
		m, ok := elem.(map[string]any)
		code, _ := m["code"].(string)
		if !ok || code == "" {
			return "", nil, false
		}
		return code, m, true
	case path == "send" || strings.HasSuffix(path, ".send"):
		m, ok := elem.(map[string]any)
		field, _ := m["field"].(string)
		if !ok || field == "" {
			return "", nil, false
		}
		target, _ := m["target"].(string)
		if target != "" {
			field = target + ":" + field
		}
		return field, m, true
	}
	return "", nil, false
}
