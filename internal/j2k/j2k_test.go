package j2k

import (
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// be2/be4 append a big-endian uint16/uint32.
func be2(b []byte, v uint16) []byte { return append(b, byte(v>>8), byte(v)) }
func be4(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// buildSIZ builds an SOC + SIZ prefix for numcomps components of the given
// image / tile geometry. Each component is 8-bit unsigned, dx=dy=1.
func buildSIZ(x1, y1, tdx, tdy uint32, numcomps int) []byte {
	var b []byte
	b = be2(b, msSOC)
	b = be2(b, msSIZ)
	// Lsiz = 38 + 3*numcomps
	b = be2(b, uint16(38+3*numcomps))
	b = be2(b, 0)   // Rsiz
	b = be4(b, x1)  // Xsiz
	b = be4(b, y1)  // Ysiz
	b = be4(b, 0)   // X0siz
	b = be4(b, 0)   // Y0siz
	b = be4(b, tdx) // XTsiz
	b = be4(b, tdy) // YTsiz
	b = be4(b, 0)   // XT0siz
	b = be4(b, 0)   // YT0siz
	b = be2(b, uint16(numcomps))
	for i := 0; i < numcomps; i++ {
		b = append(b, 7, 1, 1) // Ssiz=7 (8-bit unsigned), XRsiz=1, YRsiz=1
	}
	return b
}

func decodeHeader(t *testing.T, data []byte) (*image.Image, error) {
	t.Helper()
	s := cio.NewMemoryInputStream(data)
	d := CreateDecompress()
	return d.ReadHeader(s, nil)
}

func TestReadHeaderMalformed(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"noSOC", []byte{0x00, 0x00}},
		{"socOnly", func() []byte { return be2(nil, msSOC) }()},
		{"sizTruncated", append(be2(be2(nil, msSOC), msSIZ), 0x00, 0x20)}, // Lsiz says 32 but no data
		{"sizTooSmall", func() []byte {
			b := be2(nil, msSOC)
			b = be2(b, msSIZ)
			b = be2(b, 10) // Lsiz too small
			b = append(b, make([]byte, 8)...)
			return b
		}()},
		{"zeroImageSize", buildSIZ(0, 0, 1, 1, 1)},
		{"zeroTileSize", buildSIZ(16, 16, 0, 0, 1)},
		{"absurdTileCount", buildSIZ(0xffffffff, 0xffffffff, 1, 1, 1)},
		{"garbageAfterSOC", []byte{0xff, 0x4f, 0x12, 0x34, 0x56, 0x78}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Must return an error, and must never panic.
			if _, err := decodeHeader(t, c.data); err == nil {
				t.Fatalf("expected error for malformed input %q, got nil", c.name)
			}
		})
	}
}

func TestReadHeaderZeroComponents(t *testing.T) {
	// Csiz=0: remaining SIZ parameters (0) match, but image size checks apply.
	data := buildSIZ(16, 16, 16, 16, 0)
	if _, err := decodeHeader(t, data); err == nil {
		t.Fatalf("expected error for zero-component SIZ")
	}
}

// TestMergePPMOverflow crafts PPM markers whose Nppm lengths overflow, and
// asserts merge fails with an error rather than panicking.
func TestMergePPMOverflow(t *testing.T) {
	d := CreateDecompress()
	d.CP.Ppm = 1
	// Two markers, each declaring a huge Nppm so the running total overflows.
	huge := []byte{0xff, 0xff, 0xff, 0xff} // Nppm = 0xffffffff
	d.CP.PpmMarkers = []cparams.Ppx{{Data: append([]byte(nil), huge...)}, {Data: append([]byte(nil), huge...)}}
	d.CP.PpmMarkersCount = 2
	if err := d.mergePPM(); err == nil {
		t.Fatalf("expected PPM overflow error")
	}
}

// FuzzReadHeader ensures ReadHeader never panics on arbitrary bytes.
func FuzzReadHeader(f *testing.F) {
	f.Add(buildSIZ(16, 16, 16, 16, 1))
	f.Add([]byte{0xff, 0x4f})
	f.Fuzz(func(t *testing.T, data []byte) {
		s := cio.NewMemoryInputStream(data)
		d := CreateDecompress()
		_, _ = d.ReadHeader(s, nil) // must not panic
	})
}

// FuzzDecode ensures the full decode path never panics on arbitrary bytes. A
// size cap prevents fuzzing from OOMing on legitimately-huge declared images.
func FuzzDecode(f *testing.F) {
	f.Add(buildSIZ(16, 16, 16, 16, 1))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := cio.NewMemoryInputStream(data)
		d := CreateDecompress()
		d.SetStrictMode(false)
		img, err := d.ReadHeader(s, nil)
		if err != nil || img == nil {
			return
		}
		// Bound total decoded size to avoid OOM under the fuzzer.
		const maxPixels = 1 << 20
		var total uint64
		for i := uint32(0); i < img.Numcomps; i++ {
			total += uint64(img.Comps[i].W) * uint64(img.Comps[i].H)
			if total > maxPixels {
				return
			}
		}
		if err := d.SetDecodeArea(img, 0, 0, 0, 0); err != nil {
			return
		}
		_ = d.Decode(s, img, nil) // must not panic
	})
}
