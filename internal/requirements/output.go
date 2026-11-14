package requirements

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// groupByTeam groups requirements by team, teams in name order and each
// team's list in the order given.
func groupByTeam(qs []Requirement) (teams []string, groups map[string][]Requirement) {
	groups = map[string][]Requirement{}
	for _, q := range qs {
		if _, ok := groups[q.Team]; !ok {
			teams = append(teams, q.Team)
		}
		groups[q.Team] = append(groups[q.Team], q)
	}
	sort.Strings(teams)
	return teams, groups
}

// line is one requirement as the markdown listing shows it: a line, then for
// a Compute the rule to implement.
func (r *Result) line(q Requirement) string {
	text := q.What
	if q.Kind == Compute && q.Before != "" && q.After != "" {
		text = q.Subject + ": " + q.Before + " -> " + q.After
	}
	if q.Placed == "default" {
		text += "   (placed by default: no stage names it)"
	}
	if len(q.Needs) > 0 {
		text += "   needs " + strings.Join(q.Needs, ", ")
	}
	switch {
	case q.Status == StatusDone:
		text += "   done"
	case q.Status == StatusUndocumented && q.Note != "":
		text += "   " + q.Note
	case q.Note != "":
		text += "   open: " + q.Note
	}
	out := fmt.Sprintf(" %s  %-7s  %s\n", q.ID, q.Kind, text)
	if q.Rule != "" {
		for _, l := range strings.Split(q.Rule, "\n") {
			out += strings.Repeat(" ", 12) + l + "\n"
		}
	}
	return out
}

func (r *Result) writeTeam(w io.Writer, team string, qs []Requirement) error {
	if _, err := fmt.Fprintf(w, "%s: %s (base %s)\n", team, r.Flow, r.Base); err != nil {
		return err
	}
	for _, q := range qs {
		if _, err := io.WriteString(w, r.line(q)); err != nil {
			return err
		}
	}
	return nil
}

// listed are the requirements the listing shows: everything except done ones,
// unless ShowDone.
func (r *Result) listed(qs []Requirement) []Requirement {
	var out []Requirement
	for _, q := range qs {
		if q.Status != StatusDone || r.ShowDone {
			out = append(out, q)
		}
	}
	return out
}

// render writes the listing of qs: teams with open items, then the items
// dsheets cannot tell under their own heading, then how many are done. all is
// what the count is taken over. Nothing is written when nothing is left.
func (r *Result) render(w io.Writer, qs, all []Requirement) error {
	var open, undocumented []Requirement
	for _, q := range r.listed(qs) {
		if q.Status == StatusUndocumented {
			undocumented = append(undocumented, q)
		} else {
			open = append(open, q)
		}
	}
	wrote := false
	blocks := func(list []Requirement) error {
		teams, groups := groupByTeam(list)
		for i, team := range teams {
			if i > 0 {
				if _, err := io.WriteString(w, "\n"); err != nil {
					return err
				}
			}
			if err := r.writeTeam(w, team, groups[team]); err != nil {
				return err
			}
			wrote = true
		}
		return nil
	}
	if err := blocks(open); err != nil {
		return err
	}
	if len(undocumented) > 0 {
		if wrote {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "Not documented (dsheets cannot tell):\n"); err != nil {
			return err
		}
		if err := blocks(undocumented); err != nil {
			return err
		}
	}
	if done, total := (&Result{Requirements: all}).Counts(); wrote && done > 0 {
		_, err := fmt.Fprintf(w, "\n%d of %d done\n", done, total)
		return err
	}
	return nil
}

// WriteMarkdown prints what is left per team. A change with nothing left
// prints nothing.
func (r *Result) WriteMarkdown(w io.Writer) error {
	return r.render(w, r.Requirements, r.Requirements)
}

// WriteJSON prints the whole result as JSON, done items included, each with
// its status.
func (r *Result) WriteJSON(w io.Writer) error {
	out := *r
	if out.Requirements == nil {
		out.Requirements = []Requirement{}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "team"
	}
	return s
}

// WriteFolder writes one markdown and one JSON file per team into dir. The
// markdown follows the listing; the JSON holds all of the team's items.
func (r *Result) WriteFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	teams, groups := groupByTeam(r.Requirements)
	for _, team := range teams {
		part := *r
		part.Requirements = groups[team]
		var md, js bytes.Buffer
		if err := r.render(&md, groups[team], groups[team]); err != nil {
			return err
		}
		if err := part.WriteJSON(&js); err != nil {
			return err
		}
		name := filepath.Join(dir, slug(team))
		if err := os.WriteFile(name+".md", md.Bytes(), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(name+".json", js.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}
