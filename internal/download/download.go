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
	"path/filepath"
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
	OutDir        string // used when Output is empty
	Quiet         bool   // no console output (UI mode)
	Stats         *Stats // optional live progress for the UI
}

// Stats exposes a running download's progress to another goroutine (the UI).
type Stats struct {
	mu      sync.Mutex
	file    string
	size    int64
	resumed int64
	perLink []atomic.Int64
}

func (s *Stats) set(file string, size, resumed int64, perLink []atomic.Int64) {
	s.mu.Lock()
	s.file, s.size, s.resumed, s.perLink = file, size, resumed, perLink
	s.mu.Unlock()
}

// Resumed returns the bytes that were already on disk when the download
// started (an earlier, interrupted run); they belong to no link.
func (s *Stats) Resumed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resumed
}

// Snapshot returns the output file, total size and bytes received per link.
func (s *Stats) Snapshot() (file string, size int64, perLink []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	perLink = make([]int64, len(s.perLink))
	for i := range s.perLink {
		perLink[i] = s.perLink[i].Load()
	}
	return s.file, s.size, perLink
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
	start    int64 // first byte of the range: start..pos-1 is on disk
	pos, end int64 // next byte to write, last byte (inclusive)
	link     int   // index of the link downloading it
}

func (t *task) remaining() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.end - t.pos + 1
}

const minSteal = 256 << 10 // don't split ranges smaller than 256 KiB

type sched struct {
	mu     sync.Mutex
	cond   *sync.Cond
	todo   []*task
	active map[*task]bool
	all    []*task // every range ever handed out, to know what is on disk
	have   []span  // what was on disk before this run
	links  int
}

// newSched queues what is missing from [0, size), in chunks; have is what an
// earlier run already wrote (merged spans, nil for a new download).
func newSched(size, chunk int64, nLinks int, have []span) *sched {
	s := &sched{active: map[*task]bool{}, links: nLinks, have: have}
	s.cond = sync.NewCond(&s.mu)
	for _, g := range gaps(size, have) {
		for p := g[0]; p <= g[1]; p += chunk {
			t := &task{start: p, pos: p, end: min(p+chunk-1, g[1])}
			s.todo = append(s.todo, t)
			s.all = append(s.all, t)
		}
	}
	return s
}

// written returns the byte ranges that are on disk now.
func (s *sched) written() []span {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]span(nil), s.have...)
	for _, t := range s.all {
		t.mu.Lock()
		if t.pos > t.start {
			out = append(out, span{t.start, t.pos - 1})
		}
		t.mu.Unlock()
	}
	return merge(out)
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
		if len(s.todo) > 0 && s.worthTaking(li, rate, s.todo[0].remaining()) {
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
			nt := &task{start: mid, pos: mid, end: victim.end, link: li}
			victim.end = mid - 1
			victim.mu.Unlock()
			s.active[nt] = true
			s.all = append(s.all, nt)
			return nt
		}
		if len(s.active) == 0 && len(s.todo) == 0 {
			return nil
		}
		s.cond.Wait()
	}
}

