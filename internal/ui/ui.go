// Package ui serves a local web dashboard: live per-link throughput, the proxy,
// and downloads started from the page.
package ui

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NassB/dualnet/internal/doctor"
	"github.com/NassB/dualnet/internal/download"
	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/notify"
	"github.com/NassB/dualnet/internal/store"
	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/update"
)

//go:embed index.html
var indexHTML []byte

type Server struct {
	Pool      *links.Pool
	ProxyAddr string
	OutDir    string
	Tun       *TunControl
	Store     *store.Store
	Version   string
	Autostart *Autostart
	Update    *update.Checker
	Notify    *notify.Notifier
	// DoctorEnv replaces the real system probes of the installation check (tests).
	DoctorEnv func() doctor.Env
	// SpeedTestURL and SpeedWindow replace the speed test's server and length (tests).
	SpeedTestURL string
	SpeedWindow  time.Duration
	speedMu      sync.Mutex
	// Native is true when the page is shown in dualnet's own window.
	Native bool
	// OnSettings is called after a setting changed in the UI (to save it).
	OnSettings func()

	mu     sync.Mutex
	jobs   []*job
	nextID int
}

type job struct {
	id      int
	url     string
	state   string // running, done, error, cancelled
	err     string
	started time.Time
	ended   time.Time
	stats   *download.Stats
	links   []*links.Link // links used, in the order of stats' per-link counters
	cancel  context.CancelFunc
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page())
	})
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/download", s.startDownload)
	mux.HandleFunc("POST /api/cancel", s.cancelJob)
	mux.HandleFunc("POST /api/reveal", s.reveal)
	mux.HandleFunc("POST /api/sysproxy", s.setSysProxy)
	mux.HandleFunc("POST /api/transparent", s.setTun)
	mux.HandleFunc("POST /api/limit", s.setLimit)
	mux.HandleFunc("POST /api/budget", s.setBudget)
	mux.HandleFunc("POST /api/rule", s.setRule)
	mux.HandleFunc("POST /api/applimit", s.setAppLimit)
	mux.HandleFunc("GET /api/doctor", s.getDoctor)
	mux.HandleFunc("POST /api/speedtest", s.speedTest)
	mux.HandleFunc("POST /api/role", s.setRole)
	mux.HandleFunc("POST /api/onboarding", s.setOnboarding)
	mux.HandleFunc("POST /api/system", s.setSystem)
	mux.HandleFunc("POST /api/power", s.setPower)
	mux.HandleFunc("POST /api/autostart", s.setAutostart)
	mux.HandleFunc("POST /api/update/open", s.openUpdate)
	mux.HandleFunc("POST /api/updatecheck", s.setUpdateCheck)
	mux.HandleFunc("POST /api/notify", s.setNotify)
	mux.HandleFunc("POST /api/notify/test", s.testNotify)
	mux.HandleFunc("GET /api/diagnostics", s.getDiagnostics)
	mux.HandleFunc("GET /api/log", s.getLog)
	mux.HandleFunc("POST /api/logfolder", s.openLogFolder)
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Store.History(30))
	})
	return guard(mux)
}

type linkView struct {
	Name      string  `json:"name"`
	IP        string  `json:"ip"`
	Up        bool    `json:"up"`
	Healthy   bool    `json:"healthy"`
	Ping      int64   `json:"ping"`
	Limit     int64   `json:"limit"`
	Budget    float64 `json:"budget"`  // monthly data budget in GB (0 = none)
	MonthGB   float64 `json:"monthGB"` // data used this month, GB
	Over      bool    `json:"overBudget"`
	Overflow  bool    `json:"overflow"`  // the link only serves when the others are busy
	Standby   bool    `json:"standby"`   // ...and is idle right now
	SpeedMbps float64 `json:"speedMbps"` // last speed test
	SpeedAt   int64   `json:"speedAt"`   // when, unix milliseconds
	RxMbps    float64 `json:"rxMbps"`
	TxMbps    float64 `json:"txMbps"`
	Active    int64   `json:"active"`
	TotalRx   int64   `json:"totalRx"`
	TotalTx   int64   `json:"totalTx"`
}

