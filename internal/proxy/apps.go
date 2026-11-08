package proxy

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/ratelimit"
)

// UnknownApp names the traffic whose owning program could not be found.
const UnknownApp = "unknown"

// AppStats counts what one application does through dualnet.
type AppStats struct {
	Name string

	active, total, rx, tx, last atomic.Int64
	mu                          sync.Mutex
	rxByLink                    map[string]int64

	// download cap shared by all the application's connections
	lim     ratelimit.Bucket
	limFn   func() int // current cap in Mb/s, asked again every second
	limAt   atomic.Int64
	limMbps atomic.Int64
	limitMu sync.Mutex
}

// bindLimit tells the application where to read its cap from.
func (a *AppStats) bindLimit(fn func() int) {
	if fn == nil {
		return
	}
	a.limitMu.Lock()
	a.limFn = fn
	a.limitMu.Unlock()
	a.refreshLimit()
}

func (a *AppStats) refreshLimit() {
	a.limAt.Store(time.Now().UnixNano())
	a.limitMu.Lock()
	fn := a.limFn
	a.limitMu.Unlock()
	if fn == nil {
		return
	}
	if m := int64(fn()); m != a.limMbps.Swap(m) {
		a.lim.Set(float64(m) * 1e6 / 8)
	}
}

// throttle accounts n received bytes against the application's cap.
func (a *AppStats) throttle(n int) {
	if time.Since(time.Unix(0, a.limAt.Load())) > time.Second {
		a.refreshLimit()
	}
	a.lim.Wait(n)
}

func (a *AppStats) touch()  { a.last.Store(time.Now().UnixNano()) }
func (a *AppStats) opened() { a.active.Add(1); a.total.Add(1); a.touch() }
func (a *AppStats) closed() { a.active.Add(-1); a.touch() }
func (a *AppStats) addTx(n int) {
	a.tx.Add(int64(n))
	a.touch()
}
func (a *AppStats) addRx(link string, n int) {
	a.rx.Add(int64(n))
	a.mu.Lock()
	a.rxByLink[link] += int64(n)
	a.mu.Unlock()
	a.touch()
}

// AppView is a copy of an application's counters.
type AppView struct {
	Name     string
	Active   int64            // connections open now
	Total    int64            // connections since dualnet started
	Rx, Tx   int64            // bytes received / sent
	RxByLink map[string]int64 // bytes received through each link
	Limit    int              // download cap in Mb/s (0 = none)
}

// AppRegistry holds the counters of every application seen recently.
type AppRegistry struct {
	mu   sync.Mutex
	apps map[string]*AppStats
}

func NewAppRegistry() *AppRegistry { return &AppRegistry{apps: map[string]*AppStats{}} }

// Get returns (creating it if needed) the counters of an application. Names are
// compared without regard to case; the first spelling seen is displayed.
func (r *AppRegistry) Get(name string) *AppStats {
	key := strings.ToLower(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.apps[key]
	if a == nil {
		a = &AppStats{Name: name, rxByLink: map[string]int64{}}
		a.touch()
		r.apps[key] = a
	}
	return a
}

// Snapshot returns the busiest applications first (by data moved), at most 20,
// and forgets those with no open connection that were idle for 10 minutes.
func (r *AppRegistry) Snapshot() []AppView {
	cut := time.Now().Add(-10 * time.Minute).UnixNano()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AppView, 0, len(r.apps))
	for k, a := range r.apps {
		if a.active.Load() <= 0 && a.last.Load() < cut {
			delete(r.apps, k)
			continue
		}
		v := AppView{Name: a.Name, Active: a.active.Load(), Total: a.total.Load(), Rx: a.rx.Load(), Tx: a.tx.Load(), RxByLink: map[string]int64{}, Limit: int(a.limMbps.Load())}
		a.mu.Lock()
		for l, b := range a.rxByLink {
			v.RxByLink[l] = b
		}
		a.mu.Unlock()
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		bi, bj := out[i].Rx+out[i].Tx, out[j].Rx+out[j].Tx
		if bi != bj {
			return bi > bj
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// allowFor returns the filter of the links an application may use, or nil when
// it is not restricted.
func (s *Server) allowFor(app string) func(*links.Link) bool {
	if app == "" || app == UnknownApp || s.Rules == nil {
		return nil
	}
	names := s.Rules(app)
	if len(names) == 0 {
		return nil
	}
	return func(l *links.Link) bool {
		for _, n := range names {
			if n == l.Name {
				return true
			}
		}
		return false
	}
}

// filterLinks keeps the links an application may use. If none of them is
// usable it returns them all: a rule restricts, it must never cut an app off.
func filterLinks(order []*links.Link, allow func(*links.Link) bool) []*links.Link {
	if allow == nil {
		return order
	}
	var out []*links.Link
	for _, l := range order {
		if allow(l) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return order
	}
	return out
}
