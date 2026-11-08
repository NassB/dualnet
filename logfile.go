package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/NassB/dualnet/internal/store"
)

// setupLog writes log lines to %APPDATA%\dualnet\dualnet.log (and the console
// when there is one). The file is restarted when it passes 5 MB, keeping the
// previous one as dualnet.old.log.
func setupLog(console bool) {
	dir := store.DefaultDir()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "dualnet.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		os.Rename(path, filepath.Join(dir, "dualnet.old.log"))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	if console {
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	} else {
		log.SetOutput(f)
	}
}
