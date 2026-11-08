package installer

import (
	"strings"
	"testing"
)

func TestPaths(t *testing.T) {
	if exeIn(`C:\Program Files\dualnet`) != `C:\Program Files\dualnet\dualnet.exe` {
		t.Fatal(exeIn(`C:\Program Files\dualnet`))
	}
	if !samePath(`c:\Program Files\dualnet\`, `C:\PROGRAM FILES\dualnet`) {
		t.Fatal("paths must compare without regard to case or a trailing separator")
	}
	if !inside(`C:\Program Files\dualnet\dualnet.exe`, `C:\Program Files\dualnet`) {
		t.Fatal("the exe is inside its folder")
	}
	if inside(`C:\Program Files\dualnet2\x.exe`, `C:\Program Files\dualnet`) {
		t.Fatal("a sibling folder with the same prefix is not inside")
	}
	if !inside(`C:\Program Files\dualnet`, `C:\Program Files\dualnet`) {
		t.Fatal("the folder is inside itself")
	}
}

func TestCommands(t *testing.T) {
	c := selfDeleteCommand(`C:\Program Files\dualnet`)
	if !strings.Contains(c, `rmdir /s /q "C:\Program Files\dualnet"`) || !strings.HasPrefix(c, "ping ") {
		t.Fatal(c)
	}
	if got := uninstallString(`C:\Program Files\dualnet\dualnet.exe`); got != `"C:\Program Files\dualnet\dualnet.exe" uninstall` {
		t.Fatal(got)
	}
}
