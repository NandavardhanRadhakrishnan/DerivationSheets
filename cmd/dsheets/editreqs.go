package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/requirements"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/validate"
)

// reqState is the PM's change that serve --flow opened, for the editor's
// Requirements view. Requests are served one at a time: mu guards the
// prepared change, which Decide reads while a refresh would replace it.
type reqState struct {
	mu    sync.Mutex
	ref   string
	top   string
	fe    *sources.Fetcher
	prep  *requirements.Prepared
	err   error // why the change could not be opened
	teams []string
	why   string
	// refreshErr is why the last Fetch again failed while an earlier read
	// of the change is kept; nil once a read succeeds.
	refreshErr error
}

// openRequirements opens the PM's change ref for the repository being
// edited. A failure is kept for the endpoint to report, not returned: the
// editor serves without the view.
func (es *editServer) openRequirements(ref string, fe *sources.Fetcher) {
	es.reqs = &reqState{ref: ref, top: es.ed.Root, fe: fe}
	if es.msg != nil {
		fmt.Fprintf(es.msg, "reading %s...\n", ref)
	}
	es.reqs.mu.Lock()
	defer es.reqs.mu.Unlock()
	es.reqs.open(false)
}

// open opens the change again, fetching the branch afresh when refresh is
// set; the caller holds mu. When it fails after
// an earlier read succeeded, that read is kept and the failure is kept in
// refreshErr: a failed Fetch again does not drop the change.
func (rs *reqState) open(refresh bool) {
	fe := rs.fe
	if refresh {
		fe = fe.Refetching()
	}
	prep, err := requirements.OpenChange(rs.top, rs.ref, fe)
	if err != nil {
		if rs.prep != nil {
			rs.refreshErr = err
		} else {
			rs.err = err
		}
		return
	}
	rs.prep, rs.err, rs.refreshErr = prep, nil, nil
	rs.teams, rs.why = prep.TeamsOf(rs.top)
}

// reqRequest is the body of POST /edit/requirements.
type reqRequest struct {
	Draft   editor.SheetDraft `json:"draft"`
	Refresh bool              `json:"refresh"`
}

// requirementsEndpoint reruns the PM's cases against the unsaved draft.
// It answers {"view"}, {"stale"}, or {"error"}, all with status 200; a
// draft that cannot be evaluated or is out of date never gets a view, nor
// does one the cases did not run on (D-22: never done on a guess). While a
// failed Fetch again keeps the change last read, every answer also carries
// {"refresh_error"}.
func (es *editServer) requirementsEndpoint(w http.ResponseWriter, r *http.Request) {
	var req reqRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonError(w, http.StatusRequestEntityTooLarge, "the draft is larger than 1 MB")
		} else {
			jsonError(w, http.StatusBadRequest, "reading the draft: "+err.Error())
		}
		return
	}
	d := req.Draft
	if d.File != es.file {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("this server edits %s, not %q", es.file, d.File))
		return
	}
	rs := es.reqs
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if req.Refresh {
		rs.open(true)
	}
	if rs.prep == nil {
		msg := "no change is open"
		if rs.err != nil {
			msg = rs.err.Error()
		}
		writeJSONStatus(w, http.StatusOK, map[string]string{"error": msg})
		return
	}
	answer := func(key string, v any) {
		out := map[string]any{key: v}
		if rs.refreshErr != nil {
			out["refresh_error"] = rs.refreshErr.Error()
		}
		writeJSONStatus(w, http.StatusOK, out)
	}
	if stale := es.uncheckable(rs, d); stale != "" {
		answer("stale", stale)
		return
	}
	ov, eng, reason, err := es.ed.DraftOverlay(d)
	if err != nil {
		answer("stale", err.Error())
		return
	}
	if eng == nil {
		if reason == "" {
			reason = "the draft cannot be evaluated"
		}
		answer("stale", reason)
		return
	}
	res := rs.prep.Decide(ov)
	if stale := es.notRunOn(rs, res); stale != "" {
		answer("stale", stale)
		return
	}
	answer("view", editor.RequirementsView(rs.ref, res, rs.prep.Analysis(), rs.teams, rs.why, eng))
}

// uncheckable says why the PM's cases cannot be decided on the draft before
// running them, "" when they can: the working tree does not stand in for
// the repository, the draft is out of date, or the sheet has an error. An
// error the saved sheet already had counts too: it leaves the team's flow
// out of the cases, and its items would fall back to the rule text.
func (es *editServer) uncheckable(rs *reqState, d editor.SheetDraft) string {
	if !rs.prep.ReadsDrafts() {
		return "this repository has no origin remote, so the PM's cases run on the registry's copy, not on your draft"
	}
	res, err := es.ed.CheckSheet(d)
	switch {
	case errors.Is(err, editor.ErrDraft):
		return "the draft cannot be applied: " + err.Error()
	case err != nil:
		return fmt.Sprintf("%s cannot be read: %v", es.file, err)
	}
	if len(res.Conflict) > 0 {
		return res.Conflict[0] + " changed on disk since this draft was started"
	}
	for _, p := range res.Problems {
		if p.New {
			return "the draft has an error: " + p.Diagnostic.Message
		}
	}
	for _, p := range res.Problems {
		if p.Severity == validate.Error {
			return fmt.Sprintf("the sheet has errors (%s); the PM's cases cannot run on it", p.Diagnostic.Message)
		}
	}
	return ""
}

// notRunOn says why the result of Decide does not reflect the draft, ""
// when it does: this repository was read from a pinned tag or commit, or
// the cases ran for its teams but not on the sheet being edited, or a flow
// of its teams was left out.
func (es *editServer) notRunOn(rs *reqState, res *requirements.Result) string {
	if len(res.Pinned) > 0 {
		src := res.Pinned[0]
		return fmt.Sprintf("this repository is pinned to %s in the registry (as %s); the draft cannot be checked", src.Ref, src.Alias)
	}
	if res.Report == nil {
		return "" // no cases ran: Check read the draft's rule text through the overlay
	}
	// A flow of this repository's left out leaves its items to the rule
	// text over a broken flow: never done on that guess (D-22).
	for _, team := range rs.teams {
		for _, n := range res.Report.TeamNotes[team] {
			if strings.Contains(n, "left out") {
				return fmt.Sprintf("the PM's cases did not run on this draft for %s: %s", team, n)
			}
		}
	}
	// A team with no flow of its own is decided by the rule text read
	// through the draft (D-25(a)); only when cases ran for this repository
	// without this sheet is the draft unchecked.
	ran := false
	here, err := os.Stat(es.dir)
	if err != nil {
		return err.Error()
	}
	for _, team := range rs.teams {
		for _, dir := range res.Report.Sheets[team] {
			ran = true
			if fi, err := os.Stat(dir); err == nil && os.SameFile(fi, here) {
				return ""
			}
		}
	}
	if ran {
		return fmt.Sprintf("the PM's cases ran for %s, but not on this sheet: no flow of this repository was matched with it", strings.Join(rs.teams, ", "))
	}
	return ""
}
