package cases

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRegenerateIsIdempotent(t *testing.T) {
	e := fixture(t)
	path := filepath.Join("..", "..", "specs", "order-checkout", FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := ParseFile(data)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := RefTime(old)
	if err != nil {
		t.Fatal(err)
	}
	got, sum := Regenerate(e, old, ref)
	want := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("regenerated cases.toml differs from the committed one:\n%s", got)
	}
	if sum.Cases != len(old.Cases) || sum.Targets == 0 || sum.Covered > sum.Targets {
		t.Errorf("summary %+v", sum)
	}
}

func TestSummaryLines(t *testing.T) {
	s := Summary{Cases: 7, Covered: 30, Targets: 31, Evaluated: 120,
		Uncovered: []Target{{Key: "zone:[>= 2]", Kind: "rule"}},
		FillIn:    []string{`  case "a": fill in expected x by hand (depends on a text rule)`}}
	want := []string{
		"wrote x/cases.toml: 7 cases covering 30 of 31 targets (120 inputs evaluated)",
		"  not covered: zone:[>= 2] (rule)",
		`  case "a": fill in expected x by hand (depends on a text rule)`,
	}
	if got := s.Lines("x/cases.toml"); !slices.Equal(got, want) {
		t.Errorf("Lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	s.Kept = 2
	if got := s.Lines("x/cases.toml"); got[0] != "kept 2 hand-written expected values" || !strings.HasPrefix(got[1], "wrote ") {
		t.Errorf("with kept values, Lines starts %q", got[:2])
	}
}

func TestRefTime(t *testing.T) {
	def, _ := time.Parse(time.RFC3339, DefaultReferenceTime)
	for _, old := range []*File{nil, {}} {
		got, err := RefTime(old)
		if err != nil || !got.Equal(def) {
			t.Errorf("RefTime(%v) = %v, %v; want the default", old, got, err)
		}
	}
	got, err := RefTime(&File{ReferenceTime: "2026-01-01T00:00:00Z"})
	if err != nil || got.Format(time.RFC3339) != "2026-01-01T00:00:00Z" {
		t.Errorf("RefTime = %v, %v", got, err)
	}
	if _, err := RefTime(&File{ReferenceTime: "yesterday"}); err == nil {
		t.Error("RefTime accepted a malformed reference time")
	}
}
