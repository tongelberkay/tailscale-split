package proc

import (
	"strings"
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
// local socket, followed by the paths of its parent, grandparent and so on
// (up to maxDepth). It returns nil if the owner cannot be identified.
//
// Ancestors matter because apps hand their networking to child processes:
// new Teams (ms-teams.exe) does its web traffic in msedgewebview2.exe.
func Lookup(f Flow) []string {
	pid, ok := pidFor(f)
	if !ok || pid == 0 || pid == 4 { // idle, System
		return nil
	}
	return chain(pid)
}

const maxDepth = 4

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

type procInfo struct {
	path    string
	parent  uint32
	created int64 // FILETIME; 0 if unknown
}

type cached struct {
	info procInfo
	at   time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[uint32]cached{}
)

// PIDs are reused, so cached entries expire quickly.
const cacheTTL = 3 * time.Second

func chain(pid uint32) []string {
	child := info(pid)
	if child.path == "" {
		return nil
	}
	out := []string{child.path}
	for range maxDepth {
		if child.parent == 0 || child.parent == 4 {
			break
		}
		parent := info(child.parent)
		// A parent that started after the child is an unrelated process
		// that inherited a reused PID.
		if parent.path == "" || (child.created != 0 && parent.created > child.created) {
			break
		}
		out = append(out, parent.path)
		child = parent
	}
	return out
}

func info(pid uint32) procInfo {
	now := time.Now()
	cacheMu.Lock()
	c, ok := cache[pid]
	if len(cache) > 4096 {
		clear(cache)
	}
	cacheMu.Unlock()
	if ok && now.Sub(c.at) < cacheTTL {
		return c.info
	}
	pi := query(pid)
	cacheMu.Lock()
	cache[pid] = cached{pi, now}
	cacheMu.Unlock()
	return pi
}

func query(pid uint32) procInfo {
	var pi procInfo
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return pi
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return pi
	}
	pi.path = windows.UTF16ToString(buf[:n])
	var bi windows.PROCESS_BASIC_INFORMATION
	if windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&bi), uint32(unsafe.Sizeof(bi)), nil) == nil {
		pi.parent = uint32(bi.InheritedFromUniqueProcessId)
	}
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		pi.created = created.Nanoseconds()
	}
	return pi
}

// Running lists the distinct executable paths of running processes this
// user can query (some system processes are hidden from non-admin users).
func Running() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	seen := map[string]bool{}
	var out []string
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == 0 || e.ProcessID == 4 {
			continue
		}
		p := info(e.ProcessID).path
		if k := strings.ToLower(p); p != "" && !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}
