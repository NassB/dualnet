package links

import (
	"context"
	"time"
)

// probeTargets are well-known anycast addresses; a TCP connect to port 443 tells
// whether the link reaches the internet and how long it takes.
var probeTargets = []string{"1.1.1.1:443", "8.8.8.8:443", "9.9.9.9:443"}

// probe checks the link once: it succeeds if any target accepts a connection.
func (l *Link) probe(ctx context.Context) {
	for _, t := range probeTargets {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		start := time.Now()
		l.mu.RLock()
		d := *l.dialer
		l.mu.RUnlock()
		c, err := d.DialContext(cctx, "tcp4", t)
		cancel()
		if err == nil {
			c.Close()
			l.latency.Store(time.Since(start).Milliseconds())
			l.fails.Store(0)
			l.unhealthy.Store(false)
			return
		}
	}
	// Two failed rounds in a row: stop sending new connections to this link.
	if l.fails.Add(1) >= 2 {
		l.unhealthy.Store(true)
		l.latency.Store(0)
	}
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
