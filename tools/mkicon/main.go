// mkicon draws dualnet's icon (two links merging into one arrow) and writes
// assets/icon.ico (16–256 px), assets/icon.png and rsrc_windows_amd64.syso
// (the .exe icon). Run from the repo root: go run ./tools/mkicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

var (
	bg     = color.NRGBA{0x14, 0x1a, 0x24, 0xff}
	blue   = color.NRGBA{0x4c, 0x8d, 0xff, 0xff}
	orange = color.NRGBA{0xff, 0x9a, 0x3c, 0xff}
	white  = color.NRGBA{0xf2, 0xf5, 0xfa, 0xff}
)

// draw renders the icon at size n with 4x4 supersampling.
func draw(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const ss = 4
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/ss) / float64(n)
					v := (float64(y) + (float64(sy)+0.5)/ss) / float64(n)
					c, ok := sample(u, v)
					if ok {
						r += float64(c.R)
						g += float64(c.G)
						b += float64(c.B)
						a += 255
					}
				}
			}
			k := float64(ss * ss)
			if a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{uint8(r / (a / 255)), uint8(g / (a / 255)), uint8(b / (a / 255)), uint8(a / k)})
			}
		}
	}
	return img
}

// sample returns the color at (u,v) in [0,1]², ok=false for transparent.
func sample(u, v float64) (color.NRGBA, bool) {
	// rounded square background
	if !inRoundRect(u, v, 0.04, 0.04, 0.96, 0.96, 0.22) {
		return color.NRGBA{}, false
	}
	// two strands (blue top, orange bottom) curving into one line, then an arrow head
	w := 0.085
	// merge point
	mx, my := 0.58, 0.5
	if u < mx {
		t := u / mx
		off := 0.22 * (1 - smooth(t))
		if math.Abs(v-(my-off)) < w/2 && u > 0.14 {
			return blue, true
		}
		if math.Abs(v-(my+off)) < w/2 && u > 0.14 {
			return orange, true
		}
	} else {
		// combined shaft
		if u < 0.72 && math.Abs(v-my) < w*0.75 {
			return white, true
		}
		// arrow head: triangle from x=0.68 to 0.88
		if u >= 0.66 && u <= 0.88 {
			half := 0.2 * (0.88 - u) / 0.22
			if math.Abs(v-my) < half {
				return white, true
			}
		}
	}
	return bg, true
}

func smooth(t float64) float64 { return t * t * (3 - 2*t) }

func inRoundRect(u, v, x0, y0, x1, y1, r float64) bool {
	if u < x0 || u > x1 || v < y0 || v > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(u, x1-r))
	cy := math.Max(y0+r, math.Min(v, y1-r))
	return (u-cx)*(u-cx)+(v-cy)*(v-cy) <= r*r
}

func main() {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var pngs [][]byte
	for _, s := range sizes {
		img := draw(s)
		if s >= 256 {
			var b bytes.Buffer
			png.Encode(&b, img)
			pngs = append(pngs, b.Bytes())
		} else {
			pngs = append(pngs, dib(img)) // small sizes: classic DIB, required by Windows
		}
	}
	// ICO with PNG-compressed entries (supported since Windows Vista)
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s == 256 {
			dim = 0
		}
		binary.Write(&ico, binary.LittleEndian, struct {
			W, H, Colors, Res uint8
			Planes, Bpp       uint16
			Size, Offset      uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(pngs[i])), uint32(offset)})
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		ico.Write(p)
	}
	must(os.WriteFile("assets/icon.ico", ico.Bytes(), 0o644))
	var big bytes.Buffer
	png.Encode(&big, draw(256))
	must(os.WriteFile("assets/icon.png", big.Bytes(), 0o644))
	must(os.WriteFile("rsrc_windows_amd64.syso", writeSyso(pngs, sizes), 0o644))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// dib encodes img as an icon DIB: BITMAPINFOHEADER (double height), bottom-up
// BGRA pixels, then an all-zero AND mask (transparency comes from alpha).
func dib(img *image.NRGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	var b bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&b, le, uint32(40))
	binary.Write(&b, le, int32(w))
	binary.Write(&b, le, int32(2*h))
	binary.Write(&b, le, uint16(1))
	binary.Write(&b, le, uint16(32))
	binary.Write(&b, le, [6]uint32{0, uint32(w * h * 4), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	maskRow := ((w + 31) / 32) * 4
	b.Write(make([]byte, maskRow*h))
	return b.Bytes()
}
