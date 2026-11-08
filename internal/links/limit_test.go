package links

import (
	"net"
	"testing"
	"time"
)

func TestThrottleCapsRate(t *testing.T) {
	l := New("t", net.IPv4(127, 0, 0, 1))
	l.SetLimitMbps(8) // 1 MB/s
	start := time.Now()
	for i := 0; i < 50; i++ {
		l.Throttle(10_000) // 500 KB total
	}
	el := time.Since(start)
	if el < 350*time.Millisecond || el > 900*time.Millisecond {
		t.Fatalf("500 KB at 1 MB/s took %v, want ~0.5 s", el)
	}
	l.SetLimitMbps(0)
	start = time.Now()
	l.Throttle(10_000_000)
	if time.Since(start) > 10*time.Millisecond {
		t.Fatal("no cap: must not wait")
	}
}
