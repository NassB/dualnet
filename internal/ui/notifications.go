package ui

import "net/http"

// setNotify turns the link notifications on or off.
func (s *Server) setNotify(w http.ResponseWriter, r *http.Request) {
	if s.Store != nil {
		s.Store.SetNotify(r.URL.Query().Get("on") == "1")
	}
	w.WriteHeader(http.StatusNoContent)
}

// testNotify sends a test notification so the user can check they are displayed.
func (s *Server) testNotify(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	if err := s.Notify.Test(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
