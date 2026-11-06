package tomledit

import "strings"

// Bytes writes the document back.
func (d *Doc) Bytes() ([]byte, error) {
	var b strings.Builder
	for _, sg := range d.segs {
		for _, line := range d.lines[sg.start:sg.end] {
			b.WriteString(line)
		}
	}
	return []byte(b.String()), nil
}
