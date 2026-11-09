// Package store keeps dualnet's data between runs, in %APPDATA%\dualnet:
// daily traffic per link (history.json) and settings such as speed caps
// (settings.json).
package store

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// Usage is the traffic of one link over one day.
type Usage struct {
	Rx int64 `json:"rx"`
	Tx int64 `json:"tx"`
}

// Settings are the user's choices that survive a restart.
type Settings struct {
	Limits  map[string]float64 `json:"limits"`            // link name -> Mb/s cap
	Enabled *bool              `json:"enabled,omitempty"` // main switch; nil = on
	// UpdateCheck: ask GitHub for new releases once a day; nil = yes.
	UpdateCheck *bool `json:"updateCheck,omitempty"`
	// Overflow: links that only serve when the others are busy.
	Overflow map[string]bool `json:"overflow,omitempty"`
	// SpeedTests: the last speed test of each link.
	SpeedTests map[string]SpeedTest `json:"speedTests,omitempty"`
	// OnboardingDone: the first-run checklist was dismissed.
	OnboardingDone *bool `json:"onboardingDone,omitempty"`
	// Rules: applications restricted to one link (transparent mode).
	Rules []Rule `json:"rules,omitempty"`
	// DomainRules: destination domains sent over one link.
	DomainRules []DomainRule `json:"domainRules,omitempty"`
	// BudgetWarnPercent: when a link has used this share of its budget, warn (0 = 80).
	BudgetWarnPercent int `json:"budgetWarnPercent,omitempty"`
	// Budgets: monthly data budget per link in GB (absent = none).
	Budgets map[string]float64 `json:"budgets,omitempty"`
	// Notify: Windows notifications for link events; nil = yes.
	Notify *bool `json:"notify,omitempty"`
}

// NotifyEnabled reports whether link notifications are on (default).
func (s *Store) NotifyEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Notify == nil || *s.settings.Notify
}

// SetNotify saves the notification choice.
func (s *Store) SetNotify(on bool) {
	s.mu.Lock()
	s.settings.Notify = &on
	s.mu.Unlock()
	s.Save()
}

// UpdateCheckEnabled reports whether the daily update check is on (default).
func (s *Store) UpdateCheckEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.UpdateCheck == nil || *s.settings.UpdateCheck
}

// SetUpdateCheck saves the update-check choice.
func (s *Store) SetUpdateCheck(on bool) {
	s.mu.Lock()
	s.settings.UpdateCheck = &on
	s.mu.Unlock()
	s.Save()
}

// Enabled reports the saved main-switch position (on by default).
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Enabled == nil || *s.settings.Enabled
}

// SetEnabled saves the main-switch position.
func (s *Store) SetEnabled(on bool) {
	s.mu.Lock()
	s.settings.Enabled = &on
	s.mu.Unlock()
	s.Save()
}

type Store struct {
	Dir string

	mu       sync.Mutex
	history  map[string]map[string]Usage // day (2006-01-02) -> link -> usage
	settings Settings
	last     map[*links.Link]Usage
}

// Open loads the store from dir (created if needed).
func Open(dir string) *Store {
	s := &Store{Dir: dir, history: map[string]map[string]Usage{}, last: map[*links.Link]Usage{}}
	s.settings.Limits = map[string]float64{}
	os.MkdirAll(dir, 0o755)
	readJSON(filepath.Join(dir, "history.json"), &s.history)
	readJSON(filepath.Join(dir, "settings.json"), &s.settings)
	if s.settings.Limits == nil {
		s.settings.Limits = map[string]float64{}
	}
	return s
}

// DefaultDir is %APPDATA%\dualnet, or the folder named by the DUALNET_DATA
// environment variable (to keep a copy apart, or for tests).
func DefaultDir() string {
	if d := os.Getenv("DUALNET_DATA"); d != "" {
		return d
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "dualnet")
}

const maxJSONSize = 16 << 20

