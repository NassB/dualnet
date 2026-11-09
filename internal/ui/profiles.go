package ui

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/NassB/dualnet/internal/store"
)

func (s *Server) getProfiles(w http.ResponseWriter, r *http.Request) {
	names := []string{}
	if s.Store != nil {
		names = s.Store.Profiles()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(names)
}

// profile acts on a profile: ?name=<name>&action=save|apply|delete.
func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	name, action := r.URL.Query().Get("name"), r.URL.Query().Get("action")
	if s.Store == nil || store.NormalizeProfile(name) == "" {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	switch action {
	case "save":
		s.Store.SaveProfile(name)
	case "delete":
		s.Store.DeleteProfile(name)
	case "apply":
		if !s.Store.ApplyProfile(name, s.Pool) {
			http.NotFound(w, r)
			return
		}
		s.Store.Enforce(s.Pool, time.Now())
	default:
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
