package ui

import (
	"context"
	"net/http"

	"github.com/NassB/dualnet/internal/update"
)

type updateView struct {
	Available bool   `json:"available"`
	Latest    string `json:"latest"`
	Check     bool   `json:"check"` // the "check for updates" setting
}

func (s *Server) updateState() updateView {
	v := updateView{Check: s.Store == nil || s.Store.UpdateCheckEnabled()}
	if s.Update != nil {
		info := s.Update.Info()
		v.Available, v.Latest = info.Available, info.Latest
	}
	return v
}

// openUpdate opens the release page found by the last check in the default
// browser. The URL comes from the checker, which only keeps pages of our own repository.
func (s *Server) openUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Update == nil || !s.Update.Info().Available {
		http.Error(w, "no update", http.StatusNotFound)
		return
	}
	if err := OpenBrowser(s.Update.Info().URL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setUpdateCheck turns the daily check on or off (and checks right away when turned on).
func (s *Server) setUpdateCheck(w http.ResponseWriter, r *http.Request) {
	on := r.URL.Query().Get("on") == "1"
	if s.Store != nil {
		s.Store.SetUpdateCheck(on)
	}
	if on && s.Update != nil {
		go s.Update.Check(context.Background())
	}
	w.WriteHeader(http.StatusNoContent)
}

// compile-time check that the checker type is the one we expect
var _ = (*update.Checker)(nil)
