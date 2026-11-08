package links

import (
	"sync"
	"time"
)

// limiter is a token bucket in bytes per second (0 = unlimited).
type limiter struct {
	mu     sync.Mutex
	rate   float64 // bytes/s
	tokens float64
	last   time.Time
}

func (b *limiter) set(bytesPerSec float64) {
	b.mu.Lock()
	b.rate, b.tokens, b.last = bytesPerSec, 0, time.Now()
	b.mu.Unlock()
}

// wait blocks until n bytes may pass.
func (b *limiter) wait(n int) {
	b.mu.Lock()
	if b.rate <= 0 {
		b.mu.Unlock()
		return
	}
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if burst := b.rate / 4; b.tokens > burst { // at most 250 ms of burst
		b.tokens = burst
	}
	b.last = now
	b.tokens -= float64(n)
	var d time.Duration
	if b.tokens < 0 {
		d = time.Duration(-b.tokens / b.rate * float64(time.Second))
	}
	b.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
}

// SetLimitMbps caps the link's download speed (0 removes the cap).
func (l *Link) SetLimitMbps(mbps float64) {
	l.limitMbps.Store(int64(mbps))
	l.lim.set(mbps * 1e6 / 8)
}

// LimitMbps returns the current cap (0 = none).
func (l *Link) LimitMbps() int64 { return l.limitMbps.Load() }

// Throttle accounts n received bytes against the cap, sleeping if needed.
func (l *Link) Throttle(n int) { l.lim.wait(n) }
