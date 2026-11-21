// Package ui serves a local web dashboard: live per-link throughput, the proxy,
// and downloads started from the page.
package ui

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NassB/dualnet/internal/doctor"
	"github.com/NassB/dualnet/internal/download"
	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/notify"
	"github.com/NassB/dualnet/internal/ratelimit"
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
	// Restart starts the program again and stops this one, after an update
	// replaced the executable (nil = the interface cannot install updates).
	// CurrentSSID returns the Wi-Fi network name ("" = none); nil = ask Windows.
	CurrentSSID func() string
	// Journal receives the link events to show in the History page (nil = none).
	Journal  *Journal
	Restart  func() error
	updating atomic.Bool
	// OnSettings is called after a setting changed in the UI (to save it).
	OnSettings func()

	mu     sync.Mutex
	jobs   []*job
	nextID int
	loaded sync.Once
}

type job struct {
	id       int
	url      string
	state    string // queued, running, paused, done, error, cancelled
	err      string
	workers  int    // connections per link
	dir      string // folder chosen when the download was added
	started  time.Time
	ended    time.Time
	stats    *download.Stats
	links    []*links.Link // links used, in the order of stats' per-link counters
	ctx      context.Context
	cancel   context.CancelFunc
	dropped  bool              // cancelled by the user: the partial file is deleted
	pausing  bool              // paused by the user: the partial file is kept
	capMbps  float64           // speed cap of this download in Mb/s (0 = none)
	limit    *ratelimit.Bucket // enforces capMbps; changes live
	sha      string            // expected SHA-256 (lower-case hex), "" = none
	shaState string            // "", "checking", "ok" or "mismatch"
	shaGot   string            // actual hash after a mismatch
	name     string            // name wanted by the user before the download started ("" = the server's)
}

func (j *job) setCap(mbps float64) {
	j.capMbps = mbps
	j.limit.Set(mbps * 1e6 / 8)
}

func (s *Server) Handler() http.Handler {
	s.loaded.Do(s.loadJobs) // the download list of the previous run
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page())
	})
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/download", s.startDownload)
	mux.HandleFunc("POST /api/cancel", s.cancelJob)
	mux.HandleFunc("POST /api/pause", s.pauseJob)
	mux.HandleFunc("POST /api/jobcap", s.jobCap)
	mux.HandleFunc("POST /api/jobsha", s.jobSha)
	mux.HandleFunc("POST /api/jobname", s.jobName)
	mux.HandleFunc("POST /api/jobdir", s.jobDir)
	mux.HandleFunc("POST /api/resume", s.resumeJob)
	mux.HandleFunc("POST /api/retry", s.retryJob)
	mux.HandleFunc("POST /api/move", s.moveJob)
	mux.HandleFunc("POST /api/startnow", s.startNow)
	mux.HandleFunc("POST /api/clear", s.clearJobs)
	mux.HandleFunc("POST /api/remove", s.removeJob)
	mux.HandleFunc("POST /api/maxdownloads", s.setMaxDownloads)
	mux.HandleFunc("POST /api/downloaddir", s.setDownloadDir)
	mux.HandleFunc("POST /api/downloaddir/browse", s.browseDownloadDir)
	mux.HandleFunc("POST /api/reveal", s.reveal)
	mux.HandleFunc("POST /api/openfile", s.openFile)
	mux.HandleFunc("POST /api/sysproxy", s.setSysProxy)
	mux.HandleFunc("POST /api/transparent", s.setTun)
	mux.HandleFunc("POST /api/limit", s.setLimit)
	mux.HandleFunc("POST /api/budget", s.setBudget)
	mux.HandleFunc("POST /api/budgetwarn", s.setBudgetWarn)
	mux.HandleFunc("POST /api/rule", s.setRule)
	mux.HandleFunc("POST /api/applimit", s.setAppLimit)
	mux.HandleFunc("GET /api/profiles", s.getProfiles)
	mux.HandleFunc("POST /api/profile", s.profile)
	mux.HandleFunc("GET /api/wifi", s.getWifi)
	mux.HandleFunc("POST /api/wifiprofile", s.setWifiProfile)
	mux.HandleFunc("GET /api/domainrules", s.getDomainRules)
	mux.HandleFunc("POST /api/domainrule", s.setDomainRule)
	mux.HandleFunc("GET /api/doctor", s.getDoctor)
	mux.HandleFunc("POST /api/speedtest", s.speedTest)
	mux.HandleFunc("POST /api/role", s.setRole)
	mux.HandleFunc("POST /api/onboarding", s.setOnboarding)
	mux.HandleFunc("POST /api/welcome", s.setWelcome)
	mux.HandleFunc("POST /api/system", s.setSystem)
	mux.HandleFunc("POST /api/power", s.setPower)
	mux.HandleFunc("POST /api/autostart", s.setAutostart)
	mux.HandleFunc("POST /api/update/open", s.openUpdate)
	mux.HandleFunc("POST /api/update/install", s.installUpdate)
	mux.HandleFunc("POST /api/updatecheck", s.setUpdateCheck)
	mux.HandleFunc("POST /api/notify", s.setNotify)
	mux.HandleFunc("POST /api/notify/test", s.testNotify)
	mux.HandleFunc("GET /api/diagnostics", s.getDiagnostics)
	mux.HandleFunc("GET /api/log", s.getLog)
	mux.HandleFunc("POST /api/logfolder", s.openLogFolder)
	mux.HandleFunc("GET /api/history", s.getHistory)
	mux.HandleFunc("GET /api/journal", s.getJournal)
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
	ID       int     `json:"id"`
	URL      string  `json:"url"`
	File     string  `json:"file"`
	Size     int64   `json:"size"`
	Done     int64   `json:"done"`
	PerLink  []int64 `json:"perLink"`
	Mbps     float64 `json:"mbps"`
	State    string  `json:"state"`
	Err      string  `json:"err,omitempty"`
	Elapsed  float64 `json:"elapsed"`
	CapMbps  float64 `json:"capMbps"`
	Sha      string  `json:"sha256,omitempty"`
	ShaState string  `json:"shaState,omitempty"`
	ShaGot   string  `json:"shaGot,omitempty"`
}

