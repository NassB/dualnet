package ui

import (
	"reflect"
	"testing"
)

func TestOpenCommand(t *testing.T) {
	for _, tt := range []struct {
		os, name string
		args     []string
	}{
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", "https://example.com"}},
		{"darwin", "open", []string{"https://example.com"}},
		{"linux", "xdg-open", []string{"https://example.com"}},
	} {
		name, args, err := openCommand(tt.os, "https://example.com")
		if err != nil || name != tt.name || !reflect.DeepEqual(args, tt.args) {
			t.Errorf("%s: %s %v, %v", tt.os, name, args, err)
		}
	}
	if _, _, err := openCommand("other", "x"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestOpenBrowserRejectsInvalidURLs(t *testing.T) {
	for _, target := range []string{"file:///tmp/a", "https:", "javascript:alert(1)", ""} {
		if err := OpenBrowser(target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
}
