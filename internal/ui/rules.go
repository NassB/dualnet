package ui

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

type appView struct {
	Name    string  `json:"name"`
	Active  int64   `json:"active"`
	Total   int64   `json:"total"`
	Rx      int64   `json:"rx"`
	Tx      int64   `json:"tx"`
	RxMbps  float64 `json:"rxMbps"`
	PerLink []int64 `json:"perLink"` // bytes received through each link, in the order of the links list
	Rule    string  `json:"rule"`    // link the application is restricted to ("" = none)
	Limit   int     `json:"limit"`   // download cap in Mb/s (0 = none)
}

type ruleView struct {
	App   string `json:"app"`
	Link  string `json:"link"`
	Limit int    `json:"limit"`
}

// appViews turns the per-application counters into what the page shows. prev
// holds each application's received bytes at the previous tick (for the speed).
func (s *Server) appViews(all []*links.Link, dt time.Duration, prev map[string]int64) []appView {
	out := []appView{}
	for _, a := range s.Tun.Apps() {
		last, seen := prev[a.Name]
		prev[a.Name] = a.Rx
		if !seen {
			last = a.Rx
		}
		v := appView{Name: a.Name, Active: a.Active, Total: a.Total, Rx: a.Rx, Tx: a.Tx,
			RxMbps: links.Mbps(a.Rx-last, dt), PerLink: make([]int64, len(all))}
		for i, l := range all {
			v.PerLink[i] = a.RxByLink[l.Name]
		}
		if s.Store != nil {
			v.Rule = s.Store.RuleLink(a.Name)
			v.Limit = s.Store.RuleLimit(a.Name)
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) ruleViews() []ruleView {
	out := []ruleView{}
	if s.Store != nil {
		for _, r := range s.Store.Rules() {
			out = append(out, ruleView{App: r.App, Link: r.Link, Limit: r.Limit})
		}
	}
	return out
}

// setRule restricts an application to a link: ?app=<exe name>&link=<link name>.
// An empty link removes the rule.
func (s *Server) setRule(w http.ResponseWriter, r *http.Request) {
	app := strings.TrimSpace(r.URL.Query().Get("app"))
	link := r.URL.Query().Get("link")
	if app == "" || len(app) > 128 || strings.ContainsAny(app, `/\:*?"<>|`) {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	if link != "" {
		known := false
		for _, l := range s.Pool.All() {
			if l.Name == link {
				known = true
			}
		}
		if !known {
			http.NotFound(w, r)
			return
		}
	}
	s.Store.SetRule(app, link)
	w.WriteHeader(http.StatusNoContent)
}

// setAppLimit caps an application's download speed: ?app=<exe name>&mbps=<n>.
// 0 removes the cap.
func (s *Server) setAppLimit(w http.ResponseWriter, r *http.Request) {
	app := strings.TrimSpace(r.URL.Query().Get("app"))
	mbps, err := strconv.Atoi(r.URL.Query().Get("mbps"))
	if app == "" || len(app) > 128 || strings.ContainsAny(app, `/\:*?"<>|`) || err != nil || mbps < 0 || mbps > 100000 {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	s.Store.SetRuleLimit(app, mbps)
	w.WriteHeader(http.StatusNoContent)
}
