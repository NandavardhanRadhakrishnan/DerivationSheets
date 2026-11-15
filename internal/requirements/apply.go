package requirements

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/tomledit"
)

// ApplyOptions say where Apply works and what it names.
type ApplyOptions struct {
	// Registry gives each owner repository's clone URL, the branch the
	// registry follows (the default branch when empty) and its dsheets folder.
	Registry map[string]sources.Source
	// Clones are existing clones to use, by alias. The others are cloned
	// (sparsely, only the dsheets folder) into Work/<alias>, or reused there
	// when they already exist.
	Clones map[string]string
	Work   string
	// Branches names the new branch of each repository; the default is
	// dsheets/<flow>.
	Branches map[string]string
	// Change labels the change in commits and the record file; the flow's
	// name when empty.
	Change string
	// NoPush stops after the local commit.
	NoPush bool
}

// Applied is one repository's new branch.
type Applied struct {
	Repo, Branch, Dir string
	IDs               []string // the requirements the commit satisfies
	Pushed            bool
	Description       string // a pull request description to paste
}

// job is one repository's work, prepared before anything is changed.
type job struct {
	repo, dir, branch, base string
	src                     sources.Source
	reqs                    []Requirement     // Schema requirements for this repository
	content                 map[string][]byte // new content by file
	done                    []Requirement     // the requirements whose edit changed a file
	imported                map[string]string
}

