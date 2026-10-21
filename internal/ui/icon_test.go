package ui

import (
	"encoding/binary"
	"testing"
)

func TestIconHeader(t *testing.T) {
	ico := Icon(1, 2, 3)
	if binary.LittleEndian.Uint16(ico[2:]) != 1 || binary.LittleEndian.Uint16(ico[4:]) == 0 {
		t.Fatal("bad ICONDIR")
	}
	if n := binary.LittleEndian.Uint16(ico[4:]); n != 5 {
		t.Fatalf("%d sizes, want 5", n)
	}
	size := binary.LittleEndian.Uint32(ico[14:])
	off := binary.LittleEndian.Uint32(ico[18:])
	if int(off+size) > len(ico) {
		t.Fatalf("entry says %d+%d, file is %d", off, size, len(ico))
	}
	if binary.LittleEndian.Uint32(ico[off:]) != 40 {
		t.Fatal("bad BITMAPINFOHEADER")
	}
	if want := 40 + 16*16*4 + 16*4; int(size) != want {
		t.Fatalf("image size %d, want %d", size, want)
	}
}