// worthTaking reports whether link li should start n more bytes: near the end, a
// slow link that would still be busy after the others finished everything must
// leave the bytes to them (otherwise the whole download waits for it).
// Caller holds s.mu.
func (s *sched) worthTaking(li int, rate func(int) float64, n int64) bool {
	my, total, fastest := rate(li), 0.0, 0.0
	for i := 0; i < s.links; i++ {
		r := rate(i)
		total += r
		fastest = max(fastest, r)
	}
	if my <= 0 || my >= fastest {
		return true // no measurement yet, or we are the fastest link
	}
	var left int64
	for _, t := range s.todo {
		left += t.remaining()
	}
	for t := range s.active {
		left += t.remaining()
	}
	return float64(n)/my < float64(left)/total
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
	say := func(format string, a ...any) {
		if !opts.Quiet {
			fmt.Printf(format, a...)
		}
	}
	clients := make([]*http.Client, len(ls))
	for i, l := range ls {
		clients[i] = clientFor(l)
	}

	size, rangesOK, name, valid, err := probe(ctx, ls, clients, opts.URL)
	if err != nil {
		return err
	}
	if opts.Output == "" {
		opts.Output = filepath.Join(opts.OutDir, safeName(name))
	}
	release, err := claim(opts.Output)
	if err != nil {
		return err
	}
	defer release()
	part := opts.Output + partSuffix

	if !rangesOK || size <= 0 {
		say("The server does not accept range requests: only one link will be used.\n")
		f, err := os.Create(part)
		if err != nil {
			return err
		}
		err = single(ctx, clients[0], opts.URL, f)
		f.Close()
		if err != nil {
			return err
		}
		return os.Rename(part, opts.Output)
	}
	// What an interrupted run left, if the server still offers the same file.
	have := resumable(opts.Output, opts.URL, size, valid)
	var f *os.File
	if have != nil {
		f, err = os.OpenFile(part, os.O_RDWR, 0o644)
	} else {
		os.Remove(opts.Output + stateSuffix)
		if f, err = os.Create(part); err == nil {
			err = f.Truncate(size)
		}
	}
	if err != nil {
		if f != nil {
			f.Close()
		}
		return err
	}
	defer f.Close()
	resumed := total(have)
	say("File: %s (%s)\n", opts.Output, links.HumanBytes(float64(size)))
	if resumed > 0 {
		say("Resuming: %s already downloaded.\n", links.HumanBytes(float64(resumed)))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sc := newSched(size, opts.ChunkSize, len(ls), have)
	save := func() {
		if valid != "" {
			saveState(opts.Output, state{URL: opts.URL, Size: size, Validator: valid, Done: sc.written()})
		}
	}
	saved := make(chan struct{})
	go func() { // remember regularly what is on disk, to resume after a crash too
		defer close(saved)
		tk := time.NewTicker(2 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				save()
			}
		}
	}()
	// Periodic wake-up: waiting workers re-evaluate (rates change, links die).
	go func() {
		tk := time.NewTicker(200 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				sc.cond.Broadcast()
				return
			case <-tk.C:
				sc.cond.Broadcast()
			}
		}
	}()

	var done atomic.Int64
	done.Store(resumed)
	perLink := make([]atomic.Int64, len(ls))
	if opts.Stats != nil {
		opts.Stats.set(opts.Output, size, resumed, perLink)
	}
	failures := make([]atomic.Int32, len(ls))
	alive := make([]atomic.Int32, len(ls))
	t0 := time.Now()
	// Bytes/s per link; 0 until there is a meaningful measurement (avoids 0/0 = NaN).
	rate := func(i int) float64 {
		el := time.Since(t0).Seconds()
		if el < 0.3 || alive[i].Load() == 0 {
			return 0 // a dead link must not make others wait for it
		}
		return float64(perLink[i].Load()) / el
	}

	var workers sync.WaitGroup
	for li := range ls {
		for w := 0; w < opts.WorkersPerLnk; w++ {
			workers.Add(1)
			alive[li].Add(1)
			go func(li int) {
				defer workers.Done()
				defer alive[li].Add(-1)
				for {
					t := sc.next(ctx, li, rate)
					if t == nil {
						return
					}
					_, err := fetchTask(ctx, clients[li], opts.URL, t, f, func(n int64) {
						done.Add(n)
						perLink[li].Add(n)
					})
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
						say("\n%s: too many errors, link dropped (%v)\n", ls[li], err)
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
	if !opts.Quiet {
		go progress(ls, perLink, &done, size, start, stop)
	}
	workers.Wait()
	close(stop)
	cancel()
	<-saved

	if done.Load() < size {
		save() // the next run of the same URL continues from here
		return fmt.Errorf("incomplete download (%d/%d bytes, %d queued, %d active)", done.Load(), size, len(sc.todo), len(sc.active))
	}
	if err := f.Close(); err != nil {
		return err
	}
	os.Remove(opts.Output + stateSuffix)
	if err := os.Rename(part, opts.Output); err != nil {
		return err
	}
	el := time.Since(start)
	say("\nDone in %s — average %.1f Mb/s\n", el.Round(time.Second), links.Mbps(size-resumed, el))
	for i, l := range ls {
		say("  %-30s %s (%.0f%%)\n", l.String(), links.HumanBytes(float64(perLink[i].Load())), 100*float64(perLink[i].Load())/float64(size))
	}
	return nil
}

// probe asks for the first byte to learn the size, whether ranges work, the
// file name and the validator of this version of the file.
func probe(ctx context.Context, ls []*links.Link, clients []*http.Client, rawURL string) (int64, bool, string, string, error) {
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
					return n, true, name, validator(resp.Header), nil
				}
			}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.ContentLength, false, name, "", nil
		}
		lastErr = fmt.Errorf("HTTP %s", resp.Status)
	}
	return 0, false, "", "", lastErr
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
func fetchTask(ctx context.Context, c *http.Client, rawURL string, t *task, f *os.File, count func(int64)) (int64, error) {
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
			count(int64(n))
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
