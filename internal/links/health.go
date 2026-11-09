package links

import (
	"context"
	"time"
)

// probeTargets are well-known anycast addresses; a TCP connect to port 443 tells
// whether the link reaches the internet and how long it takes.
var probeTargets = []string{"1.1.1.1:443", "8.8.8.8:443", "9.9.9.9:443"}

const probeTimeout = 3 * time.Second

// Probe checks the link once (the "dualnet doctor" command uses it) and reports
// whether this attempt reached the internet.
func (l *Link) Probe(ctx context.Context) bool { return l.probe(ctx) }

// probe checks the link once: it succeeds if any target accepts a connection.
// The targets are tried at the same time, so a link that stopped answering is
// known after one timeout (3 s), not one per target.
func (l *Link) probe(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	l.mu.RLock()
	d := *l.dialer
	l.mu.RUnlock()
	start := time.Now()
	answers := make(chan time.Duration, len(probeTargets)) // -1 = failed
	for _, t := range probeTargets {
		go func(t string) {
			c, err := d.DialContext(cctx, "tcp4", t)
			if err != nil {
				answers <- -1
				return
			}
			c.Close()
			answers <- time.Since(start)
		}(t)
	}
	for range probeTargets {
		if took := <-answers; took >= 0 { // the first target to answer gives the ping
			l.latency.Store(smooth(l.latency.Load(), max(took.Milliseconds(), 1)))
			l.fails.Store(0)
			l.unhealthy.Store(false)
			return true
		}
	}
	// Two failed rounds in a row: stop sending new connections to this link.
	if l.fails.Add(1) >= 2 {
		l.unhealthy.Store(true)
		l.latency.Store(0)
	}
	return false
}

// smooth mixes a new ping sample into the previous value (0 = none yet), so one
// slow answer does not reorder the links: rules that follow the lowest ping
// would otherwise hop from link to link.
func smooth(prev, sample int64) int64 {
	if prev <= 0 {
		return sample
	}
	return max((prev*7+sample*3+5)/10, 1)
}

// SetHealthy marks the link as reaching (or not reaching) the internet (tests).
func (l *Link) SetHealthy(ok bool) {
	l.unhealthy.Store(!ok)
	l.fails.Store(0)
}

// SetLatencyMs sets the measured ping (tests).
func (l *Link) SetLatencyMs(ms int64) { l.latency.Store(ms) }

// Health probes every connected link every interval until ctx is done, and
// calls OnChange when a link stops or starts answering.
func (p *Pool) Health(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, l := range p.All() {
			if !l.Up() {
				continue
			}
			go func(l *Link) {
				was := l.Healthy()
				l.probe(ctx)
				if now := l.Healthy(); now != was && p.OnChange != nil {
					if now {
						p.OnChange(l, EventAnswering)
					} else {
						p.OnChange(l, EventNotAnswering)
					}
				}
			}(l)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
