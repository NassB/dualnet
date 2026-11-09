package ui

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/NassB/dualnet/internal/store"
)

// getHistory returns the traffic per day: ?days=<n> (default 30, at most 366)
// and format=json (default) or csv (a download, one row per day and link).
func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 366 {
			http.Error(w, "invalid value", http.StatusBadRequest)
			return
		}
		days = n
	}
	h := s.Store.History(days)
	switch r.URL.Query().Get("format") {
	case "", "json":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(h)
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="dualnet-history.csv"`)
		writeHistoryCSV(csv.NewWriter(w), h)
	default:
		http.Error(w, "invalid value", http.StatusBadRequest)
	}
}

func writeHistoryCSV(cw *csv.Writer, h []store.Day) {
	cw.Write([]string{"date", "link", "rx_bytes", "tx_bytes"})
	for _, d := range h {
		names := make([]string, 0, len(d.Links))
		for n := range d.Links {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			u := d.Links[n]
			cw.Write([]string{d.Date, n, strconv.FormatInt(u.Rx, 10), strconv.FormatInt(u.Tx, 10)})
		}
	}
	cw.Flush()
}
