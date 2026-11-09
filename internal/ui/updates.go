package ui

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/NassB/dualnet/internal/update"
)

type updateView struct {
	Available bool   `json:"available"`
	Latest    string `json:"latest"`
	Check     bool   `json:"check"`   // the "check for updates" setting
	Install   bool   `json:"install"` // this build can install the update itself
}

func (s *Server) updateState() updateView {
	v := updateView{Check: s.Store == nil || s.Store.UpdateCheckEnabled()}
	if s.Update != nil {
		info := s.Update.Info()
		v.Available, v.Latest = info.Available, info.Latest
		v.Install = s.Restart != nil
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

// installUpdate downloads the release found by the last check, verifies its
// checksum, puts it in place of the running program and restarts dualnet.
func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Update == nil || s.Restart == nil {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	if !s.updating.CompareAndSwap(false, true) {
		http.Error(w, "already updating", http.StatusConflict)
		return
	}
	exe, err := os.Executable()
	var next string
	if err == nil {
		next, err = s.Update.Fetch(r.Context(), exe)
	}
	if err == nil {
		err = update.Swap(exe, next)
	}
	if err != nil {
		s.updating.Store(false)
		log.Printf("update: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("update: %s installed, restarting", s.Update.Info().Latest)
	w.WriteHeader(http.StatusNoContent)
	go func() {
		time.Sleep(500 * time.Millisecond) // let the answer reach the page
		if err := s.Restart(); err != nil {
			log.Printf("update: restart: %v (the new version starts at the next launch)", err)
			s.updating.Store(false)
		}
	}()
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
