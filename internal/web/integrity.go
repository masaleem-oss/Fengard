package web

import (
	"fmt"
	"net/http"

	"github.com/masaleem-oss/Fengard/internal/integrity"
)

// security check

func (s *Server) securityStatus(w http.ResponseWriter, r *http.Request) {
	type watch struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		On    bool   `json:"on"`
	}
	set := s.Config.Get().Settings
	watches := make([]watch, 0, len(integrity.Watches))
	for _, id := range integrity.Watches {
		watches = append(watches, watch{ID: id, Label: integrity.Labels[id], On: set.Watching(id)})
	}
	st := integrity.Status{Changes: []integrity.Change{}}
	if s.Integrity != nil {
		st = s.Integrity.Status()
	}
	writeJSON(w, struct {
		integrity.Status
		Enabled bool    `json:"enabled"`
		Watches []watch `json:"watches"`
	}{st, !set.SecurityCheckOff, watches})
}

func (s *Server) securityCheck(w http.ResponseWriter, r *http.Request) {
	if s.Integrity == nil || !s.Integrity.Status().Available {
		httpError(w, http.StatusNotFound, "the security check only runs on a router")
		return
	}
	if !s.Integrity.Trigger() {
		httpError(w, http.StatusConflict, "a check is already running")
		return
	}
	s.audit(r, "Ran the security check", "")
	writeJSON(w, map[string]bool{"started": true})
}

func (s *Server) securityApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if !decode(w, r, &body) {
		return
	}
	if s.Integrity == nil {
		httpError(w, http.StatusNotFound, "the security check only runs on a router")
		return
	}
	if len(body.IDs) > 10000 {
		httpError(w, http.StatusBadRequest, "too many changes at once")
		return
	}
	if !body.All && len(body.IDs) == 0 {
		httpError(w, http.StatusBadRequest, "pick the changes to approve")
		return
	}
	n := s.Integrity.Approve(body.IDs, body.All)
	if n > 0 {
		what := "1 security check change"
		if n != 1 {
			what = fmt.Sprintf("%d security check changes", n)
		}
		s.audit(r, "Approved "+what, "")
	}
	writeJSON(w, map[string]int{"approved": n})
}
