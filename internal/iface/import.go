package iface

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ImportOptions select what to import and describe the result.
type ImportOptions struct {
	// Pointer selects a schema inside the document, e.g.
	// "#/components/schemas/Order".
	Pointer string
	// Operation and Part select an OpenAPI request or response body:
	// Part is "request" or "response" (first 2xx), or "response:404".
	Operation string
	Part      string

	Name, Title, Kind, Owner, Status, Version string
}

// Import reads OpenAPI (YAML or JSON), Avro (.avsc) or JSON Schema.
func Import(path string, opt ImportOptions) (*Interface, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if strings.HasSuffix(strings.ToLower(path), ".json") || strings.HasSuffix(strings.ToLower(path), ".avsc") {
		err = json.Unmarshal(data, &doc)
	} else {
		var y any
		err = yaml.Unmarshal(data, &y)
		doc = normalizeYAML(y)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	var in *Interface
	switch {
	case isAvro(path, doc):
		in, err = importAvro(doc)
	case isOpenAPI(doc):
		in, err = importOpenAPI(doc, opt)
	default:
		in, err = importJSONSchema(doc, opt.Pointer)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	in.Imported = filepath.ToSlash(path)
	for _, kv := range [][2]*string{{&in.Name, &opt.Name}, {&in.Title, &opt.Title}, {&in.Kind, &opt.Kind},
		{&in.Owner, &opt.Owner}, {&in.Status, &opt.Status}, {&in.Version, &opt.Version}} {
		if *kv[1] != "" {
			*kv[0] = *kv[1]
		}
	}
	if in.Status == "" {
		in.Status = StatusExisting
	}
	if in.Name == "" {
		in.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	in.Name = slug(in.Name)
	return in, nil
}

// normalizeYAML converts map[string]any trees from yaml.v3, which may hold
// non-string keys such as response codes, to JSON-like values.
func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalizeYAML(e)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, e := range x {
			out[fmt.Sprint(k)] = normalizeYAML(e)
		}
		return out
	case []any:
		for i := range x {
			x[i] = normalizeYAML(x[i])
		}
	}
	return v
}

func isOpenAPI(doc any) bool {
	m, ok := doc.(map[string]any)
	return ok && (m["openapi"] != nil || m["swagger"] != nil)
}

func isAvro(path string, doc any) bool {
	if strings.HasSuffix(strings.ToLower(path), ".avsc") {
		return true
	}
	m, ok := doc.(map[string]any)
	return ok && m["type"] == "record" && m["fields"] != nil
}

// --- JSON Schema and OpenAPI ---

type walker struct {
	root   any
	fields []Field
	seen   map[string]bool
}

func pointer(doc any, ptr string) (any, error) {
	ptr = strings.TrimPrefix(strings.TrimPrefix(ptr, "#"), "/")
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

func (w *walker) deref(s map[string]any) map[string]any {
	for range 32 {
		ref, ok := s["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#") {
			return s
		}
		n, err := pointer(w.root, ref)
		if err != nil {
			return s
		}
		next, ok := n.(map[string]any)
		if !ok {
			return s
		}
		s = next
	}
	return s
}

// properties merges allOf members, so composed objects list all fields.
func (w *walker) properties(s map[string]any) (map[string]any, []string) {
	s = w.deref(s)
	props := map[string]any{}
	var req []string
	if p, ok := s["properties"].(map[string]any); ok {
		for k, v := range p {
			props[k] = v
		}
	}
	for _, r := range asList(s["required"]) {
		req = append(req, fmt.Sprint(r))
	}
	for _, part := range asList(s["allOf"]) {
		if pm, ok := part.(map[string]any); ok {
			p, r := w.properties(pm)
			for k, v := range p {
				props[k] = v
			}
			req = append(req, r...)
		}
	}
	return props, req
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// schemaType reads type, format and nullability, looking through oneOf and
// anyOf unions with null.
func (w *walker) schemaType(s map[string]any) (typ string, nullable bool, inner map[string]any) {
	s = w.deref(s)
	inner = s
	if b, _ := s["nullable"].(bool); b {
		nullable = true
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		for _, alt := range asList(s[key]) {
			am, ok := alt.(map[string]any)
			if !ok {
				continue
			}
			am = w.deref(am)
			if am["type"] == "null" {
				nullable = true
				continue
			}
			if typ == "" {
				typ, _, inner = w.schemaType(am)
			}
		}
	}
	if typ != "" {
		return typ, nullable, inner
	}
	t := s["type"]
	if list, ok := t.([]any); ok {
		for _, x := range list {
			if x == "null" {
				nullable = true
			} else if t2, ok := x.(string); ok && typ == "" {
				typ = t2
			}
		}
	} else if t2, ok := t.(string); ok {
		typ = t2
	}
	if typ == "" && s["properties"] != nil || typ == "" && s["allOf"] != nil {
		typ = "object"
	}
	switch typ {
	case "integer":
		typ = "number"
	case "string":
		switch s["format"] {
		case "date":
			typ = "date"
		case "date-time":
			typ = "datetime"
		}
	}
	return typ, nullable, inner
}

func (w *walker) walk(s map[string]any, prefix string, parentRequired bool, depth int) {
	props, req := w.properties(s)
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		ps, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		typ, nullable, inner := w.schemaType(ps)
		required := parentRequired && contains(req, name) && !nullable
		f := Field{Name: full, Type: typ, Required: required}
		f.Description, _ = inner["description"].(string)
		if f.Description == "" {
			f.Description, _ = ps["description"].(string)
		}
		for _, e := range asList(inner["enum"]) {
			f.Enum = append(f.Enum, fmt.Sprint(e))
		}
		if typ == "array" {
			if items, ok := inner["items"].(map[string]any); ok {
				it, _, _ := w.schemaType(items)
				if it != "" {
					f.Description = strings.TrimSpace("list of " + it + ". " + f.Description)
				}
			}
		}
		if !w.seen[full] {
			w.seen[full] = true
			w.fields = append(w.fields, f)
		}
		if typ == "object" && depth < 16 {
			w.walk(inner, full, required, depth+1)
		}
	}
}

func importJSONSchema(doc any, ptr string) (*Interface, error) {
	node, err := pointer(doc, ptr)
	if err != nil {
		return nil, err
	}
	s, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the selected node is not a schema object")
	}
	w := &walker{root: doc, seen: map[string]bool{}}
	s = w.deref(s)
	w.walk(s, "", true, 0)
	in := &Interface{Kind: "event", Fields: w.fields}
	in.Title, _ = s["title"].(string)
	in.Description, _ = s["description"].(string)
	in.Name = in.Title
	if in.Name == "" && ptr != "" {
		in.Name = ptr[strings.LastIndex(ptr, "/")+1:]
	}
	if len(w.fields) == 0 {
		return nil, fmt.Errorf("no fields found; select an object schema with --pointer")
	}
	return in, nil
}

func importOpenAPI(doc any, opt ImportOptions) (*Interface, error) {
	if opt.Pointer != "" {
		in, err := importJSONSchema(doc, opt.Pointer)
		if err != nil {
			return nil, err
		}
		in.Kind = "api-request"
		return in, nil
	}
	if opt.Operation == "" {
		return nil, fmt.Errorf("an OpenAPI file needs --operation (with --part request or response) or --pointer")
	}
	paths, _ := doc.(map[string]any)["paths"].(map[string]any)
	var op map[string]any
	var where string
	for p, item := range paths {
		methods, _ := item.(map[string]any)
		for method, o := range methods {
			om, ok := o.(map[string]any)
			if ok && om["operationId"] == opt.Operation {
				op, where = om, strings.ToUpper(method)+" "+p
			}
		}
	}
	if op == nil {
		return nil, fmt.Errorf("no operation with operationId %q", opt.Operation)
	}
	part, status, _ := strings.Cut(opt.Part, ":")
	var body map[string]any
	in := &Interface{Name: opt.Operation}
	switch part {
	case "", "request":
		body, _ = op["requestBody"].(map[string]any)
		in.Kind = "api-request"
		in.Title = where + " request"
	case "response":
		responses, _ := op["responses"].(map[string]any)
		if status == "" {
			codes := make([]string, 0, len(responses))
			for c := range responses {
				codes = append(codes, c)
			}
			sort.Strings(codes)
			for _, c := range codes {
				if strings.HasPrefix(c, "2") {
					status = c
					break
				}
			}
		}
		body, _ = responses[status].(map[string]any)
		in.Kind = "api-response"
		in.Title = fmt.Sprintf("%s response %s", where, status)
		in.Name = opt.Operation + "-response"
	default:
		return nil, fmt.Errorf("--part must be request, response or response:<status>")
	}
	w := &walker{root: doc, seen: map[string]bool{}}
	if body == nil {
		return nil, fmt.Errorf("%s has no %s body", where, part)
	}
	body = w.deref(body)
	content, _ := body["content"].(map[string]any)
	var media map[string]any
	if m, ok := content["application/json"].(map[string]any); ok {
		media = m
	} else {
		for _, m := range content {
			media, _ = m.(map[string]any)
			break
		}
	}
	schema, _ := media["schema"].(map[string]any)
	if schema == nil {
		return nil, fmt.Errorf("%s %s has no JSON schema", where, part)
	}
	w.walk(schema, "", true, 0)
	in.Fields = w.fields
	in.Description, _ = op["summary"].(string)
	return in, nil
}

// --- Avro ---

func importAvro(doc any) (*Interface, error) {
	rec, ok := doc.(map[string]any)
	if !ok || rec["type"] != "record" {
		return nil, fmt.Errorf("an Avro schema must be a record")
	}
	in := &Interface{Kind: "kafka-topic"}
	in.Name, _ = rec["name"].(string)
	in.Description, _ = rec["doc"].(string)
	if ns, _ := rec["namespace"].(string); ns != "" {
		in.Title = ns + "." + in.Name
	}
	named := map[string]any{}
	var walk func(r map[string]any, prefix string, parentRequired bool, depth int)
	walk = func(r map[string]any, prefix string, parentRequired bool, depth int) {
		if n, ok := r["name"].(string); ok {
			named[n] = r
		}
		for _, f := range asList(r["fields"]) {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			name, _ := fm["name"].(string)
			full := name
			if prefix != "" {
				full = prefix + "." + name
			}
			typ, nullable, sub := avroType(fm["type"], named)
			fd := Field{Name: full, Type: typ, Required: parentRequired && !nullable}
			fd.Description, _ = fm["doc"].(string)
			if sm, ok := sub.(map[string]any); ok && sm["type"] == "enum" {
				for _, s := range asList(sm["symbols"]) {
					fd.Enum = append(fd.Enum, fmt.Sprint(s))
				}
			}
			in.Fields = append(in.Fields, fd)
			if sm, ok := sub.(map[string]any); ok && sm["type"] == "record" && depth < 16 {
				walk(sm, full, fd.Required, depth+1)
			}
		}
	}
	walk(rec, "", true, 0)
	return in, nil
}

// avroType maps an Avro type to a field type, reporting whether null is
// allowed, and returns the resolved complex type for nesting.
func avroType(t any, named map[string]any) (string, bool, any) {
	switch x := t.(type) {
	case string:
		if n, ok := named[x]; ok {
			return avroType(n, named)
		}
		switch x {
		case "string", "bytes", "fixed":
			return "string", false, nil
		case "int", "long", "float", "double":
			return "number", false, nil
		case "boolean":
			return "boolean", false, nil
		case "null":
			return "", true, nil
		}
		return x, false, nil
	case []any:
		var typ string
		var sub any
		nullable := false
		for _, alt := range x {
			if alt == "null" {
				nullable = true
				continue
			}
			if typ == "" {
				typ, _, sub = avroType(alt, named)
			}
		}
		return typ, nullable, sub
	case map[string]any:
		if n, ok := x["name"].(string); ok {
			named[n] = x
		}
		switch x["logicalType"] {
		case "date":
			return "date", false, nil
		case "timestamp-millis", "timestamp-micros", "local-timestamp-millis", "local-timestamp-micros":
			return "datetime", false, nil
		case "decimal":
			return "number", false, nil
		}
		switch x["type"] {
		case "record":
			return "object", false, x
		case "enum":
			return "string", false, x
		case "array":
			return "array", false, nil
		case "map":
			return "object", false, nil
		}
		return avroType(x["type"], named)
	}
	return "", false, nil
}

func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			if i > 0 && !prevDash {
				b.WriteByte('-')
			}
			b.WriteRune(r + 32)
			prevDash = false
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
