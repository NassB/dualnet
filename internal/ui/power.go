package ui

import (
	"net/http"

	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/transparent"
)

// The main switch: "dualnet on" captures the PC's traffic — transparent mode when
// running as administrator, otherwise the Windows proxy. "Off" puts Windows back
// to its normal networking (links are still shown, nothing goes through dualnet).

type powerView struct {
	On   bool   `json:"on"`
	Mode string `json:"mode"` // "transparent", "proxy" or ""
}

func (s *Server) powerState() powerView {
	switch {
	case s.Tun.On():
		return powerView{On: true, Mode: "transparent"}
	case transparent.Admin():
		// As administrator the switch means transparent mode only.
		return powerView{}
	case s.ProxyAddr != "":
		if on, server, _ := sysproxy.Status(); on && server == s.ProxyAddr {
			return powerView{On: true, Mode: "proxy"}
		}
	}
	return powerView{}
}

// SetPower turns dualnet on or off and remembers the choice for the next start.
func (s *Server) SetPower(on bool) error {
	var err error
	if on {
		if transparent.Admin() {
			err = s.Tun.Set(true)
		} else if s.ProxyAddr != "" {
			err = sysproxy.Enable(s.ProxyAddr)
		}
	} else {
		s.Tun.Set(false)
		err = sysproxy.Disable()
		if err == nil && s.ProxyAddr != "" {
			err = sysproxy.ClearOurs(s.ProxyAddr)
		}
	}
	if err == nil && s.Store != nil {
		s.Store.SetEnabled(on)
	}
	return err
}

func (s *Server) setPower(w http.ResponseWriter, r *http.Request) {
	if err := s.SetPower(r.URL.Query().Get("on") == "1"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Autostart hooks (set by the main package; nil = not available).
type Autostart struct {
	Enabled func() bool
	Set     func(bool) error
}

func (s *Server) setAutostart(w http.ResponseWriter, r *http.Request) {
	if s.Autostart == nil || s.Autostart.Set == nil {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	if err := s.Autostart.Set(r.URL.Query().Get("on") == "1"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) autostartState() *bool {
	if s.Autostart == nil || s.Autostart.Enabled == nil {
		return nil
	}
	v := s.Autostart.Enabled()
	return &v
}
