package impact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flowrender"
)

// heading says what changed for a group of findings.
func heading(f Finding) string {
	iface := f.Interface
	if f.Owner != "" {
		iface += " (" + f.Owner + ")"
	}
	switch f.Status {
	case flow.Missing:
		return iface + " no longer carries " + f.Field
	case flow.Warn:
		return iface + " " + f.Field + " no longer fits"
	}
	return f.Flow + ": " + f.Message
}

// detail is one consuming flow's line under a heading.
func detail(f Finding) string {
	return fmt.Sprintf("%s, %s: %s", f.Flow, f.Where, strings.TrimPrefix(f.Message, "missing; "))
}

// group orders findings by what changed, keeping the first-seen order.
func group(fs []Finding) (heads []string, by map[string][]Finding) {
	by = map[string][]Finding{}
	for _, f := range fs {
		h := heading(f)
		if _, ok := by[h]; !ok {
			heads = append(heads, h)
		}
		by[h] = append(by[h], f)
	}
	return heads, by
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// WriteText writes the report for a terminal or a CI log: what the change
// breaks, grouped by what changed and then by consuming flow.
func (r *Report) WriteText(w io.Writer) error {
	var b strings.Builder
	heads, by := group(r.New)
	for _, h := range heads {
		b.WriteString(h + "\n")
		for _, f := range by[h] {
			if f.Status != StatusError {
				b.WriteString("  " + detail(f) + "\n")
			}
		}
	}
	if len(r.Resolved) > 0 {
		b.WriteString("\nResolved by this change\n")
		for _, f := range r.Resolved {
			if f.Status == StatusError {
				b.WriteString("  " + f.Flow + ": " + f.Message + "\n")
			} else {
				b.WriteString(fmt.Sprintf("  %s, %s: %s %s\n", f.Flow, f.Where, f.Interface, f.Field))
			}
		}
	}
	if len(r.Pinned) > 0 {
		b.WriteString("\nPinned, not affected yet\n")
		for _, p := range r.Pinned {
			b.WriteString(fmt.Sprintf("  %s pins %s to %s\n", p.Flow, r.Self, p.Ref))
		}
	}
	if len(r.Failing) > 0 {
		b.WriteString("\nAlready failing before this change (only errors compared)\n")
		for _, f := range r.Failing {
			b.WriteString("  " + f.Flow + ": " + f.Reason + "\n")
		}
	}
	if len(r.NotChecked) > 0 {
		b.WriteString("\nNot checked\n")
		for _, s := range r.NotChecked {
			name := s.Repo
			if s.Flow != "" {
				name = s.Flow
			}
			b.WriteString("  " + name + ": " + s.Reason + "\n")
		}
	}
	if len(heads) > 0 || b.Len() > 0 {
		b.WriteString("\n")
	}
	if len(r.Sources) > 0 {
		var revs []string
		for alias, rev := range r.Sources {
			revs = append(revs, alias+"@"+rev)
		}
		sort.Strings(revs)
		b.WriteString("sources: " + strings.Join(revs, ", ") + "\n")
	}
	checked := plural(r.Checked, "checked flow", "checked flows")
	if len(r.New) == 0 {
		b.WriteString("No new problems in " + checked + "\n")
	} else {
		b.WriteString(plural(len(r.New), "new problem", "new problems") + " in " + checked + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteJSON writes the report as JSON, with its exit code.
func (r *Report) WriteJSON(w io.Writer) error {
	out := *r
	for _, s := range []*[]Finding{&out.New, &out.Resolved} {
		if *s == nil {
			*s = []Finding{}
		}
	}
	if out.Pinned == nil {
		out.Pinned = []Pinned{}
	}
	if out.NotChecked == nil {
		out.NotChecked = []Skipped{}
	}
	if out.Failing == nil {
		out.Failing = []Skipped{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		*Report
		ExitCode int `json:"exit_code"`
	}{&out, r.ExitCode()})
}

type pageGroup struct {
	Repo  string
	Items []pageItem
}

type pageItem struct{ Heading, Detail string }

type pageView struct {
	Flow string
	HTML string
}

// WriteHTML writes one self-contained page: the new problems grouped by
// consuming repository, then each affected flow's checked view.
func (r *Report) WriteHTML(w io.Writer) error {
	byRepo := map[string][]pageItem{}
	var repos []string
	for _, f := range r.New {
		if _, ok := byRepo[f.Repo]; !ok {
			repos = append(repos, f.Repo)
		}
		d := ""
		if f.Status != StatusError {
			d = detail(f)
		}
		byRepo[f.Repo] = append(byRepo[f.Repo], pageItem{heading(f), d})
	}
	var groups []pageGroup
	for _, repo := range repos {
		groups = append(groups, pageGroup{repo, byRepo[repo]})
	}
	names := make([]string, 0, len(r.Views))
	for n := range r.Views {
		names = append(names, n)
	}
	sort.Strings(names)
	var views []pageView
	for _, n := range names {
		var buf bytes.Buffer
		if err := flowrender.Write(&buf, flowrender.Build(r.Views[n], time.Now(), true)); err != nil {
			return err
		}
		views = append(views, pageView{n, buf.String()})
	}
	var text bytes.Buffer
	if err := r.WriteText(&text); err != nil {
		return err
	}
	summary := strings.TrimSpace(text.String())
	if i := strings.LastIndex(summary, "\n"); i >= 0 {
		summary = summary[i+1:]
	}
	return page.Execute(w, map[string]any{
		"Self": r.Self, "Summary": summary, "Groups": groups, "Views": views,
		"Resolved": r.Resolved, "Pinned": r.Pinned, "NotChecked": r.NotChecked, "Failing": r.Failing,
		"Generated": time.Now().Format("2 Jan 2006 15:04"),
	})
}

var page = template.Must(template.New("impact").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Impact of {{.Self}}</title>
<style>
:root { --bg: #ffffff; --fg: #1c1d20; --muted: #5d6068; --line: #d9dbe0; --bad: #b42318; --panel: #f6f7f9; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #141518; --fg: #e8e9ec; --muted: #a0a3ab; --line: #34363c; --bad: #f97066; --panel: #1d1f23; }
}
body { margin: 0; background: var(--bg); color: var(--fg); font: 15px/1.5 system-ui, sans-serif; }
main { max-width: 1100px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 22px; margin: 0 0 4px; }
h2 { font-size: 17px; margin: 28px 0 8px; }
.muted { color: var(--muted); }
ul { padding-left: 20px; }
li { margin: 6px 0; }
.change { color: var(--bad); font-weight: 600; }
.detail { color: var(--muted); }
iframe { width: 100%; height: 640px; border: 1px solid var(--line); border-radius: 6px; background: #fff; }
section.repo { background: var(--panel); border: 1px solid var(--line); border-radius: 6px; padding: 4px 16px; margin: 12px 0; }
</style>
</head>
<body>
<main>
<h1>Impact of {{.Self}}</h1>
<p class="muted">{{.Summary}} · generated {{.Generated}}</p>
{{range .Groups}}
<section class="repo">
<h2>{{.Repo}}</h2>
<ul>{{range .Items}}<li><span class="change">{{.Heading}}</span>{{if .Detail}}<br><span class="detail">{{.Detail}}</span>{{end}}</li>{{end}}</ul>
</section>
{{end}}
{{if .Resolved}}<h2>Resolved by this change</h2>
<ul>{{range .Resolved}}<li>{{.Flow}}{{if .Field}}, {{.Where}}: {{.Interface}} {{.Field}}{{else}}: {{.Message}}{{end}}</li>{{end}}</ul>{{end}}
{{if .Pinned}}<h2>Pinned, not affected yet</h2>
<ul>{{range .Pinned}}<li>{{.Flow}} pins {{$.Self}} to {{.Ref}}</li>{{end}}</ul>{{end}}
{{if .Failing}}<h2>Already failing before this change</h2>
<p class="muted">Only errors are compared for these flows.</p>
<ul>{{range .Failing}}<li>{{.Flow}}: {{.Reason}}</li>{{end}}</ul>{{end}}
{{if .NotChecked}}<h2>Not checked</h2>
<ul>{{range .NotChecked}}<li>{{if .Flow}}{{.Flow}}{{else}}{{.Repo}}{{end}}: {{.Reason}}</li>{{end}}</ul>{{end}}
{{range .Views}}
<h2>{{.Flow}}, with this change</h2>
<iframe title="{{.Flow}}" srcdoc="{{.HTML}}"></iframe>
{{end}}
</main>
</body>
</html>
`))
