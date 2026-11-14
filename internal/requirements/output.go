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

// byTeam groups the requirements by team, teams in name order and each
// team's list in the order they were given.
func (r *Result) byTeam() (teams []string, groups map[string][]Requirement) {
	groups = map[string][]Requirement{}
	for _, q := range r.Requirements {
		if _, ok := groups[q.Team]; !ok {
			teams = append(teams, q.Team)
		}
		groups[q.Team] = append(groups[q.Team], q)
	}
	sort.Strings(teams)
	return teams, groups
}

// line is one requirement as a line of the markdown listing.
func line(q Requirement) string {
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
	return fmt.Sprintf(" %s  %-7s  %s\n", q.ID, q.Kind, text)
}

func writeTeam(w io.Writer, r *Result, team string, qs []Requirement) error {
	if _, err := fmt.Fprintf(w, "%s: %s (base %s)\n", team, r.Flow, r.Base); err != nil {
		return err
	}
	for _, q := range qs {
		if _, err := io.WriteString(w, line(q)); err != nil {
			return err
		}
	}
	return nil
}

// WriteMarkdown prints the requirements per team. A change with none prints
// nothing.
func (r *Result) WriteMarkdown(w io.Writer) error {
	teams, groups := r.byTeam()
	for i, team := range teams {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if err := writeTeam(w, r, team, groups[team]); err != nil {
			return err
		}
	}
	return nil
}

// WriteJSON prints the whole result as JSON.
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

// WriteFolder writes one markdown and one JSON file per team into dir.
func (r *Result) WriteFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	teams, groups := r.byTeam()
	for _, team := range teams {
		part := *r
		part.Requirements = groups[team]
		var md, js bytes.Buffer
		if err := writeTeam(&md, r, team, groups[team]); err != nil {
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
