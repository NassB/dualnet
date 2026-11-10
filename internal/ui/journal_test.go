package ui

import (
	"fmt"
	"testing"
)

func TestJournal(t *testing.T) {
	var none *Journal
	none.Add("x", "added") // a missing journal ignores events
	if len(none.List()) != 0 {
		t.Fatal("a nil journal is empty")
	}
	j := &Journal{}
	j.Add("Wi-Fi", "disconnected")
	j.Add("Wi-Fi", "reconnected")
	if l := j.List(); len(l) != 2 || l[0].Event != "reconnected" || l[1].Event != "disconnected" {
		t.Fatalf("most recent first, got %+v", l)
	}
	for i := 0; i < journalSize+50; i++ {
		j.Add("L", fmt.Sprint(i))
	}
	l := j.List()
	if len(l) != journalSize || l[0].Event != fmt.Sprint(journalSize+49) {
		t.Fatalf("%d events kept, first %q", len(l), l[0].Event)
	}
}
