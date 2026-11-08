package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/NassB/dualnet/internal/installer"
	"github.com/NassB/dualnet/internal/store"
)

func printSteps(r *installer.Result) {
	if r == nil {
		return
	}
	for _, s := range r.Steps {
		fmt.Println(" -", s)
	}
}

// runInstall copies dualnet to Program Files and registers it.
func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	dir := fs.String("dir", "", "install folder (default: Program Files\\dualnet)")
	fs.Parse(args)
	installer.Version = version
	r, err := installer.Install(*dir)
	printSteps(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		return 1
	}
	fmt.Println("dualnet is installed: start it from the Start Menu.")
	return 0
}

// runUninstall removes dualnet; -purge also deletes the data folder.
func runUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete your data (history, settings, rules, log)")
	force := fs.Bool("force", false, "do not ask for confirmation")
	fs.Parse(args)
	data := store.DefaultDir()
	if !*force {
		what := "Remove dualnet (program, shortcut, start-with-Windows task)"
		if *purge {
			what += " AND your data in " + data
		}
		fmt.Print(what + "? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("nothing was changed.")
			return 1
		}
	}
	r, err := installer.Uninstall(installer.Options{Purge: *purge, DataDir: data})
	printSteps(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall failed:", err)
		return 1
	}
	fmt.Println("dualnet is removed.")
	return 0
}
