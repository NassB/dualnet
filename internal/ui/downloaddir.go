package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// downloadDir is the folder of the Downloads page: the one chosen in Settings,
// or the default (-dir, the user's Downloads folder).
func (s *Server) downloadDir() string {
	if s.Store != nil {
		if d := s.Store.DownloadDir(); d != "" {
			return d
		}
	}
	return s.OutDir
}

// setDownloadDir saves the download folder: JSON body {"dir": "..."}; "" goes
// back to the default. The folder is created if needed and must be writable.
// Downloads already started or queued keep their folder.
func (s *Server) setDownloadDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || s.Store == nil {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	if req.Dir != "" {
		if !filepath.IsAbs(req.Dir) {
			http.Error(w, "the folder must be a full path", http.StatusBadRequest)
			return
		}
		req.Dir = filepath.Clean(req.Dir)
		if err := writableDir(req.Dir); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	s.Store.SetDownloadDir(req.Dir)
	w.WriteHeader(http.StatusNoContent)
}

// writableDir creates dir if needed and checks a file can be written in it.
func writableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.New("cannot create the folder: " + err.Error())
	}
	f, err := os.CreateTemp(dir, ".dualnet-write-test-*")
	if err != nil {
		return errors.New("cannot write to the folder: " + err.Error())
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// browseDownloadDir shows the system's folder picker and returns the chosen
// folder as JSON {"dir": "..."} ("" = cancelled). It does not save it.
func (s *Server) browseDownloadDir(w http.ResponseWriter, r *http.Request) {
	dir, err := pickFolder(s.downloadDir())
	if errors.Is(err, errNoPicker) {
		http.Error(w, "not available", http.StatusNotImplemented)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"dir": dir})
}

var errNoPicker = errors.New("no folder picker on this system")
