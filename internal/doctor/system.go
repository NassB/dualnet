package doctor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/NassB/dualnet/internal/sysproxy"
	"github.com/NassB/dualnet/internal/transparent"
	"github.com/NassB/dualnet/internal/winsys"
)

// UseSystem fills the platform probes with the real ones.
func (e *Env) UseSystem() {
	e.Admin = transparent.Admin()
	e.WebView2 = webView2Version
	e.SimultaneousAllowed = winsys.SimultaneousAllowed
	e.ProxyStatus = func() (bool, string) {
		on, server, _ := sysproxy.Status()
		return on, server
	}
	e.AdapterUp = func(name string) bool {
		ifc, err := net.InterfaceByName(name)
		return err == nil && ifc.Flags&net.FlagUp != 0
	}
	e.Reach = reach
	e.DataWritable = dataWritable
	if exe, err := os.Executable(); err == nil {
		e.ExePath = exe
	}
}

// reach resolves a name and opens a TCP connection through the system route.
func reach() error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	const host = "www.msftconnecttest.com"
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		if err == nil {
			err = errors.New("no address")
		}
		return errors.New("DNS lookup of " + host + ": " + err.Error())
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "80"))
	if err != nil {
		return errors.New("connecting to " + host + ": " + err.Error())
	}
	c.Close()
	return nil
}

func dataWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(filepath.Clean(name))
}
