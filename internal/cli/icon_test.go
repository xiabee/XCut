package cli

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"
)

// TestBrandIconShape: the bolt lands inside the tile on every brand size,
// and the tile's rounded corners stay transparent.
func TestBrandIconShape(t *testing.T) {
	for _, size := range []int{16, 32, 48} {
		img := brandIcon(size)
		b := img.Bounds()
		cornerRadius := size / 5
		if cornerRadius >= 2 {
			if c := rgbaAt(img, 0, 0); c.A != 0 {
				t.Fatalf("size %d: corner pixel must be transparent", size)
			}
		}
		// The bolt's widest span (x 3..16 of 20) must paint near-white.
		found := false
		for y := 0; y < size && !found; y++ {
			for x := 0; x < size && !found; x++ {
				if c := rgbaAt(img, x, y); c.R > 0xd0 && c.G > 0xf0 {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("size %d: no bolt pixel found", size)
		}
		_ = b
	}
}

// TestIcoBytesStructure: ICONDIR, one ICONDIRENTRY per image with that
// image's own size and offset, PNG payloads back to back.
func TestIcoBytesStructure(t *testing.T) {
	ico, err := icoBytes([]image.Image{brandIcon(16), brandIcon(32), brandIcon(48)})
	if err != nil {
		t.Fatal(err)
	}
	if string(ico[0:4]) != "\x00\x00\x01\x00" {
		t.Fatalf("bad ICONDIR magic: % x", ico[0:4])
	}
	count := binary.LittleEndian.Uint16(ico[4:6])
	if count != 3 {
		t.Fatalf("entry count = %d, want 3", count)
	}

	offset := 6 + 16*int(count)
	for i, wantSize := range []int{16, 32, 48} {
		entry := ico[6+16*i : 6+16*(i+1)]
		if w := int(entry[0]); w != wantSize {
			t.Fatalf("entry %d width byte = %d, want %d", i, w, wantSize)
		}
		if h := int(entry[1]); h != wantSize {
			t.Fatalf("entry %d height byte = %d, want %d", i, h, wantSize)
		}
		size := binary.LittleEndian.Uint32(entry[8:12])
		start := binary.LittleEndian.Uint32(entry[12:16])
		if int(start) != offset {
			t.Fatalf("entry %d offset = %d, want %d", i, start, offset)
		}
		payload := ico[start : start+size]
		if !bytes.HasPrefix(payload, []byte("\x89PNG")) {
			t.Fatalf("entry %d payload is not a PNG: % x", i, payload[:4])
		}
		offset += int(size)
	}
	if offset != len(ico) {
		t.Fatalf("ico length %d, entries cover %d", len(ico), offset)
	}
}

func rgbaAt(img image.Image, x, y int) (c struct{ R, G, B, A uint8 }) {
	r, g, b, a := img.At(x, y).RGBA()
	return struct{ R, G, B, A uint8 }{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
