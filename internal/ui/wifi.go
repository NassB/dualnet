package ui

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NassB/dualnet/internal/store"
	"github.com/NassB/dualnet/internal/wifi"
)

func (s *Server) currentSSID() string {
	if s.CurrentSSID != nil {
		return s.CurrentSSID()
	}
	return wifi.CurrentSSID()
}

// getWifi returns the Wi-Fi network the PC is on and the rules that apply
// profiles by network.
func (s *Server) getWifi(w http.ResponseWriter, r *http.Request) {
	rules := []store.WifiRule{}
	if s.Store != nil {
		rules = s.Store.WifiRules()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Current string           `json:"current"`
		Rules   []store.WifiRule `json:"rules"`
	}{s.currentSSID(), rules})
}

// setWifiProfile sets the profile applied on a network: ?ssid=<name>&profile=<profile>
// (an empty profile removes the rule).
func (s *Server) setWifiProfile(w http.ResponseWriter, r *http.Request) {
	ssid, profile := strings.TrimSpace(r.URL.Query().Get("ssid")), r.URL.Query().Get("profile")
	if s.Store == nil || !wifi.Valid(ssid) {
		http.Error(w, "invalid value", http.StatusBadRequest)
		return
	}
	if !s.Store.SetWifiProfile(ssid, profile) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
