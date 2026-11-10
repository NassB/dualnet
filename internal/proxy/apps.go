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

	// speed caps shared by all the application's connections, one for what it
	// downloads and one for what it uploads
	down, up appCap
}

// appCap is one speed cap of an application. of gives the cap in Mb/s (nil = no
// cap at all) and is asked again every second, so a change made in the
// interface applies to connections that are already open.
type appCap struct {
	of   func(app string) int
	b    ratelimit.Bucket
	at   atomic.Int64
	mbps atomic.Int64
}

// wait accounts n bytes against the cap, pausing the caller if it is exceeded.
func (c *appCap) wait(app string, n int) {
	if c.of == nil {
		return
	}
	if time.Since(time.Unix(0, c.at.Load())) > time.Second {
		c.at.Store(time.Now().UnixNano())
		if m := int64(c.of(app)); m != c.mbps.Swap(m) {
			c.b.Set(float64(m) * 1e6 / 8)
		}
	}
	c.b.Wait(n)
}

// throttle accounts n received bytes against the application's download cap.
func (a *AppStats) throttle(n int) { a.down.wait(a.Name, n) }

// throttleUp accounts n sent bytes against the application's upload cap.
func (a *AppStats) throttleUp(n int) { a.up.wait(a.Name, n) }

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
	UpLimit  int              // upload cap in Mb/s (0 = none)
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
	return r.get(name, nil, nil)
}

// get is Get, giving a new application the functions that read its caps.
func (r *AppRegistry) get(name string, downOf, upOf func(string) int) *AppStats {
	key := strings.ToLower(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.apps[key]
	if a == nil {
		a = &AppStats{Name: name, rxByLink: map[string]int64{}}
		a.down.of, a.up.of = downOf, upOf
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
		v := AppView{Name: a.Name, Active: a.active.Load(), Total: a.total.Load(), Rx: a.rx.Load(), Tx: a.tx.Load(), RxByLink: map[string]int64{}, Limit: int(a.down.mbps.Load()), UpLimit: int(a.up.mbps.Load())}
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

// LowestPing, in place of a link name in an application's rule, sends each new
// connection of the application over the link that answers fastest at that
// moment (games, calls), instead of the one with the most spare capacity.
const LowestPing = "*ping"

// wantsLowPing reports whether an application's rule asks for the lowest ping.
func (s *Server) wantsLowPing(app string) bool {
	if app == "" || app == UnknownApp || s.Rules == nil {
		return false
	}
	names := s.Rules(app)
	return len(names) > 0 && names[0] == LowestPing
}

// allowFor returns the filter of the links an application may use, or nil when
// it is not restricted.
func (s *Server) allowFor(app string) func(*links.Link) bool {
	if app == "" || app == UnknownApp || s.Rules == nil {
		return nil
	}
	names := s.Rules(app)
	if len(names) == 0 || names[0] == LowestPing {
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
