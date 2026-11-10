package ui

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Journal keeps the last link events (a link set aside, back, unplugged, over
// budget…) in memory, to show them in the interface. It is lost when dualnet
// stops: the log file keeps the long-term record.
type Journal struct {
	mu     sync.Mutex
	events []JournalEvent
}

// JournalEvent is one entry. Event is one of the links.Event* values; the page
// turns it into a sentence in the user's language.
type JournalEvent struct {
	At    time.Time `json:"at"`
	Link  string    `json:"link"`
	Event string    `json:"event"`
}

const journalSize = 200

// Add records an event now. A nil journal ignores it.
func (j *Journal) Add(link, event string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, JournalEvent{time.Now(), link, event})
	if n := len(j.events); n > journalSize {
		j.events = append([]JournalEvent(nil), j.events[n-journalSize:]...)
	}
}

// List returns the events, most recent first.
func (j *Journal) List() []JournalEvent {
	out := []JournalEvent{}
	if j == nil {
		return out
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.events) - 1; i >= 0; i-- {
		out = append(out, j.events[i])
	}
	return out
}

func (s *Server) getJournal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.Journal.List())
}
