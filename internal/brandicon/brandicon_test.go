package brandicon

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// TestBrandShape: the bolt lands inside the tile on every brand size, and
// the tile's rounded corners stay transparent.
func TestBrandShape(t *testing.T) {
	for _, size := range []int{16, 32, 48} {
		img := Brand(size)
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
	}
}

// TestICOStructure: ICONDIR, one ICONDIRENTRY per image with that image's
// own size and offset, PNG payloads back to back.
func TestICOStructure(t *testing.T) {
	ico, err := ICO([]image.Image{Brand(16), Brand(32), Brand(48)})
	if err != nil {
		t.Fatal(err)
	}
	if string(ico[0:4]) != "\x00\x00\x01\x00" {
		t.Fatalf("bad ICONDIR magic: % x", ico[0:4])
	}
	count := binaryLE16(ico[4:6])
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
		size := uint32(entry[8]) | uint32(entry[9])<<8 | uint32(entry[10])<<16 | uint32(entry[11])<<24
		start := uint32(entry[12]) | uint32(entry[13])<<8 | uint32(entry[14])<<16 | uint32(entry[15])<<24
		if int(start) != offset {
			t.Fatalf("entry %d offset = %d, want %d", i, start, offset)
		}
		payload := ico[start : start+size]
		if string(payload[0:4]) != "\x89PNG" {
			t.Fatalf("entry %d payload is not a PNG: % x", i, payload[:4])
		}
		offset += int(size)
	}
	if offset != len(ico) {
		t.Fatalf("ico length %d, entries cover %d", len(ico), offset)
	}
}

func binaryLE16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }

// TestICORefusesWhatOneByteCannotHold: an entry's width and height are one byte
// each, where 0 means 256. Before the guard, a 512 px image returned no error
// and wrote dimension bytes 0,0 — a container that lies about its payload.
func TestICORefusesWhatOneByteCannotHold(t *testing.T) {
	for _, im := range []image.Image{flat(257, 257), flat(512, 512), flat(300, 10), flat(0, 0)} {
		b := im.Bounds().Dx()
		_, err := ICO([]image.Image{im})
		if err == nil {
			t.Fatalf("ICO accepted a %d px wide image: the one-byte field encodes it as %d", b, b&0xff)
		}
		// Name the guard, not any error: with the bound relaxed the 0 px case
		// still fails later, inside png.Encode, and a test that accepted that
		// would pass with the guard deleted.
		if msg := err.Error(); !strings.Contains(msg, "does not fit an ICO entry") {
			t.Fatalf("a %d px image failed for the wrong reason: %s", b, msg)
		}
	}
	if _, err := ICO(nil); err == nil {
		t.Fatal("ICO accepted no images: that is a container with zero entries")
	}
	// The other side of the bound, so the guard is a bound and not a ban.
	ico, err := ICO([]image.Image{flat(256, 256)})
	if err != nil {
		t.Fatalf("256 px is expressible (0 means 256): %v", err)
	}
	if ico[6] != 0 || ico[7] != 0 {
		t.Fatalf("256 px must encode as 0, got %d,%d", ico[6], ico[7])
	}
}

// TestBrandOnePixelIsDefined pins behaviour the compiler does not define: at
// size 1 the gradient denominator is zero, and converting that NaN to an integer
// is implementation-defined (it happened to yield the t=0 colour on amd64 only
// because int(NaN) is -2^63 there, which is divisible by 256).
func TestBrandOnePixelIsDefined(t *testing.T) {
	got := rgbaAt(Brand(1), 0, 0)
	want := struct{ R, G, B, A uint8 }{0x16, 0x8f, 0x8a, 0xff}
	if got != want {
		t.Fatalf("Brand(1) = %02x %02x %02x %02x, want the top of the gradient %02x %02x %02x %02x",
			got.R, got.G, got.B, got.A, want.R, want.G, want.B, want.A)
	}
}

func flat(w, h int) image.Image {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.NRGBA{0x20, 0x40, 0x60, 0xff})
		}
	}
	return im
}

func rgbaAt(img image.Image, x, y int) (c struct{ R, G, B, A uint8 }) {
	r, g, b, a := img.At(x, y).RGBA()
	return struct{ R, G, B, A uint8 }{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// TestBrandICOShipsTheThreeSizes: the icon baked into the Windows binary and the
// one scripts/genicon writes are both this function's output, and nothing else
// builds them — a size list that drifts past what the container can express, or
// an entry whose offset no longer matches its payload, is invisible until the
// desktop shell shows a blank icon.
func TestBrandICOShipsTheThreeSizes(t *testing.T) {
	ico, err := BrandICO()
	if err != nil {
		t.Fatal(err)
	}
	if string(ico[0:4]) != "\x00\x00\x01\x00" {
		t.Fatalf("bad ICONDIR magic: % x", ico[0:4])
	}
	if n := binaryLE16(ico[4:6]); n != 3 {
		t.Fatalf("entry count = %d, want 3", n)
	}
	offset := 6 + 16*3
	for i, want := range []int{16, 32, 48} {
		entry := ico[6+16*i : 6+16*(i+1)]
		if int(entry[0]) != want || int(entry[1]) != want {
			t.Fatalf("entry %d is %dx%d, want %dx%d", i, entry[0], entry[1], want, want)
		}
		size := uint32(entry[8]) | uint32(entry[9])<<8 | uint32(entry[10])<<16 | uint32(entry[11])<<24
		start := uint32(entry[12]) | uint32(entry[13])<<8 | uint32(entry[14])<<16 | uint32(entry[15])<<24
		if int(start) != offset {
			t.Fatalf("entry %d offset = %d, want %d", i, start, offset)
		}
		if string(ico[start:start+4]) != "\x89PNG" {
			t.Fatalf("entry %d payload is not a PNG: % x", i, ico[start:start+4])
		}
		offset += int(size)
	}
	if offset != len(ico) {
		t.Fatalf("ico is %d bytes but the entries cover %d", len(ico), offset)
	}
}
