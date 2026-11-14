// Package requirements turns a change to a flow into the list of things each
// team's code has to do, in a logical order. It compares the flow's analysis
// at a base revision with the analysis at head.
package requirements

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
)

// Kind says what sort of work a requirement is.
type Kind string

const (
	Schema  Kind = "Schema"  // the interface's owner adds or changes a field
	Compute Kind = "Compute" // a team computes a row, or stops computing it
	Carry   Kind = "Carry"   // a team holds a value it does not compute
	Send    Kind = "Send"    // the producer of a boundary fills a field
	Receive Kind = "Receive" // the consumer of a boundary reads a field
)

// rank is the order requirements are listed in.
var rank = map[Kind]int{Schema: 0, Compute: 1, Carry: 2, Send: 3, Receive: 4}

// Requirement is one atomic thing a team has to do.
type Requirement struct {
	ID      string   `json:"id"`
	Kind    Kind     `json:"kind"`
	Team    string   `json:"team"`
	Repo    string   `json:"repo,omitempty"`
	Flow    string   `json:"flow"`
	Subject string   `json:"subject"`
	What    string   `json:"what"`
	Before  string   `json:"before,omitempty"`
	After   string   `json:"after,omitempty"`
	Why     string   `json:"why,omitempty"`
	Placed  string   `json:"placed,omitempty"` // "default": no stage names the row, so analysis placed it
	Needs   []string `json:"needs,omitempty"`
	// File is the interface file, relative to its repository's top folder.
	// Set on Schema requirements only.
	File string `json:"file,omitempty"`
	// Edit is what --apply writes into File. Set on Schema requirements only.
	Edit *SchemaEdit `json:"edit,omitempty"`
}

// SchemaEdit is one change to an interface file. Op is "add" (a new field),
// "require" (make an existing field required) or "type" (change its type).
type SchemaEdit struct {
	Op       string `json:"op"`
	Field    string `json:"field"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// MakeID derives a stable id from what the requirement is, so reruns and
// trackers line up.
func MakeID(kind Kind, team, flow, subject string) string {
	sum := sha1.Sum([]byte(string(kind) + "|" + team + "|" + flow + "|" + subject))
	return "R-" + hex.EncodeToString(sum[:])[:6]
}

// Sort orders requirements: Schema, Compute, Carry, Send, Receive, then by
// team and subject.
func Sort(rs []Requirement) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.Team != b.Team {
			return a.Team < b.Team
		}
		return a.Subject < b.Subject
	})
}
