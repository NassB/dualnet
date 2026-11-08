package store

import "testing"

func TestRules(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if s.RuleLink("game.exe") != "" || s.RuleLinks("game.exe") != nil || len(s.Rules()) != 0 {
		t.Fatal("no rules to begin with")
	}
	s.SetRule("Game.EXE", "Wi-Fi") // names are compared without regard to case
	s.SetRule("steam.exe", "Phone")
	if s.RuleLink("game.exe") != "Wi-Fi" || s.RuleLink("GAME.exe") != "Wi-Fi" {
		t.Fatal("rule not found case-insensitively")
	}
	if got := s.RuleLinks("steam.exe"); len(got) != 1 || got[0] != "Phone" {
		t.Fatalf("RuleLinks = %v", got)
	}
	// changing a rule replaces it (and keeps its place)
	s.SetRule("game.exe", "Phone")
	if r := s.Rules(); len(r) != 2 || r[0] != (Rule{"game.exe", "Phone"}) || r[1] != (Rule{"steam.exe", "Phone"}) {
		t.Fatalf("after replacing: %+v", r)
	}
	// empty names are ignored, an empty link removes
	s.SetRule("  ", "Wi-Fi")
	s.SetRule("steam.exe", "")
	if r := s.Rules(); len(r) != 1 || r[0].App != "game.exe" {
		t.Fatalf("after removing: %+v", r)
	}
	// persisted
	if r := Open(dir).Rules(); len(r) != 1 || r[0] != (Rule{"game.exe", "Phone"}) {
		t.Fatalf("after reload: %+v", r)
	}
}
