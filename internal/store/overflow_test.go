package store

import "testing"

func TestOverflowSetting(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if s.IsOverflow("Phone") {
		t.Fatal("no link is an overflow link by default")
	}
	s.SetOverflow("Phone", true)
	s.SetOverflow("Wi-Fi", true)
	s.SetOverflow("Wi-Fi", false)
	r := Open(dir)
	if !r.IsOverflow("Phone") || r.IsOverflow("Wi-Fi") {
		t.Fatalf("after reload: Phone=%v Wi-Fi=%v", r.IsOverflow("Phone"), r.IsOverflow("Wi-Fi"))
	}
}
