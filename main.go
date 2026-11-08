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
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/NassB/dualnet/internal/download"
	"github.com/NassB/dualnet/internal/links"
	"github.com/NassB/dualnet/internal/proxy"
)

func usage() {
	fmt.Fprintln(os.Stderr, `dualnet — combine plusieurs connexions internet

Usage :
  dualnet interfaces
  dualnet get   [-if "Wi-Fi,Ethernet 2"] [-o fichier] [-chunk 4] [-workers 4] <url>
  dualnet proxy [-if "Wi-Fi,Ethernet 2"] [-listen 127.0.0.1:1080] [-sticky 10m]`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
		chunk := fs.Int("chunk", 4, "taille des morceaux en Mo")
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
		fs.Parse(os.Args[2:])
		ls := discover(*ifs)
		s := &proxy.Server{Links: ls, Sticky: *sticky}
		go func() { <-ctx.Done(); os.Exit(0) }()
		check(s.ListenAndServe(*listen))

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

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur :", err)
		os.Exit(1)
	}
}
