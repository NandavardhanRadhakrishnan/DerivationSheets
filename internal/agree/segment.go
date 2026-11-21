package agree

import (
	"fmt"
	"strings"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/flow"
)

// segment is the stretch of the PM's flow one team's own flow must cover.
type segment struct {
	Team    string
	In, Out int   // PM boundary indexes; for a callee: its request and response
	Calls   []int // request boundary indexes of calls made inside the segment
	Steps   []int // PM step indexes of the team's steps in the segment
	Callee  bool
}

// segments splits the PM's flow by team. Step k sits between boundaries k and
// k+1; a caller's step after a call joins the segment of the step before it.
func segments(pm *flow.Analysis) []segment {
	var out []segment
	segOf := map[int]int{}
	for k, st := range pm.Steps {
		if st.Role == flow.StepCallee {
			segOf[k] = len(out)
			out = append(out, segment{Team: st.Team(), In: k, Out: k + 1, Steps: []int{k}, Callee: true})
			continue
		}
		idx := len(out)
		if st.KeepsFrom >= 0 {
			idx = segOf[st.KeepsFrom]
		} else {
			out = append(out, segment{Team: st.Team(), In: k})
		}
		segOf[k] = idx
		sg := &out[idx]
		sg.Steps = append(sg.Steps, k)
		if pm.Boundaries[k+1].Role == flow.RoleRequest {
			sg.Calls = append(sg.Calls, k+1)
		} else {
			sg.Out = k + 1
		}
	}
	return out
}

// match is a team flow found for a segment.
type match struct {
	A       *flow.Analysis
	StageOf map[int]string // PM request boundary index -> the team flow's call stage id
}

func ifaceName(b *flow.Boundary) string {
	if b == nil || b.Iface == nil {
		return ""
	}
	return b.Iface.Name
}

// findFlow picks the one team flow whose first and last boundaries' interface
// names equal the segment's In and Out, and which has, for every call in the
// segment, a request boundary with the same interface name. note says why
// none was found, or names the candidates when several match; none says
// that no flow matched at all.
func findFlow(pm *flow.Analysis, sg segment, flows []*flow.Analysis) (m *match, note string, none bool) {
	in, out := ifaceName(pm.Boundaries[sg.In]), ifaceName(pm.Boundaries[sg.Out])
	var found []*match
	for _, a := range flows {
		if a == nil || len(a.Boundaries) == 0 {
			continue
		}
		if in == "" || out == "" || ifaceName(a.Boundaries[0]) != in || ifaceName(a.Boundaries[len(a.Boundaries)-1]) != out {
			continue
		}
		cand := &match{A: a, StageOf: map[int]string{}}
		used := map[string]bool{}
		ok := true
		for _, c := range sg.Calls {
			want, hit := ifaceName(pm.Boundaries[c]), false
			for _, b := range a.Boundaries {
				if b.Role == flow.RoleRequest && !used[b.StageID] && want != "" && ifaceName(b) == want {
					used[b.StageID], cand.StageOf[c], hit = true, b.StageID, true
					break
				}
			}
			if !hit {
				ok = false
				break
			}
		}
		if ok {
			found = append(found, cand)
		}
	}
	switch len(found) {
	case 1:
		return found[0], "", false
	case 0:
		note = fmt.Sprintf("no flow of %s takes %s in and gives %s out", sg.Team, in, out)
		if len(sg.Calls) > 0 {
			var names []string
			for _, c := range sg.Calls {
				names = append(names, ifaceName(pm.Boundaries[c]))
			}
			note += ", with a call request for " + strings.Join(names, ", ")
		}
		return nil, note, true
	}
	dirs := make([]string, len(found))
	for i, f := range found {
		dirs[i] = f.A.Flow.Dir
	}
	return nil, fmt.Sprintf("several flows of %s take %s in and give %s out: %s", sg.Team, in, out, strings.Join(dirs, ", ")), false
}
