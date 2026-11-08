//go:build !windows

package transparent

import (
	"errors"

	"github.com/xjasonlyu/tun2socks/v2/proxy"
)

var ErrNotAdmin = errors.New("transparent mode: Windows only")

type Tunnel struct{}

func Admin() bool                         { return false }
func Start(proxy.Dialer) (*Tunnel, error) { return nil, ErrNotAdmin }
func (t *Tunnel) Stop()                   {}
