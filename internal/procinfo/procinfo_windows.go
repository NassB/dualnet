// Package procinfo finds which program owns a network connection, from its
// local address and port. In transparent mode the connections dualnet captures
// were opened by an application on this PC, so their source port identifies it
// in Windows' TCP/UDP tables.
package procinfo

import (
	"encoding/binary"
	"net/netip"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi = syscall.NewLazyDLL("iphlpapi.dll")
	procTCP  = iphlpapi.NewProc("GetExtendedTcpTable")
	procUDP  = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	afInet                = 2
	tcpTableOwnerPidAll   = 5
	udpTableOwnerPid      = 1
	errInsufficientBuffer = 122
)

type row struct {
	addr netip.Addr
	port uint16
	pid  uint32
}

// readTable reads an IPv4 owner-PID table. Layout: a uint32 count, then rows of
// rowSize bytes; addr/port/pid are byte offsets inside a row. Ports are stored
// in network byte order.
func readTable(proc *syscall.LazyProc, class uintptr, rowSize, addrOff, portOff, pidOff int) []row {
	var size uint32
	proc.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, class, 0) // asks for the size
	for try := 0; try < 4; try++ {
		buf := make([]byte, size+4096) // the table can grow between the two calls
		size = uint32(len(buf))
		r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, class, 0)
		if r == errInsufficientBuffer {
			continue
		}
		if r != 0 {
			return nil
		}
		n := int(binary.LittleEndian.Uint32(buf))
		if 4+n*rowSize > len(buf) {
			return nil
		}
		rows := make([]row, 0, n)
		for i := 0; i < n; i++ {
			b := buf[4+i*rowSize : 4+(i+1)*rowSize]
			p := binary.LittleEndian.Uint32(b[portOff:])
			rows = append(rows, row{
				addr: netip.AddrFrom4([4]byte(b[addrOff : addrOff+4])),
				port: uint16(p&0xff)<<8 | uint16(p>>8&0xff),
				pid:  binary.LittleEndian.Uint32(b[pidOff:]),
			})
		}
		return rows
	}
	return nil
}

type tables struct {
	tcp, udp []row
	at       time.Time
}

var (
	tabMu sync.Mutex
	tabs  tables
)

func find(rows []row, ip netip.Addr, port uint16, udp bool) (uint32, bool) {
	for _, r := range rows {
		if r.port != port {
			continue
		}
		// TCP: the local address must match (another local address may use the
		// same port number). UDP sockets are often bound to 0.0.0.0.
		if r.addr == ip || (udp && r.addr.IsUnspecified()) || !ip.IsValid() {
			return r.pid, true
		}
	}
	return 0, false
}

// owner returns the PID of the process owning the local endpoint.
//
// The connection already existed when the caller asked, so a snapshot of the
// tables whose reading *started after the call began* is guaranteed to contain
// it. A snapshot started earlier may miss a just-opened connection: it is
// refreshed. Concurrent callers (a burst of new connections) share one refresh.
func owner(proto string, ip netip.Addr, port uint16) (uint32, bool) {
	udp := proto == "udp"
	ip = ip.Unmap()
	called := time.Now()
	tabMu.Lock()
	defer tabMu.Unlock()
	if !tabs.at.After(called) {
		started := time.Now()
		tabs = tables{
			tcp: readTable(procTCP, tcpTableOwnerPidAll, 24, 4, 8, 20),
			udp: readTable(procUDP, udpTableOwnerPid, 12, 0, 4, 8),
			at:  started,
		}
	}
	rows := tabs.tcp
	if udp {
		rows = tabs.udp
	}
	return find(rows, ip, port, udp)
}

type nameEntry struct {
	name string
	at   time.Time
}

var (
	nameMu    sync.Mutex
	nameCache = map[uint32]nameEntry{}
)

// exeName returns the file name of the program running as pid ("" if unknown).
// Cached for 20 s (PIDs are reused).
func exeName(pid uint32) string {
	nameMu.Lock()
	defer nameMu.Unlock()
	if e, ok := nameCache[pid]; ok && time.Since(e.at) < 20*time.Second {
		return e.name
	}
	name := ""
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		buf := make([]uint16, 1024)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
			name = filepath.Base(windows.UTF16ToString(buf[:n]))
		}
		windows.CloseHandle(h)
	}
	if len(nameCache) > 2000 { // bounded
		nameCache = map[uint32]nameEntry{}
	}
	nameCache[pid] = nameEntry{name, time.Now()}
	return name
}

// AppFor returns the executable name (e.g. "steam.exe") of the program that owns
// the local endpoint ip:port of a "tcp" or "udp" flow, or "" when unknown.
func AppFor(proto string, ip netip.Addr, port uint16) string {
	pid, ok := owner(proto, ip, port)
	if !ok || pid == 0 {
		return ""
	}
	return exeName(pid)
}
