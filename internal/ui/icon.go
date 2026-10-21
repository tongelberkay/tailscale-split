package ui

import (
	"bytes"
	"encoding/binary"
)

// Icon builds a 32x32 .ico with a filled circle of the given colour and a
// darker ring, so the tray needs no image files.
func Icon(r, g, b uint8) []byte {
	const size = 32
	// 32-bit BGRA pixels, bottom-up, followed by a 1-bit AND mask.
	pixels := make([]byte, 0, size*size*4)
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			d := dx*dx + dy*dy
			switch {
			case d <= 11.5*11.5:
				pixels = append(pixels, b, g, r, 255)
			case d <= 14.5*14.5:
				pixels = append(pixels, b/2, g/2, r/2, 255)
			default:
				pixels = append(pixels, 0, 0, 0, 0)
			}
		}
	}
	mask := make([]byte, size*size/8) // all zero: alpha channel decides

	var bmp bytes.Buffer
	le := func(v any) { binary.Write(&bmp, binary.LittleEndian, v) }
	le(uint32(40))      // BITMAPINFOHEADER size
	le(int32(size))     // width
	le(int32(size * 2)) // height covers XOR + AND masks
	le(uint16(1))       // planes
	le(uint16(32))      // bits per pixel
	le(uint32(0))       // BI_RGB
	le(uint32(len(pixels) + len(mask)))
	le([4]int32{}) // resolution and palette fields
	bmp.Write(pixels)
	bmp.Write(mask)

	var ico bytes.Buffer
	w := func(v any) { binary.Write(&ico, binary.LittleEndian, v) }
	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(1)) // count
	w([4]uint8{size, size, 0, 0})
	w(uint16(1))  // planes
	w(uint16(32)) // bpp
	w(uint32(bmp.Len()))
	w(uint32(6 + 16)) // offset of image data
	ico.Write(bmp.Bytes())
	return ico.Bytes()
}
