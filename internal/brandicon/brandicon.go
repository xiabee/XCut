package brandicon

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// brandIcon draws the xcut mark programmatically — a dark rounded tile
// with the same lightning shape the web UI logo uses — so the desktop
// client ships a real icon without committing any binary asset (and
// without a bundling question). Sizes 16/32/48 cover title bar, taskbar
// and Alt-Tab.
// BrandICO renders the mark at every brand size (16/32/48) and wraps the
// set into a single ICO container.
func BrandICO() ([]byte, error) {
	return ICO([]image.Image{Brand(16), Brand(32), Brand(48)})
}

// Brand draws the mark at one size.
func Brand(size int) draw.Image {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	// Background tile with a subtle vertical two-tone (green→deep teal).
	for y := 0; y < size; y++ {
		t := float64(y) / float64(size-1)
		bg := color.RGBA{
			uint8(lerp(0x16, 0x0d, t)),
			uint8(lerp(0x8f, 0x5c, t)),
			uint8(lerp(0x8a, 0x71, t)),
			0xff,
		}
		for x := 0; x < size; x++ {
			if roundedRectContains(size, x, y) {
				dst.Set(x, y, bg)
			}
		}
	}
	// Lightning bolt: the web logo's path M3 4h9l-2.2 4H16v2H8.5L6 14H3
	// l3.2-5.2L3 4z (viewBox 0 0 20 20), scaled into the tile.
	bolt := [][2]float64{
		{3, 4}, {12, 4}, {9.8, 8}, {16, 8}, {16, 10}, {8.5, 10},
		{6, 14}, {3, 14}, {6.2, 8.8}, {3, 4},
	}
	boltColor := color.RGBA{0xe8, 0xff, 0xf2, 0xff}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px := (float64(x) + 0.5) * 20 / float64(size)
			py := (float64(y) + 0.5) * 20 / float64(size)
			if pointInPolygon(px, py, bolt) {
				dst.Set(x, y, boltColor)
			}
		}
	}
	return dst
}

func lerp(a, b int, t float64) int { return a + int(float64(b-a)*t) }

// roundedRectContains tests the tile's rounded-rect silhouette: corner
// radius scales with the icon (20% of size), everything inside passes.
func roundedRectContains(size, x, y int) bool {
	r := size / 5
	if r < 2 {
		return true
	}
	for _, ix := range [2]int{x, size - 1 - x} {
		for _, iy := range [2]int{y, size - 1 - y} {
			if ix < r && iy < r {
				// Corner circle is centered at (r, r) of the quadrant.
				dx := float64(r - ix)
				dy := float64(r - iy)
				if dx*dx+dy*dy > float64(r)*float64(r) {
					return false
				}
			}
		}
	}
	return true
}

// pointInPolygon is the even-odd ray cast.
func pointInPolygon(px, py float64, poly [][2]float64) bool {
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]
		if (yi > py) != (yj > py) {
			x := (xj-xi)*(py-yi)/(yj-yi) + xi
			if px < x {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// icoBytes wraps PNG images into a single .ico container (PNG-compressed
// entries, Vista+). Only the ICONDIR + ICONDIRENTRY framing is written
// here; the entries are the PNG payloads verbatim.
func ICO(imgs []image.Image) ([]byte, error) {
	var pngs [][]byte
	for _, im := range imgs {
		b := image.NewRGBA(im.Bounds())
		draw.Draw(b, b.Bounds(), im, image.Point{}, draw.Src)
		var buf bytes.Buffer
		if err := png.Encode(&buf, b); err != nil {
			return nil, err
		}
		pngs = append(pngs, buf.Bytes())
	}

	var out bytes.Buffer
	out.Write([]byte{0, 0}) // reserved
	out.Write([]byte{1, 0}) // type: icon
	writeLE16(&out, uint16(len(pngs)))

	offset := 6 + 16*len(pngs)
	for i, im := range imgs {
		// Width/height are one byte each; 256 encodes as 0 — every brand
		// size is far below that.
		out.WriteByte(byte(im.Bounds().Dx() & 0xff))
		out.WriteByte(byte(im.Bounds().Dy() & 0xff))
		out.Write([]byte{0, 0})  // palette count, reserved
		out.Write([]byte{1, 0})  // color planes
		out.Write([]byte{32, 0}) // bits per pixel
		writeLE32(&out, uint32(len(pngs[i])))
		writeLE32(&out, uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes(), nil
}

func writeLE16(b *bytes.Buffer, v uint16) {
	b.WriteByte(byte(v))
	b.WriteByte(byte(v >> 8))
}

func writeLE32(b *bytes.Buffer, v uint32) {
	b.WriteByte(byte(v))
	b.WriteByte(byte(v >> 8))
	b.WriteByte(byte(v >> 16))
	b.WriteByte(byte(v >> 24))
}
