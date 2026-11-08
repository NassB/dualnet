package ui

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/speedtest"
)

// speedOf returns a link's last speed test (Mb/s and unix milliseconds), or zeros.
func (s *Server) speedOf(l *links.Link) (float64, int64) {
	if s.Store == nil {
		return 0, 0
	}
	if r, ok := s.Store.SpeedTest(l.Name); ok {
		return r.Mbps, r.At.UnixMilli()
	}
	return 0, 0
}

// speedTest measures one link: POST /api/speedtest?link=<name>. It answers when
// the test is over (about 7 s); only one test runs at a time.
func (s *Server) speedTest(w http.ResponseWriter, r *http.Request) {
	var l *links.Link
	for _, x := range s.Pool.All() {
		if x.Name == r.URL.Query().Get("link") {
			l = x
		}
	}
	if l == nil {
		http.NotFound(w, r)
		return
	}
	if !l.Usable() {
		http.Error(w, "link is not usable", http.StatusConflict)
		return
	}
	if !s.speedMu.TryLock() {
		http.Error(w, "speed test already running", http.StatusConflict)
		return
	}
	defer s.speedMu.Unlock()

	url := s.SpeedTestURL
	if url == "" {
		url = speedtest.DefaultURL
	}
	window := s.SpeedWindow
	if window == 0 {
		window = 7 * time.Second
	}
	res, err := speedtest.Run(r.Context(), l, url, window, 4)
	if err != nil {
		http.Error(w, "speed test failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	at := time.Now()
	if s.Store != nil {
		s.Store.SetSpeedTest(l.Name, res.Mbps, at)
	}
	if s.Tun != nil && s.Tun.Proxy != nil {
		s.Tun.Proxy.SetSpeedTest(l, res.Mbps, at) // the spread uses the measured capacity at once
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"mbps": res.Mbps, "bytes": res.Bytes})
}
