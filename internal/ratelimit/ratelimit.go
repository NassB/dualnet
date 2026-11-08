// Package ratelimit is a small token bucket used to cap download speeds.
package ratelimit

import (
	"sync"
	"time"
)

// Bucket limits a byte stream to a rate in bytes per second (0 = unlimited).
type Bucket struct {
	mu     sync.Mutex
	rate   float64
	tokens float64
	last   time.Time
}

// Set changes the rate (0 removes the cap) and empties the bucket.
func (b *Bucket) Set(bytesPerSec float64) {
	b.mu.Lock()
	b.rate, b.tokens, b.last = bytesPerSec, 0, time.Now()
	b.mu.Unlock()
}

// Rate returns the current rate in bytes per second.
func (b *Bucket) Rate() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rate
}

// Wait blocks until n bytes may pass.
func (b *Bucket) Wait(n int) {
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
