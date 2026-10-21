package proc

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// row builders mirror the iphlpapi layouts byte for byte.
func be16dword(p uint16) []byte { b := make([]byte, 4); binary.BigEndian.PutUint16(b, p); return b }
func le32(v uint32) []byte      { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

func mkTable(rows ...[]byte) []byte {
	t := le32(uint32(len(rows)))
	for _, r := range rows {
		t = append(t, r...)
	}
	return t
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func a4(s string) []byte  { a := netip.MustParseAddr(s).As4(); return a[:] }
func a16(s string) []byte { a := netip.MustParseAddr(s).As16(); return a[:] }

func TestFindTCP4(t *testing.T) {
	f := Flow{Local: netip.MustParseAddrPort("172.31.255.1:50000"), Remote: netip.MustParseAddrPort("1.1.1.1:443")}
	other := cat(le32(5), a4("172.31.255.1"), be16dword(50000), a4("8.8.8.8"), be16dword(443), le32(11))
	match := cat(le32(2), a4("172.31.255.1"), be16dword(50000), a4("1.1.1.1"), be16dword(443), le32(4242))
	pid, ok, err := findTCP4(mkTable(other, match), f)
	if err != nil || !ok || pid != 4242 {
		t.Fatalf("pid=%d ok=%v err=%v", pid, ok, err)
	}
	if _, _, err := findTCP4(mkTable(other)[:20], f); err == nil {
		t.Fatal("truncated table accepted")
	}
}

func TestFindTCP6(t *testing.T) {
	f := Flow{Local: netip.MustParseAddrPort("[fd55:6170:7073::1]:40000"), Remote: netip.MustParseAddrPort("[2606:4700::1111]:443")}
	row := cat(a16("fd55:6170:7073::1"), le32(0), be16dword(40000), a16("2606:4700::1111"), le32(0), be16dword(443), le32(5), le32(77))
	if len(row) != 56 {
		t.Fatalf("row size %d", len(row))
	}
	pid, ok, err := findTCP6(mkTable(row), f)
	if err != nil || !ok || pid != 77 {
		t.Fatalf("pid=%d ok=%v err=%v", pid, ok, err)
	}
}

func TestFindUDP(t *testing.T) {
	f := Flow{UDP: true, Local: netip.MustParseAddrPort("172.31.255.1:5353"), Remote: netip.MustParseAddrPort("1.1.1.1:53")}
	wild := cat(a4("0.0.0.0"), be16dword(5353), le32(10))
	exact := cat(a4("172.31.255.1"), be16dword(5353), le32(20))
	if pid, ok, _ := findUDP4(mkTable(wild), f); !ok || pid != 10 {
		t.Fatalf("wildcard: pid=%d ok=%v", pid, ok)
	}
	if pid, ok, _ := findUDP4(mkTable(wild, exact), f); !ok || pid != 20 {
		t.Fatalf("exact should win: pid=%d ok=%v", pid, ok)
	}
	f6 := Flow{UDP: true, Local: netip.MustParseAddrPort("[fd55:6170:7073::1]:443")}
	row6 := cat(a16("::"), le32(0), be16dword(443), le32(30))
	if len(row6) != 28 {
		t.Fatalf("row size %d", len(row6))
	}
	if pid, ok, _ := findUDP6(mkTable(row6), f6); !ok || pid != 30 {
		t.Fatalf("v6: pid=%d ok=%v", pid, ok)
	}
}
