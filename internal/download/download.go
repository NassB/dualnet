// Package download fetches one file in parallel chunks spread over several links,
// so the bandwidth of all links adds up for that single file (HTTP Range requests).
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type Options struct {
	URL           string
	Output        string
	ChunkSize     int64
	WorkersPerLnk int
}

func clientFor(l *links.Link) *http.Client {
	tr := &http.Transport{
		DialContext:         l.DialContext,
		MaxIdleConnsPerHost: 16,
		ForceAttemptHTTP2:   false, // one TCP connection per worker keeps links independent
		TLSHandshakeTimeout: 15 * time.Second,
	}
	return &http.Client{Transport: tr}
}

type chunk struct{ start, end int64 } // inclusive

// Run downloads opts.URL using all links.
func Run(ctx context.Context, ls []*links.Link, opts Options) error {
	clients := make([]*http.Client, len(ls))
	for i, l := range ls {
		clients[i] = clientFor(l)
	}

	size, rangesOK, name, err := probe(ctx, ls, clients, opts.URL)
	if err != nil {
		return err
	}
	if opts.Output == "" {
		opts.Output = name
	}
	f, err := os.Create(opts.Output)
	if err != nil {
		return err
	}
	defer f.Close()

	if !rangesOK || size <= 0 {
		fmt.Println("Le serveur n'accepte pas les téléchargements par morceaux : un seul lien sera utilisé.")
		return single(ctx, clients[0], opts.URL, f)
	}
	if err := f.Truncate(size); err != nil {
		return err
	}
	fmt.Printf("Fichier : %s (%s)\n", opts.Output, links.HumanBytes(float64(size)))

	queue := make(chan chunk, int(size/opts.ChunkSize)+2)
	var pending sync.WaitGroup
	for s := int64(0); s < size; s += opts.ChunkSize {
		e := s + opts.ChunkSize - 1
		if e >= size {
			e = size - 1
		}
		pending.Add(1)
		queue <- chunk{s, e}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var done atomic.Int64
	var fatal atomic.Value
	perLink := make([]atomic.Int64, len(ls))
	failures := make([]atomic.Int32, len(ls))

	go func() { pending.Wait(); close(queue) }()

	var workers sync.WaitGroup
	for li := range ls {
		for w := 0; w < opts.WorkersPerLnk; w++ {
			workers.Add(1)
			go func(li int) {
				defer workers.Done()
				for c := range queue {
					n, err := fetchChunk(ctx, clients[li], opts.URL, c, f)
					done.Add(n)
					perLink[li].Add(n)
					if err != nil {
						if ctx.Err() != nil {
							pending.Done()
							return
						}
						// Return the remaining part of the chunk to the queue so another worker
						// (possibly on the other link) picks it up.
						rest := chunk{c.start + n, c.end}
						if failures[li].Add(1) > 20 {
							fmt.Printf("\n%s : trop d'erreurs, lien abandonné (%v)\n", ls[li], err)
							queue <- rest
							return
						}
						time.Sleep(500 * time.Millisecond)
						queue <- rest
						continue
					}
					pending.Done()
				}
			}(li)
		}
	}

	start := time.Now()
	stop := make(chan struct{})
	go progress(ls, perLink, &done, size, start, stop)
	workers.Wait()
	close(stop)

	if v := fatal.Load(); v != nil {
		return v.(error)
	}
	if done.Load() < size {
		return errors.New("téléchargement incomplet")
	}
	el := time.Since(start)
	fmt.Printf("\nTerminé en %s — moyenne %.1f Mb/s\n", el.Round(time.Second), links.Mbps(size, el))
	for i, l := range ls {
		fmt.Printf("  %-30s %s (%.0f%%)\n", l.String(), links.HumanBytes(float64(perLink[i].Load())), 100*float64(perLink[i].Load())/float64(size))
	}
	return nil
}

func probe(ctx context.Context, ls []*links.Link, clients []*http.Client, rawURL string) (int64, bool, string, error) {
	var lastErr error
	for i, c := range clients {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		req.Header.Set("Range", "bytes=0-0")
		resp, err := c.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s : %w", ls[i], err)
			continue
		}
		resp.Body.Close()
		name := fileName(resp, rawURL)
		if resp.StatusCode == http.StatusPartialContent {
			cr := resp.Header.Get("Content-Range") // bytes 0-0/12345
			if idx := strings.LastIndex(cr, "/"); idx >= 0 {
				if n, err := strconv.ParseInt(cr[idx+1:], 10, 64); err == nil {
					return n, true, name, nil
				}
			}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.ContentLength, false, name, nil
		}
		lastErr = fmt.Errorf("HTTP %s", resp.Status)
	}
	return 0, false, "", lastErr
}

func fileName(resp *http.Response, rawURL string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, p, err := mime.ParseMediaType(cd); err == nil && p["filename"] != "" {
			return path.Base(p["filename"])
		}
	}
	if u, err := url.Parse(resp.Request.URL.String()); err == nil {
		if b := path.Base(u.Path); b != "/" && b != "." && b != "" {
			return b
		}
	}
	_ = rawURL
	return "download.bin"
}

func fetchChunk(ctx context.Context, c *http.Client, rawURL string, ch chunk, f *os.File) (int64, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", ch.start, ch.end))
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("HTTP %s", resp.Status)
	}
	buf := make([]byte, 64*1024)
	off := ch.start
	for off <= ch.end {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if int64(n) > ch.end-off+1 {
				n = int(ch.end - off + 1)
			}
			if _, err := f.WriteAt(buf[:n], off); err != nil {
				return off - ch.start, err
			}
			off += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return off - ch.start, rerr
		}
	}
	if off <= ch.end {
		return off - ch.start, io.ErrUnexpectedEOF
	}
	return off - ch.start, nil
}

func single(ctx context.Context, c *http.Client, rawURL string, f *os.File) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func progress(ls []*links.Link, perLink []atomic.Int64, done *atomic.Int64, size int64, start time.Time, stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	prev := make([]int64, len(ls))
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			var parts []string
			total := 0.0
			for i := range ls {
				cur := perLink[i].Load()
				mb := links.Mbps(cur-prev[i], time.Second)
				prev[i] = cur
				total += mb
				parts = append(parts, fmt.Sprintf("%s %.1f", ls[i].Name, mb))
			}
			pct := 100 * float64(done.Load()) / float64(size)
			fmt.Printf("\r%5.1f%%  total %.1f Mb/s  [%s]      ", pct, total, strings.Join(parts, " | "))
		}
	}
}
