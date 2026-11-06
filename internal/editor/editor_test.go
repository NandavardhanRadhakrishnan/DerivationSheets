package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/store"
)

const sheetPath = "specs/order-checkout/sheet.toml"
const casesPath = "specs/order-checkout/cases.toml"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if e.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// workspace copies specs/order-checkout into a folder; with commit, a git
// repository holding it in one commit.
func workspace(t *testing.T, commit bool) (string, *Editor) {
	t.Helper()
	dir := t.TempDir()
	copyDir(t, filepath.Join("..", "..", "specs", "order-checkout"), filepath.Join(dir, "specs", "order-checkout"))
	copyDir(t, filepath.Join("..", "..", "schema"), filepath.Join(dir, "schema"))
	if commit {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		git(t, dir, "init", "-q")
		git(t, dir, "config", "core.autocrlf", "false")
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-q", "-m", "one")
	}
	st, err := store.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, &Editor{Store: st, Root: dir}
}

func load(t *testing.T, ed *Editor, path string) store.File {
	t.Helper()
	f, err := ed.Store.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func onDisk(t *testing.T, dir, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// formulaDraft changes discount's formula.
func formulaDraft(t *testing.T, ed *Editor, formula string) SheetDraft {
	t.Helper()
	f := load(t, ed, sheetPath)
	r := rowOf(t, f.Data, "discount")
	r.Formula = formula
	return SheetDraft{File: sheetPath, Token: f.Token, Rows: map[string]RowData{"discount": r}}
}

func TestSaveSheetChangesOnlyEdit(t *testing.T) {
	dir, ed := workspace(t, true)
	casesBefore := onDisk(t, dir, casesPath)
	res, err := ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saved || res.Version != 2 || !res.VersionRaised || len(res.Cases) == 0 {
		t.Fatalf("result %+v", res)
	}
	diff := git(t, dir, "diff", "-U0", "--", sheetPath)
	var changed []string
	for _, l := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && !strings.HasPrefix(l, "+++") && !strings.HasPrefix(l, "---") {
			changed = append(changed, l)
		}
	}
	want := []string{"-version = 1", "+version = 2", `-formula = "round(subtotal * discount_rate)"`, `+formula = "round(subtotal * discount_rate * 2)"`}
	if !slices.Equal(changed, want) {
		t.Errorf("diff:\n%s", strings.Join(changed, "\n"))
	}
	if after := onDisk(t, dir, casesPath); after == casesBefore || !strings.Contains(after, "version        = 2") {
		t.Errorf("cases.toml not regenerated:\n%s", after)
	}
	if res.Tokens[sheetPath] != store.Token([]byte(onDisk(t, dir, sheetPath))) || res.Tokens[casesPath] == "" {
		t.Errorf("tokens %v", res.Tokens)
	}
}

func TestSaveSheetConflict(t *testing.T) {
	dir, ed := workspace(t, true)
	d := formulaDraft(t, ed, "round(subtotal * discount_rate * 2)")
	changed := onDisk(t, dir, sheetPath) + "\n# edited elsewhere\n"
	os.WriteFile(filepath.Join(dir, filepath.FromSlash(sheetPath)), []byte(changed), 0o644)
	res, err := ed.SaveSheet(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved || !slices.Equal(res.Conflict, []string{sheetPath}) {
		t.Fatalf("result %+v", res)
	}
	if onDisk(t, dir, sheetPath) != changed {
		t.Error("file overwritten")
	}
}

func TestSaveSheetRefusesNewError(t *testing.T) {
	dir, ed := workspace(t, true)
	before := onDisk(t, dir, sheetPath)
	res, err := ed.SaveSheet(formulaDraft(t, ed, "subtotal * coupon_rate"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved {
		t.Fatal("saved a draft with a new error")
	}
	found := false
	for _, p := range res.Problems {
		if p.New && p.Code == "ref-undefined" && p.Row == "discount" {
			found = true
		}
	}
	if !found {
		t.Errorf("problems %+v", res.Problems)
	}
	if onDisk(t, dir, sheetPath) != before {
		t.Error("file written")
	}
}

func TestSaveSheetKeepsOldErrors(t *testing.T) {
	dir, ed := workspace(t, false)
	csv := filepath.Join(dir, "specs", "order-checkout", "data", "shipping_zones.csv")
	data, _ := os.ReadFile(csv)
	os.WriteFile(csv, append(data, []byte("ZZ,NOWHERE,EXTRA\n")...), 0o644)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "core.autocrlf", "false")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "one")

	res, err := ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saved {
		t.Fatalf("not saved: %+v", res)
	}
	old := false
	for _, p := range res.Problems {
		if p.New {
			t.Errorf("new problem %+v", p)
		}
		if p.Severity == "error" && p.Row == "zone" {
			old = true
		}
	}
	if !old {
		t.Errorf("the existing error is not reported: %+v", res.Problems)
	}
}

func TestSaveSheetVersionOncePerCommit(t *testing.T) {
	dir, ed := workspace(t, true)
	res, err := ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil || res.Version != 2 || !res.VersionRaised {
		t.Fatalf("first save: %+v, %v", res, err)
	}
	res, err = ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 3)"))
	if err != nil || !res.Saved || res.Version != 2 || res.VersionRaised {
		t.Fatalf("second save: %+v, %v", res, err)
	}
	git(t, dir, "commit", "-q", "-am", "two")
	res, err = ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 4)"))
	if err != nil || res.Version != 3 || !res.VersionRaised {
		t.Fatalf("after commit: %+v, %v", res, err)
	}
}

