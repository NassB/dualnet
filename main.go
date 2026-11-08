// dualnet combines several internet connections (e.g. Wi-Fi box + USB phone tethering)
// on one Windows PC, without any remote server:
//
//	dualnet interfaces                 list network interfaces
//	dualnet get [options] <url>        download one file over all links at once
//	dualnet proxy [options]            local SOCKS5/HTTP proxy spreading connections
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/NassB/dualnet/internal/download"
	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/notify"
	"github.com/NassB/dualnet/internal/proxy"
	"github.com/NassB/dualnet/internal/store"
	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/ui"
	"github.com/NassB/dualnet/internal/update"
)

// version is set at build time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, `dualnet — combine several internet connections

Usage:
  dualnet       (no argument: notification-area icon, as administrator)
  dualnet ui    [-if ...] [-listen 127.0.0.1:8090] [-proxy 127.0.0.1:1080] [-dir folder] [-sticky 0] [-system-proxy] [-transparent]
  dualnet window [http://127.0.0.1:8090]   (window for a running dualnet)
  dualnet version
  dualnet interfaces
  dualnet get   [-if "Wi-Fi,Ethernet 2"] [-o file] [-chunk 4] [-workers 4] <url>
  dualnet proxy [-if "Wi-Fi,Ethernet 2"] [-listen 127.0.0.1:1080] [-sticky 10m] [-system-proxy]`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		// Double-click on dualnet.exe: run in the notification area.
		os.Args = append(os.Args, "tray")
	}
	// SIGTERM is what Windows sends when the console window is closed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println("dualnet", version)

	case "interfaces":
		lines, err := links.ListAll()
		check(err)
		fmt.Println(strings.Join(lines, "\n"))
		if ls, err := links.Discover(nil); err == nil {
			fmt.Println("\nAutomatic selection:")
			for _, l := range ls {
				fmt.Println("  ", l)
			}
		}

	case "get":
		fs := flag.NewFlagSet("get", flag.ExitOnError)
		ifs := fs.String("if", "", "interfaces to use (names or IPs, comma-separated)")
		out := fs.String("o", "", "output file")
		chunk := fs.Int("chunk", 2, "chunk size in MB")
		workers := fs.Int("workers", 4, "parallel connections per link")
		fs.Parse(os.Args[2:])
		if fs.NArg() != 1 {
			usage()
		}
		ls := discover(*ifs)
		check(download.Run(ctx, ls, download.Options{
			URL:           fs.Arg(0),
			Output:        *out,
			ChunkSize:     int64(*chunk) << 20,
			WorkersPerLnk: *workers,
		}))

	case "proxy":
		fs := flag.NewFlagSet("proxy", flag.ExitOnError)
		ifs := fs.String("if", "", "interfaces to use (names or IPs, comma-separated)")
		listen := fs.String("listen", "127.0.0.1:1080", "listen address")
		sticky := fs.Duration("sticky", 10*time.Minute, "keep a site on the same link for this long")
		sys := fs.Bool("system-proxy", false, "point the Windows proxy to dualnet (restored on exit)")
		fs.Parse(os.Args[2:])
		restoreStale()
		pool := watchPool(ctx, *ifs)
		s := &proxy.Server{Pool: pool, Sticky: *sticky}
		if *sys {
			check(sysproxy.Enable(*listen))
			fmt.Println("Windows proxy set to", *listen, "(restored on exit)")
		}
		go exitRestoring(ctx)
		check(s.ListenAndServe(*listen))

	case "ui":
		fs := flag.NewFlagSet("ui", flag.ExitOnError)
		cfg := uiConfig{}
		fs.StringVar(&cfg.ifs, "if", "", "interfaces to use (names or IPs, comma-separated)")
		fs.StringVar(&cfg.listen, "listen", "127.0.0.1:8090", "web interface address")
		fs.StringVar(&cfg.proxyAddr, "proxy", "127.0.0.1:1080", `proxy address ("" to disable)`)
		home, _ := os.UserHomeDir()
		fs.StringVar(&cfg.dir, "dir", filepath.Join(home, "Downloads"), "download folder")
		open := fs.Bool("open", true, "open the browser")
		fs.DurationVar(&cfg.sticky, "sticky", 10*time.Minute, "keep a site on the same link (0 = spread every connection, best for Steam)")
		fs.BoolVar(&cfg.systemProxy, "system-proxy", false, "point the Windows proxy to dualnet at startup (restored on exit)")
		fs.BoolVar(&cfg.transparent, "transparent", false, "capture all PC traffic with a virtual adapter (administrator)")
		fs.Parse(os.Args[2:])
		setupLog(true)
		app, err := startUI(ctx, cfg)
		check(err)
		fmt.Println("Interface:", app.url)
		if *open {
			ui.OpenBrowser(app.url)
		}
		go exitRestoring(ctx)
		check(app.serve())

	case "window":
		// Opens dualnet's window on an already running instance (no admin needed).
		url := trayURL
		if len(os.Args) > 2 {
			url = os.Args[2]
		}
		openWindow(url)

	case "tray":
		setupLog(false)
		log.Println("dualnet started (notification area)")
		runTray(ctx, len(os.Args) > 2 && os.Args[2] == "-hidden")

	default:
		usage()
	}
}

type uiConfig struct {
	ifs, listen, proxyAddr, dir string
	sticky                      time.Duration
	systemProxy, transparent    bool
	// app mode (double-click): follow the saved main switch instead of the flags
	app bool
}

type uiApp struct {
	url   string
	tun   *ui.TunControl
	srv   *ui.Server
	serve func() error
}

// startUI starts the link pool, proxy, transparent mode (if asked), history and
// the web server's listener. cleanup is set to undo it all on exit.
func startUI(ctx context.Context, cfg uiConfig) (*uiApp, error) {
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return nil, err
	}
	restoreStale()
	if cfg.proxyAddr != "" {
		sysproxy.ClearOurs(cfg.proxyAddr) // leftover from an earlier run
	}
	pool := watchPool(ctx, cfg.ifs)
	proxyAddrInUse = cfg.proxyAddr
	p := &proxy.Server{Pool: pool, Sticky: cfg.sticky}
	if cfg.proxyAddr != "" {
		go func() { check(p.ListenAndServe(cfg.proxyAddr)) }()
	}
	st := store.Open(store.DefaultDir())
	for _, l := range pool.All() {
		st.ApplyLimit(l)
	}
	prevChange := pool.OnChange
	notifier := &notify.Notifier{Enabled: st.NotifyEnabled, French: windowsFrench}
	pool.OnChange = func(l *links.Link, ev string) {
		if ev == links.EventAdded {
			st.ApplyLimit(l)
		}
		prevChange(l, ev)
		notifier.LinkEvent(l.Name, ev, len(pool.Up()))
	}
	go st.Run(ctx, pool)
	checker := &update.Checker{Current: version, Repo: "NassB/dualnet", Enabled: st.UpdateCheckEnabled}
	go checker.Run(ctx, 24*time.Hour)
	tun := &ui.TunControl{Proxy: p}
	cleanup = func() {
		tun.Stop()
		st.Record(pool.All(), time.Now())
		st.Save()
	}
	if cfg.transparent && (!cfg.app || st.Enabled()) {
		if err := tun.Set(true); err != nil {
			return nil, err
		}
		fmt.Println("Transparent mode on: all IPv4 traffic goes through dualnet.")
	}
	if cfg.systemProxy && cfg.proxyAddr != "" {
		if err := sysproxy.Enable(cfg.proxyAddr); err != nil {
			return nil, err
		}
	}
	srv := &ui.Server{Pool: pool, ProxyAddr: cfg.proxyAddr, OutDir: cfg.dir, Tun: tun, Store: st, Version: version, Update: checker, Notify: notifier, Autostart: autostartHooks(),
		OnSettings: func() { st.RememberLimits(pool.All()) }}
	return &uiApp{
		url:   "http://" + ln.Addr().String(),
		tun:   tun,
		srv:   srv,
		serve: func() error { return http.Serve(ln, srv.Handler()) },
	}, nil
}

// shutdown undoes every system change and exits.
func shutdown(code int) {
	cleanup()
	if sysproxy.Pending() {
		sysproxy.Disable()
	}
	sysproxy.ClearOurs(proxyAddrInUse)
	os.Exit(code)
}

// watchPool discovers the links and keeps them current (every 2 s) while dualnet runs.
func watchPool(ctx context.Context, sel string) *links.Pool {
	var parts []string
	if sel != "" {
		parts = strings.Split(sel, ",")
	}
	pool := links.NewPool(parts)
	fmt.Println("Connected links:")
	for _, l := range pool.Up() {
		fmt.Println("  ", l)
	}
	if len(pool.Up()) < 2 {
		fmt.Println("Only one link for now: others are added as soon as they connect.")
	}
	pool.OnChange = func(l *links.Link, ev string) { log.Printf("Link %s: %s", l, ev) }
	go pool.Watch(ctx, 2*time.Second)
	go pool.Health(ctx, 5*time.Second)
	return pool
}

func discover(sel string) []*links.Link {
	var parts []string
	if sel != "" {
		parts = strings.Split(sel, ",")
	}
	ls, err := links.Discover(parts)
	check(err)
	fmt.Println("Links used:")
	for _, l := range ls {
		fmt.Println("  ", l)
	}
	if len(ls) < 2 {
		fmt.Println("Warning: only one link found, nothing to combine.")
	}
	return ls
}

// restoreStale puts back the Windows proxy settings if a previous dualnet run was
// killed while it had them pointing to itself.
func restoreStale() {
	if sysproxy.Pending() {
		if err := sysproxy.Disable(); err == nil {
			fmt.Println("Restored the Windows proxy left by a previous session.")
		}
	}
}

// exitRestoring waits for Ctrl+C / window close, restores the proxy, then exits.
func exitRestoring(ctx context.Context) {
	<-ctx.Done()
	fmt.Println("Stopping: Windows settings restored.")
	shutdown(0)
}

// proxyAddrInUse is dualnet's proxy address, so a Windows proxy left pointing
// to it can be cleared on exit.
var proxyAddrInUse = "127.0.0.1:1080"

// cleanup undoes system changes on exit (transparent mode adapter).
var cleanup = func() {}

func check(err error) {
	if err != nil {
		log.Println("Error:", err)
		cleanup()
		sysproxy.Disable()
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
