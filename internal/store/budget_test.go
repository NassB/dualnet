package store

import (
	"net"
	"testing"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

func TestBudgetEnforcement(t *testing.T) {
	warned = map[string]string{} // package-level: start clean
	s := Open(t.TempDir())
	wifi := links.New("Wi-Fi", net.IPv4(192, 168, 1, 2))
	phone := links.New("Phone", net.IPv4(10, 0, 0, 2))
	pool := links.PoolOf(wifi, phone)
	var events []string
	pool.OnChange = func(l *links.Link, ev string) { events = append(events, l.Name+":"+ev) }

	nov := time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)
	use := func(day, link string, gb float64) {
		if s.history[day] == nil {
			s.history[day] = map[string]Usage{}
		}
		s.history[day][link] = Usage{Rx: int64(gb * 1e9 * 0.9), Tx: int64(gb * 1e9 * 0.1)}
	}
	use("2026-11-03", "Phone", 20)
	use("2026-10-30", "Phone", 500) // last month: must not count

	if got := s.MonthUsage("Phone", nov); got < 19_990_000_000 || got > 20_010_000_000 {
		t.Fatalf("month usage = %d, want ~20e9 (October excluded)", got)
	}

	// no budget: nothing happens
	s.Enforce(pool, nov)
	if phone.OverBudget() || len(events) != 0 {
		t.Fatalf("no budget set but: over=%v events=%v", phone.OverBudget(), events)
	}

	// 20 GB used of 50: under 80 %, quiet
	s.SetBudget("Phone", 50)
	s.Enforce(pool, nov)
	if phone.OverBudget() || len(events) != 0 {
		t.Fatalf("40%% used: over=%v events=%v", phone.OverBudget(), events)
	}

	// 41 GB of 50: warned once, still in use
	use("2026-11-04", "Phone", 21)
	s.Enforce(pool, nov)
	s.Enforce(pool, nov)
	if phone.OverBudget() || len(events) != 1 || events[0] != "Phone:"+links.EventBudgetWarn {
		t.Fatalf("82%% used: over=%v events=%v, want exactly one warning", phone.OverBudget(), events)
	}

	// 61 GB of 50: excluded, and no longer offered by the pool
	use("2026-11-05", "Phone", 20)
	s.Enforce(pool, nov)
	if !phone.OverBudget() || phone.Usable() {
		t.Fatal("budget used: the link must be excluded")
	}
	if up := pool.Up(); len(up) != 1 || up[0] != wifi {
		t.Fatalf("pool.Up() = %v, want only Wi-Fi", up)
	}
	if events[len(events)-1] != "Phone:"+links.EventOverBudget {
		t.Fatalf("events = %v", events)
	}

	// a new month brings it back
	dec := time.Date(2026, 12, 1, 8, 0, 0, 0, time.Local)
	s.Enforce(pool, dec)
	if phone.OverBudget() || len(pool.Up()) != 2 || events[len(events)-1] != "Phone:"+links.EventBudgetOK {
		t.Fatalf("new month: over=%v up=%d events=%v", phone.OverBudget(), len(pool.Up()), events)
	}

	// raising the budget also brings it back
	s.Enforce(pool, nov)
	if !phone.OverBudget() {
		t.Fatal("back in November it is over budget again")
	}
	s.SetBudget("Phone", 100)
	s.Enforce(pool, nov)
	if phone.OverBudget() {
		t.Fatal("budget raised to 100 GB: the link must be usable again")
	}
}

// The internet must keep working: the last usable link is never excluded, and
// an excluded link is released when the others are gone.
func TestBudgetNeverExcludesTheLastLink(t *testing.T) {
	warned = map[string]string{}
	s := Open(t.TempDir())
	a := links.New("A", net.IPv4(10, 0, 0, 1))
	b := links.New("B", net.IPv4(10, 0, 0, 2))
	pool := links.PoolOf(a, b)
	var events []string
	pool.OnChange = func(l *links.Link, ev string) { events = append(events, l.Name+":"+ev) }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)
	s.history["2026-11-02"] = map[string]Usage{"A": {Rx: 200e9}, "B": {Rx: 300e9}}
	s.SetBudget("A", 10)
	s.SetBudget("B", 10)

	s.Enforce(pool, now)
	if up := pool.Up(); len(up) != 1 || up[0] != b {
		t.Fatalf("both over budget: usable = %v, want exactly B (A excluded first)", up)
	}

	// B disappears: A must carry the traffic after all
	b.SetUp(false)
	s.Enforce(pool, now)
	if a.OverBudget() || len(pool.Up()) != 1 || pool.Up()[0] != a {
		t.Fatalf("B down: A over=%v usable=%v, want A released", a.OverBudget(), pool.Up())
	}
	if events[len(events)-1] != "A:"+links.EventBudgetKept {
		t.Fatalf("events = %v, want the 'kept' notice last", events)
	}

	// B comes back: A is excluded again, B stays in use
	b.SetUp(true)
	s.Enforce(pool, now)
	if !a.OverBudget() || b.OverBudget() || len(pool.Up()) != 1 {
		t.Fatalf("B back: A over=%v B over=%v usable=%d", a.OverBudget(), b.OverBudget(), len(pool.Up()))
	}
}

func TestBudgetPersisted(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	s.SetBudget("Phone", 30)
	s.SetBudget("Gone", 5)
	s.SetBudget("Gone", 0)
	r := Open(dir)
	if r.Budget("Phone") != 30 || r.Budget("Gone") != 0 {
		t.Fatalf("budgets after reload: Phone=%v Gone=%v", r.Budget("Phone"), r.Budget("Gone"))
	}
}

func TestWarnPercent(t *testing.T) {
	s := Open(t.TempDir())
	if s.WarnPercent() != 80 {
		t.Fatal("default must be 80")
	}
	if s.SetWarnPercent(49) || s.SetWarnPercent(100) {
		t.Fatal("out-of-range value accepted")
	}
	if !s.SetWarnPercent(90) || Open(s.Dir).WarnPercent() != 90 {
		t.Fatal("threshold not saved")
	}
}
