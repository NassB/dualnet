package proxy

import (
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// A speed test gives a link's capacity right away, instead of waiting for
// traffic to reveal it. It is trusted for a week, then ignored.
const seedValidity = 7 * 24 * time.Hour

type seed struct {
	mbps float64
	at   time.Time
}

// SetSpeedTest records a measured capacity (Mb/s) of a link, taken at time at.
func (s *Server) SetSpeedTest(l *links.Link, mbps float64, at time.Time) {
	s.init()
	s.mu.Lock()
	s.seed[l] = seed{mbps, at}
	s.mu.Unlock()
}

// capacityOf is the best estimate of what a link can carry, in Mb/s: the peak
// seen in traffic (slowly decaying) or a recent speed test, whichever is higher,
// never above the link's speed cap. The caller holds s.mu.
func (s *Server) capacityOf(l *links.Link) float64 {
	c := s.rate[l]
	if sd, ok := s.seed[l]; ok && time.Since(sd.at) < seedValidity && sd.mbps > c {
		c = sd.mbps
	}
	if lim := float64(l.LimitMbps()); lim > 0 && lim < c {
		c = lim
	}
	return c
}

// Capacity returns the estimated capacity of a link in Mb/s.
func (s *Server) Capacity(l *links.Link) float64 {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacityOf(l)
}
