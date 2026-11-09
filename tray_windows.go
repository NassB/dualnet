package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/systray"
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/store"
	"github.com/NassB/dualnet/internal/transparent"
	"github.com/NassB/dualnet/internal/ui"
)

//go:embed assets/icon.ico
var trayIcon []byte

//go:embed assets/icon-off.ico
var trayOffIcon []byte

// trayIconFor is the notification-area icon: in colour while dualnet is on, grey when off.
func trayIconFor(on bool) []byte {
	if on {
		return trayIcon
	}
	return trayOffIcon
}

const trayURL = "http://127.0.0.1:8090"

// The window and its WebView2 must live on the process's main thread.
func init() { runtime.LockOSThread() }

// runTray is the app mode (double-click on dualnet.exe): elevated, no console,
// dualnet's own window (WebView2) plus an icon in the notification area.
// Closing the window keeps dualnet running in the notification area; "Quit"
// stops it and restores normal networking. hidden starts with the window closed
// (used when starting with Windows).
func runTray(ctx context.Context, hidden bool) {
	if !transparent.Admin() {
		// Relaunch elevated (UAC prompt), hidden; this instance exits.
		args := "tray"
		if hidden {
			args += " -hidden"
		}
		if err := relaunchElevated(args); err != nil {
			messageBox("dualnet", tr("dualnet needs administrator rights for transparent mode.", "dualnet a besoin des droits administrateur pour le mode transparent.")+"\n\n"+err.Error())
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
		app:         true,
	}
	app, err := startUI(ctx, cfg)
	if err != nil {
		if strings.Contains(err.Error(), "bind") {
			// Already running: ask it to show its window (its UI is the window).
			showExisting()
			return
		}
		messageBox("dualnet", tr("Could not start:", "Démarrage impossible :")+"\n"+err.Error())
		shutdown(1)
	}
	go func() {
		if err := app.serve(); err != nil {
			messageBox("dualnet", err.Error())
			shutdown(1)
		}
	}()
	go func() { <-ctx.Done(); shutdown(0) }()

	win := &appWindow{url: app.url, show: make(chan struct{}, 1)}
	go runTrayIcon(app, win)
	if !hidden {
		win.show <- struct{}{}
	}
	win.loop() // main thread: opens the window each time it is asked to
}

// ---------- window ----------

type appWindow struct {
	url  string
	show chan struct{}

	mu  sync.Mutex
	cur webview2.WebView
}

// Show opens the window, or brings it to the front if it is already open.
func (a *appWindow) Show() {
	a.mu.Lock()
	w := a.cur
	a.mu.Unlock()
	if w != nil {
		w.Dispatch(func() { focusWindow(uintptr(w.Window())) })
		return
	}
	select {
	case a.show <- struct{}{}:
	default:
	}
}

func (a *appWindow) loop() {
	for range a.show {
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			AutoFocus: true,
			DataPath:  filepath.Join(store.DefaultDir(), "webview"),
			WindowOptions: webview2.WindowOptions{
				Title:  "dualnet",
				Width:  1180,
				Height: 800,
				IconId: 1, // the .exe's icon group (rsrc_windows_amd64.syso)
				Center: true,
			},
		})
		if w == nil {
			// WebView2 runtime missing: fall back to the browser.
			messageBox("dualnet", tr(
				"Microsoft Edge WebView2 is not installed, so the interface opens in your browser.\n\nInstall it from https://go.microsoft.com/fwlink/p/?LinkId=2124703 to get dualnet's own window.",
				"Microsoft Edge WebView2 n'est pas installé : l'interface s'ouvre dans votre navigateur.\n\nInstallez-le depuis https://go.microsoft.com/fwlink/p/?LinkId=2124703 pour avoir la fenêtre de dualnet."))
			if err := ui.OpenBrowser(a.url); err != nil {
				log.Printf("opening browser: %v", err)
			}
			continue
		}
		w.SetSize(900, 620, webview2.HintMin)
		darkTitleBar(uintptr(w.Window()))
		w.Navigate(a.url)
		a.mu.Lock()
		a.cur = w
		a.mu.Unlock()
		w.Run() // returns when the window is closed
		a.mu.Lock()
		a.cur = nil
		a.mu.Unlock()
	}
}

// ---------- notification-area icon ----------

