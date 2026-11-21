package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// hashFile streams a file through SHA-256 and returns the lower-case hex sum.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkSha hashes the file of a finished job and stores the result ("ok" or
// "mismatch"). Called with s.mu held; it is released while the file is read.
func (s *Server) checkSha(j *job) {
	want := j.sha
	file, _, _ := j.stats.Snapshot()
	j.shaState, j.shaGot = "checking", ""
	s.mu.Unlock()
	got, err := hashFile(file)
	s.mu.Lock()
	if j.sha != want { // changed while hashing: that request owns the state
		return
	}
	switch {
	case err != nil:
		j.shaState, j.shaGot = "mismatch", "" // file unreadable: cannot be confirmed
	case got == want:
		j.shaState, j.shaGot = "ok", got
	default:
		j.shaState, j.shaGot = "mismatch", got
	}
}

// jobSha sets (or with an empty value removes) the expected SHA-256 of a
// download; on a finished one it is checked at once.
func (s *Server) jobSha(w http.ResponseWriter, r *http.Request) {
	sum := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sha256")))
	if sum != "" && !sha256Re.MatchString(sum) {
		http.Error(w, "invalid sha256 (64 hex characters)", http.StatusBadRequest)
		return
	}
	j := s.find(r)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j.sha, j.shaState, j.shaGot = sum, "", ""
	if sum != "" && j.state == "done" {
		s.saveJobs()
		s.checkSha(j)
	}
	s.saveJobs()
	w.WriteHeader(http.StatusNoContent)
}
