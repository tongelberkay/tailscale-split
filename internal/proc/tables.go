// Package proc finds which process owns a TCP or UDP socket, so that a
// connection to the proxy can be matched to an executable path.
package proc

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// Flow is the 5-tuple seen from the application's side: Local is the app's
// own socket address, Remote is where it is connecting to.
type Flow struct {
	UDP    bool
	Local  netip.AddrPort
	Remote netip.AddrPort
}

var errShort = errors.New("proc: truncated socket table")

// The parsers below decode the iphlpapi *_OWNER_PID tables. They are kept free
// of syscalls so they can be tested on any OS. Addresses are stored in network
// byte order; ports are in network byte order in the low 16 bits of a DWORD.

func port(b []byte) uint16  { return binary.BigEndian.Uint16(b[0:2]) }
func dword(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// findTCP4 parses MIB_TCPTABLE_OWNER_PID (rows of 6 DWORDs).
func findTCP4(t []byte, f Flow) (uint32, bool, error) {
	const row = 24
	n, rows, err := header(t, row)
	if err != nil {
		return 0, false, err
	}
	for i := 0; i < n; i++ {
		r := rows[i*row:]
		local := netip.AddrPortFrom(netip.AddrFrom4([4]byte(r[4:8])), port(r[8:]))
		remote := netip.AddrPortFrom(netip.AddrFrom4([4]byte(r[12:16])), port(r[16:]))
		if local == f.Local && remote == f.Remote {
			return dword(r[20:]), true, nil
		}
	}
	return 0, false, nil
}

// findTCP6 parses MIB_TCP6TABLE_OWNER_PID (56-byte rows).
func findTCP6(t []byte, f Flow) (uint32, bool, error) {
	const row = 56
	n, rows, err := header(t, row)
	if err != nil {
		return 0, false, err
	}
	for i := 0; i < n; i++ {
		r := rows[i*row:]
		local := netip.AddrPortFrom(netip.AddrFrom16([16]byte(r[0:16])), port(r[20:]))
		remote := netip.AddrPortFrom(netip.AddrFrom16([16]byte(r[24:40])), port(r[44:]))
		if local == f.Local && remote == f.Remote {
			return dword(r[52:]), true, nil
		}
	}
	return 0, false, nil
}

// findUDP4 parses MIB_UDPTABLE_OWNER_PID (rows of 3 DWORDs). UDP sockets
// have no remote end in the table, so match the local port and accept a
// wildcard local address.
func findUDP4(t []byte, f Flow) (uint32, bool, error) {
	const row = 12
	n, rows, err := header(t, row)
	if err != nil {
		return 0, false, err
	}
	var wildcard uint32
	found := false
	for i := 0; i < n; i++ {
		r := rows[i*row:]
		if port(r[4:]) != f.Local.Port() {
			continue
		}
		addr := netip.AddrFrom4([4]byte(r[0:4]))
		if addr == f.Local.Addr() {
			return dword(r[8:]), true, nil
		}
		if addr.IsUnspecified() && !found {
			wildcard, found = dword(r[8:]), true
		}
	}
	return wildcard, found, nil
}

// findUDP6 parses MIB_UDP6TABLE_OWNER_PID (28-byte rows).
func findUDP6(t []byte, f Flow) (uint32, bool, error) {
	const row = 28
	n, rows, err := header(t, row)
	if err != nil {
		return 0, false, err
	}
	var wildcard uint32
	found := false
	for i := 0; i < n; i++ {
		r := rows[i*row:]
		if port(r[20:]) != f.Local.Port() {
			continue
		}
		addr := netip.AddrFrom16([16]byte(r[0:16]))
		if addr == f.Local.Addr() {
			return dword(r[24:]), true, nil
		}
		if addr.IsUnspecified() && !found {
			wildcard, found = dword(r[24:]), true
		}
	}
	return wildcard, found, nil
}

func header(t []byte, row int) (int, []byte, error) {
	if len(t) < 4 {
		return 0, nil, errShort
	}
	n := int(dword(t))
	if n < 0 || len(t)-4 < n*row {
		return 0, nil, errShort
	}
	return n, t[4:], nil
}
