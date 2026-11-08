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
	if !strings.HasPrefix(c, "ping ") || !strings.HasSuffix(c, `& rmdir "C:\Program Files\dualnet"`) ||
		!strings.Contains(c, `del /f /q "C:\Program Files\dualnet\dualnet.exe"`) || strings.Contains(c, "/s") {
		t.Fatal(c) // never a recursive delete
	}
	if got := uninstallString(`C:\Program Files\dualnet\dualnet.exe`); got != `"C:\Program Files\dualnet\dualnet.exe" uninstall` {
		t.Fatal(got)
	}
}

func TestSafety(t *testing.T) {
	if !installable(nil) || !installable([]string{"dualnet.exe", "wintun.dll"}) || !installable([]string{"DualNet.EXE"}) {
		t.Fatal("an empty folder or one with dualnet.exe must be accepted")
	}
	if installable([]string{"Documents", "notes.txt"}) {
		t.Fatal("a folder with other people's files must be refused")
	}
	if !dataDirOK(`C:\Users\me\AppData\Roaming\dualnet`) || !dataDirOK(`D:\x\DualNet\`) {
		t.Fatal("dualnet's own folder is fine")
	}
	for _, bad := range []string{"", `C:\Users\me\Documents`, `C:\`, `C:\Users\me`} {
		if dataDirOK(bad) {
			t.Fatalf("%q must not be purged", bad)
		}
	}
}