// Apply writes the interface edits of the Schema requirements into their
// owner repositories: each gets a new branch cut from the tip of the branch
// the registry follows, one commit holding the edits and a record file, and
// the branch is pushed unless o.NoPush. It never touches an existing branch
// and never forces a push. Every repository is checked before any is
// changed. A repository whose interfaces already declare everything is left
// alone and left out of the result.
func Apply(r *Result, o ApplyOptions) ([]Applied, error) {
	byRepo := map[string][]Requirement{}
	for _, q := range r.Requirements {
		if q.Kind == Schema && q.Edit != nil && q.Edit.Op == "add" && q.Edit.Type == "" {
			// The sheet cannot say what type the field has, and a guess would
			// be written into another team's contract.
			return nil, fmt.Errorf("the type of `%s` (%s) is unknown, so nothing was applied; add the field to %s by hand, or give the row a type in the sheet", q.Edit.Field, q.ID, q.File)
		}
		if q.Kind == Schema && q.Edit != nil {
			byRepo[q.Repo] = append(byRepo[q.Repo], q)
		}
	}
	var repos []string
	for repo := range byRepo {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	change := o.Change
	if change == "" {
		change = r.Flow
	}

	var jobs []*job
	for _, repo := range repos {
		j := &job{repo: repo, reqs: byRepo[repo], src: o.Registry[repo], branch: "dsheets/" + r.Flow}
		if b := o.Branches[repo]; b != "" {
			j.branch = b
		}
		if err := prepare(j, o); err != nil {
			return nil, fmt.Errorf("%s: %w", repo, err)
		}
		if len(j.done) > 0 {
			jobs = append(jobs, j)
		}
	}

	var out []Applied
	for _, j := range jobs {
		a, err := commitJob(j, r, change, o)
		if err != nil {
			return out, fmt.Errorf("%s: %w", j.repo, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// prepare gets a clone, checks the branch name is free, and works out the new
// content of each interface file from the branch the registry follows.
func prepare(j *job, o ApplyOptions) error {
	if dir := o.Clones[j.repo]; dir != "" {
		j.dir = dir
	} else {
		if j.src.Git == "" {
			return fmt.Errorf("is not in the registry; name a clone of it with --apply %s=<path>", j.repo)
		}
		j.dir = filepath.Join(o.Work, j.repo)
	}
	ref := j.src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := os.Stat(filepath.Join(j.dir, ".git")); err != nil {
		if j.src.Git == "" {
			return fmt.Errorf("%s is not a git clone", j.dir)
		}
		if err := cloneSparse(j.src, j.dir); err != nil {
			return err
		}
		j.base = "HEAD"
	} else {
		if out, err := gitRun(j.dir, "status", "--porcelain"); err != nil {
			return err
		} else if strings.TrimSpace(out) != "" {
			return fmt.Errorf("%s has uncommitted changes; commit or stash them first", j.dir)
		}
		if _, err := gitRun(j.dir, "fetch", "--quiet", "origin", ref); err != nil {
			return err
		}
		j.base = "FETCH_HEAD"
	}
	if _, err := gitRun(j.dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+j.branch); err == nil {
		return fmt.Errorf("branch %s already exists in %s; choose another with --branch %s=<name>", j.branch, j.dir, j.repo)
	}
	if !o.NoPush {
		out, err := gitRun(j.dir, "ls-remote", "--heads", "origin", "refs/heads/"+j.branch)
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("branch %s already exists on the remote; choose another with --branch %s=<name>", j.branch, j.repo)
		}
	}

	j.content, j.imported = map[string][]byte{}, map[string]string{}
	files := map[string][]Requirement{}
	var order []string
	for _, q := range j.reqs {
		if _, ok := files[q.File]; !ok {
			order = append(order, q.File)
		}
		files[q.File] = append(files[q.File], q)
	}
	for _, file := range order {
		data, err := gitRun(j.dir, "show", j.base+":"+file)
		if err != nil {
			return fmt.Errorf("reading %s: %w", file, err)
		}
		edited, changed, imported, err := editInterface([]byte(data), files[file])
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if len(changed) == 0 {
			continue
		}
		j.content[file] = edited
		j.imported[file] = imported
		j.done = append(j.done, changed...)
	}
	return nil
}

// cloneSparse makes a shallow clone of the source's branch with only its
// dsheets folder checked out, so a service's code is never downloaded.
func cloneSparse(src sources.Source, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"clone", "--quiet", "--depth", "1", "--filter=blob:none", "--sparse"}
	if src.Ref != "" {
		args = append(args, "--branch", src.Ref)
	}
	if _, err := gitRun("", append(args, src.Git, dir)...); err != nil {
		return err
	}
	if d := sources.CleanDir(src.Dir); d != "" {
		if _, err := gitRun(dir, "sparse-checkout", "set", "--cone", d); err != nil {
			return err
		}
	}
	return nil
}

// editInterface applies the edits to an interface file. changed lists the
// requirements whose edit altered the file; a field the file already has as
// asked changes nothing. imported is the file's `imported` key.
func editInterface(data []byte, qs []Requirement) (out []byte, changed []Requirement, imported string, err error) {
	doc, err := tomledit.Parse(data, tomledit.Interface)
	if err != nil {
		return nil, nil, "", fmt.Errorf("does not parse; fix it by hand first: %w", err)
	}
	if k := doc.Get("imported"); k != nil {
		imported, _ = k.Value.(string)
	}
	for _, q := range qs {
		e := q.Edit
		t := doc.Find("field", "name", e.Field)
		switch e.Op {
		case "add":
			if t != nil {
				continue
			}
			t = &tomledit.Table{Name: "field", Array: true}
			t.Set("name", e.Field)
			t.Set("type", e.Type)
			t.Set("required", e.Required)
			if imported != "" {
				t.Set("local", true)
			}
			doc.Insert(len(doc.Tables), t)
		case "require", "type":
			if t == nil {
				return nil, nil, "", fmt.Errorf("has no field %s", e.Field)
			}
			key, want := "required", any(true)
			if e.Op == "type" {
				key, want = "type", any(e.Type)
			}
			if k := t.Get(key); k != nil && k.Value == want {
				continue
			}
			t.Set(key, want)
		default:
			return nil, nil, "", fmt.Errorf("unknown edit %q", e.Op)
		}
		changed = append(changed, q)
	}
	if len(changed) == 0 {
		return data, nil, imported, nil
	}
	out, err = doc.Bytes()
	return out, changed, imported, err
}

func commitJob(j *job, r *Result, change string, o ApplyOptions) (Applied, error) {
	if _, err := gitRun(j.dir, "switch", "--quiet", "-c", j.branch, j.base); err != nil {
		return Applied{}, err
	}
	var add []string
	for file, data := range j.content {
		p := filepath.Join(j.dir, filepath.FromSlash(file))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return Applied{}, err
		}
		add = append(add, file)
	}
	sort.Strings(add)
	record := path.Join(sources.CleanDir(j.src.Dir), "changes", slug(change)+".md")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(j.dir, filepath.FromSlash(record))), 0o755); err != nil {
		return Applied{}, err
	}
	var ids, lines []string
	for _, q := range j.done {
		ids = append(ids, q.ID)
		lines = append(lines, fmt.Sprintf("- %s  %s", q.ID, q.What))
	}
	body := fmt.Sprintf("# %s\n\n- Flow: %s\n- Compared with: %s\n- Requirements this commit satisfies:\n\n%s\n", change, r.Flow, r.Base, strings.Join(lines, "\n"))
	if err := os.WriteFile(filepath.Join(j.dir, filepath.FromSlash(record)), []byte(body), 0o644); err != nil {
		return Applied{}, err
	}
	if _, err := gitRun(j.dir, append([]string{"add", "--"}, append(add, record)...)...); err != nil {
		return Applied{}, err
	}
	var trailers []string
	for _, id := range ids {
		trailers = append(trailers, "Dsheets-Requirement: "+id)
	}
	trailers = append(trailers, "Dsheets-Change: "+change)
	subject := fmt.Sprintf("dsheets: %s: interface changes", change)
	if _, err := gitRun(j.dir, "commit", "--quiet", "-m", subject, "-m", strings.Join(lines, "\n"), "-m", strings.Join(trailers, "\n")); err != nil {
		return Applied{}, err
	}
	a := Applied{Repo: j.repo, Branch: j.branch, Dir: j.dir, IDs: ids, Description: description(j, r, change)}
	if !o.NoPush {
		if _, err := gitRun(j.dir, "push", "--quiet", "--set-upstream", "origin", j.branch); err != nil {
			return a, fmt.Errorf("the branch %s is committed in %s but the push failed (use --no-push, or a fork): %w", j.branch, j.dir, err)
		}
		a.Pushed = true
	}
	return a, nil
}

// description is the text for the owner's pull request: the interface
// changes the commit makes, and what else the owning team still has to do.
func description(j *job, r *Result, change string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\nInterface changes for flow `%s` (compared with `%s`), made by `dsheets requirements --apply`:\n\n", change, r.Flow, r.Base)
	teams := map[string]bool{}
	for _, q := range j.done {
		fmt.Fprintf(&b, "- %s  %s\n", q.ID, q.What)
		teams[q.Team] = true
		if j.imported[q.File] != "" {
			fmt.Fprintf(&b, "  (%s is imported from %s: make the same change there, or importing again undoes this)\n", path.Base(q.File), j.imported[q.File])
		}
	}
	var rest []string
	for _, q := range r.Requirements {
		if q.Kind != Schema && teams[q.Team] {
			rest = append(rest, fmt.Sprintf("- %s  %-7s  %s", q.ID, q.Kind, q.What))
		}
	}
	if len(rest) > 0 {
		b.WriteString("\nStill to do in this pull request:\n\n" + strings.Join(rest, "\n") + "\n")
	}
	return b.String()
}

func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
