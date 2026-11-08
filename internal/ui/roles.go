package ui

import (
	"net/http"

	"github.com/NassB/dualnet/internal/links"
)

// overflowOf reports a link's role: whether it is an overflow link, and whether
// it is currently kept on standby.
func (s *Server) overflowOf(l *links.Link) (overflow, standby bool) {
	if s.Store == nil {
		return false, false
	}
	overflow = s.Store.IsOverflow(l.Name)
	if overflow && s.Tun != nil && s.Tun.Proxy != nil {
		standby = s.Tun.Proxy.Standby(l)
	}
	return
}

// setRole sets how a link is used: ?link=<name>&mode=normal|overflow.
func (s *Server) setRole(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != "normal" && mode != "overflow" {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	name := r.URL.Query().Get("link")
	for _, l := range s.Pool.All() {
		if l.Name == name {
			s.Store.SetOverflow(name, mode == "overflow")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.NotFound(w, r)
}