type jobView struct {
	ID      int     `json:"id"`
	URL     string  `json:"url"`
	File    string  `json:"file"`
	Size    int64   `json:"size"`
	Done    int64   `json:"done"`
	PerLink []int64 `json:"perLink"`
	Mbps    float64 `json:"mbps"`
	State   string  `json:"state"`
	Err     string  `json:"err,omitempty"`
	Elapsed float64 `json:"elapsed"`
}

type snapshot struct {
	T     int64      `json:"t"`
	Links []linkView `json:"links"`
	Jobs  []jobView  `json:"jobs"`
	Proxy string     `json:"proxy"`
	Dir   string     `json:"dir"`
	Sys   sysView    `json:"sys"`
	Tun   tunView    `json:"tun"`
	Sys2  systemView `json:"system"`
	Ver   string     `json:"version"`
	Power powerView  `json:"power"`
	Upd   updateView `json:"update"`
	Notif bool       `json:"notify"`
	Onb   bool       `json:"onboarded"`
	Apps  []appView  `json:"apps"`
	Rules []ruleView `json:"rules"`
	Auto  *bool      `json:"autostart,omitempty"` // nil = option not available
}

// sysView is the Windows proxy state: on/off, its address, and whether it points to us.
type sysView struct {
	On     bool   `json:"on"`
	Server string `json:"server"`
	Ours   bool   `json:"ours"`
	Err    string `json:"err,omitempty"`
}

func (s *Server) sysState() sysView {
	on, server, err := sysproxy.Status()
	v := sysView{On: on, Server: server, Ours: on && server == s.ProxyAddr}
	if err != nil {
		v.Err = err.Error()
	}
	return v
}

