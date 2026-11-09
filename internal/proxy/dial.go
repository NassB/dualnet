package proxy

import (
	"context"
	"net"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// Connecting over the links, best first, without waiting for a dead one.
//
// A link can stop carrying traffic while Windows still shows it connected (no
// mobile signal, a box that lost its line); the health check needs several
// seconds to notice. Trying the links one after the other would make every new
// connection wait for the first link's timeout. Instead the next link is
// started as soon as the previous one is late, and the first connection that
// opens wins ("happy eyeballs", applied to links).
const (
	dialTimeout  = 4 * time.Second        // one attempt on one link
	dialHeadMin  = 300 * time.Millisecond // head start of a link over the next one...
	dialHeadMax  = 1500 * time.Millisecond
	dialHeadPing = 2 // ...plus this many times its ping (a slow link is not a dead link)
)

// headStart is how long a link may take to connect before the next one is tried as well.
func headStart(l *links.Link) time.Duration {
	d := dialHeadMin + dialHeadPing*time.Duration(l.LatencyMs())*time.Millisecond
	return min(d, dialHeadMax)
}

type dialResult struct {
	c   net.Conn
	l   *links.Link
	err error
}

// raceDial connects with dial over the links of order, best first. The next
// link is started when the previous attempt fails, or when it is late and
// hold(next) is false: a held link (an overflow link kept idle to save its
// data) is only used after a real failure, never because another was slow.
func raceDial(order []*links.Link, dial func(context.Context, *links.Link) (net.Conn, error), hold func(*links.Link) bool) (net.Conn, *links.Link, error) {
	if len(order) == 0 {
		return nil, nil, errNoLink
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan dialResult, len(order))
	next, pending := 0, 0
	late := time.NewTimer(time.Hour)
	defer late.Stop()
	start := func() {
		l := order[next]
		next++
		pending++
		late.Reset(headStart(l))
		go func() {
			dctx, done := context.WithTimeout(ctx, dialTimeout)
			defer done()
			c, err := dial(dctx, l)
			results <- dialResult{c, l, err}
		}()
	}
	start()
	lastErr := errNoLink
	for pending > 0 {
		select {
		case r := <-results:
			pending--
			if r.err == nil {
				cancel() // stops the attempts still running; r.c is already open and stays so
				go func(n int) {
					for ; n > 0; n-- {
						if o := <-results; o.c != nil {
							o.c.Close() // opened just after the winner
						}
					}
				}(pending)
				return r.c, r.l, nil
			}
			lastErr = r.err
			if next < len(order) {
				start()
			}
		case <-late.C:
			if next < len(order) && !hold(order[next]) {
				start()
			}
		}
	}
	cancel()
	return nil, nil, lastErr
}
