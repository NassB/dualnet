package store

import "testing"

func TestRules(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if s.RuleLink("game.exe") != "" || s.RuleLinks("game.exe") != nil || len(s.Rules()) != 0 {
		t.Fatal("no rules to begin with")
	}
	s.SetRule("Game.EXE", "Wi-Fi") // names are compared without regard to case
	s.SetRule("launcher.exe", "Phone")
	if s.RuleLink("game.exe") != "Wi-Fi" || s.RuleLink("GAME.exe") != "Wi-Fi" {
		t.Fatal("rule not found case-insensitively")
	}
	if got := s.RuleLinks("launcher.exe"); len(got) != 1 || got[0] != "Phone" {
		t.Fatalf("RuleLinks = %v", got)
	}
	// changing a rule replaces it (and keeps its place)
	s.SetRule("game.exe", "Phone")
	if r := s.Rules(); len(r) != 2 || r[0] != (Rule{App: "game.exe", Link: "Phone"}) || r[1] != (Rule{App: "launcher.exe", Link: "Phone"}) {
		t.Fatalf("after replacing: %+v", r)
	}
	// empty names are ignored, an empty link removes
	s.SetRule("  ", "Wi-Fi")
	s.SetRule("launcher.exe", "")
	if r := s.Rules(); len(r) != 1 || r[0].App != "game.exe" {
		t.Fatalf("after removing: %+v", r)
	}
	// persisted
	if r := Open(dir).Rules(); len(r) != 1 || r[0] != (Rule{App: "game.exe", Link: "Phone"}) {
		t.Fatalf("after reload: %+v", r)
	}
}

func TestRuleLimit(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	s.SetRuleLimit("Game.exe", 20)
	if s.RuleLimit("game.exe") != 20 || s.RuleLink("game.exe") != "" {
		t.Fatalf("limit alone: %+v", s.Rules())
	}
	s.SetRule("game.exe", "Wi-Fi") // adding a link keeps the cap
	if r := s.Rules(); len(r) != 1 || r[0] != (Rule{App: "game.exe", Link: "Wi-Fi", Limit: 20}) {
		t.Fatalf("link added: %+v", r)
	}
	s.SetRule("game.exe", "") // removing the link keeps the cap
	if s.RuleLimit("game.exe") != 20 || len(s.Rules()) != 1 {
		t.Fatalf("link removed: %+v", s.Rules())
	}
	if Open(dir).RuleLimit("game.exe") != 20 {
		t.Fatal("cap not persisted")
	}
	s.SetRule("game.exe", "Phone")
	s.RemoveRule("Game.EXE") // removes link and cap together
	if len(s.Rules()) != 0 {
		t.Fatalf("RemoveRule left %+v", s.Rules())
	}
	s.SetRuleLimit("game.exe", 5)
	s.SetRuleLimit("game.exe", -5) // nothing left: the rule disappears
	if len(s.Rules()) != 0 {
		t.Fatalf("empty rule kept: %+v", s.Rules())
	}
}
