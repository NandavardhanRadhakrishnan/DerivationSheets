package editor

import (
	"sort"
	"strconv"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// BoundaryData is an interface reference and the rows its fields carry.
type BoundaryData struct {
	Interface string            `json:"interface"`
	Carries   map[string]string `json:"carries,omitempty"` // field -> row ID
}

// StageData is one [[stage]] as the browser edits it. Fields hold what the
// file writes: ID is "" when the file names no id.
type StageData struct {
	Orig           *int              `json:"orig"` // index in the file; nil for a new stage
	ID             string            `json:"id,omitempty"`
	Kind           string            `json:"kind"`
	Interface      string            `json:"interface,omitempty"`
	Carries        map[string]string `json:"carries,omitempty"`
	Producer       string            `json:"producer,omitempty"`
	Caller         string            `json:"caller,omitempty"`
	Callee         string            `json:"callee,omitempty"`
	Computes       []string          `json:"computes,omitempty"`
	CalleeComputes []string          `json:"callee_computes,omitempty"`
	Request        *BoundaryData     `json:"request,omitempty"`
	Response       *BoundaryData     `json:"response,omitempty"`
}

// OutputData is the [output] table's editable keys. Its interface is not
// edited here.
type OutputData struct {
	Interface string            `json:"interface,omitempty"`
	Producer  string            `json:"producer,omitempty"`
	Computes  []string          `json:"computes,omitempty"`
	Carries   map[string]string `json:"carries,omitempty"`
}

// FieldEdit adds a field to an interface file, or changes the type and
// requiredness of one it has.
type FieldEdit struct {
	Interface string `json:"interface"` // relative to the store root
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Required  bool   `json:"required"`
	Add       bool   `json:"add,omitempty"`
}

// FlowDraft is what the browser sends for a flow: the parts of flow.toml
// it changed, whole, and edits to interface files.
type FlowDraft struct {
	File  string        `json:"file"` // flow.toml, relative to the store root
	Token string        `json:"token"`
	Input *BoundaryData `json:"input,omitempty"` // carries only; nil leaves [input] alone
	// Stages is every stage in the new order; nil leaves the stages alone.
	// A stage of the file left out is an error, never a deletion.
	Stages  []StageData `json:"stages,omitempty"`
	Deleted []int       `json:"deleted,omitempty"` // file indexes of deleted stages
	Output  *OutputData `json:"output,omitempty"`
	Fields  []FieldEdit `json:"fields,omitempty"`
	// Tokens are the versions of the interface files Fields change, as the
	// page loaded them, by path.
	Tokens map[string]string `json:"tokens,omitempty"`
}

// StagesFromDoc reads the stages of a parsed flow.toml.
func StagesFromDoc(doc *tomledit.Doc) []StageData {
	out := []StageData{}
	for i, t := range stageTables(doc) {
		i := i
		sd := StageData{Orig: &i, ID: str(t, "id"), Kind: str(t, "kind"), Interface: str(t, "interface"),
			Carries: carries(t), Producer: str(t, "producer"), Caller: str(t, "caller"), Callee: str(t, "callee"),
			Computes: strs(t.Get("computes")), CalleeComputes: strs(t.Get("callee_computes"))}
		if subs := t.Sub("request"); len(subs) > 0 {
			sd.Request = &BoundaryData{Interface: str(subs[0], "interface"), Carries: carries(subs[0])}
		}
		if subs := t.Sub("response"); len(subs) > 0 {
			sd.Response = &BoundaryData{Interface: str(subs[0], "interface"), Carries: carries(subs[0])}
		}
		out = append(out, sd)
	}
	return out
}

// OutputFromDoc reads [output].
func OutputFromDoc(doc *tomledit.Doc) OutputData {
	t := table(doc, "output")
	if t == nil {
		return OutputData{}
	}
	return OutputData{Interface: str(t, "interface"), Producer: str(t, "producer"), Computes: strs(t.Get("computes")), Carries: carries(t)}
}

// InputFromDoc reads [input].
func InputFromDoc(doc *tomledit.Doc) BoundaryData {
	t := table(doc, "input")
	if t == nil {
		return BoundaryData{}
	}
	return BoundaryData{Interface: str(t, "interface"), Carries: carries(t)}
}

func carries(t *tomledit.Table) map[string]string {
	k := t.Get("carries")
	if k == nil {
		return nil
	}
	out := map[string]string{}
	in, _ := k.Value.(tomledit.Inline)
	for _, x := range in {
		if s, ok := x.Value.(string); ok {
			out[x.Name] = s
		}
	}
	return out
}

func table(doc *tomledit.Doc, name string) *tomledit.Table {
	for _, t := range doc.Tables {
		if t.Name == name && !t.Array {
			return t
		}
	}
	return nil
}

func stageTables(doc *tomledit.Doc) []*tomledit.Table {
	var out []*tomledit.Table
	for _, t := range doc.Tables {
		if t.Name == "stage" {
			out = append(out, t)
		}
	}
	return out
}

// Apply returns the flow.toml content the draft describes. Interface
// edits are applied separately, by ApplyFields.
func (d FlowDraft) Apply(orig []byte) ([]byte, error) {
	doc, err := tomledit.Parse(orig, tomledit.Flow)
	if err != nil {
		return nil, err
	}
	if d.Input != nil {
		t := table(doc, "input")
		if t == nil {
			return nil, draftErr("the flow has no [input] table")
		}
		setCarries(t, d.Input.Carries)
	}
	if d.Output != nil {
		t := table(doc, "output")
		if t == nil {
			return nil, draftErr("the flow has no [output] table")
		}
		setStr(t, "producer", d.Output.Producer)
		setList(t, "computes", d.Output.Computes)
		setCarries(t, d.Output.Carries)
	}
	if d.Stages != nil || d.Deleted != nil {
		if err := d.applyStages(doc); err != nil {
			return nil, err
		}
	}
	return doc.Bytes()
}

func (d FlowDraft) applyStages(doc *tomledit.Doc) error {
	old := stageTables(doc)
	deleted := map[int]bool{}
	for _, i := range d.Deleted {
		if i < 0 || i >= len(old) {
			return draftErr("cannot delete stage %d: the flow has %d stages", i+1, len(old))
		}
		deleted[i] = true
	}
	seen := map[int]bool{}
	var next []*tomledit.Table
	ids := map[string]bool{}
	for _, sd := range d.Stages {
		var t *tomledit.Table
		if sd.Orig != nil {
			i := *sd.Orig
			switch {
			case i < 0 || i >= len(old):
				return draftErr("stage %d does not exist", i+1)
			case deleted[i]:
				return draftErr("stage %d is both kept and deleted", i+1)
			case seen[i]:
				return draftErr("stage %d is listed twice", i+1)
			}
			seen[i] = true
			t = old[i]
			if was := str(t, "id"); was != "" && sd.ID != was {
				return draftErr("stage %q: stage IDs cannot be renamed once saved (got %q)", was, sd.ID)
			}
		} else {
			t = &tomledit.Table{Name: "stage", Array: true}
		}
		if sd.ID != "" {
			if ids[sd.ID] {
				return draftErr("two stages have the ID %q", sd.ID)
			}
			ids[sd.ID] = true
		}
		applyStage(t, sd)
		next = append(next, t)
	}
	var missing []string
	for i := range old {
		if !seen[i] && !deleted[i] {
			missing = append(missing, strconv.Itoa(i+1))
		}
	}
	if len(missing) > 0 {
		return draftErr("the stages leave out stage %s; delete stages explicitly", strings.Join(missing, ", "))
	}

	// The stages take the slots the file's stages held, in order; more
	// stages than slots follow the last slot, and with no slots they go
	// before [output].
	slots := len(old)
	var tables []*tomledit.Table
	k, slot := 0, 0
	for _, t := range doc.Tables {
		if t.Name != "stage" {
			tables = append(tables, t)
			continue
		}
		slot++
		if k < len(next) {
			tables = append(tables, next[k])
			k++
		}
		if slot == slots && k < len(next) {
			tables = append(tables, next[k:]...)
			k = len(next)
		}
	}
	if k < len(next) {
		pos := len(tables)
		for i, t := range tables {
			if t.Name == "output" {
				pos = i
				break
			}
		}
		tables = append(tables[:pos], append(append([]*tomledit.Table{}, next[k:]...), tables[pos:]...)...)
	}
	doc.Tables = tables
	return nil
}

// applyStage writes a stage's keys. Keys the kind does not use are removed.
func applyStage(t *tomledit.Table, sd StageData) {
	setStr(t, "id", sd.ID)
	setStr(t, "kind", sd.Kind)
	call := sd.Kind == "call"
	if call {
		setStr(t, "interface", "")
		setCarries(t, nil)
		setStr(t, "producer", "")
		setStr(t, "caller", sd.Caller)
		setStr(t, "callee", sd.Callee)
		setList(t, "callee_computes", sd.CalleeComputes)
	} else {
		setStr(t, "interface", sd.Interface)
		setCarries(t, sd.Carries)
		setStr(t, "producer", sd.Producer)
		setStr(t, "caller", "")
		setStr(t, "callee", "")
		setList(t, "callee_computes", nil)
	}
	setList(t, "computes", sd.Computes)
	for _, part := range []string{"request", "response"} {
		b := sd.Request
		if part == "response" {
			b = sd.Response
		}
		if !call || b == nil {
			removeSubs(t, part, 0)
			continue
		}
		s := subAt(t, part, 0, false)
		setStr(s, "interface", b.Interface)
		setCarries(s, b.Carries)
	}
	// New sub-tables go in the layout's order: request, then response.
	sort.SliceStable(t.Subs, func(i, j int) bool { return subRank(t.Subs[i].Name) < subRank(t.Subs[j].Name) })
}

func subRank(name string) int {
	switch name {
	case "request":
		return 0
	case "response":
		return 1
	}
	return 2
}

// setCarries writes carries as an inline table, keeping the file's key
// order for keys it already has and adding new ones in name order; it
// removes carries when empty.
func setCarries(t *tomledit.Table, m map[string]string) {
	if len(m) == 0 {
		if k := t.Get("carries"); k != nil {
			t.Delete("carries")
		}
		return
	}
	in := tomledit.Inline{}
	done := map[string]bool{}
	if k := t.Get("carries"); k != nil {
		old, _ := k.Value.(tomledit.Inline)
		for _, x := range old {
			if v, ok := m[x.Name]; ok {
				in = append(in, &tomledit.Key{Name: x.Name, Value: v})
				done[x.Name] = true
			}
		}
	}
	var rest []string
	for f := range m {
		if !done[f] {
			rest = append(rest, f)
		}
	}
	sort.Strings(rest)
	for _, f := range rest {
		in = append(in, &tomledit.Key{Name: f, Value: m[f]})
	}
	set(t, "carries", in)
}
