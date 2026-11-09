package ui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NassB/dualnet/internal/doctor"
	"github.com/NassB/dualnet/internal/transparent"
)

func (s *Server) logFile() string {
	if s.Store == nil {
		return ""
	}
	return filepath.Join(s.Store.Dir, "dualnet.log")
}

// tailLines returns the last n lines of the file at path (reading at most the
// last 256 KB), or "" when it cannot be read.
func tailLines(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const window = 256 << 10
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	start := int64(0)
	if fi.Size() > window {
		start = fi.Size() - window
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) > 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\r\n"), "\n")
	if start > 0 && len(lines) > 1 {
		lines = lines[1:] // the first line is probably cut
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return strings.Join(lines, "\n")
}

// diagnostics builds the text users paste into a bug report.
func (s *Server) diagnostics() string {
	var b strings.Builder
	p := s.powerState()
	fmt.Fprintf(&b, "dualnet %s\n", s.Version)
	fmt.Fprintf(&b, "%s/%s, %s, go %s, administrator: %v\n", runtime.GOOS, runtime.GOARCH, osVersion(), runtime.Version(), transparent.Admin())
	fmt.Fprintf(&b, "time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "power: on=%v mode=%q  transparent=%v  proxy=%q\n", p.On, p.Mode, s.Tun != nil && s.Tun.On(), s.ProxyAddr)
	if s.Store != nil {
		fmt.Fprintf(&b, "settings: enabled=%v updateCheck=%v\n", s.Store.Enabled(), s.Store.UpdateCheckEnabled())
	}
	b.WriteString("\nlinks:\n")
	if s.Pool != nil {
		for _, l := range s.Pool.All() {
			fmt.Fprintf(&b, "  %-24s %-16s up=%v healthy=%v ping=%dms cap=%dMb/s active=%d busy=%d rx=%d tx=%d\n",
				l.Name, l.IP(), l.Up(), l.Healthy(), l.LatencyMs(), l.LimitMbps(), l.Active.Load(), l.Busy(10*time.Second), l.BytesIn.Load(), l.BytesOut.Load())
		}
	}
	b.WriteString("\nchecks:\n")
	env := s.doctorEnv()
	env.Reach = nil // no network round trip just to copy a report
	b.WriteString(doctor.Format(doctor.Run(env)))
	b.WriteString("\nlast log lines:\n")
	b.WriteString(tailLines(s.logFile(), 200))
	b.WriteString("\n")
	return b.String()
}

func (s *Server) getDiagnostics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, s.diagnostics())
}

func (s *Server) getLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, tailLines(s.logFile(), 300))
}

// openLogFolder opens the folder holding the log, history and settings in Explorer.
func (s *Server) openLogFolder(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		http.NotFound(w, r)
		return
	}
	if err := openFolder(s.Store.Dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