func TestSaveSheetUncommitted(t *testing.T) {
	dir, ed := workspace(t, false)
	res, err := ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil || !res.Saved || res.Version != 1 || res.VersionRaised {
		t.Fatalf("%+v, %v", res, err)
	}
	if !strings.Contains(onDisk(t, dir, sheetPath), "version = 1") {
		t.Error("version changed")
	}
}

func TestSaveSheetCasesKeepReferenceTime(t *testing.T) {
	dir, ed := workspace(t, true)
	// A text row makes an expected value "?", which is filled in by hand.
	f := load(t, ed, sheetPath)
	note := RowData{ID: "gift_note", Name: "Gift note", Uses: []string{"subtotal"}, Text: "Written by the gift team.", Sends: []SendData{{Field: "giftNote", Usage: "R"}}}
	res, err := ed.SaveSheet(SheetDraft{File: sheetPath, Token: f.Token, Rows: map[string]RowData{"gift_note": note}})
	if err != nil || !res.Saved {
		t.Fatalf("%+v, %v", res, err)
	}
	cases := onDisk(t, dir, casesPath)
	if !strings.Contains(cases, `giftNote = "?"`) {
		t.Fatalf("no \"?\" for the text row:\n%s", cases)
	}
	cases = strings.Replace(cases, `reference_time = "2026-11-02T10:00:00+05:30"`, `reference_time = "2026-01-01T00:00:00Z"`, 1)
	cases = strings.Replace(cases, `giftNote = "?"`, `giftNote = "Happy birthday"`, 1)
	os.WriteFile(filepath.Join(dir, filepath.FromSlash(casesPath)), []byte(cases), 0o644)

	res, err = ed.SaveSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil || !res.Saved {
		t.Fatalf("%+v, %v", res, err)
	}
	after := onDisk(t, dir, casesPath)
	if !strings.Contains(after, `reference_time = "2026-01-01T00:00:00Z"`) || !strings.Contains(after, `giftNote = "Happy birthday"`) {
		t.Errorf("reference time or hand-written value lost:\n%s", after)
	}
}

func TestCheckSheetWritesNothing(t *testing.T) {
	dir, ed := workspace(t, true)
	sheet, cases := onDisk(t, dir, sheetPath), onDisk(t, dir, casesPath)
	res, err := ed.CheckSheet(formulaDraft(t, ed, "round(subtotal * discount_rate * 2)"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Saved || res.Version != 2 || !res.VersionRaised {
		t.Errorf("result %+v", res)
	}
	for _, p := range res.Problems {
		if p.New {
			t.Errorf("new problem %+v", p)
		}
	}
	if onDisk(t, dir, sheetPath) != sheet || onDisk(t, dir, casesPath) != cases {
		t.Error("files written")
	}
}

func TestSaveSheetNoChange(t *testing.T) {
	dir, ed := workspace(t, true)
	before := onDisk(t, dir, sheetPath)
	f := load(t, ed, sheetPath)
	res, err := ed.SaveSheet(SheetDraft{File: sheetPath, Token: f.Token})
	if err != nil || !res.Unchanged || res.VersionRaised {
		t.Fatalf("%+v, %v", res, err)
	}
	if onDisk(t, dir, sheetPath) != before {
		t.Error("file written")
	}
}

func TestSheetRefs(t *testing.T) {
	_, ed := workspace(t, false)
	refs, err := ed.SheetRefs(formulaDraft(t, ed, "subtotal * discount_rate + coupon_rate"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(refs.Uses["discount"], []string{"subtotal", "discount_rate"}) || !slices.Equal(refs.Unknown["discount"], []string{"coupon_rate"}) {
		t.Errorf("uses %v, unknown %v", refs.Uses, refs.Unknown)
	}
	if c := refs.Cycle["discount"]; c[0] != "discount" || !slices.Contains(c, "total") || slices.Contains(c, "subtotal") {
		t.Errorf("cycle for discount = %v", c)
	}
}
