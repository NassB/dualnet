package links

// SetLimitMbps caps the link's download speed (0 removes the cap).
func (l *Link) SetLimitMbps(mbps float64) {
	l.limitMbps.Store(int64(mbps))
	l.lim.Set(mbps * 1e6 / 8)
}

// LimitMbps returns the current cap (0 = none).
func (l *Link) LimitMbps() int64 { return l.limitMbps.Load() }

// Throttle accounts n received bytes against the cap, sleeping if needed.
func (l *Link) Throttle(n int) { l.lim.Wait(n) }
