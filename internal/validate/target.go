package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Target is the target request schema: a JSON Schema, or a schema inside an
// OpenAPI document selected with a JSON pointer after "#".
type Target struct {
	root   any            // whole document, for resolving $ref
	schema map[string]any // the selected schema
}

// LoadTarget reads a JSON or YAML file, with an optional "#/pointer".
func LoadTarget(spec string) (*Target, error) {
	path, ptr, _ := strings.Cut(spec, "#")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if strings.HasSuffix(path, ".json") {
		err = json.Unmarshal(data, &doc)
	} else {
		err = yaml.Unmarshal(data, &doc)
	}
	if err != nil {
		return nil, err
	}
	t := &Target{root: doc}
	node, err := pointer(doc, ptr)
	if err != nil {
		return nil, err
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q is not a schema object", spec)
	}
	t.schema = t.deref(m)
	return t, nil
}

func pointer(doc any, ptr string) (any, error) {
	ptr = strings.TrimPrefix(ptr, "/")
	if ptr == "" {
		return doc, nil
	}
	cur := doc
	for _, p := range strings.Split(ptr, "/") {
		p = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("pointer segment %q does not resolve", p)
		}
		if cur, ok = m[p]; !ok {
			return nil, fmt.Errorf("pointer segment %q does not resolve", p)
		}
	}
	return cur, nil
}

// deref follows local $refs.
func (t *Target) deref(m map[string]any) map[string]any {
	for range 32 {
		ref, ok := m["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#") {
			return m
		}
		n, err := pointer(t.root, ref[1:])
		if err != nil {
			return m
		}
		next, ok := n.(map[string]any)
		if !ok {
			return m
		}
		m = next
	}
	return m
}

// object returns the schema at a dotted path, or nil.
func (t *Target) object(path []string) map[string]any {
	cur := t.schema
	for _, p := range path {
		props, _ := cur["properties"].(map[string]any)
		next, ok := props[p].(map[string]any)
		if !ok {
			return nil
		}
		cur = t.deref(next)
	}
	return cur
}

// HasField reports whether a dotted field is declared.
func (t *Target) HasField(field string) bool {
	return t.object(strings.Split(field, ".")) != nil
}

// RequiredFields returns required fields that are not sent: required at the
// top level, and required inside any object that a sent field lives in.
func (t *Target) RequiredFields(sent []string) []string {
	isSent := map[string]bool{}
	parents := map[string]bool{"": true}
	for _, f := range sent {
		isSent[f] = true
		parts := strings.Split(f, ".")
		for i := 1; i < len(parts); i++ {
			p := strings.Join(parts[:i], ".")
			parents[p] = true
			isSent[p] = true
		}
	}
	var out []string
	for parent := range parents {
		var path []string
		if parent != "" {
			path = strings.Split(parent, ".")
		}
		obj := t.object(path)
		if obj == nil {
			continue
		}
		req, _ := obj["required"].([]any)
		for _, r := range req {
			name, _ := r.(string)
			full := name
			if parent != "" {
				full = parent + "." + name
			}
			if !isSent[full] {
				out = append(out, full)
			}
		}
	}
	return out
}