type snapshot struct {
	T          int64      `json:"t"`
	OS         string     `json:"os"`
	Links      []linkView `json:"links"`
	Jobs       []jobView  `json:"jobs"`
	Proxy      string     `json:"proxy"`
	Dir        string     `json:"dir"`
	DirCustom  bool       `json:"dirCustom"` // chosen in Settings (not the default)
	Sys        sysView    `json:"sys"`
	Tun        tunView    `json:"tun"`
	Sys2       systemView `json:"system"`
	Ver        string     `json:"version"`
	Power      powerView  `json:"power"`
	Upd        updateView `json:"update"`
	Notif      bool       `json:"notify"`
	Onb        bool       `json:"onboarded"`
	Welcomed   bool       `json:"welcomed"`
	BudgetWarn int        `json:"budgetWarn"`
	MaxDl      int        `json:"maxDownloads"`
	Apps       []appView  `json:"apps"`
	Rules      []ruleView `json:"rules"`
	Auto       *bool      `json:"autostart,omitempty"` // nil = option not available
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
			snap := snapshot{T: now.UnixMilli(), OS: runtime.GOOS, Proxy: s.ProxyAddr, Dir: s.downloadDir(), DirCustom: s.Store != nil && s.Store.DownloadDir() != "", Links: []linkView{}, Jobs: []jobView{}, Sys: s.sysState(), Tun: s.Tun.view(), Sys2: s.systemState(), Ver: s.Version, Power: s.powerState(), Auto: s.autostartState(), Upd: s.updateState(), Notif: s.Store == nil || s.Store.NotifyEnabled(), Onb: s.Store != nil && s.Store.OnboardingDone(), Welcomed: s.Store == nil || s.Store.Welcomed()}
			if s.Store != nil {
				snap.BudgetWarn = s.Store.WarnPercent()
			}
			snap.MaxDl = s.maxDownloads()
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
				done := j.stats.Resumed() // already on disk from an interrupted run
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
				elapsed := 0.0
				if !j.started.IsZero() { // a queued download has not started
					elapsed = end.Sub(j.started).Seconds()
				}
				name := "" // not chosen yet (queued): the page shows the address
				if file != "" {
					name = filepath.Base(file)
				}
				v := jobView{ID: j.id, URL: j.url, File: name, Size: size, Done: done,
					PerLink: per, State: j.state, Err: j.err, Elapsed: elapsed, CapMbps: j.capMbps,
					Sha: j.sha, ShaState: j.shaState, ShaGot: j.shaGot}
				if j.state == "running" {
					if last, seen := prevJob[j.id]; seen { // no speed on a page's first tick
						v.Mbps = links.Mbps(done-last, dt)
					}
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
		Sha     string `json:"sha256"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	req.Sha = strings.ToLower(strings.TrimSpace(req.Sha))
	if req.Sha != "" && !sha256Re.MatchString(req.Sha) {
		http.Error(w, "invalid sha256 (64 hex characters)", http.StatusBadRequest)
		return
	}
	if u, err := url.Parse(req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	if req.Workers <= 0 || req.Workers > 32 {
		req.Workers = 4
	}
	if len(s.Pool.Up()) == 0 {
		http.Error(w, "no connected link", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	for _, j := range s.jobs {
		if (j.state == "running" || j.state == "queued" || j.state == "paused") && j.url == req.URL {
			s.mu.Unlock()
			cancel()
			http.Error(w, "already downloading", http.StatusConflict)
			return
		}
	}
	s.nextID++
	s.jobs = append(s.jobs, &job{id: s.nextID, url: req.URL, state: "queued", workers: req.Workers, dir: s.downloadDir(), stats: &download.Stats{}, cancel: cancel, ctx: ctx, limit: &ratelimit.Bucket{}, sha: req.Sha})
	s.pump()
	s.saveJobs()
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// maxDownloads is how many downloads run at once (0 = no limit).
func (s *Server) maxDownloads() int {
	if s.Store == nil {
		return store.DefaultMaxDownloads
	}
	return s.Store.MaxDownloads()
}

// setMaxDownloads sets how many downloads run at once: ?n=<0-8> (0 = no limit).
func (s *Server) setMaxDownloads(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("n"))
	if err != nil || n < 0 || n > 8 || s.Store == nil {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	s.Store.SetMaxDownloads(n)
	s.mu.Lock()
	s.pump() // a higher limit starts waiting downloads at once
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// pump starts queued downloads, oldest first, while fewer than the limit run.
// A lower limit never stops a running download. Called with s.mu held.
func (s *Server) pump() {
	limit, running := s.maxDownloads(), 0
	for _, j := range s.jobs {
		if j.state == "running" {
			running++
		}
	}
	for _, j := range s.jobs {
		if j.state != "queued" || (limit > 0 && running >= limit) {
			continue
		}
		if s.begin(j) {
			running++
		}
	}
}

// begin starts a job now (limit not checked) and reports whether it runs.
// Called with s.mu held.
func (s *Server) begin(j *job) bool {
	up := s.Pool.Up()
	if len(up) == 0 {
		j.state, j.err, j.ended = "error", "no connected link", time.Now()
		s.saveJobs()
		return false
	}
	j.state, j.started, j.links = "running", time.Now(), up
	s.saveJobs()
	go s.run(j)
	return true
}

// moveJob changes the place of a queued download among the queued ones (the
// queue order is the order of s.jobs): to=up starts it sooner, down later,
// top first.
func (s *Server) moveJob(w http.ResponseWriter, r *http.Request) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	to := r.URL.Query().Get("to")
	if to != "up" && to != "down" && to != "top" {
		http.Error(w, "to must be up, down or top", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.state != "queued" {
		http.Error(w, "not queued", http.StatusConflict)
		return
	}
	var q []*job // queued jobs in queue order
	for _, o := range s.jobs {
		if o.state == "queued" {
			q = append(q, o)
		}
	}
	i := 0
	for q[i] != j {
		i++
	}
	switch {
	case to == "top":
		copy(q[1:i+1], q[:i])
		q[0] = j
	case to == "up" && i > 0:
		q[i-1], q[i] = q[i], q[i-1]
	case to == "down" && i < len(q)-1:
		q[i+1], q[i] = q[i], q[i+1]
	}
	n := 0 // put the queued jobs back in the slots the queued jobs occupied
	for k, o := range s.jobs {
		if o.state == "queued" {
			s.jobs[k] = q[n]
			n++
		}
	}
	s.saveJobs()
	w.WriteHeader(http.StatusNoContent)
}

// startNow runs a queued or paused download at once, even above the limit;
// nothing running is stopped.
func (s *Server) startNow(w http.ResponseWriter, r *http.Request) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch j.state {
	case "paused":
		j.ctx, j.cancel = context.WithCancel(context.Background())
		j.pausing = false
	case "queued":
	default:
		http.Error(w, "not queued or paused", http.StatusConflict)
		return
	}
	s.begin(j)
	w.WriteHeader(http.StatusNoContent)
}

// run downloads one job, then lets the next queued one start.
func (s *Server) run(j *job) {
	out, _, _ := j.stats.Snapshot() // set when resuming: same file as before the pause
	err := download.Run(j.ctx, j.links, download.Options{
		URL: j.url, Output: out, OutDir: j.dir, Name: j.name, ChunkSize: 2 << 20, WorkersPerLnk: j.workers,
		Quiet: true, Stats: j.stats, Limit: j.limit,
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	j.ended = time.Now()
	switch {
	case j.ctx.Err() != nil && j.pausing && !j.dropped:
		j.state = "paused" // the .part stays for the resume
		j.ended = time.Time{}
	case j.ctx.Err() != nil:
		j.state = "cancelled"
		if file, _, _ := j.stats.Snapshot(); j.dropped {
			download.RemovePartial(file) // cancelled by the user: nothing to resume
		}
	case err != nil:
		j.state, j.err = "error", err.Error()
	default:
		j.state = "done"
		if j.sha != "" {
			s.checkSha(j) // releases s.mu while the file is read
		}
	}
	if err != nil {
		log.Printf("download %d: %v", j.id, err)
	}
	if s.Notify != nil && (j.state == "done" || j.state == "error") {
		file, total, _ := j.stats.Snapshot()
		if file == "" {
			file = j.url
		}
		if j.state == "done" && j.shaState == "mismatch" {
			s.Notify.DownloadFailed(file, "SHA-256 check failed")
		} else if j.state == "done" {
			s.Notify.DownloadDone(file, total)
		} else {
			s.Notify.DownloadFailed(file, j.err)
		}
	}
	s.pump()
	s.saveJobs()
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
		s.mu.Lock()
		j.dropped = true
		switch j.state {
		case "queued": // never started: nothing to stop or delete
			j.state, j.ended = "cancelled", time.Now()
		case "paused": // no download running: delete the partial file here
			j.state, j.ended = "cancelled", time.Now()
			file, _, _ := j.stats.Snapshot()
			download.RemovePartial(file)
		}
		s.saveJobs()
		s.mu.Unlock()
		j.cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

// pauseJob stops a download but keeps its partial file; resume continues it.
func (s *Server) pauseJob(w http.ResponseWriter, r *http.Request) {
	if j := s.find(r); j != nil {
		s.mu.Lock()
		switch j.state {
		case "queued":
			j.state = "paused"
		case "running":
			j.pausing = true
			j.cancel() // run() sets the state once the download has stopped
		}
		s.saveJobs()
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

// jobCap sets the speed cap of a download in Mb/s (0 = none), live if it runs.
func (s *Server) jobCap(w http.ResponseWriter, r *http.Request) {
	mbps, err := strconv.ParseFloat(r.URL.Query().Get("mbps"), 64)
	if err != nil || math.IsNaN(mbps) || mbps < 0 || mbps > 10000 {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	j.setCap(mbps)
	s.saveJobs()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// resumeJob puts a paused download back in the queue.
func (s *Server) resumeJob(w http.ResponseWriter, r *http.Request) {
	if j := s.find(r); j != nil {
		s.mu.Lock()
		if j.state == "paused" {
			j.ctx, j.cancel = context.WithCancel(context.Background())
			j.pausing, j.state = false, "queued"
			s.pump()
			s.saveJobs()
		}
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

// retryJob puts a failed download back in the queue; a partial file left by
// the failed run is continued like on resume.
func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.state != "error" {
		http.Error(w, "not failed", http.StatusConflict)
		return
	}
	for _, o := range s.jobs {
		if o != j && (o.state == "running" || o.state == "queued" || o.state == "paused") && o.url == j.url {
			http.Error(w, "already downloading", http.StatusConflict)
			return
		}
	}
	j.ctx, j.cancel = context.WithCancel(context.Background())
	j.pausing, j.dropped, j.err, j.ended, j.state = false, false, "", time.Time{}, "queued"
	s.pump()
	s.saveJobs()
	w.WriteHeader(http.StatusNoContent)
}

// finished reports whether a job has ended (done, error or cancelled).
func finished(j *job) bool { return j.state == "done" || j.state == "error" || j.state == "cancelled" }

// clearJobs removes every finished job from the list; files stay on disk.
func (s *Server) clearJobs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.jobs[:0]
	for _, j := range s.jobs {
		if !finished(j) {
			keep = append(keep, j)
		}
	}
	s.jobs = keep
	s.saveJobs()
	w.WriteHeader(http.StatusNoContent)
}

// removeJob removes one finished job from the list (409 while it is active).
func (s *Server) removeJob(w http.ResponseWriter, r *http.Request) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !finished(j) {
		http.Error(w, "still active", http.StatusConflict)
		return
	}
	for i, o := range s.jobs {
		if o == j {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			break
		}
	}
	s.saveJobs()
	w.WriteHeader(http.StatusNoContent)
}

// reveal opens the downloaded file's location.
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
	if err := revealFile(abs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// openFile opens a finished download with its default application. Only the
// job's own output file is ever opened, and programs are refused: opening
// them would run them.
func (s *Server) openFile(w http.ResponseWriter, r *http.Request) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	state := j.state
	s.mu.Unlock()
	if state != "done" {
		http.Error(w, "the download is not finished", http.StatusConflict)
		return
	}
	file, _, _ := j.stats.Snapshot()
	abs, err := filepath.Abs(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	if isProgram(abs) {
		http.Error(w, "programs are not opened from here; use Show in folder", http.StatusForbidden)
		return
	}
	if err := openPath(abs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
