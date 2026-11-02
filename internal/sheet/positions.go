package sheet

import (
	"regexp"
	"strconv"
	"strings"
)

// Positions maps sheet.toml content to line numbers. The TOML decoder does
// not expose positions, so this scans the text. It understands the shapes the
// format uses: top-level keys, [[row]] blocks, [row.*] sub-tables,
// [[row.reject]] blocks and multi-line arrays such as table rows.
type Positions struct {
	File string
	Top  map[string]int
	Rows []*RowPos
}

// RowPos holds line numbers for one [[row]] block.
type RowPos struct {
	Line int
	// Keys are dotted: "uses", "send.field", "table.rows", "reject.0.when".
	Keys map[string]int
	// TableRules holds the line of each decision-table rule.
	TableRules []int
}

var (
	keyLine    = regexp.MustCompile(`^([A-Za-z0-9_\-]+)\s*=`)
	headerLine = regexp.MustCompile(`^\[\[?\s*([A-Za-z0-9_.\-]+)\s*\]\]?`)
)

// ScanPositions builds Positions from sheet.toml text.
func ScanPositions(file, text string) *Positions {
	p := &Positions{File: file, Top: map[string]int{}}
	var cur *RowPos
	section := "" // "", "row", "row.table", "row.send", "reject.N"
	rejects := -1
	depth := 0           // bracket depth of an open multi-line array
	arrayKey := ""       // dotted key of that array
	inMultiline := false // inside a """ or ''' string

	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		t := strings.TrimSpace(line)
		if inMultiline {
			if strings.Contains(t, `"""`) || strings.Contains(t, `'''`) {
				inMultiline = false
			}
			continue
		}
		if depth > 0 {
			if arrayKey == "table.rows" && depth == 1 && strings.HasPrefix(t, "[") && cur != nil {
				cur.TableRules = append(cur.TableRules, n)
			}
			depth += bracketDelta(t)
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if m := headerLine.FindStringSubmatch(t); m != nil && !keyLine.MatchString(t) {
			name := m[1]
			switch {
			case name == "row" && strings.HasPrefix(t, "[["):
				cur = &RowPos{Line: n, Keys: map[string]int{}}
				p.Rows = append(p.Rows, cur)
				section = "row"
				rejects = -1
			case name == "row.reject" && cur != nil:
				rejects++
				section = "reject." + strconv.Itoa(rejects)
				cur.Keys[section] = n
			case strings.HasPrefix(name, "row.") && cur != nil:
				section = strings.TrimPrefix(name, "row.")
				cur.Keys[section] = n
			default:
				// A top-level table such as [display] or [display.columns]:
				// its keys are recorded as dotted top-level keys.
				cur = nil
				section = name
				p.Top[name] = n
			}
			continue
		}
		m := keyLine.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		key := m[1]
		full := key
		switch {
		case cur == nil && section != "":
			full = section + "." + key
			p.Top[full] = n
		case cur == nil:
			p.Top[key] = n
		case section == "row":
			cur.Keys[key] = n
		default:
			full = section + "." + key
			cur.Keys[full] = n
		}
		if strings.HasSuffix(strings.TrimSpace(t[len(m[0]):]), `"""`) || strings.HasSuffix(strings.TrimSpace(t[len(m[0]):]), `'''`) {
			inMultiline = true
			continue
		}
		value := t[len(m[0]):]
		if d := bracketDelta(value); d > 0 {
			depth = d
			arrayKey = full
			if full == "table.rows" && cur != nil {
				// Rules may start on the same line as `rows = [`.
				inner := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "["))
				if strings.HasPrefix(inner, "[") {
					cur.TableRules = append(cur.TableRules, n)
				}
			}
		} else if full == "table.rows" && cur != nil {
			for range strings.Count(value, "[") - 1 {
				cur.TableRules = append(cur.TableRules, n)
			}
		}
	}
	return p
}

// bracketDelta counts [ minus ] outside strings and comments.
func bracketDelta(s string) int {
	d := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return d
		case c == '[':
			d++
		case c == ']':
			d--
		}
	}
	return d
}

// RowLine returns the line of a key in row index i, falling back to the
// row's [[row]] line, then 0.
func (p *Positions) RowLine(i int, keys ...string) int {
	if p == nil || i < 0 || i >= len(p.Rows) {
		return 0
	}
	for _, k := range keys {
		if n, ok := p.Rows[i].Keys[k]; ok {
			return n
		}
	}
	return p.Rows[i].Line
}

// TableRuleLine returns the line of rule j in row i's decision table.
func (p *Positions) TableRuleLine(i, j int) int {
	if p == nil || i < 0 || i >= len(p.Rows) {
		return 0
	}
	if j >= 0 && j < len(p.Rows[i].TableRules) {
		return p.Rows[i].TableRules[j]
	}
	return p.RowLine(i, "table.rows", "table")
}
