package links

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// Pool keeps the set of links current while dualnet runs: an interface that
// connects later (Wi-Fi coming back, phone plugged in) is added, one that
// disconnects is marked down and no longer used, and a changed IP is followed.
// Link objects are never removed, so counters and UI history stay stable.
type Pool struct {
	selectors []string
	mu        sync.Mutex
	all       []*Link
	static    bool
	// OnChange is called (without the lock) when a link appears, goes down or comes back.
	OnChange func(l *Link, event string)
}

// NewPool discovers the current links. selectors works as in Discover.
func NewPool(selectors []string) *Pool {
	p := &Pool{selectors: selectors}
	p.Refresh()
	return p
}

// All returns every link ever seen, connected or not, in discovery order.
func (p *Pool) All() []*Link {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Link(nil), p.all...)
}

// Up returns the links currently usable.
func (p *Pool) Up() []*Link {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Link
	for _, l := range p.all {
		if l.Up() {
			out = append(out, l)
		}
	}
	return out
}

// Refresh re-reads the interfaces once.
func (p *Pool) Refresh() {
	if p.static {
		return
	}
	current := map[string]net.IP{}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		ip := firstIPv4(ifc)
		if ip == nil || !p.wanted(ifc.Name, ip) {
			continue
		}
		current[ifc.Name] = ip
	}

	type change struct {
		l  *Link
		ev string
	}
	var changes []change
	p.mu.Lock()
	known := map[string]*Link{}
	for _, l := range p.all {
		known[l.Name] = l
	}
	for name, ip := range current {
		l, ok := known[name]
		if !ok {
			l = newLink(name, ip)
			p.all = append(p.all, l)
			changes = append(changes, change{l, "ajouté"})
			continue
		}
		l.setIP(ip)
		if !l.up.Swap(true) {
			changes = append(changes, change{l, "reconnecté"})
		}
	}
	for _, l := range p.all {
		if _, ok := current[l.Name]; !ok && l.up.Swap(false) {
			changes = append(changes, change{l, "déconnecté"})
		}
	}
	p.mu.Unlock()
	if p.OnChange != nil {
		for _, c := range changes {
			p.OnChange(c.l, c.ev)
		}
	}
}

// Watch refreshes every interval until ctx is done.
func (p *Pool) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Refresh()
		}
	}
}

func (p *Pool) wanted(name string, ip net.IP) bool {
	// Never use dualnet's own virtual adapter (transparent mode) as a link: it would loop.
	if strings.EqualFold(name, "dualnet") || ip.Equal(net.IPv4(198, 18, 0, 1)) {
		return false
	}
	if len(p.selectors) > 0 {
		return matches(p.selectors, name, ip)
	}
	lname := strings.ToLower(name)
	for _, s := range skipNames {
		if strings.Contains(lname, s) {
			return false
		}
	}
	return true
}

// PoolOf returns a fixed pool made of ls (tests); Refresh does nothing on it.
func PoolOf(ls ...*Link) *Pool { return &Pool{all: ls, static: true} }

// SetUp marks a link connected or not (tests).
func (l *Link) SetUp(up bool) { l.up.Store(up) }
