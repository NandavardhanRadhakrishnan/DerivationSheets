package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"derivationsheets/internal/cases"
	"derivationsheets/internal/eval"
	"derivationsheets/internal/evidence"
	"derivationsheets/internal/sheet"
)

func setup(t *testing.T) (*eval.Engine, *cases.File) {
	t.Helper()
	s, err := sheet.Load(filepath.Join("..", "..", "specs", "premium-calculation"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eval.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := time.Parse(time.RFC3339, cases.DefaultReferenceTime)
	gen := cases.Generate(e, ref)
	return e, &cases.File{Sheet: s.Slug, Version: s.Version, Cases: gen.Cases}
}

// perfect builds results.json content in which every case behaves as expected.
func perfect(cf *cases.File) map[string]any {
	out := map[string]any{}
	for _, c := range cf.Cases {
		if c.ExpectedReject != "" {
			out[c.ID] = map[string]any{"reject": c.ExpectedReject}
			continue
		}
		p := map[string]any{}
		for _, f := range c.Expected {
			p[f.Name] = f.Value
		}
		out[c.ID] = map[string]any{"payload": p}
	}
	return out
}

func write(t *testing.T, v any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.json")
	data, _ := json.Marshal(map[string]any{"cases": v})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAllMatch(t *testing.T) {
	e, cf := setup(t)
	results, err := ReadResults(write(t, perfect(cf)))
	if err != nil {
		t.Fatal(err)
	}
	rep := Verify(e, cf, results, "results.json", time.Now())
	if len(rep.Problems) > 0 || len(rep.Notes) > 0 {
		t.Fatalf("problems %v notes %v", rep.Problems, rep.Notes)
	}
	for _, r := range e.Sheet.Rows {
		st := evidence.RowStatus(rep.Run, r.ID, nil)
		if st.State != evidence.Verified {
			t.Errorf("row %s is %s", r.ID, st.State)
		}
	}
	if st := evidence.BranchStatus(rep.Run, "age:reject:AGE_LIMIT"); st.State != evidence.Verified {
		t.Errorf("age reject is %s", st.State)
	}
}

func TestOneValueChanged(t *testing.T) {
	e, cf := setup(t)
	res := perfect(cf)
	// Change riskClassCode in the first case that sends HIGH.
	var target string
	for _, c := range cf.Cases {
		for _, f := range c.Expected {
			if f.Name == "riskClassCode" && f.Value == "HIGH" && target == "" {
				target = c.ID
			}
		}
	}
	res[target].(map[string]any)["payload"].(map[string]any)["riskClassCode"] = "ELV"
	results, _ := ReadResults(write(t, res))
	rep := Verify(e, cf, results, "results.json", time.Now())
	t.Log(rep.Problems)
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], target) || !strings.Contains(rep.Problems[0], "riskClassCode") {
		t.Fatalf("problems = %v; want one naming case %q and field riskClassCode", rep.Problems, target)
	}
	if st := evidence.RowStatus(rep.Run, "risk_class", nil); st.State != evidence.Contradicted {
		t.Errorf("risk_class is %s", st.State)
	}
	if st := evidence.BranchStatus(rep.Run, "risk_class:[true,true]"); st.State != evidence.Contradicted {
		t.Errorf("cell [true,true] is %s", st.State)
	}
	if st := evidence.BranchStatus(rep.Run, "risk_class:[false,false]"); st.State != evidence.Verified {
		t.Errorf("cell [false,false] is %s", st.State)
	}
	// Rows upstream, and rows whose own outputs matched, stay verified.
	for _, id := range []string{"is_smoker", "has_ped", "loading", "net_premium", "medical_check"} {
		if st := evidence.RowStatus(rep.Run, id, nil); st.State != evidence.Verified {
			t.Errorf("%s is %s", id, st.State)
		}
	}
}

func TestSentWhenItShouldNotBe(t *testing.T) {
	e, cf := setup(t)
	res := perfect(cf)
	var target string
	for _, c := range cf.Cases {
		if c.ExpectedReject != "" {
			continue
		}
		has := false
		for _, f := range c.Expected {
			has = has || f.Name == "waitingPeriodMonths"
		}
		if !has {
			target = c.ID
			break
		}
	}
	res[target].(map[string]any)["payload"].(map[string]any)["waitingPeriodMonths"] = 24
	results, _ := ReadResults(write(t, res))
	rep := Verify(e, cf, results, "results.json", time.Now())
	if st := evidence.BranchStatus(rep.Run, "waiting_period:send.when:false"); st.State != evidence.Contradicted {
		t.Errorf("send.when:false is %s; problems %v", st.State, rep.Problems)
	}
}
