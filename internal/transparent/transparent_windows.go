// Package transparent captures all of Windows' internet traffic (games, downloads…)
// with a virtual network adapter (Wintun) and hands every connection to dualnet's
// link picker. No per-application proxy setting is needed.
//
// How it works:
//   - a Wintun adapter "dualnet" is created (requires administrator rights);
//   - routes 0.0.0.0/1 and 128.0.0.0/1 send IPv4 traffic to it (more specific
//     than the real default routes, which stay in place);
//   - it gets a private IPv6 address and ::/1, 8000::/1 routes: Windows then
//     prefers IPv4 for internet destinations, and IPv6 attempts are refused so
//     applications fall back to IPv4 (dualnet's links are IPv4);
//   - tun2socks (gVisor TCP/IP stack) terminates the connections and dials them
//     again through a link. dualnet's own sockets are bound to a link's IP, so
//     Windows (strong host model) sends them out of that interface, not back
//     into the adapter.
//
// When dualnet stops or crashes, the adapter disappears and its routes with it.
package transparent

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"

	"github.com/NassB/dualnet/internal/winexec"
)

// wintun.dll 0.14.1 (amd64) from wintun.net, signed by WireGuard LLC.
// See WINTUN-LICENSE.txt.
//
//go:embed wintun.dll
var wintunDLL []byte

const adapterName = "dualnet"

var ErrNotAdmin = errors.New("transparent mode requires administrator rights")

// Admin reports whether the process runs elevated.
func Admin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// Tunnel is a running transparent capture.
type Tunnel struct {
	dev     device.Device
	stack   *stack.Stack
	tun     *tunnel.Tunnel
	restore func()
	once    sync.Once
}

// Start creates the adapter and routes, and sends captured connections to d.
func Start(d proxy.Dialer) (*Tunnel, error) {
	if !Admin() {
		return nil, ErrNotAdmin
	}
	if err := installDLL(); err != nil {
		return nil, fmt.Errorf("installing wintun.dll: %w", err)
	}
	dev, err := tun.Open(adapterName, 1500)
	if err != nil {
		return nil, fmt.Errorf("creating the virtual adapter: %w", err)
	}
	t := &Tunnel{dev: dev}
	t.tun = tunnel.New(d, statistic.DefaultManager)
	t.restore = tunnel.ReplaceGlobal(t.tun)
	t.tun.ProcessAsync()
	t.stack, err = core.CreateStack(&core.Config{LinkEndpoint: dev, TransportHandler: t.tun})
	if err != nil {
		t.Stop()
		return nil, fmt.Errorf("network stack: %w", err)
	}
	if err := configure(); err != nil {
		t.Stop()
		return nil, err
	}
	return t, nil
}

// Stop removes the adapter (and so its routes). Safe to call more than once.
func (t *Tunnel) Stop() {
	t.once.Do(func() {
		if t.stack != nil {
			t.stack.Close()
		}
		if t.dev != nil {
			t.dev.Close()
		}
		if t.tun != nil {
			t.tun.Close()
		}
		if t.restore != nil {
			t.restore()
		}
	})
}

// installDLL writes wintun.dll next to the executable, where Wintun loads it from.
func installDLL() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, wintunDLL) {
		return nil
	}
	return os.WriteFile(path, wintunDLL, 0o644)
}

func configure() error {
	cmds := [][]string{
		{"interface", "ipv4", "set", "address", "name=" + adapterName, "source=static", "address=198.18.0.1", "mask=255.254.0.0"},
		{"interface", "ipv4", "add", "route", "prefix=0.0.0.0/1", "interface=" + adapterName, "nexthop=0.0.0.0", "metric=1", "store=active"},
		{"interface", "ipv4", "add", "route", "prefix=128.0.0.0/1", "interface=" + adapterName, "nexthop=0.0.0.0", "metric=1", "store=active"},
		{"interface", "ipv6", "add", "address", "interface=" + adapterName, "address=fd64:6e74::1/64", "store=active"},
		{"interface", "ipv6", "add", "route", "prefix=::/1", "interface=" + adapterName, "metric=1", "store=active"},
		{"interface", "ipv6", "add", "route", "prefix=8000::/1", "interface=" + adapterName, "metric=1", "store=active"},
	}
	for _, c := range cmds {
		if out, err := winexec.Command("netsh", c...).CombinedOutput(); err != nil {
			return fmt.Errorf("netsh %v : %v (%s)", c, err, bytes.TrimSpace(out))
		}
	}
	return nil
}
