package j2k

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// buildCOD builds a minimal, valid COD marker segment (Scod=0, LRCP, 1 layer,
// no MCT, 5 resolution levels, 64x64 code-blocks, reversible 5/3). The layout
// mirrors what readCOD parses: SGcod (5 bytes) + SPcod (5 bytes).
func buildCOD() []byte {
	var b []byte
	b = be2(b, msCOD)
	b = be2(b, 12)               // Lcod = 2 (length field) + 5 (SGcod) + 5 (SPcod)
	b = append(b, 0)             // Scod
	b = append(b, 0)             // SGcod: progression order (LRCP)
	b = be2(b, 1)                // SGcod: number of layers
	b = append(b, 0)             // SGcod: MCT
	b = append(b, 4, 4, 4, 0, 1) // SPcod: decomp levels, cblkw exp, cblkh exp, cblksty, qmfbid
	return b
}

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

// TestReadHeaderDuplicateCOD is the C19 regression. In strict mode a second COD
// marker in the main header must be rejected (matching the JPEG 2000 "no more
// than one COD marker per tile" constraint) rather than silently overwriting
// the coding parameters set by the first COD. In the default relaxed mode the
// duplicate must be tolerated, matching OpenJPEG 2.5.4 (which ships that guard
// disabled, upstream #1043) so no legitimate file is rejected.
func TestReadHeaderDuplicateCOD(t *testing.T) {
	data := buildSIZ(16, 16, 16, 16, 1)
	data = append(data, buildCOD()...)
	data = append(data, buildCOD()...) // duplicate COD

	// Strict mode: reject with the duplicate-COD diagnostic.
	var msgs []string
	mgr := &event.Manager{ErrorHandler: func(s string) { msgs = append(msgs, s) }}
	d := CreateDecompress()
	d.SetStrictMode(true)
	if _, err := d.ReadHeader(cio.NewMemoryInputStream(data), mgr); err == nil {
		t.Fatalf("strict: expected error for duplicate COD marker, got nil")
	} else if !errors.Is(err, ErrMarkerHandler) {
		t.Fatalf("strict: expected ErrMarkerHandler, got %v", err)
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "No more than one COD marker per tile") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("strict: expected the duplicate-COD diagnostic, got messages %v", msgs)
	}

	// Relaxed mode (default): the duplicate must NOT trigger the marker-handler
	// rejection (the header may still fail later for unrelated reasons such as a
	// missing QCD, but never with ErrMarkerHandler from the COD guard).
	dr := CreateDecompress()
	if _, err := dr.ReadHeader(cio.NewMemoryInputStream(data), &event.Manager{}); errors.Is(err, ErrMarkerHandler) {
		t.Fatalf("relaxed: duplicate COD must be tolerated, got ErrMarkerHandler: %v", err)
	}
}

// TestGetTileBeforeReadHeader is the C40 regression: GetTile called before
// ReadHeader (privateImage still nil) must return an error, not panic on a nil
// dereference.
func TestGetTileBeforeReadHeader(t *testing.T) {
	d := CreateDecompress()
	out := &image.Image{Numcomps: 1, Comps: make([]image.Comp, 1)}
	err := d.GetTile(nil, out, 0, nil)
	if !errors.Is(err, ErrHeaderNotRead) {
		t.Fatalf("expected ErrHeaderNotRead, got %v", err)
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

// TestMreaderBoundsSafe verifies the marker cursor zero-extends past the end of
// a short segment instead of indexing out of range (regression for the QCD
// SIQNT truncation crasher: a 2-byte QCD payload with Sqcx=SIQNT made
// readSQcdSQcc read a 2-byte SPqcx at offset 1 of a 2-byte buffer).
func TestMreaderBoundsSafe(t *testing.T) {
	r := &mreader{data: []byte{0xAB}}
	if got := r.u(1); got != 0xAB {
		t.Fatalf("u(1)=%#x want 0xAB", got)
	}
	// Reading past the end must not panic; missing low bytes read as 0.
	if got := r.u(2); got != 0 {
		t.Fatalf("u(2) past end=%#x want 0", got)
	}
	r2 := &mreader{data: []byte{0x12, 0x34}}
	r2.pos = 1
	if got := r2.u(2); got != 0x3400 { // 0x34 then a zero-extended byte
		t.Fatalf("u(2) partial=%#x want 0x3400", got)
	}
}

// TestReadSQcdSQccTruncated feeds the exact truncated SIQNT quantization
// segment from the fuzz crasher and asserts an error (not a panic).
func TestReadSQcdSQccTruncated(t *testing.T) {
	d := CreateDecompress()
	d.privateImage = &image.Image{Numcomps: 1}
	tcp := &cparams.TCP{TCCPs: make([]cparams.TCCP, 1)}
	// Sqcx=0x41 -> qntsty = 0x41&0x1f = 1 (SIQNT); only 1 payload byte left.
	data := []byte{0x41, 0x30}
	hs := len(data)
	if err := d.readSQcdSQcc(tcp, 0, data, &hs); err == nil {
		t.Fatalf("expected error for truncated SIQNT quantization segment")
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
