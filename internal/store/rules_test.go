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

func TestUpLimit(t *testing.T) {
	s := Open(t.TempDir())
	s.SetRuleUpLimit("Backup.exe", 5)
	if s.RuleUpLimit("backup.exe") != 5 || s.RuleLimit("backup.exe") != 0 || s.RuleLink("backup.exe") != "" {
		t.Fatal("the upload cap is independent of the download cap and the link")
	}
	s.SetRuleLimit("backup.exe", 20)
	s.SetRule("backup.exe", "Wi-Fi")
	s.SetRule("backup.exe", "")
	if s.RuleUpLimit("backup.exe") != 5 || s.RuleLimit("backup.exe") != 20 {
		t.Fatal("changing the link must keep both caps")
	}
	if s2 := Open(s.Dir); s2.RuleUpLimit("backup.exe") != 5 {
		t.Fatal("the upload cap must survive a restart")
	}
	s.SetRuleLimit("backup.exe", 0)
	s.SetRuleUpLimit("backup.exe", 0)
	if len(s.Rules()) != 0 {
		t.Fatal("a rule with nothing left is dropped")
	}
	s.SetRuleUpLimit("x.exe", 3)
	s.RemoveRule("x.exe")
	if s.RuleUpLimit("x.exe") != 0 {
		t.Fatal("removing a rule removes its upload cap")
	}
}
