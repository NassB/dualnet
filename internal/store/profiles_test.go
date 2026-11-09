package store

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/NassB/dualnet/internal/links"
)

func TestProfiles(t *testing.T) {
	s := Open(t.TempDir())
	l := links.New("Phone", net.IPv4(127, 0, 0, 1))
	pool := links.PoolOf(l)

	s.SetBudget("Phone", 50)
	s.SetOverflow("Phone", true)
	s.SetRule("game.exe", "Phone")
	if !s.SaveProfile(" Travel ") || s.SaveProfile("a<b") {
		t.Fatal("name validation")
	}

	s.SetBudget("Phone", 0)
	s.SetOverflow("Phone", false)
	s.SetRule("game.exe", "")
	l.SetLimitMbps(7)

	if s.ApplyProfile("nope", pool) {
		t.Fatal("unknown profile applied")
	}
	if !s.ApplyProfile("Travel", pool) {
		t.Fatal("apply failed")
	}
	if s.Budget("Phone") != 50 || !s.IsOverflow("Phone") || s.RuleLink("game.exe") != "Phone" {
		t.Fatal("profile choices not restored")
	}
	if l.LimitMbps() != 0 {
		t.Fatalf("cap not cleared: %v", l.LimitMbps())
	}
	if got := Open(s.Dir).Profiles(); len(got) != 1 || got[0] != "Travel" {
		t.Fatalf("not persisted: %v", got)
	}
	s.DeleteProfile("Travel")
	if len(s.Profiles()) != 0 {
		t.Fatal("not deleted")
	}
}

func TestCorruptSettingsAreKept(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "settings.json")
	os.WriteFile(bad, []byte(`{"rules": [`), 0o600)
	s := Open(dir)
	s.SetRule("game.exe", "Wi-Fi") // saves: must not destroy the unreadable file
	if b, err := os.ReadFile(bad + ".bad"); err != nil || string(b) != `{"rules": [` {
		t.Fatalf("the corrupt file was not kept: %v %q", err, b)
	}
}

func TestProfileKeepsWarnPercentAndResetsWarnings(t *testing.T) {
	s := Open(t.TempDir())
	s.SetWarnPercent(60)
	s.SaveProfile("Travel")
	s.SetWarnPercent(90)
	warnMu.Lock()
	warned["Phone"] = "2026-11"
	warnMu.Unlock()
	if !s.ApplyProfile("Travel", links.PoolOf()) || s.WarnPercent() != 60 {
		t.Fatalf("warn percent after apply = %d, want 60", s.WarnPercent())
	}
	warnMu.Lock()
	n := len(warned)
	warnMu.Unlock()
	if n != 0 {
		t.Fatal("applying a profile must allow the warnings again")
	}
}
