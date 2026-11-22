package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/editor"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/requirements"
	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
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
	es.reqs.open()
}

// open opens the change again; the caller holds mu.
func (rs *reqState) open() {
	rs.prep, rs.teams, rs.why = nil, nil, ""
	rs.prep, rs.err = requirements.OpenChange(rs.top, rs.ref, rs.fe)
	if rs.err != nil {
		rs.prep = nil
		return
	}
	rs.teams, rs.why = rs.prep.TeamsOf(rs.top)
}

// reqRequest is the body of POST /edit/requirements.
type reqRequest struct {
	Draft   editor.SheetDraft `json:"draft"`
	Refresh bool              `json:"refresh"`
}

// requirementsEndpoint reruns the PM's cases against the unsaved draft.
// It answers {"view"}, {"stale"}, or {"error"}, all with status 200; a
// draft that cannot be evaluated or is out of date never gets a view.
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
		rs.open()
	}
	if rs.prep == nil {
		msg := "no change is open"
		if rs.err != nil {
			msg = rs.err.Error()
		}
		writeJSONStatus(w, http.StatusOK, map[string]string{"error": msg})
		return
	}
	// A token conflict, or an error the draft introduces, means the draft is
	// not something to decide cases on: answer stale rather than a view.
	res, err := es.ed.CheckSheet(d)
	if err != nil {
		draftError(w, err)
		return
	}
	if len(res.Conflict) > 0 {
		writeJSONStatus(w, http.StatusOK, map[string]string{"stale": res.Conflict[0] + " changed on disk since this draft was started"})
		return
	}
	for _, p := range res.Problems {
		if p.New {
			writeJSONStatus(w, http.StatusOK, map[string]string{"stale": "the draft has an error: " + p.Diagnostic.Message})
			return
		}
	}
	ov, eng, reason, err := es.ed.DraftOverlay(d)
	if err != nil {
		draftError(w, err)
		return
	}
	if eng == nil {
		if reason == "" {
			reason = "the draft cannot be evaluated"
		}
		writeJSONStatus(w, http.StatusOK, map[string]string{"stale": reason})
		return
	}
	view := editor.RequirementsView(rs.ref, rs.prep.Decide(ov), rs.prep.Analysis(), rs.teams, rs.why, eng)
	writeJSONStatus(w, http.StatusOK, map[string]any{"view": view})
}
