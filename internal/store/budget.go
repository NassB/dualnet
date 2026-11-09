package store

import (
	"strings"
	"sync"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// A monthly data budget per link (in GB, decimal like mobile plans). When a
// link has used its budget, dualnet stops sending it new connections until the
// next month — unless it is the only usable link: the internet must keep working.
//
// Only the data that goes through dualnet on that link counts, not other
// traffic the phone or the PC uses on its own.

var (
	warnMu sync.Mutex
	warned = map[string]string{} // link -> month in which the warning was given
)

// Budget returns the monthly budget of a link in GB (0 = none).
func (s *Store) Budget(name string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Budgets[name]
}

// SetBudget sets a link's monthly budget in GB (0 or less removes it).
func (s *Store) SetBudget(name string, gb float64) {
	s.mu.Lock()
	if s.settings.Budgets == nil {
		s.settings.Budgets = map[string]float64{}
	}
	if gb <= 0 {
		delete(s.settings.Budgets, name)
	} else {
		s.settings.Budgets[name] = gb
	}
	s.mu.Unlock()
	s.Save()
}

// WarnPercent returns the budget share (50-99) at which a warning is given; 80 by default.
func (s *Store) WarnPercent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.settings.BudgetWarnPercent; p >= 50 && p <= 99 {
		return p
	}
	return 80
}

// SetWarnPercent sets the warning threshold; it reports false outside 50-99.
func (s *Store) SetWarnPercent(p int) bool {
	if p < 50 || p > 99 {
		return false
	}
	s.mu.Lock()
	s.settings.BudgetWarnPercent = p
	s.mu.Unlock()
	s.Save()
	return true
}

// MonthUsage returns the bytes (received + sent) a link carried in the calendar
// month of now.
func (s *Store) MonthUsage(name string, now time.Time) int64 {
	prefix := now.Format("2006-01")
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for day, m := range s.history {
		if strings.HasPrefix(day, prefix) {
			u := m[name]
			total += u.Rx + u.Tx
		}
	}
	return total
}

func otherUsable(ls []*links.Link, except *links.Link) bool {
	for _, l := range ls {
		if l != except && l.Usable() {
			return true
		}
	}
	return false
}

// Enforce applies the budgets: it excludes links that used theirs, brings them
// back when the month changes (or the budget is raised/removed) and reports
// each change through pool.OnChange.
func (s *Store) Enforce(pool *links.Pool, now time.Time) {
	ls := pool.All()
	warnAt := float64(s.WarnPercent()) / 100
	month := now.Format("2006-01")
	type change struct {
		l *links.Link
		e string
	}
	var changes []change
	for _, l := range ls {
		budget := s.Budget(l.Name)
		used := float64(s.MonthUsage(l.Name, now)) / 1e9
		switch {
		case budget > 0 && used >= budget:
			switch {
			case !l.OverBudget() && l.Up() && l.Healthy() && otherUsable(ls, l): // never exclude the last usable link
				l.SetOverBudget(true)
				changes = append(changes, change{l, links.EventOverBudget})
			case l.OverBudget() && !otherUsable(ls, l):
				// the other links are gone: this one has to carry the traffic after all
				l.SetOverBudget(false)
				changes = append(changes, change{l, links.EventBudgetKept})
			}
		default:
			if l.OverBudget() {
				l.SetOverBudget(false)
				changes = append(changes, change{l, links.EventBudgetOK})
			}
			if budget > 0 && used >= warnAt*budget {
				warnMu.Lock()
				first := warned[l.Name] != month
				warned[l.Name] = month
				warnMu.Unlock()
				if first {
					changes = append(changes, change{l, links.EventBudgetWarn})
				}
			}
		}
	}
	if pool.OnChange != nil {
		for _, c := range changes {
			pool.OnChange(c.l, c.e)
		}
	}
}
