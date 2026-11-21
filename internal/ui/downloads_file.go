package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/NassB/dualnet/internal/download"
	"github.com/NassB/dualnet/internal/ratelimit"
)

// maxKeptFinished is how many finished downloads are kept between runs.
const maxKeptFinished = 200

// savedJob is a download as kept in downloads.json.
type savedJob struct {
	ID       int       `json:"id"`
	URL      string    `json:"url"`
	State    string    `json:"state"`
	Err      string    `json:"err,omitempty"`
	Workers  int       `json:"workers"`
	Dir      string    `json:"dir"`
	CapMbps  float64   `json:"capMbps,omitempty"`
	Sha      string    `json:"sha256,omitempty"`
	ShaState string    `json:"shaState,omitempty"`
	ShaGot   string    `json:"shaGot,omitempty"`
	File     string    `json:"file,omitempty"`
	Size     int64     `json:"size"`
	Done     int64     `json:"done"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended"`
}

func (s *Server) downloadsPath() string {
	if s.Store == nil || s.Store.Dir == "" {
		return ""
	}
	return filepath.Join(s.Store.Dir, "downloads.json")
}

// saveJobs writes the list (in queue order) to downloads.json, through a
// temporary file. Called with s.mu held after a state change.
func (s *Server) saveJobs() {
	path := s.downloadsPath()
	if path == "" {
		return
	}
	out := make([]savedJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		file, size, used := j.stats.Snapshot()
		done := j.stats.Resumed()
		for _, b := range used {
			done += b
		}
		out = append(out, savedJob{ID: j.id, URL: j.url, State: j.state, Err: j.err, Workers: j.workers, Dir: j.dir, CapMbps: j.capMbps,
			Sha: j.sha, ShaState: j.shaState, ShaGot: j.shaGot, File: file, Size: size, Done: done, Started: j.started, Ended: j.ended})
	}
	// keep only the most recent finished jobs
	n := 0
	for _, j := range s.jobs {
		if finished(j) {
			n++
		}
	}
	kept := out[:0]
	for i, v := range out {
		if finished(s.jobs[i]) && n > maxKeptFinished {
			n--
			continue
		}
		kept = append(kept, v)
	}
	b, err := json.MarshalIndent(kept, "", "  ")
	if err == nil {
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		os.Remove(path + ".tmp")
	}
}

// loadJobs restores the list saved by an earlier run. Running and queued
// downloads come back paused: nothing starts by itself after a restart.
func (s *Server) loadJobs() {
	path := s.downloadsPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var saved []savedJob
	if json.Unmarshal(b, &saved) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range saved {
		if v.ID <= 0 || v.URL == "" {
			continue
		}
		switch v.State {
		case "running", "queued":
			v.State = "paused"
			v.Ended = time.Time{}
		case "paused", "done", "error", "cancelled":
		default:
			continue
		}
		st := &download.Stats{}
		st.Preset(v.File, v.Size, v.Done)
		ctx, cancel := context.WithCancel(context.Background())
		j := &job{id: v.ID, url: v.URL, state: v.State, err: v.Err, workers: v.Workers, dir: v.Dir,
			started: v.Started, ended: v.Ended, stats: st, ctx: ctx, cancel: cancel, limit: &ratelimit.Bucket{}, sha: v.Sha, shaState: v.ShaState, shaGot: v.ShaGot}
		if j.shaState == "checking" { // interrupted by a restart
			j.shaState = ""
		}
		j.setCap(v.CapMbps)
		s.jobs = append(s.jobs, j)
		if v.ID > s.nextID {
			s.nextID = v.ID
		}
	}
}