func runTrayIcon(app *uiApp, win *appWindow) {
	runtime.LockOSThread() // the icon's hidden window and message loop live on this thread
	systray.Run(func() {
		systray.SetIcon(trayIconFor(app.tun.On()))
		systray.SetTitle("dualnet")
		systray.SetTooltip("dualnet " + version + " — " + tr("combined internet links", "connexions internet combinées"))
		open := systray.AddMenuItem(tr("Open dualnet", "Ouvrir dualnet"), "")
		systray.AddSeparator()
		power := systray.AddMenuItemCheckbox(tr("dualnet on", "dualnet activé"), "", app.tun.On())
		auto := systray.AddMenuItemCheckbox(tr("Start with Windows", "Démarrer avec Windows"), "", autostartEnabled())
		systray.AddSeparator()
		quit := systray.AddMenuItem(tr("Quit (restores normal networking)", "Quitter (rétablit le réseau normal)"), "")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					win.Show()
				case <-power.ClickedCh:
					if err := app.srv.SetPower(!power.Checked()); err != nil {
						messageBox("dualnet", err.Error())
					}
					setChecked(power, app.tun.On())
				case <-auto.ClickedCh:
					if err := setAutostart(!auto.Checked()); err != nil {
						messageBox("dualnet", tr("Start with Windows: ", "Démarrage automatique : ")+err.Error())
					}
					setChecked(auto, autostartEnabled())
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
		// Keep the checkbox, the icon and the tooltip current (changes can come from the window).
		go func() {
			prev := map[*links.Link]int64{}
			last, lastOn := time.Now(), app.tun.On()
			for range time.Tick(2 * time.Second) {
				on := app.tun.On()
				setChecked(power, on)
				if on != lastOn {
					lastOn = on
					systray.SetIcon(trayIconFor(on))
				}
				now := time.Now()
				dt := now.Sub(last)
				last = now
				var speeds []linkSpeed
				for _, l := range app.srv.Pool.All() {
					cur := l.BytesIn.Load()
					if p, seen := prev[l]; seen && l.Up() {
						speeds = append(speeds, linkSpeed{l.Name, links.Mbps(cur-p, dt)})
					}
					prev[l] = cur
				}
				state := tr("off", "désactivé")
				if on {
					state = tr("on", "actif")
				}
				systray.SetTooltip(trayTooltip(version, state, speeds))
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

// ---------- Win32 helpers ----------

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")
	kernel = syscall.NewLazyDLL("kernel32.dll")
)

// showExisting brings the window of an already running dualnet to the front.
func showExisting() {
	title, _ := syscall.UTF16PtrFromString("dualnet")
	hwnd, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		focusWindow(hwnd)
		return
	}
	messageBox("dualnet", tr(
		"dualnet is already running: open it from its icon in the notification area.\n\nIf it is an older version started from PowerShell, close it (Ctrl+C) and start dualnet.exe again.",
		"dualnet est déjà lancé : ouvrez-le depuis son icône près de l'horloge.\n\nSi c'est une ancienne version lancée depuis PowerShell, fermez-la (Ctrl+C) puis relancez dualnet.exe."))
}

func focusWindow(hwnd uintptr) {
	const swRestore = 9
	user32.NewProc("ShowWindow").Call(hwnd, swRestore)
	user32.NewProc("SetForegroundWindow").Call(hwnd)
}

// darkTitleBar matches the title bar to Windows' dark mode.
func darkTitleBar(hwnd uintptr) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	light, _, err := k.GetIntegerValue("AppsUseLightTheme")
	k.Close()
	if err != nil || light != 0 {
		return
	}
	on := int32(1)
	const dwmwaUseImmersiveDarkMode = 20
	dwmapi.NewProc("DwmSetWindowAttribute").Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), 4)
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
	kernel.NewProc("FreeConsole").Call()
}

func messageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONINFORMATION)
}

// tr picks the French text when Windows' display language is French, English otherwise.
func tr(en, fr string) string {
	if windowsFrench {
		return fr
	}
	return en
}

var windowsFrench = func() bool {
	lang, _, _ := kernel.NewProc("GetUserDefaultUILanguage").Call()
	return lang&0x3ff == 0x0c // LANG_FRENCH
}()

// openWindow shows the window for url until it is closed.
func openWindow(url string) {
	w := &appWindow{url: url, show: make(chan struct{}, 1)}
	w.show <- struct{}{}
	close(w.show)
	w.loop()
}
