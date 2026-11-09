package download

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Resuming.
//
// A file is downloaded as <name>.part and renamed when it is complete, so a
// half-written file never looks finished. While it runs, the byte ranges
// already written are saved next to it (<name>.part.json). Starting the same
// URL again continues from there, provided the server still offers the same
// file: same size and same validator (ETag or Last-Modified). Without a
// validator nothing proves the file did not change, so it starts over.

const (
	partSuffix  = ".part"
	stateSuffix = ".part.json"
)

type span [2]int64 // first and last byte, inclusive

type state struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Validator string `json:"validator"`
	Done      []span `json:"done"`
}

// validator identifies a version of the remote file ("" = the server gives none).
func validator(h http.Header) string {
	if e := h.Get("ETag"); e != "" {
		return "etag:" + e
	}
	if m := h.Get("Last-Modified"); m != "" {
		return "modified:" + m
	}
	return ""
}

// resumable returns the ranges already on disk for out, or nil to start over.
func resumable(out, url string, size int64, valid string) []span {
	if valid == "" {
		return nil
	}
	b, err := os.ReadFile(out + stateSuffix)
	if err != nil {
		return nil
	}
	var st state
	if json.Unmarshal(b, &st) != nil || st.URL != url || st.Size != size || st.Validator != valid {
		return nil
	}
	if fi, err := os.Stat(out + partSuffix); err != nil || fi.Size() != size {
		return nil
	}
	done := merge(st.Done)
	for _, d := range done {
		if d[0] < 0 || d[1] >= size || d[0] > d[1] {
			return nil // not a state file we wrote
		}
	}
	return done
}

func saveState(out string, st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := out + stateSuffix + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, out+stateSuffix)
}

// RemovePartial deletes what an unfinished download of out left behind (the
// user cancelled it: there is nothing to resume).
func RemovePartial(out string) {
	if out == "" {
		return
	}
	os.Remove(out + partSuffix)
	os.Remove(out + stateSuffix)
	os.Remove(out + stateSuffix + ".tmp")
}

// merge sorts spans and joins those that touch or overlap.
func merge(in []span) []span {
	s := append([]span(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var out []span
	for _, d := range s {
		if n := len(out); n > 0 && d[0] <= out[n-1][1]+1 {
			out[n-1][1] = max(out[n-1][1], d[1])
			continue
		}
		out = append(out, d)
	}
	return out
}

func total(s []span) int64 {
	var n int64
	for _, d := range s {
		n += d[1] - d[0] + 1
	}
	return n
}

// gaps returns what is missing from [0, size) given the merged spans done.
func gaps(size int64, done []span) []span {
	var out []span
	next := int64(0)
	for _, d := range done {
		if d[0] > next {
			out = append(out, span{next, d[0] - 1})
		}
		next = d[1] + 1
	}
	if next < size {
		out = append(out, span{next, size - 1})
	}
	return out
}

// safeName turns a name chosen by the server (Content-Disposition, URL) into a
// plain file name: no folder part with either separator, no character Windows
// refuses, never "." or "..". A hostile server must not be able to write
// outside the download folder.
func safeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	if name == "" || strings.Trim(name, "._") == "" {
		return "download.bin"
	}
	return name
}

// Two downloads must never write the same file at once.
var (
	busyMu sync.Mutex
	busy   = map[string]bool{}
)

// ErrBusy is returned when the output file is already being downloaded.
var ErrBusy = errors.New("this file is already being downloaded")

func claim(out string) (release func(), err error) {
	key := out
	if abs, e := filepath.Abs(out); e == nil {
		key = strings.ToLower(abs)
	}
	busyMu.Lock()
	defer busyMu.Unlock()
	if busy[key] {
		return nil, ErrBusy
	}
	busy[key] = true
	return func() {
		busyMu.Lock()
		delete(busy, key)
		busyMu.Unlock()
	}, nil
}
