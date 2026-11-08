package main

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/NassB/dualnet/internal/transparent"
	"github.com/NassB/dualnet/internal/ui"
)

//go:embed assets/icon.ico
var trayIcon []byte

const trayURL = "http://127.0.0.1:8090"

// runTray is the double-click mode: elevated, no console window, an icon in the
// notification area, the UI on 127.0.0.1:8090 and the transparent mode on.
func runTray(ctx context.Context) {
	if !transparent.Admin() {
		// Relaunch elevated (UAC prompt), hidden; this instance exits.
		if err := relaunchElevated("tray"); err != nil {
			messageBox("dualnet", "dualnet a besoin des droits administrateur pour le mode transparent.\n\n"+err.Error())
		}
		return
	}
	hideConsole()
	home, _ := os.UserHomeDir()
	cfg := uiConfig{
		listen:      "127.0.0.1:8090",
		proxyAddr:   "127.0.0.1:1080",
		dir:         filepath.Join(home, "Downloads"),
		transparent: true,
	}
	app, err := startUI(ctx, cfg)
	if err != nil {
		if strings.Contains(err.Error(), "bind") {
			ui.OpenBrowser(trayURL) // already running: just show it
			return
		}
		messageBox("dualnet", "Démarrage impossible :\n"+err.Error())
		shutdown(1)
	}
	go func() {
		if err := app.serve(); err != nil {
			messageBox("dualnet", err.Error())
			shutdown(1)
		}
	}()
	go func() { <-ctx.Done(); shutdown(0) }()
	ui.OpenBrowser(app.url)

	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("dualnet")
		systray.SetTooltip("dualnet — Wi-Fi + partage combinés")
		open := systray.AddMenuItem("Ouvrir l'interface", "")
		systray.AddSeparator()
		transp := systray.AddMenuItemCheckbox("Mode transparent (tout le PC)", "", app.tun.On())
		auto := systray.AddMenuItemCheckbox("Démarrer avec Windows", "", autostartEnabled())
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quitter (rétablit le réseau normal)", "")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					ui.OpenBrowser(app.url)
				case <-transp.ClickedCh:
					if err := app.tun.Set(!transp.Checked()); err != nil {
						messageBox("dualnet", err.Error())
					}
					setChecked(transp, app.tun.On())
				case <-auto.ClickedCh:
					if err := setAutostart(!auto.Checked()); err != nil {
						messageBox("dualnet", "Démarrage automatique : "+err.Error())
					}
					setChecked(auto, autostartEnabled())
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
		// Keep the checkbox in sync with changes made from the web UI.
		go func() {
			for range time.Tick(2 * time.Second) {
				setChecked(transp, app.tun.On())
			}
		}()
	}, func() { shutdown(0) })
}

func setChecked(m *systray.MenuItem, on bool) {
	if on && !m.Checked() {
		m.Check()
	} else if !on && m.Checked() {
		m.Uncheck()
	}
}

func relaunchElevated(args string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(args)
	dir, _ := syscall.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_HIDE)
}

func hideConsole() {
	syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}

func messageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONINFORMATION)
}
