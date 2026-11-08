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
	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/ui"
)

func usage() {
	fmt.Fprintln(os.Stderr, `dualnet — combine plusieurs connexions internet

Usage :
  dualnet ui    [-if ...] [-listen 127.0.0.1:8090] [-proxy 127.0.0.1:1080] [-dir dossier] [-sticky 0] [-system-proxy]
  dualnet interfaces
  dualnet get   [-if "Wi-Fi,Ethernet 2"] [-o fichier] [-chunk 4] [-workers 4] <url>
  dualnet proxy [-if "Wi-Fi,Ethernet 2"] [-listen 127.0.0.1:1080] [-sticky 10m] [-system-proxy]`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
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
		ls := discover(*ifs)
		s := &proxy.Server{Links: ls, Sticky: *sticky}
		if *sys {
			check(sysproxy.Enable(*listen))
			fmt.Println("Proxy Windows réglé sur", *listen, "(restauré à la fermeture)")
		}
		go exitRestoring(ctx)
		check(s.ListenAndServe(*listen))

	case "ui":
		fs := flag.NewFlagSet("ui", flag.ExitOnError)
		ifs := fs.String("if", "", "interfaces à utiliser (noms ou IP, séparés par des virgules)")
		listen := fs.String("listen", "127.0.0.1:8090", "adresse de l'interface web")
		proxyAddr := fs.String("proxy", "127.0.0.1:1080", `adresse du proxy ("" pour le désactiver)`)
		home, _ := os.UserHomeDir()
		dir := fs.String("dir", filepath.Join(home, "Downloads"), "dossier des téléchargements")
		open := fs.Bool("open", true, "ouvrir le navigateur")
		sticky := fs.Duration("sticky", 10*time.Minute, "garder un même site sur le même lien (0 = répartir chaque connexion, idéal pour Steam)")
		sys := fs.Bool("system-proxy", false, "régler le proxy de Windows sur dualnet au démarrage (restauré à la fermeture)")
		fs.Parse(os.Args[2:])
		restoreStale()
		ls := discover(*ifs)
		if *proxyAddr != "" {
			p := &proxy.Server{Links: ls, Sticky: *sticky}
			go func() { check(p.ListenAndServe(*proxyAddr)) }()
		}
		if *sys && *proxyAddr != "" {
			check(sysproxy.Enable(*proxyAddr))
		}
		srv := &ui.Server{Links: ls, ProxyAddr: *proxyAddr, OutDir: *dir}
		ln, err := net.Listen("tcp", *listen)
		check(err)
		url := "http://" + ln.Addr().String()
		fmt.Println("Interface :", url)
		if *open {
			ui.OpenBrowser(url)
		}
		go exitRestoring(ctx)
		check(http.Serve(ln, srv.Handler()))

	default:
		usage()
	}
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
	if sysproxy.Pending() {
		if err := sysproxy.Disable(); err != nil {
			fmt.Fprintln(os.Stderr, "Impossible de restaurer le proxy Windows :", err)
		} else {
			fmt.Println("Proxy Windows restauré.")
		}
	}
	os.Exit(0)
}

func check(err error) {
	if err != nil {
		sysproxy.Disable()
		fmt.Fprintln(os.Stderr, "Erreur :", err)
		os.Exit(1)
	}
}
