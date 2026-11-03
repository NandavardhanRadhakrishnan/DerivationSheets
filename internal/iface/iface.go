// Package iface models interfaces: the boundaries a flow passes through,
// such as an API request or response, a Kafka topic or an event. Interfaces
// are saved as TOML files so any flow can bring them in, and they can be
// imported from OpenAPI, Avro or JSON Schema.
package iface

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/brand"
)

// Kinds of interface.
var Kinds = []string{"api-request", "api-response", "kafka-topic", "event", "file", "other"}

// Statuses of an interface.
const (
	StatusExisting = "existing" // already in production; changes are requests to its owner
	StatusProposed = "proposed" // being designed as part of the flow
)

// Interface is a set of fields that crosses a boundary.
type Interface struct {
	Name        string
	Title       string
	Kind        string
	Owner       string
	Status      string
	Version     string
	Imported    string
	Description string
	Fields      []Field

	// File is where the interface was loaded from, and Ref how the flow
	// referred to it ("shipping:interfaces/quote.toml" or a path).
	File string
	Ref  string
	// Revision is the source revision, for interfaces from git sources.
	Revision string

	byName map[string]*Field
}

// Field is one field of an interface. Nested fields use dotted names.
type Field struct {
	Name        string
	Type        string // string, number, boolean, date, datetime, object, array
	Required    bool
	Description string
	Enum        []string
}

// Field returns a field by its dotted name.
func (i *Interface) Field(name string) *Field {
	if i.byName == nil {
		i.byName = map[string]*Field{}
		for k := range i.Fields {
			i.byName[i.Fields[k].Name] = &i.Fields[k]
		}
	}
	return i.byName[name]
}

// Label is the title, or the name.
func (i *Interface) Label() string {
	if i.Title != "" {
		return i.Title
	}
	return i.Name
}

// Load reads an interface file.
func Load(path string) (*Interface, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, _ := de.Position()
			return nil, fmt.Errorf("%s:%d: %v", path, line, err)
		}
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if problems := CheckShape(raw); len(problems) > 0 {
		return nil, fmt.Errorf("%s: %s", path, strings.Join(problems, "; "))
	}
	in := &Interface{File: path}
	in.Name, _ = raw["interface"].(string)
	in.Title, _ = raw["title"].(string)
	in.Kind, _ = raw["kind"].(string)
	in.Owner, _ = raw["owner"].(string)
	in.Status, _ = raw["status"].(string)
	in.Version, _ = raw["version"].(string)
	in.Imported, _ = raw["imported"].(string)
	in.Description, _ = raw["description"].(string)
	if in.Status == "" {
		in.Status = StatusExisting
	}
	fields, _ := raw["field"].([]any)
	for _, f := range fields {
		m, _ := f.(map[string]any)
		var fd Field
		fd.Name, _ = m["name"].(string)
		fd.Type, _ = m["type"].(string)
		fd.Required, _ = m["required"].(bool)
		fd.Description, _ = m["description"].(string)
		if es, ok := m["enum"].([]any); ok {
			for _, e := range es {
				fd.Enum = append(fd.Enum, fmt.Sprint(e))
			}
		}
		in.Fields = append(in.Fields, fd)
	}
	return in, nil
}

// CheckShape reports structural problems in a decoded interface file.
func CheckShape(raw map[string]any) []string {
	var out []string
	allowed := map[string]bool{"interface": true, "title": true, "kind": true, "owner": true, "status": true,
		"version": true, "imported": true, "description": true, "field": true}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowed[k] {
			out = append(out, fmt.Sprintf("unknown key %q", k))
		}
	}
	if s, _ := raw["interface"].(string); s == "" {
		out = append(out, "interface: a name is required")
	}
	if k, ok := raw["kind"].(string); ok && !contains(Kinds, k) {
		out = append(out, fmt.Sprintf("kind %q: expected one of %s", k, strings.Join(Kinds, ", ")))
	}
	if s, ok := raw["status"].(string); ok && s != StatusExisting && s != StatusProposed {
		out = append(out, fmt.Sprintf("status %q: expected %q or %q", s, StatusExisting, StatusProposed))
	}
	fields, _ := raw["field"].([]any)
	seen := map[string]bool{}
	for i, f := range fields {
		m, ok := f.(map[string]any)
		if !ok {
			out = append(out, fmt.Sprintf("field %d: must be a table", i+1))
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			out = append(out, fmt.Sprintf("field %d: name is required", i+1))
		} else if seen[name] {
			out = append(out, fmt.Sprintf("field %q is listed twice", name))
		}
		seen[name] = true
		for k := range m {
			switch k {
			case "name", "type", "required", "description", "enum":
			default:
				out = append(out, fmt.Sprintf("field %q: unknown key %q", name, k))
			}
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// Write saves an interface as TOML. schemaRel, when set, adds a #:schema
// line for editors.
func Write(path string, in *Interface, schemaRel string) error {
	var b strings.Builder
	if schemaRel != "" {
		fmt.Fprintf(&b, "#:schema %s\n", schemaRel)
	}
	if in.Imported != "" {
		fmt.Fprintf(&b, "# Imported by `%s interface import` from %s. Re-import to refresh the fields.\n", brand.Command, in.Imported)
	}
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-11s = %s\n", k, strconv.Quote(v))
		}
	}
	kv("interface", in.Name)
	kv("title", in.Title)
	kv("kind", in.Kind)
	kv("owner", in.Owner)
	kv("status", in.Status)
	kv("version", in.Version)
	kv("imported", in.Imported)
	kv("description", in.Description)
	for _, f := range in.Fields {
		b.WriteString("\n[[field]]\n")
		fmt.Fprintf(&b, "name     = %s\n", strconv.Quote(f.Name))
		if f.Type != "" {
			fmt.Fprintf(&b, "type     = %s\n", strconv.Quote(f.Type))
		}
		fmt.Fprintf(&b, "required = %t\n", f.Required)
		if len(f.Enum) > 0 {
			qs := make([]string, len(f.Enum))
			for i, e := range f.Enum {
				qs[i] = strconv.Quote(e)
			}
			fmt.Fprintf(&b, "enum     = [%s]\n", strings.Join(qs, ", "))
		}
		if f.Description != "" {
			fmt.Fprintf(&b, "description = %s\n", strconv.Quote(f.Description))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// KindLabel names a kind for display: "Kafka topic", "API request".
func KindLabel(kind string) string {
	if l, ok := map[string]string{
		"api-request": "API request", "api-response": "API response", "kafka-topic": "Kafka topic",
		"event": "Event", "file": "File", "other": "Interface",
	}[kind]; ok {
		return l
	}
	return "Interface"
}
