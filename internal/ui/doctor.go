package ui

import (
	"encoding/json"
	"net/http"

	"github.com/NassB/dualnet/internal/doctor"
)

// doctorEnv describes the system for the installation check. Tests replace it
// through Server.DoctorEnv.
func (s *Server) doctorEnv() doctor.Env {
	if s.DoctorEnv != nil {
		return s.DoctorEnv()
	}
	e := doctor.Env{Pool: s.Pool, ProxyAddr: s.ProxyAddr}
	e.UseSystem()
	if s.Tun != nil {
		e.TunOn = s.Tun.On()
		e.PowerMode = s.powerState().Mode
	}
	if s.Store != nil {
		e.DataDir = s.Store.Dir
	}
	if s.Autostart != nil && s.Autostart.Enabled != nil {
		e.Autostart = s.Autostart.Enabled
	}
	return e
}

// getDoctor runs the checks (a few seconds at most: one of them goes to the network).
func (s *Server) getDoctor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(doctor.Run(s.doctorEnv()))
}

// setOnboarding hides (done=1) or shows again (done=0) the first-run checklist.
func (s *Server) setOnboarding(w http.ResponseWriter, r *http.Request) {
	if s.Store != nil {
		s.Store.SetOnboardingDone(r.URL.Query().Get("done") == "1")
	}
	w.WriteHeader(http.StatusNoContent)
}
