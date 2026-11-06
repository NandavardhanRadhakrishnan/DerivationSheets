package tomledit

import (
	"regexp"
	"strings"
)

// Layout is the canonical form of one kind of file, by table path: "" for
// the top level, "row" for [[row]], "row.send" for its [row.send].
type Layout struct {
	Tables map[string]TableLayout
}

// TableLayout is the canonical form of one table.
type TableLayout struct {
	// Order lists keys in canonical order; other keys follow in their
	// current order.
	Order []string
	// Unaligned keys are written "key = value"; every other key lines up
	// its "=" with the longest of them.
	Unaligned []string
	// Multiline arrays are written one element per line, cells lined up.
	Multiline []string
	// Subs orders sub-tables; others follow in their current order.
	Subs []string
}

// Layouts of the three kinds of file the editor writes.
var (
	Sheet     = &Layout{}
	Flow      = &Layout{}
	Interface = &Layout{}
)

var firstKey = regexp.MustCompile(`^([A-Za-z0-9_\-]+)\s*=`)

// LayoutFor picks a layout by the file's first top-level key: sheet, flow
// or interface. It returns nil for anything else.
func LayoutFor(data []byte) *Layout {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := firstKey.FindStringSubmatch(t)
		if m == nil {
			return nil
		}
		switch m[1] {
		case "sheet":
			return Sheet
		case "flow":
			return Flow
		case "interface":
			return Interface
		}
		return nil
	}
	return nil
}