// setSysProxy turns the Windows proxy on (pointing to dualnet) or restores the previous settings.
func (s *Server) setSysProxy(w http.ResponseWriter, r *http.Request) {
	var err error
	if r.URL.Query().Get("on") == "1" {
		if s.ProxyAddr == "" {
			http.Error(w, "dualnet proxy is disabled", http.StatusBadRequest)
			return
		}
		err = sysproxy.Enable(s.ProxyAddr)
	} else {
		err = sysproxy.Disable()
		if err == nil && s.ProxyAddr != "" {
			err = sysproxy.ClearOurs(s.ProxyAddr) // a leftover that has no backup to restore
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// events streams a snapshot every 500 ms (Server-Sent Events). Rates are computed
// per client from counter deltas.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	const every = 500 * time.Millisecond
	prevRx := map[*links.Link]int64{}
	prevTx := map[*links.Link]int64{}
	prevJob := map[int]int64{}
	prevApp := map[string]int64{}
	for _, l := range s.Pool.All() {
		prevRx[l], prevTx[l] = l.BytesIn.Load(), l.BytesOut.Load()
	}
	last := time.Now()
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case now := <-tk.C:
			dt := now.Sub(last)
			last = now
			snap := snapshot{T: now.UnixMilli(), Proxy: s.ProxyAddr, Dir: s.OutDir, Links: []linkView{}, Jobs: []jobView{}, Sys: s.sysState(), Tun: s.Tun.view(), Sys2: s.systemState(), Ver: s.Version, Power: s.powerState(), Auto: s.autostartState(), Upd: s.updateState(), Notif: s.Store == nil || s.Store.NotifyEnabled(), Onb: s.Store != nil && s.Store.OnboardingDone()}
			all := s.Pool.All()
			index := map[*links.Link]int{}
			for i, l := range all {
				index[l] = i
				rx, tx := l.BytesIn.Load(), l.BytesOut.Load()
				pr, ok := prevRx[l]
				if !ok { // link appeared since the last tick
					pr, prevTx[l] = rx, tx
				}
				snap.Links = append(snap.Links, linkView{
					Name: l.Name, IP: l.IP().String(), Up: l.Up(), Healthy: l.Healthy(), Ping: l.LatencyMs(), Limit: l.LimitMbps(), Over: l.OverBudget(),
					RxMbps: links.Mbps(rx-pr, dt), TxMbps: links.Mbps(tx-prevTx[l], dt),
					Active: l.Active.Load(), TotalRx: rx, TotalTx: tx,
				})
				if n := len(snap.Links); n > 0 {
					snap.Links[n-1].Budget, snap.Links[n-1].MonthGB = s.budgetOf(l, now)
					snap.Links[n-1].SpeedMbps, snap.Links[n-1].SpeedAt = s.speedOf(l)
					snap.Links[n-1].Overflow, snap.Links[n-1].Standby = s.overflowOf(l)
				}
				prevRx[l], prevTx[l] = rx, tx
			}
			s.mu.Lock()
			for _, j := range s.jobs {
				file, size, used := j.stats.Snapshot()
				// Re-index per-link bytes on the pool order so colors match the UI.
				per := make([]int64, len(all))
				var done int64
				for i, b := range used {
					if i < len(j.links) {
						per[index[j.links[i]]] += b
					}
					done += b
				}
				end := j.ended
				if end.IsZero() {
					end = now
				}
				v := jobView{ID: j.id, URL: j.url, File: filepath.Base(file), Size: size, Done: done,
					PerLink: per, State: j.state, Err: j.err, Elapsed: end.Sub(j.started).Seconds()}
				if j.state == "running" {
					v.Mbps = links.Mbps(done-prevJob[j.id], dt)
				}
				prevJob[j.id] = done
				snap.Jobs = append(snap.Jobs, v)
			}
			s.mu.Unlock()
			snap.Apps = s.appViews(all, dt, prevApp)
			snap.Rules = s.ruleViews()
			b, _ := json.Marshal(snap)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}

func (s *Server) startDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string `json:"url"`
		Workers int    `json:"workers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	if u, err := url.Parse(req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	if req.Workers <= 0 || req.Workers > 32 {
		req.Workers = 4
	}
	up := s.Pool.Up()
	if len(up) == 0 {
		http.Error(w, "no connected link", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.nextID++
	j := &job{id: s.nextID, url: req.URL, state: "running", started: time.Now(), stats: &download.Stats{}, links: up, cancel: cancel}
	s.jobs = append(s.jobs, j)
	s.mu.Unlock()

	go func() {
		err := download.Run(ctx, up, download.Options{
			URL: req.URL, OutDir: s.OutDir, ChunkSize: 2 << 20, WorkersPerLnk: req.Workers,
			Quiet: true, Stats: j.stats,
		})
		s.mu.Lock()
		defer s.mu.Unlock()
		j.ended = time.Now()
		switch {
		case ctx.Err() != nil:
			j.state = "cancelled"
		case err != nil:
			j.state, j.err = "error", err.Error()
		default:
			j.state = "done"
		}
		if err != nil {
			log.Printf("download %d: %v", j.id, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) find(r *http.Request) *job {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	if j := s.find(r); j != nil {
		j.cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

// reveal opens Explorer with the downloaded file selected.
func (s *Server) reveal(w http.ResponseWriter, r *http.Request) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	file, _, _ := j.stats.Snapshot()
	abs, err := filepath.Abs(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	exec.Command("explorer", "/select,", abs).Start()
	w.WriteHeader(http.StatusNoContent)
}

// setLimit caps a link's download speed: ?link=<name>&mbps=<n> (0 = no cap).
func (s *Server) setLimit(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("link")
	mbps, err := strconv.ParseFloat(r.URL.Query().Get("mbps"), 64)
	if err != nil || mbps < 0 {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	for _, l := range s.Pool.All() {
		if l.Name == name {
			l.SetLimitMbps(mbps)
			if s.OnSettings != nil {
				s.OnSettings()
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.NotFound(w, r)
}

// OpenBrowser opens url in the default browser (Windows).
func OpenBrowser(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
