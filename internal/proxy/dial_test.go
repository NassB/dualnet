package proxy

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type fakeConn struct {
	net.Conn
	closed *atomic.Int32
}

func (f fakeConn) Close() error { f.closed.Add(1); return nil }

func TestRaceDial(t *testing.T) {
	a, b := links.New("a", net.IPv4(127, 0, 0, 1)), links.New("b", net.IPv4(127, 0, 0, 1))
	order := []*links.Link{a, b}
	var closed atomic.Int32
	never := func(*links.Link) bool { return false }
	// dialer: each link answers after its delay (or fails), unless cancelled first
	dialer := func(wait map[*links.Link]time.Duration, fail map[*links.Link]bool, tried *atomic.Int32) func(context.Context, *links.Link) (net.Conn, error) {
		return func(ctx context.Context, l *links.Link) (net.Conn, error) {
			tried.Add(1)
			select {
			case <-time.After(wait[l]):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if fail[l] {
				return nil, errors.New("refused")
			}
			return fakeConn{closed: &closed}, nil
		}
	}

	// the best link answers in time: the other one is not even tried
	var tried atomic.Int32
	_, l, err := raceDial(order, dialer(map[*links.Link]time.Duration{a: 20 * time.Millisecond}, nil, &tried), never)
	if err != nil || l != a || tried.Load() != 1 {
		t.Fatalf("link %v, err %v, %d attempts", l, err, tried.Load())
	}

	// the best link hangs: the second one takes over after the head start, not after the timeout
	tried.Store(0)
	start := time.Now()
	_, l, err = raceDial(order, dialer(map[*links.Link]time.Duration{a: time.Minute, b: 10 * time.Millisecond}, nil, &tried), never)
	if took := time.Since(start); err != nil || l != b || took > 2*time.Second || took < dialHeadMin {
		t.Fatalf("link %v, err %v, took %v", l, err, took)
	}

	// it fails at once: the second one starts at once
	start = time.Now()
	_, l, err = raceDial(order, dialer(nil, map[*links.Link]bool{a: true}, &tried), never)
	if err != nil || l != b || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("link %v, err %v, took %v", l, err, time.Since(start))
	}

	// a held link is not started because the first one is slow...
	tried.Store(0)
	held := func(x *links.Link) bool { return x == b }
	_, l, err = raceDial(order, dialer(map[*links.Link]time.Duration{a: dialHeadMin + 200*time.Millisecond}, nil, &tried), held)
	if err != nil || l != a || tried.Load() != 1 {
		t.Fatalf("link %v, err %v, %d attempts", l, err, tried.Load())
	}
	// ...but it is when the first one fails
	_, l, err = raceDial(order, dialer(nil, map[*links.Link]bool{a: true}, &tried), held)
	if err != nil || l != b {
		t.Fatalf("link %v, err %v", l, err)
	}

	// everything fails: the last error; no link: errNoLink
	if _, _, err = raceDial(order, dialer(nil, map[*links.Link]bool{a: true, b: true}, &tried), never); err == nil || err.Error() != "refused" {
		t.Fatal(err)
	}
	if _, _, err = raceDial(nil, nil, never); err != errNoLink {
		t.Fatal(err)
	}

	// both open (the slower attempt completes although it was cancelled): the
	// loser's connection is closed, not leaked
	closed.Store(0)
	a.SetLatencyMs(0)
	stubborn := func(_ context.Context, l *links.Link) (net.Conn, error) {
		if l == a {
			time.Sleep(dialHeadMin + 400*time.Millisecond)
		}
		return fakeConn{closed: &closed}, nil
	}
	if _, l, _ = raceDial(order, stubborn, never); l != b {
		t.Fatalf("winner %v, want b", l)
	}
	for i := 0; i < 100 && closed.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if closed.Load() != 1 {
		t.Fatalf("%d connections closed, want 1", closed.Load())
	}

	// a slow link gets a longer head start
	a.SetLatencyMs(200)
	if headStart(a) != 700*time.Millisecond {
		t.Fatal(headStart(a))
	}
	a.SetLatencyMs(5000)
	if headStart(a) != dialHeadMax {
		t.Fatal(headStart(a))
	}
}
