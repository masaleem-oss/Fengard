package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/update"
)

type updateView struct {
	update.Status
	Available  bool `json:"available"`
	AutoUpdate bool `json:"autoUpdate"`
	Disabled   bool `json:"disabled,omitempty"`
}

func (s *Server) updateView() updateView {
	auto := s.Config.Get().Settings.AutoUpdate
	if s.Updater == nil || s.Updater.API == "" {
		return updateView{Status: update.Status{Current: s.Version}, AutoUpdate: auto, Disabled: true}
	}
	st := s.Updater.Status()
	return updateView{Status: st, Available: st.Available(), AutoUpdate: auto}
}

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.updateView())
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil || s.Updater.API == "" {
		httpError(w, http.StatusBadRequest, "update checks are turned off")
		return
	}
	s.Updater.Check(r.Context())
	writeJSON(w, s.updateView())
}

func (s *Server) updateInstall(w http.ResponseWriter, r *http.Request) {
	v := s.updateView()
	switch {
	case v.Disabled:
		httpError(w, http.StatusBadRequest, "update checks are turned off")
		return
	case !v.CanInstall:
		httpError(w, http.StatusBadRequest, "this install can't update itself, download the new kit from GitHub")
		return
	case !v.Available:
		httpError(w, http.StatusBadRequest, "no update available for this router")
		return
	case v.Installing:
		httpError(w, http.StatusConflict, "an update is already running")
		return
	}
	who := "admin"
	if sess := s.currentSession(r); sess != nil {
		who = sess.Username
	}
	s.Audit.Append(auditRecord(who, clientIP(r), "Started update to "+v.Latest))
	// the download outlives the request and fengard restarts at the end
	if !s.startWorker(func(ctx context.Context) {
		if err := s.Updater.Install(ctx); err != nil && ctx.Err() == nil {
			s.Alerts.Raise("update_failed:"+v.Latest, 0, alerts.Alert{Kind: "update_failed", Severity: alerts.Warning,
				Title: "Update to Fengard " + v.Latest + " failed", Detail: sentence(err.Error())})
		}
	}) {
		httpError(w, http.StatusServiceUnavailable, "Fengard is shutting down")
		return
	}
	v.Installing = true
	writeJSON(w, v)
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}
