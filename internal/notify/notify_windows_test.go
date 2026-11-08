package notify

import (
	"strings"
	"testing"
)

// Whatever the link name contains, it must reach PowerShell as data (an
// environment variable), never as part of the script.
func TestToastTextIsNotInTheScript(t *testing.T) {
	evil := `x'; Start-Process calc; $(Remove-Item C:\important) "`
	c := command("title", evil)
	for _, a := range c.Args {
		if strings.Contains(a, "calc") || strings.Contains(a, "Remove-Item") {
			t.Fatalf("user text found in the command line: %q", a)
		}
	}
	found := false
	for _, e := range c.Env {
		if e == "DUALNET_BODY="+evil {
			found = true
		}
	}
	if !found {
		t.Fatal("the text must be passed through DUALNET_BODY")
	}
	if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow {
		t.Fatal("PowerShell must run without a visible window")
	}
}
