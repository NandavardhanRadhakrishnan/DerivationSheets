package render

import (
	"html"
	"html/template"
	"strings"
)

// refFunc renders a row reference, or returns "" when id is not a row.
type refFunc func(id string) string

// exprHTML renders an expression for reading: row IDs become names, and
// operators become their usual symbols. String literals and function names
// are kept as written.
func exprHTML(src string, ref refFunc) template.HTML {
	var b strings.Builder
	ops := map[string]string{
		"==": "=", "!=": "≠", ">=": "≥", "<=": "≤", "&&": " and ", "||": " or ",
		"*": "×", "/": "÷", "-": "−",
	}
	isIdentStart := func(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	isIdent := func(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(src))
			b.WriteString(`<span class="lit">` + html.EscapeString(src[i:j]) + `</span>`)
			i = j
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdent(src[j]) {
				j++
			}
			word := src[i:j]
			k := j
			for k < len(src) && src[k] == ' ' {
				k++
			}
			call := k < len(src) && src[k] == '('
			member := i > 0 && src[i-1] == '.'
			if !call && !member {
				if r := ref(word); r != "" {
					b.WriteString(r)
					i = j
					continue
				}
			}
			switch {
			case call:
				b.WriteString(`<span class="fn">` + html.EscapeString(word) + `</span>`)
			case word == "true" || word == "false" || word == "null":
				b.WriteString(`<span class="lit">` + word + `</span>`)
			default:
				b.WriteString(html.EscapeString(word))
			}
			i = j
		default:
			if i+1 < len(src) {
				if sym, ok := ops[src[i:i+2]]; ok {
					b.WriteString(html.EscapeString(sym))
					i += 2
					continue
				}
			}
			if sym, ok := ops[src[i:i+1]]; ok {
				b.WriteString(sym)
			} else {
				b.WriteString(html.EscapeString(src[i : i+1]))
			}
			i++
		}
	}
	return template.HTML(b.String())
}