// readJSON loads path into v. A missing file is normal; anything else (too big,
// unreadable, corrupt) is logged and v is left as it was.
func readJSON(path string, v any) {
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("store: %v", err)
		}
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxJSONSize+1))
	if err != nil {
		log.Printf("store: reading %s: %v", path, err)
		return
	}
	if len(b) > maxJSONSize {
		log.Printf("store: %s is larger than %d bytes, ignored", path, maxJSONSize)
		return
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Printf("store: %s is not valid JSON, ignored: %v", path, err)
	}
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Record adds the traffic since the previous call to today's totals.
func (s *Store) Record(ls []*links.Link, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	day := now.Format("2006-01-02")
	if s.history[day] == nil {
		s.history[day] = map[string]Usage{}
	}
	for _, l := range ls {
		cur := Usage{Rx: l.BytesIn.Load(), Tx: l.BytesOut.Load()}
		prev, seen := s.last[l]
		s.last[l] = cur
		if !seen {
			continue // counters start at 0 when dualnet starts; first sample is the baseline
		}
		u := s.history[day][l.Name]
		u.Rx += cur.Rx - prev.Rx
		u.Tx += cur.Tx - prev.Tx
		s.history[day][l.Name] = u
	}
	// keep one year
	cutoff := now.AddDate(-1, 0, 0).Format("2006-01-02")
	for d := range s.history {
		if d < cutoff {
			delete(s.history, d)
		}
	}
}

// Save writes the history and settings to disk.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(filepath.Join(s.Dir, "history.json"), s.history); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.Dir, "settings.json"), s.settings)
}

// Day is one row of the history view.
type Day struct {
	Date  string           `json:"date"`
	Links map[string]Usage `json:"links"`
}

// History returns the last n days, most recent first.
func (s *Store) History(n int) []Day {
	s.mu.Lock()
	defer s.mu.Unlock()
	var days []string
	for d := range s.history {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	if len(days) > n {
		days = days[:n]
	}
	out := []Day{}
	for _, d := range days {
		m := map[string]Usage{}
		for k, v := range s.history[d] {
			m[k] = v
		}
		out = append(out, Day{Date: d, Links: m})
	}
	return out
}

// RememberLimits stores the links' current caps.
func (s *Store) RememberLimits(ls []*links.Link) {
	s.mu.Lock()
	for _, l := range ls {
		s.settings.Limits[l.Name] = float64(l.LimitMbps())
	}
	s.mu.Unlock()
	s.Save()
}

// ApplyLimit sets a newly seen link's cap from the saved settings.
func (s *Store) ApplyLimit(l *links.Link) {
	s.mu.Lock()
	v := s.settings.Limits[l.Name]
	s.mu.Unlock()
	if v > 0 {
		l.SetLimitMbps(v)
	}
}

// Run records every 10 s and saves every minute until ctx is done (then saves).
func (s *Store) Run(ctx context.Context, pool *links.Pool) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	s.Record(pool.All(), time.Now())
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			s.Record(pool.All(), time.Now())
			s.Save()
			return
		case now := <-t.C:
			s.Record(pool.All(), now)
			s.Enforce(pool, now)
			if i%6 == 0 {
				s.Save()
			}
		}
	}
}

// OnboardingDone reports whether the first-run checklist was dismissed.
func (s *Store) OnboardingDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.OnboardingDone != nil && *s.settings.OnboardingDone
}

// SetOnboardingDone saves whether the first-run checklist is dismissed.
func (s *Store) SetOnboardingDone(done bool) {
	s.mu.Lock()
	s.settings.OnboardingDone = &done
	s.mu.Unlock()
	s.Save()
}

// SpeedTest is the result of a link's last speed test.
type SpeedTest struct {
	Mbps float64   `json:"mbps"`
	At   time.Time `json:"at"`
}

// SpeedTest returns a link's last speed test.
func (s *Store) SpeedTest(name string) (SpeedTest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.settings.SpeedTests[name]
	return r, ok
}

// SetSpeedTest saves a link's speed test.
func (s *Store) SetSpeedTest(name string, mbps float64, at time.Time) {
	s.mu.Lock()
	if s.settings.SpeedTests == nil {
		s.settings.SpeedTests = map[string]SpeedTest{}
	}
	s.settings.SpeedTests[name] = SpeedTest{Mbps: mbps, At: at}
	s.mu.Unlock()
	s.Save()
}

// IsOverflow reports whether a link only serves when the others are busy.
func (s *Store) IsOverflow(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Overflow[name]
}

// SetOverflow sets whether a link only serves when the others are busy.
func (s *Store) SetOverflow(name string, on bool) {
	s.mu.Lock()
	if s.settings.Overflow == nil {
		s.settings.Overflow = map[string]bool{}
	}
	if on {
		s.settings.Overflow[name] = true
	} else {
		delete(s.settings.Overflow, name)
	}
	s.mu.Unlock()
	s.Save()
}
