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
		DialContext:           l.DialContext,
		MaxIdleConnsPerHost:   16,
		ForceAttemptHTTP2:     false, // one TCP connection per worker keeps links independent
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &http.Client{Transport: uaTransport{tr}}
}

// Some servers reject Go's default "Go-http-client" User-Agent.
type uaTransport struct{ rt http.RoundTripper }

func (u uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", "dualnet/0.1")
	}
	return u.rt.RoundTrip(r)
}

// task is a byte range being downloaded. Its end can shrink while it runs:
// an idle worker steals the second half (see sched.next).
type task struct {
	mu       sync.Mutex
	pos, end int64 // next byte to write, last byte (inclusive)
	link     int   // index of the link downloading it
}

func (t *task) remaining() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.end - t.pos + 1
}

const minSteal = 1 << 20 // don't split ranges smaller than 1 MiB

type sched struct {
	mu     sync.Mutex
	cond   *sync.Cond
	todo   []*task
	active map[*task]bool
}

func newSched(size, chunk int64) *sched {
	s := &sched{active: map[*task]bool{}}
	s.cond = sync.NewCond(&s.mu)
	for p := int64(0); p < size; p += chunk {
		s.todo = append(s.todo, &task{pos: p, end: min(p+chunk, size) - 1})
	}
	return s
}

// next returns a range to download: a queued one, or else the second half of the
// in-flight range with the most bytes left (so a fast link finishes the job of a
// slow one instead of idling). Returns nil when everything is done.
// rate(i) is link i's measured throughput; a link only steals from slower links,
// otherwise a slow link would grab work back from a fast one.
func (s *sched) next(ctx context.Context, li int, rate func(int) float64) *task {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if len(s.todo) > 0 {
			t := s.todo[0]
			s.todo = s.todo[1:]
			t.link = li
			s.active[t] = true
			return t
		}
		var victim *task
		var best int64
		myRate := rate(li)
		for t := range s.active {
			if t.link != li && rate(t.link) >= myRate*0.8 {
				continue
			}
			if r := t.remaining(); r > best {
				victim, best = t, r
			}
		}
		if victim != nil && best >= 2*minSteal {
			victim.mu.Lock()
			mid := victim.pos + (victim.end-victim.pos+1)/2
			nt := &task{pos: mid, end: victim.end, link: li}
			victim.end = mid - 1
			victim.mu.Unlock()
			s.active[nt] = true
			return nt
		}
		if len(s.active) == 0 {
			return nil
		}
		s.cond.Wait()
	}
}

// finish releases a task; any unfinished part goes back to the queue.
func (s *sched) finish(t *task) {
	s.mu.Lock()
	delete(s.active, t)
	if t.remaining() > 0 {
		s.todo = append(s.todo, t)
	}
	s.mu.Unlock()
	s.cond.Broadcast()
}

type rateLimited struct {
	status string
	after  time.Duration
}

func (r *rateLimited) Error() string { return "HTTP " + r.status }

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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sc := newSched(size, opts.ChunkSize)
	go func() { <-ctx.Done(); sc.cond.Broadcast() }()

	var done atomic.Int64
	perLink := make([]atomic.Int64, len(ls))
	failures := make([]atomic.Int32, len(ls))
	t0 := time.Now()
	rate := func(i int) float64 { return float64(perLink[i].Load()) / time.Since(t0).Seconds() }

	var workers sync.WaitGroup
	for li := range ls {
		for w := 0; w < opts.WorkersPerLnk; w++ {
			workers.Add(1)
			go func(li int) {
				defer workers.Done()
				for {
					t := sc.next(ctx, li, rate)
					if t == nil {
						return
					}
					n, err := fetchTask(ctx, clients[li], opts.URL, t, f)
					done.Add(n)
					perLink[li].Add(n)
					sc.finish(t)
					if err == nil {
						failures[li].Store(0)
						continue
					}
					if ctx.Err() != nil {
						return
					}
					fails := failures[li].Add(1)
					if fails > 8 {
						fmt.Printf("\n%s : trop d'erreurs, lien abandonné (%v)\n", ls[li], err)
						return
					}
					// Exponential backoff (servers answer 429 to bursts); a server-given
					// Retry-After wins.
					wait := time.Duration(1<<min(fails, 5)) * 500 * time.Millisecond
					var rl *rateLimited
					if errors.As(err, &rl) && rl.after > 0 {
						wait = rl.after
					}
					select {
					case <-time.After(wait):
					case <-ctx.Done():
						return
					}
				}
			}(li)
		}
	}

	start := time.Now()
	stop := make(chan struct{})
	go progress(ls, perLink, &done, size, start, stop)
	workers.Wait()
	close(stop)

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

// fetchTask downloads t from t.pos until t.end (which a stealer may lower meanwhile).
// It returns the number of bytes written.
func fetchTask(ctx context.Context, c *http.Client, rawURL string, t *task, f *os.File) (int64, error) {
	// A stalled range is abandoned and requeued instead of blocking forever.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	t.mu.Lock()
	start, reqEnd := t.pos, t.end
	t.mu.Unlock()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, reqEnd))
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			return 0, &rateLimited{status: resp.Status, after: time.Duration(secs) * time.Second}
		}
		return 0, fmt.Errorf("HTTP %s", resp.Status)
	}
	buf := make([]byte, 64*1024)
	var written int64
	for {
		n, rerr := resp.Body.Read(buf)
		t.mu.Lock()
		if left := t.end - t.pos + 1; int64(n) > left {
			n = int(left)
		}
		if n > 0 {
			// Written under the lock so a concurrent steal never overlaps these bytes.
			if _, err := f.WriteAt(buf[:n], t.pos); err != nil {
				t.mu.Unlock()
				return written, err
			}
			t.pos += int64(n)
			written += int64(n)
		}
		finished, pos := t.pos > t.end, t.pos
		t.mu.Unlock()
		if finished {
			if pos > reqEnd {
				// Whole requested range read: drain so the keep-alive connection is
				// reused (servers throttle bursts of new TLS connections).
				io.CopyN(io.Discard, resp.Body, 64*1024)
			}
			return written, nil
		}
		if rerr == io.EOF {
			return written, io.ErrUnexpectedEOF
		}
		if rerr != nil {
			return written, rerr
		}
	}
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
