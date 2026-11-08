package proxy

import "github.com/NassB/dualnet/internal/links"

// Overflow links. A link marked "overflow" (typically a phone's mobile data) is
// kept idle, saving its data, until the other links are nearly full; then it is
// added to carry the excess. It is left again once the load has stayed low for a while.
//
// "Nearly full" is measured against each link's estimated capacity (traffic peak,
// speed test, speed cap): the primary links together carry at least 80 % of it.
// Closing needs the load to stay under 50 % for 5 seconds, so the overflow link
// does not flap on and off.
//
// Never cutting anyone off: with no usable primary link, the overflow links
// serve everything; an application restricted to an overflow link by a rule gets it.
const (
	overflowOpenAt  = 0.8  // share of the primary capacity that opens the overflow links
	overflowCloseAt = 0.5  // share under which they may close again...
	overflowCloseS  = 5    // ...after this many seconds
	overflowMinCap  = 20.0 // Mb/s: below this the capacity estimate is too uncertain to open on
)

func (s *Server) isOverflow(l *links.Link) bool { return s.Overflow != nil && s.Overflow(l.Name) }

func contains(ls []*links.Link, x *links.Link) bool {
	for _, l := range ls {
		if l == x {
			return true
		}
	}
	return false
}

// allowedNow returns the links that may receive new connections: all of them,
// unless there are overflow links, usable primary links and the overflow is closed.
func (s *Server) allowedNow(up []*links.Link) []*links.Link {
	var primary []*links.Link
	over := false
	for _, l := range up {
		if s.isOverflow(l) {
			over = true
		} else {
			primary = append(primary, l)
		}
	}
	if !over || len(primary) == 0 || s.overflowOpen.Load() {
		return up
	}
	return primary
}

// evalOverflow opens or closes the overflow links from the links' current rates
// (Mb/s). Called every second by stats() with s.mu held.
func (s *Server) evalOverflow(cur map[*links.Link]float64) {
	var load, capacity float64
	primaries := 0
	for _, l := range s.Pool.Up() {
		if s.isOverflow(l) {
			continue
		}
		primaries++
		load += cur[l]
		capacity += s.capacityOf(l)
	}
	if primaries == 0 {
		s.overflowOpen.Store(false)
		s.overflowLow = 0
		return
	}
	if !s.overflowOpen.Load() {
		if capacity >= overflowMinCap && load >= overflowOpenAt*capacity {
			s.overflowOpen.Store(true)
			s.overflowLow = 0
		}
		return
	}
	if load < overflowCloseAt*capacity {
		s.overflowLow++
		if s.overflowLow >= overflowCloseS {
			s.overflowOpen.Store(false)
			s.overflowLow = 0
		}
	} else {
		s.overflowLow = 0
	}
}

// Standby reports whether l is an overflow link currently kept idle.
func (s *Server) Standby(l *links.Link) bool {
	if !s.isOverflow(l) || !l.Usable() {
		return false
	}
	return !contains(s.allowedNow(s.Pool.Up()), l)
}
