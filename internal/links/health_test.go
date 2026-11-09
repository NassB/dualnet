package links

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestProbeMarksUnhealthyAfterTwoFailures(t *testing.T) {
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	l := New("t", net.IPv4(127, 0, 0, 1))

	probeTargets = []string{ln.Addr().String()}
	l.probe(context.Background())
	if !l.Healthy() || !l.Usable() {
		t.Fatal("reachable target: link should be healthy")
	}

	probeTargets = []string{"127.0.0.1:1"} // closed port: connection refused
	l.probe(context.Background())
	if !l.Healthy() {
		t.Fatal("one failure must not mark the link unhealthy")
	}
	l.probe(context.Background())
	if l.Healthy() || l.Usable() {
		t.Fatal("two failures: link should be unhealthy")
	}
	if len(PoolOf(l).Up()) != 0 {
		t.Fatal("unhealthy link must not be offered by the pool")
	}

	probeTargets = []string{ln.Addr().String()}
	l.probe(context.Background())
	if !l.Healthy() {
		t.Fatal("link should recover after a successful check")
	}
}

func TestSmooth(t *testing.T) {
	if smooth(0, 40) != 40 {
		t.Fatal("the first sample is taken as it is")
	}
	if got := smooth(20, 220); got != 80 { // one slow answer moves the value, it does not replace it
		t.Fatal(got)
	}
	v := int64(200)
	for i := 0; i < 20; i++ {
		v = smooth(v, 20)
	}
	if v > 22 {
		t.Fatalf("must converge to the samples, got %d", v)
	}
}

func TestCloseStalled(t *testing.T) {
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { // answers one byte to the connections that send one
				b := make([]byte, 1)
				for {
					if _, err := c.Read(b); err != nil {
						return
					}
					c.Write(b)
				}
			}()
		}
	}()
	l := New("t", net.IPv4(127, 0, 0, 1))
	quiet, _ := l.DialContext(context.Background(), "tcp", ln.Addr().String())
	busy, _ := l.DialContext(context.Background(), "tcp", ln.Addr().String())
	defer busy.Close()
	time.Sleep(150 * time.Millisecond)
	busy.Write([]byte{1})
	busy.Read(make([]byte, 1)) // received something just now

	if n := l.CloseStalled(100 * time.Millisecond); n != 1 {
		t.Fatalf("%d connections closed, want only the silent one", n)
	}
	if _, err := quiet.Write([]byte{1}); err == nil {
		t.Fatal("the stalled connection must be closed")
	}
	if _, err := busy.Write([]byte{1}); err != nil {
		t.Fatal("a connection that receives data must be kept:", err)
	}
	if l.Busy(time.Minute) != 1 {
		t.Fatalf("%d connections tracked, want 1", l.Busy(time.Minute))
	}
}
