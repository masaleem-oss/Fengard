package web

import (
	"errors"
	"net/http"
	"slices"

	"github.com/masaleem-oss/Fengard/internal/config"
)

// a new private address the admin says is an existing device
func (s *Server) mergeDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Into string `json:"into"`
	}
	if !decode(w, r, &body) {
		return
	}
	into, err := config.NormIdentity(body.Into)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid device address")
		return
	}
	name := into
	if d := s.Config.Get().Device(into); d != nil {
		name = displayName(*d)
	}
	if s.update(w, r, "Merged "+mac+" into "+name, func(c *config.Config) error {
		if c.Device(mac) == nil {
			return errors.New("unknown device")
		}
		return c.MergeDevice(mac, into)
	}) {
		if s.Recognizer != nil {
			s.Recognizer.Forget(mac)
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) notSameDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Other string `json:"other"`
	}
	if !decode(w, r, &body) {
		return
	}
	other, err := config.NormIdentity(body.Other)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid device address")
		return
	}
	if s.Recognizer != nil {
		s.Recognizer.NotSame(mac, other)
	}
	s.audit(r, "Marked "+mac+" as not the same as "+other, "")
	writeJSON(w, map[string]bool{"ok": true})
}

// undoes a merge, that address shows up as its own device next time
func (s *Server) unlinkMAC(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Old string `json:"old"`
	}
	if !decode(w, r, &body) {
		return
	}
	old, err := config.NormIdentity(body.Old)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid device address")
		return
	}
	if s.update(w, r, "Unlinked "+old+" from "+mac, func(c *config.Config) error {
		d := c.Device(mac)
		if d == nil || !slices.Contains(d.OldMACs, old) {
			return errors.New("that address isn't linked to this device")
		}
		c.UnlinkMAC(old)
		return nil
	}) {
		if s.Devices != nil {
			s.Devices.Forget(old)
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}
