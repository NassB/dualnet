package ui

import (
	"net/http"

	"github.com/NassB/dualnet/internal/transparent"
	"github.com/NassB/dualnet/internal/winsys"
)

type systemView struct {
	Admin        bool           `json:"admin"`
	Simultaneous bool           `json:"simultaneous"`
	Metrics      map[string]int `json:"metrics"`
}

func (s *Server) systemState() systemView {
	return systemView{Admin: transparent.Admin(), Simultaneous: winsys.SimultaneousAllowed(), Metrics: winsys.Metrics()}
}

// setSystem handles the system buttons:
//
//	?do=simultaneous&on=1|0   allow Wi-Fi + wired link at once
//	?do=prefer&link=<name>    Windows prefers this link ("" = automatic)
func (s *Server) setSystem(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var err error
	switch q.Get("do") {
	case "simultaneous":
		err = winsys.SetSimultaneous(q.Get("on") == "1")
	case "prefer":
		var names []string
		for _, l := range s.Pool.All() {
			names = append(names, l.Name)
		}
		err = winsys.Prefer(q.Get("link"), names)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
