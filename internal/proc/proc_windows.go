package proc

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	getExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPIDAll = 5
	udpTableOwnerPID    = 1
)

// Lookup returns the executable path of the process that owns the flow's
// local socket, or "" if it cannot be identified.
func Lookup(f Flow) string {
	pid, ok := pidFor(f)
	if !ok || pid == 0 || pid == 4 { // idle, System
		return ""
	}
	return exePath(pid)
}

func pidFor(f Flow) (uint32, bool) {
	v6 := f.Local.Addr().Is6()
	af := uint32(windows.AF_INET)
	if v6 {
		af = windows.AF_INET6
	}
	proc, class := getExtendedTcpTable, uint32(tcpTableOwnerPIDAll)
	if f.UDP {
		proc, class = getExtendedUdpTable, udpTableOwnerPID
	}
	t, err := table(proc, af, class)
	if err != nil {
		return 0, false
	}
	var pid uint32
	var ok bool
	switch {
	case !f.UDP && !v6:
		pid, ok, err = findTCP4(t, f)
	case !f.UDP && v6:
		pid, ok, err = findTCP6(t, f)
	case f.UDP && !v6:
		pid, ok, err = findUDP4(t, f)
	default:
		pid, ok, err = findUDP6(t, f)
	}
	return pid, ok && err == nil
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 64<<10); return &b }}

// table fetches a socket table, growing the buffer while Windows reports
// ERROR_INSUFFICIENT_BUFFER (the table can grow between calls).
func table(p *windows.LazyProc, af, class uint32) ([]byte, error) {
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	for range 4 {
		size := uint32(len(*bp))
		r, _, _ := p.Call(uintptr(unsafe.Pointer(&(*bp)[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(af), uintptr(class), 0)
		switch windows.Errno(r) {
		case 0:
			out := make([]byte, size)
			copy(out, (*bp)[:size])
			return out, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			*bp = make([]byte, size+size/4)
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}

type cached struct {
	path string
	at   time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[uint32]cached{}
)

// PIDs are reused, so cached paths expire quickly.
const cacheTTL = 3 * time.Second

func exePath(pid uint32) string {
	now := time.Now()
	cacheMu.Lock()
	c, ok := cache[pid]
	if len(cache) > 4096 {
		clear(cache)
	}
	cacheMu.Unlock()
	if ok && now.Sub(c.at) < cacheTTL {
		return c.path
	}
	path := queryImage(pid)
	cacheMu.Lock()
	cache[pid] = cached{path, now}
	cacheMu.Unlock()
	return path
}

func queryImage(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
