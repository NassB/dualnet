package ui

import (
	"net/http"
	"sync"

	"github.com/NassB/dualnet/internal/proxy"
	"github.com/NassB/dualnet/internal/transparent"
)

// TunControl starts and stops the transparent mode.
type TunControl struct {
	Proxy *proxy.Server

	mu  sync.Mutex
	t   *transparent.Tunnel
	err string
}

// Set turns the transparent mode on or off.
func (c *TunControl) Set(on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !on {
		if c.t != nil {
			c.t.Stop()
			c.t = nil
		}
		c.err = ""
		return nil
	}
	if c.t != nil {
		return nil
	}
	t, err := transparent.Start(proxy.TunDialer{S: c.Proxy})
	if err != nil {
		c.err = err.Error()
		return err
	}
	c.t, c.err = t, ""
	return nil
}

// On reports whether the transparent mode is running.
func (c *TunControl) On() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t != nil
}

// Stop turns it off (on exit).
func (c *TunControl) Stop() { c.Set(false) }

type tunView struct {
	On    bool   `json:"on"`
	Admin bool   `json:"admin"`
	Err   string `json:"err,omitempty"`
}

func (c *TunControl) view() tunView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return tunView{On: c.t != nil, Admin: transparent.Admin(), Err: c.err}
}

func (s *Server) setTun(w http.ResponseWriter, r *http.Request) {
	if err := s.Tun.Set(r.URL.Query().Get("on") == "1"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
