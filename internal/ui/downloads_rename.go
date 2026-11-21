package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/NassB/dualnet/internal/download"
)

// jobName renames a download: ?id=&name=. See relocate.
func (s *Server) jobName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	s.relocate(w, r, name, "")
}

// jobDir moves a download to another folder: ?id= and JSON body {"dir": "..."}.
// The folder is created if needed and must be writable.
func (s *Server) jobDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !filepath.IsAbs(req.Dir) {
		http.Error(w, "the folder must be a full path", http.StatusBadRequest)
		return
	}
	req.Dir = filepath.Clean(req.Dir)
	if err := writableDir(req.Dir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.relocate(w, r, "", req.Dir)
}

// relocate gives a download a new name and/or folder (empty = unchanged).
//   - never started (no file yet): the wish is kept and used when it runs;
//   - paused, failed or done: the file (and its .part / .part.json) is renamed
//     on the same volume, and the job follows it;
//   - running: refused, pause it first. An existing file is never replaced.
func (s *Server) relocate(w http.ResponseWriter, r *http.Request, name, dir string) {
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch j.state {
	case "queued", "paused", "error", "done":
	case "running":
		http.Error(w, "pause the download first", http.StatusConflict)
		return
	default:
		http.Error(w, "the download was cancelled", http.StatusConflict)
		return
	}
	file, _, _ := j.stats.Snapshot()
	if file == "" { // nothing on disk yet
		if name != "" {
			j.name = download.SafeName(name)
		}
		if dir != "" {
			j.dir = dir
		}
		s.saveJobs()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if dir == "" {
		dir = filepath.Dir(file)
	}
	if name == "" {
		name = filepath.Base(file)
	}
	dest := filepath.Join(dir, download.SafeName(name))
	if dest == file {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.EqualFold(filepath.VolumeName(dest), filepath.VolumeName(file)) {
		http.Error(w, "cannot move to another drive", http.StatusConflict)
		return
	}
	for _, o := range s.jobs {
		if f, _, _ := o.stats.Snapshot(); o != j && strings.EqualFold(f, dest) && o.state != "cancelled" {
			http.Error(w, "a file with this name already exists", http.StatusConflict)
			return
		}
	}
	suffixes := []string{""} // names that must be free: the file itself,
	if j.state != "done" {
		suffixes = []string{"", ".part", ".part.json"}
	}
	for _, sfx := range suffixes {
		if _, err := os.Lstat(dest + sfx); err == nil {
			http.Error(w, "a file with this name already exists", http.StatusConflict)
			return
		}
	}
	var moved []string
	for _, sfx := range suffixes {
		err := os.Rename(file+sfx, dest+sfx)
		if err == nil {
			moved = append(moved, sfx)
			continue
		}
		if os.IsNotExist(err) && j.state != "done" {
			continue // a failed download may have no .part.json (or no file at all)
		}
		for _, m := range moved {
			os.Rename(dest+m, file+m)
		}
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "the file is gone", http.StatusNotFound)
		} else {
			http.Error(w, "cannot move the file: "+err.Error(), http.StatusConflict)
		}
		return
	}
	j.stats.SetFile(dest)
	j.dir = filepath.Dir(dest)
	s.saveJobs()
	w.WriteHeader(http.StatusNoContent)
}
