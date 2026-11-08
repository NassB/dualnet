// Package speedtest measures how fast one link can download, by fetching a large
// file through that link only (several streams, like a real download).
//
// The data goes through the link like any other traffic: it counts toward the
// link's monthly budget and obeys its speed cap, so the result is the speed
// dualnet can really use on that link.
package speedtest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// DefaultURL serves the requested number of bytes (Cloudflare's public speed
// test). It refuses 100 MB and more with a 403, so a stream asks for 50 MB at a
// time and asks again when the response ends.
const DefaultURL = "https://speed.cloudflare.com/__down?bytes=50000000"

// Result is what a test measured.
type Result struct {
	Mbps     float64       // download speed, megabits per second
	Bytes    int64         // downloaded during the whole test
	Duration time.Duration // length of the test
}

// Run downloads url through l for about window using streams parallel
// connections. The first second is ignored (TCP slow start).
func Run(ctx context.Context, l *links.Link, url string, window time.Duration, streams int) (Result, error) {
	if streams < 1 {
		streams = 1
	}
	warm := time.Second
	if window < 4*time.Second {
		warm = window / 4
	}
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	client := &http.Client{Transport: &http.Transport{
		DialContext:           l.DialContext,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	}}
	var total atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 64<<10)
			for ctx.Err() == nil { // the server may end a response early: ask again
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				req.Header.Set("User-Agent", "dualnet-speedtest")
				resp, err := client.Do(req)
				if err != nil {
					if ctx.Err() == nil {
						firstErr.CompareAndSwap(nil, err)
					}
					return
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					firstErr.CompareAndSwap(nil, fmt.Errorf("HTTP %s", resp.Status))
					return
				}
				for {
					n, err := resp.Body.Read(buf)
					total.Add(int64(n))
					if err != nil {
						break
					}
				}
				resp.Body.Close()
			}
		}()
	}

	start := time.Now()
	select {
	case <-time.After(warm):
	case <-ctx.Done():
	}
	b0, t0 := total.Load(), time.Now()
	<-ctx.Done()
	b1, t1 := total.Load(), time.Now()
	cancel()
	wg.Wait()

	res := Result{Bytes: total.Load(), Duration: time.Since(start)}
	if t1.Sub(t0) > 0 {
		res.Mbps = float64(b1-b0) * 8 / t1.Sub(t0).Seconds() / 1e6
	}
	if res.Bytes == 0 {
		if e, ok := firstErr.Load().(error); ok {
			return res, e
		}
		return res, errors.New("no data received")
	}
	return res, nil
}
