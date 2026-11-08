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
	"github.com/NassB/dualnet/internal/proxy"
	"github.com/NassB/dualnet/internal/store"
	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/ui"
)

func usage() {
	fmt.Fprintln(os.Stderr, `dualnet — combine plusieurs connexions internet

Usage :
  dualnet       (sans argument : icône dans la barre des tâches, en administrateur)
  dualnet ui    [-if ...] [-listen 127.0.0.1:8090] [-proxy 127.0.0.1:1080] [-dir dossier] [-sticky 0] [-system-proxy] [-transparent]
  dualnet interfaces
  dualnet get   [-if "Wi-Fi,Ethernet 2"] [-o fichier] [-chunk 4] [-workers 4] <url>
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
	case "interfaces":
		lines, err := links.ListAll()
		check(err)
		fmt.Println(strings.Join(lines, "\n"))
		if ls, err := links.Discover(nil); err == nil {
			fmt.Println("\nSélection automatique :")
			for _, l := range ls {
				fmt.Println("  ", l)
			}
		}

	case "get":
		fs := flag.NewFlagSet("get", flag.ExitOnError)
		ifs := fs.String("if", "", "interfaces à utiliser (noms ou IP, séparés par des virgules)")
		out := fs.String("o", "", "fichier de sortie")
		chunk := fs.Int("chunk", 2, "taille des morceaux en Mo")
		workers := fs.Int("workers", 4, "connexions parallèles par lien")
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
		ifs := fs.String("if", "", "interfaces à utiliser (noms ou IP, séparés par des virgules)")
		listen := fs.String("listen", "127.0.0.1:1080", "adresse d'écoute")
		sticky := fs.Duration("sticky", 10*time.Minute, "garder un même site sur le même lien pendant cette durée")
		sys := fs.Bool("system-proxy", false, "régler le proxy de Windows sur dualnet (restauré à la fermeture)")
		fs.Parse(os.Args[2:])
		restoreStale()
		pool := watchPool(ctx, *ifs)
		s := &proxy.Server{Pool: pool, Sticky: *sticky}
		if *sys {
			check(sysproxy.Enable(*listen))
			fmt.Println("Proxy Windows réglé sur", *listen, "(restauré à la fermeture)")
		}
		go exitRestoring(ctx)
		check(s.ListenAndServe(*listen))

	case "ui":
		fs := flag.NewFlagSet("ui", flag.ExitOnError)
		cfg := uiConfig{}
		fs.StringVar(&cfg.ifs, "if", "", "interfaces à utiliser (noms ou IP, séparés par des virgules)")
		fs.StringVar(&cfg.listen, "listen", "127.0.0.1:8090", "adresse de l'interface web")
		fs.StringVar(&cfg.proxyAddr, "proxy", "127.0.0.1:1080", `adresse du proxy ("" pour le désactiver)`)
		home, _ := os.UserHomeDir()
		fs.StringVar(&cfg.dir, "dir", filepath.Join(home, "Downloads"), "dossier des téléchargements")
		open := fs.Bool("open", true, "ouvrir le navigateur")
		fs.DurationVar(&cfg.sticky, "sticky", 10*time.Minute, "garder un même site sur le même lien (0 = répartir chaque connexion, idéal pour Steam)")
		fs.BoolVar(&cfg.systemProxy, "system-proxy", false, "régler le proxy de Windows sur dualnet au démarrage (restauré à la fermeture)")
		fs.BoolVar(&cfg.transparent, "transparent", false, "capter tout le trafic du PC via une carte virtuelle (administrateur)")
		fs.Parse(os.Args[2:])
		setupLog(true)
		app, err := startUI(ctx, cfg)
		check(err)
		fmt.Println("Interface :", app.url)
		if *open {
			ui.OpenBrowser(app.url)
		}
		go exitRestoring(ctx)
		check(app.serve())

	case "tray":
		setupLog(false)
		log.Println("dualnet démarré (barre des tâches)")
		runTray(ctx)

	default:
		usage()
	}
}

type uiConfig struct {
	ifs, listen, proxyAddr, dir string
	sticky                      time.Duration
	systemProxy, transparent    bool
}

type uiApp struct {
	url   string
	tun   *ui.TunControl
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
	pool := watchPool(ctx, cfg.ifs)
	p := &proxy.Server{Pool: pool, Sticky: cfg.sticky}
	if cfg.proxyAddr != "" {
		go func() { check(p.ListenAndServe(cfg.proxyAddr)) }()
	}
	st := store.Open(store.DefaultDir())
	for _, l := range pool.All() {
		st.ApplyLimit(l)
	}
	prevChange := pool.OnChange
	pool.OnChange = func(l *links.Link, ev string) {
		if ev == "ajouté" {
			st.ApplyLimit(l)
		}
		prevChange(l, ev)
	}
	go st.Run(ctx, pool)
	tun := &ui.TunControl{Proxy: p}
	cleanup = func() {
		tun.Stop()
		st.Record(pool.All(), time.Now())
		st.Save()
	}
	if cfg.transparent {
		if err := tun.Set(true); err != nil {
			return nil, err
		}
		fmt.Println("Mode transparent activé : tout le trafic IPv4 passe par dualnet.")
	}
	if cfg.systemProxy && cfg.proxyAddr != "" {
		if err := sysproxy.Enable(cfg.proxyAddr); err != nil {
			return nil, err
		}
	}
	srv := &ui.Server{Pool: pool, ProxyAddr: cfg.proxyAddr, OutDir: cfg.dir, Tun: tun, Store: st,
		OnSettings: func() { st.RememberLimits(pool.All()) }}
	return &uiApp{
		url:   "http://" + ln.Addr().String(),
		tun:   tun,
		serve: func() error { return http.Serve(ln, srv.Handler()) },
	}, nil
}

// shutdown undoes every system change and exits.
func shutdown(code int) {
	cleanup()
	if sysproxy.Pending() {
		sysproxy.Disable()
	}
	os.Exit(code)
}

// watchPool discovers the links and keeps them current (every 2 s) while dualnet runs.
func watchPool(ctx context.Context, sel string) *links.Pool {
	var parts []string
	if sel != "" {
		parts = strings.Split(sel, ",")
	}
	pool := links.NewPool(parts)
	fmt.Println("Liens connectés :")
	for _, l := range pool.Up() {
		fmt.Println("  ", l)
	}
	if len(pool.Up()) < 2 {
		fmt.Println("Un seul lien pour l'instant : les autres seront ajoutés dès qu'ils se connectent.")
	}
	pool.OnChange = func(l *links.Link, ev string) { log.Printf("Lien %s : %s", l, ev) }
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
	fmt.Println("Liens utilisés :")
	for _, l := range ls {
		fmt.Println("  ", l)
	}
	if len(ls) < 2 {
		fmt.Println("Attention : un seul lien détecté, pas d'agrégation possible.")
	}
	return ls
}

// restoreStale puts back the Windows proxy settings if a previous dualnet run was
// killed while it had them pointing to itself.
func restoreStale() {
	if sysproxy.Pending() {
		if err := sysproxy.Disable(); err == nil {
			fmt.Println("Proxy Windows d'une session précédente restauré.")
		}
	}
}

// exitRestoring waits for Ctrl+C / window close, restores the proxy, then exits.
func exitRestoring(ctx context.Context) {
	<-ctx.Done()
	fmt.Println("Arrêt : réglages Windows restaurés.")
	shutdown(0)
}

// cleanup undoes system changes on exit (transparent mode adapter).
var cleanup = func() {}

func check(err error) {
	if err != nil {
		log.Println("Erreur :", err)
		cleanup()
		sysproxy.Disable()
		fmt.Fprintln(os.Stderr, "Erreur :", err)
		os.Exit(1)
	}
}
