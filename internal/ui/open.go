package ui

import (
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
)

func launch(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening with %s: %w", name, err)
	}
	go func() {
		// explorer.exe exits with status 1 even when it did what was asked
		if err := cmd.Wait(); err != nil && name != "explorer" {
			log.Printf("opening with %s: %v", name, err)
		}
	}()
	return nil
}

func openCommand(osName, target string) (string, []string, error) {
	switch osName {
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}, nil
	case "darwin":
		return "open", []string{target}, nil
	case "linux":
		return "xdg-open", []string{target}, nil
	default:
		return "", nil, fmt.Errorf("opening files is not supported on %s", osName)
	}
}

// OpenBrowser opens an HTTP(S) URL in the default browser.
func OpenBrowser(target string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid browser URL")
	}
	name, args, err := openCommand(runtime.GOOS, target)
	if err != nil {
		return err
	}
	return launch(name, args...)
}

func openFolder(dir string) error {
	if runtime.GOOS == "windows" {
		return launch("explorer", dir)
	}
	name, args, err := openCommand(runtime.GOOS, dir)
	if err != nil {
		return err
	}
	return launch(name, args...)
}

func revealFile(path string) error {
	switch runtime.GOOS {
	case "windows":
		return launch("explorer", "/select,", path)
	case "darwin":
		return launch("open", "-R", path)
	default:
		return openFolder(filepath.Dir(path))
	}
}
