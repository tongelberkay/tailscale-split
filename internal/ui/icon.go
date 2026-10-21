package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// AppColor is the background of the program icon; the tray uses state colours.
var AppColor = color.NRGBA{0x4c, 0x5f, 0xd5, 0xff}

// Icon builds a tray .ico (16 to 48 px) of the TailSplit glyph on the given
// colour, so the tray needs no image files.
func Icon(r, g, b uint8) []byte {
	return ICO(color.NRGBA{r, g, b, 0xff}, 16, 20, 24, 32, 48)
}

// ICO encodes the glyph at each size. Sizes up to 64 px are stored as
// bitmaps, larger ones as PNG, which is what Windows expects.
func ICO(bg color.NRGBA, sizes ...int) []byte {
	images := make([][]byte, len(sizes))
	for i, s := range sizes {
		img := Render(s, bg)
		if s > 64 {
			var buf bytes.Buffer
			png.Encode(&buf, img)
			images[i] = buf.Bytes()
		} else {
			images[i] = bmp(img)
		}
	}

	var ico bytes.Buffer
	w := func(v any) { binary.Write(&ico, binary.LittleEndian, v) }
	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(len(sizes)))
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s) // 256 is stored as 0
		w([4]uint8{dim, dim, 0, 0})
		w(uint16(1))  // planes
		w(uint16(32)) // bpp
		w(uint32(len(images[i])))
		w(uint32(off))
		off += len(images[i])
	}
	for _, b := range images {
		ico.Write(b)
	}
	return ico.Bytes()
}

// bmp is the headerless DIB an .ico entry holds: 32-bit BGRA rows, bottom-up,
// then a 1-bit AND mask that the alpha channel makes redundant.
func bmp(img *image.NRGBA) []byte {
	size := img.Rect.Dx()
	pixels := make([]byte, 0, size*size*4)
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			pixels = append(pixels, c.B, c.G, c.R, c.A)
		}
	}
	mask := make([]byte, ((size+31)/32*4)*size)

	var out bytes.Buffer
	le := func(v any) { binary.Write(&out, binary.LittleEndian, v) }
	le(uint32(40))      // BITMAPINFOHEADER size
	le(int32(size))     // width
	le(int32(size * 2)) // height covers XOR + AND masks
	le(uint16(1))       // planes
	le(uint16(32))      // bits per pixel
	le(uint32(0))       // BI_RGB
	le(uint32(len(pixels) + len(mask)))
	le([4]int32{}) // resolution and palette fields
	out.Write(pixels)
	out.Write(mask)
	return out.Bytes()
}

// Render draws the glyph: one path that forks, with the upper branch ending
// in a node (the tailnet) and the lower one running straight on (everything
// else), on a rounded square. Coordinates are in units of the icon size.
func Render(size int, bg color.NRGBA) *image.NRGBA {
	const ss = 4 // supersampling per axis
	stroke := 0.105
	if size <= 20 {
		stroke = 0.13 // keep lines solid at tray size
	}
	type pt struct{ x, y float64 }
	trunk := []pt{{0.17, 0.64}, {0.42, 0.64}}
	straight := []pt{{0.42, 0.64}, {0.83, 0.64}}
	var upper []pt
	for i := 0; i <= 24; i++ { // quadratic curve from the fork to the node
		t := float64(i) / 24
		p0, p1, p2 := pt{0.42, 0.64}, pt{0.62, 0.64}, pt{0.71, 0.34}
		u := 1 - t
		upper = append(upper, pt{u*u*p0.x + 2*u*t*p1.x + t*t*p2.x, u*u*p0.y + 2*u*t*p1.y + t*t*p2.y})
	}
	node, nodeR := pt{0.72, 0.30}, 0.115

	segDist := func(p pt, line []pt) float64 {
		best := math.Inf(1)
		for i := 1; i < len(line); i++ {
			a, b := line[i-1], line[i]
			dx, dy := b.x-a.x, b.y-a.y
			t := ((p.x-a.x)*dx + (p.y-a.y)*dy) / (dx*dx + dy*dy)
			t = math.Max(0, math.Min(1, t))
			ex, ey := p.x-(a.x+t*dx), p.y-(a.y+t*dy)
			best = math.Min(best, math.Hypot(ex, ey))
		}
		return best
	}
	inRounded := func(p pt) bool {
		const m, r = 0.03, 0.22 // margin and corner radius
		cx := math.Max(m+r, math.Min(1-m-r, p.x))
		cy := math.Max(m+r, math.Min(1-m-r, p.y))
		return math.Hypot(p.x-cx, p.y-cy) <= r
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var bgN, fgN int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := pt{(float64(x) + (float64(sx)+0.5)/ss) / float64(size), (float64(y) + (float64(sy)+0.5)/ss) / float64(size)}
					if !inRounded(p) {
						continue
					}
					bgN++
					h := stroke / 2
					if segDist(p, trunk) <= h || segDist(p, straight) <= h || segDist(p, upper) <= h ||
						math.Hypot(p.x-node.x, p.y-node.y) <= nodeR {
						fgN++
					}
				}
			}
			if bgN == 0 {
				continue
			}
			// Light top to darker bottom, white glyph blended over it.
			shade := 1.08 - 0.16*(float64(y)+0.5)/float64(size)
			f := float64(fgN) / float64(bgN)
			mix := func(c uint8) uint8 {
				v := math.Min(255, float64(c)*shade)
				return uint8(v*(1-f) + 255*f + 0.5)
			}
			img.SetNRGBA(x, y, color.NRGBA{mix(bg.R), mix(bg.G), mix(bg.B), uint8(255*bgN/(ss*ss) + 0)})
		}
	}
	return img
}
