package gopenjpeg

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxFuzzDecodedBytes bounds the estimated decoded sample memory a fuzz input
// is allowed to imply before we attempt a full decode, so the fuzzer does not
// OOM on legitimately-huge declared geometry (mirrors the size cap in the
// internal j2k FuzzDecode target). 64 MiB of int32 samples.
const maxFuzzDecodedBytes = 64 << 20

// oracleSeedFiles is a curated ~40-file subset of the OpenJPEG conformance /
// non-regression corpus, spanning valid Part-1 codestreams, JP2/JPH containers,
// HTJ2K, and a broad set of known crashers (SIGSEGV/SIGFPE/asan/GDAL-fuzzer
// regressions). Paths are relative to oracle/data/input; missing files (corpus
// absent) are skipped at seed time. Small representative copies live under
// testdata/fuzzseed so seeds exist even without the oracle.
var oracleSeedFiles = []string{
	// Valid Part-1 codestreams.
	"conformance/p0_01.j2k", "conformance/p0_02.j2k", "conformance/p0_03.j2k",
	"conformance/p0_09.j2k", "conformance/p0_11.j2k", "conformance/p0_12.j2k",
	"conformance/p0_13.j2k", "conformance/p0_14.j2k", "conformance/p1_01.j2k",
	"conformance/p1_06.j2k", "conformance/p1_07.j2k",
	// Valid conformance JP2 containers.
	"conformance/file1.jp2", "conformance/file2.jp2",
	// HTJ2K.
	"nonregression/htj2k/byte.jph", "nonregression/htj2k/byte_causal.jhc",
	"nonregression/htj2k/Bretagne1_ht.j2k", "nonregression/htj2k/Bretagne1_ht_lossy.j2k",
	// Valid non-regression JP2 with alpha / chroma subsampling.
	"nonregression/basn4a08.jp2", "nonregression/basn6a08.jp2",
	"nonregression/issue411-ycc420.jp2", "nonregression/issue411-ycc422.jp2",
	"nonregression/issue411-ycc444.jp2",
	// Known crashers / malformed (the security surface).
	"nonregression/issue726.j2k", "nonregression/issue979.j2k",
	"nonregression/issue1438.j2k", "nonregression/issue1472-bigloop.j2k",
	"nonregression/issue226.j2k",
	"nonregression/huge-tile-size.jp2",
	"nonregression/issue427-null-image-size.jp2",
	"nonregression/issue427-illegal-tile-offset.jp2",
	"nonregression/issue823.jp2",
	"nonregression/gdal_fuzzer_check_number_of_tiles.jp2",
	"nonregression/gdal_fuzzer_check_comp_dx_dy.jp2",
	"nonregression/gdal_fuzzer_unchecked_numresolutions.jp2",
	"nonregression/gdal_fuzzer_assert_in_opj_j2k_read_SQcd_SQcc.patch.jp2",
	"nonregression/1851.pdf.SIGSEGV.ce9.948.jp2",
	"nonregression/2236.pdf.SIGSEGV.398.1376.jp2",
	"nonregression/26ccf3651020967f7778238ef5af08af.SIGFPE.d25.527.jp2",
	"nonregression/2977.pdf.asan.67.2198.jp2",
	"nonregression/4149.pdf.SIGSEGV.cf7.3501.jp2",
	"nonregression/451.pdf.SIGSEGV.5b5.3723.jp2",
	"nonregression/4241ac039aba57e6a9c948d519d94216_asan_heap-oob_14650f2_7469_602.jp2",
	"nonregression/broken1.jp2",
}

// addRootFuzzSeeds seeds a fuzz target from the checked-in small corpus (always
// present) and, when the oracle is available, the curated corpus subset.
func addRootFuzzSeeds(f *testing.F) {
	f.Helper()
	// Checked-in small seeds (always present, keep seeds valid without oracle).
	if entries, err := os.ReadDir("testdata/fuzzseed"); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) == ".md" {
				continue
			}
			if data, err := os.ReadFile(filepath.Join("testdata/fuzzseed", e.Name())); err == nil {
				f.Add(data)
			}
		}
	}
	// Curated oracle subset (skipped when the corpus is absent).
	for _, rel := range oracleSeedFiles {
		p := filepath.Join("oracle", "data", "input", rel)
		if data, err := os.ReadFile(p); err == nil && len(data) < 5<<20 {
			f.Add(data)
		}
	}
	// A few structural corner cases.
	f.Add([]byte{})
	f.Add([]byte{0xff, 0x4f})                                                             // SOC only
	f.Add([]byte{0x00, 0x00, 0x00, 0x0c, 0x6a, 0x50, 0x20, 0x20, 0x0d, 0x0a, 0x87, 0x0a}) // JP2 sig box only
}

// decodeOptionMatrix derives a decode-option set from the input's first bytes,
// exercising the format / reduce / layer / decode-area / tile / component /
// strict controls without depending on any particular byte being present.
func decodeOptionMatrix(data []byte) []Option {
	var c [6]byte
	for i := range c {
		if i < len(data) {
			c[i] = data[i]
		}
	}
	opts := make([]Option, 0, 6)
	switch c[0] % 3 {
	case 1:
		opts = append(opts, WithFormat(FormatJ2K))
	case 2:
		opts = append(opts, WithFormat(FormatJP2))
	}
	opts = append(opts, WithReduce(uint32(c[1]%5)))
	opts = append(opts, WithLayers(uint32(c[2]%4)))
	opts = append(opts, WithStrictMode(c[3]&1 == 1))
	switch {
	case c[4]&3 == 1:
		// Small decode area anchored at the origin.
		s := int32(c[5]%64) + 1
		opts = append(opts, WithDecodeArea(0, 0, s, s))
	case c[4]&3 == 2:
		opts = append(opts, WithTile(int(c[5]%8)))
	case c[4]&3 == 3:
		opts = append(opts, WithComponents(uint32(c[5]%5)))
	}
	return opts
}

// infoWithinCap reports whether the header geometry implies a decoded size
// within maxFuzzDecodedBytes (worst case: no reduce applied).
func infoWithinCap(info *Info) bool {
	if info == nil {
		return true
	}
	if info.X1 <= info.X0 || info.Y1 <= info.Y0 {
		return true // empty/degenerate; let the decoder reject it cheaply
	}
	var total uint64
	for _, comp := range info.Components {
		dx := uint64(comp.Dx)
		dy := uint64(comp.Dy)
		if dx == 0 {
			dx = 1
		}
		if dy == 0 {
			dy = 1
		}
		w := (uint64(info.X1) - uint64(info.X0) + dx - 1) / dx
		h := (uint64(info.Y1) - uint64(info.Y0) + dy - 1) / dy
		total += w * h * 4
		if total > maxFuzzDecodedBytes {
			return false
		}
	}
	return true
}

// FuzzDecode drives the public Decode over arbitrary bytes with an
// option matrix derived from the input, in both autodetect and forced-format
// modes. The library must never panic, hang, read out of bounds, or over-
// allocate: any panic here is a bug (the fuzzer reports it; the recover turns
// it into an attributable failure).
func FuzzDecode(f *testing.F) {
	addRootFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()

		// Cheap header geometry check to bound decoded memory.
		info, _ := ReadInfo(bytes.NewReader(data))
		if !infoWithinCap(info) {
			return
		}

		opts := decodeOptionMatrix(data)
		if img, err := Decode(bytes.NewReader(data), opts...); err == nil && img != nil {
			// Touch the result so a mis-sized component slice would surface.
			for i := 0; i < img.NumComponents(); i++ {
				_ = img.Component(i)
			}
		}
	})
}

// FuzzDecodeConcurrent runs the same decode surface under WithConcurrency(4):
// the worker scheduling (per-code-block tier-1, DWT row/column passes) must
// not introduce panics or data races on any input.
func FuzzDecodeConcurrent(f *testing.F) {
	addRootFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()

		info, _ := ReadInfo(bytes.NewReader(data))
		if !infoWithinCap(info) {
			return
		}

		opts := append(decodeOptionMatrix(data), WithConcurrency(4))
		_, _ = Decode(bytes.NewReader(data), opts...)
	})
}

// FuzzReadInfo drives ReadInfo (header-only) over arbitrary bytes in both
// autodetect and forced-format modes; it must never panic or over-allocate.
func FuzzReadInfo(f *testing.F) {
	addRootFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()
		_, _ = ReadInfo(bytes.NewReader(data))
		_, _ = ReadInfo(bytes.NewReader(data), WithFormat(FormatJ2K))
		_, _ = ReadInfo(bytes.NewReader(data), WithFormat(FormatJP2))
	})
}

// buildFuzzImage constructs a small, always-valid image from the fuzz input:
// the leading bytes pick geometry (w,h in [1,24], 1..4 components, 8-bit
// unsigned), the remainder fills sample data. Returns nil when there is not
// enough input to form even a 1x1 image.
func buildFuzzImage(data []byte) (*Image, int) {
	if len(data) < 3 {
		return nil, 0
	}
	w := int(data[0]%24) + 1
	h := int(data[1]%24) + 1
	nc := int(data[2]%4) + 1
	payload := data[3:]

	cs := ColorSpaceGray
	if nc >= 3 {
		cs = ColorSpaceSRGB
	}
	comps := make([]Component, nc)
	pi := 0
	for i := 0; i < nc; i++ {
		samples := make([]int32, w*h)
		for k := range samples {
			var v int32
			if pi < len(payload) {
				v = int32(payload[pi])
				pi++
			}
			samples[k] = v
		}
		comps[i] = Component{
			Dx: 1, Dy: 1, W: uint32(w), H: uint32(h),
			X0: 0, Y0: 0, Prec: 8, Sgnd: false, Data: samples,
		}
	}
	return NewImage(cs, 0, 0, uint32(w), uint32(h), comps), nc
}

// FuzzEncodeDecodeRoundTrip encodes a fuzz-shaped small image losslessly, then
// decodes the produced codestream and verifies the samples survive the round
// trip exactly. It exercises the encode path (which must never panic) and the
// lossless-fidelity contract. Encode options are derived from a control byte.
func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	// Seeds: a handful of tiny geometries.
	f.Add([]byte{4, 4, 1, 10, 20, 30, 40, 50})
	f.Add([]byte{8, 8, 3, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte{1, 1, 4, 0, 0, 0, 0})
	f.Add([]byte{16, 3, 2, 255, 128, 64, 0, 1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()

		img, nc := buildFuzzImage(data)
		if img == nil {
			return
		}
		// Snapshot the expected samples before Encode: the encoder is allowed
		// to consume/transform the source image's component buffers in place
		// (as the C reference does), so we must not read them back afterwards.
		wantW := make([]uint32, nc)
		wantH := make([]uint32, nc)
		want := make([][]int32, nc)
		for i := 0; i < nc; i++ {
			c := img.Component(i)
			wantW[i], wantH[i] = c.W, c.H
			want[i] = append([]int32(nil), c.Data...)
		}

		// Encode options from a control byte (kept lossless: no rate/quality
		// allocation, reversible 5/3, reversible MCT when applicable).
		var ctrl byte
		if len(data) > 0 {
			ctrl = data[len(data)-1]
		}
		encOpts := []EncodeOption{WithLossless()}
		if res := int(ctrl%5) + 1; res >= 1 {
			encOpts = append(encOpts, WithResolutions(res))
		}
		switch (ctrl >> 3) % 5 {
		case 0:
			encOpts = append(encOpts, WithProgressionOrder(ProgLRCP))
		case 1:
			encOpts = append(encOpts, WithProgressionOrder(ProgRLCP))
		case 2:
			encOpts = append(encOpts, WithProgressionOrder(ProgRPCL))
		case 3:
			encOpts = append(encOpts, WithProgressionOrder(ProgPCRL))
		case 4:
			encOpts = append(encOpts, WithProgressionOrder(ProgCPRL))
		}
		format := FormatJ2K
		if ctrl&0x40 != 0 {
			format = FormatJP2
			encOpts = append(encOpts, WithEncodeFormat(FormatJP2))
		}

		var buf bytes.Buffer
		if err := Encode(img, &buf, encOpts...); err != nil {
			// Rejecting an input (e.g. impossible resolution count for a 1x1
			// image) is fine; only a panic or a lossy round trip is a bug.
			return
		}

		var decOpts []Option
		if format == FormatJP2 {
			decOpts = append(decOpts, WithFormat(FormatJP2))
		} else {
			decOpts = append(decOpts, WithFormat(FormatJ2K))
		}
		out, err := Decode(bytes.NewReader(buf.Bytes()), decOpts...)
		if err != nil {
			t.Fatalf("lossless encode then decode failed: %v (w=%d h=%d nc=%d fmt=%d)",
				err, wantW[0], wantH[0], nc, format)
		}
		if out.NumComponents() != nc {
			t.Fatalf("component count changed: got %d want %d", out.NumComponents(), nc)
		}
		for i := 0; i < nc; i++ {
			got := out.Component(i)
			if got.W != wantW[i] || got.H != wantH[i] {
				t.Fatalf("comp %d geometry changed: got %dx%d want %dx%d",
					i, got.W, got.H, wantW[i], wantH[i])
			}
			if len(got.Data) != len(want[i]) {
				t.Fatalf("comp %d sample count changed: got %d want %d",
					i, len(got.Data), len(want[i]))
			}
			for k := range want[i] {
				if got.Data[k] != want[i][k] {
					t.Fatalf("comp %d sample %d not lossless: got %d want %d",
						i, k, got.Data[k], want[i][k])
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Encode options (C18)
// ---------------------------------------------------------------------------

// encodeFuzzVec is the compact encode-option vector FuzzEncodeOptions decodes
// from its input. Every field is a selector, not a raw value, so that byte-level
// mutation reaches interesting combinations instead of wandering through
// unreachable magnitudes. It is a struct so the seed corpus can name the shapes
// it pins (one per encode panic the audit found).
type encodeFuzzVec struct {
	W, H     byte // luma geometry selector (0 reachable: degenerate geometry)
	NComps   byte // 0..5 (0 is C4)
	Prec     byte // 0..33 (0 and >31 are C21)
	Sub      byte // component sub-sampling selector (0 is C6)
	Origin   byte // image x0/y0 selector; bit7 also requests a second Encode (C12)
	NumRes   byte // resolution count selector, 0 included
	Cblk     byte // code-block size selector
	Tile     byte // tile size selector
	TileOrig byte // tiling origin selector (C5)
	NRates   byte // rate layer count (>100 is C1)
	NQuality byte // quality layer count (>100 is C1)
	NPrec    byte // precinct pair count (0 with the flag set is C3)
	NPOC     byte // progression-order-change count (>32 is C2)
	Prog     byte // progression order
	MCT      byte // MCT mode selector (3 = custom Part-2 matrix)
	ROI      byte // ROI component/shift selector
	Mode     byte // code-block style bitmask
	Guard    byte // guard-bit selector
	MaxSize  byte // codestream / component size cap selector
	Flags    byte // bit0 signed, bit1 irreversible, bit2 JP2, bit3 SOP, bit4 EPH,
	// bit5 PLT, bit6 TLM, bit7 tile-parts
	Flags2 byte // bit0 comment, bit1 cinema2K, bit2 cinema4K, bit3 concurrency,
	// bit4 subsampling option, bit5 ICC profile, bit6 grid-inconsistent W,
	// bit7 short sample slice
}

const encodeVecLen = 22

func (v encodeFuzzVec) encode() []byte {
	return []byte{
		v.W, v.H, v.NComps, v.Prec, v.Sub, v.Origin, v.NumRes, v.Cblk,
		v.Tile, v.TileOrig, v.NRates, v.NQuality, v.NPrec, v.NPOC, v.Prog,
		v.MCT, v.ROI, v.Mode, v.Guard, v.MaxSize, v.Flags, v.Flags2,
	}
}

func decodeEncodeVec(data []byte) encodeFuzzVec {
	var b [encodeVecLen]byte
	copy(b[:], data)
	return encodeFuzzVec{
		W: b[0], H: b[1], NComps: b[2], Prec: b[3], Sub: b[4], Origin: b[5],
		NumRes: b[6], Cblk: b[7], Tile: b[8], TileOrig: b[9], NRates: b[10],
		NQuality: b[11], NPrec: b[12], NPOC: b[13], Prog: b[14], MCT: b[15],
		ROI: b[16], Mode: b[17], Guard: b[18], MaxSize: b[19], Flags: b[20],
		Flags2: b[21],
	}
}

// buildEncodeImage materialises the image described by v. Unlike
// buildFuzzImage it is allowed to produce degenerate images (zero components,
// zero-sized planes, dx==0, precision 0 or >31, short sample slices): Encode is
// the single validation choke point and must reject them with an error, never a
// panic (C4, C6, C21, C44).
func buildEncodeImage(v encodeFuzzVec, payload []byte) *Image {
	w := uint32(v.W) % 33 // 0..32
	h := uint32(v.H) % 33
	nc := int(v.NComps) % 6
	prec := uint32(v.Prec) % 34 // 0..33
	sgnd := v.Flags&1 != 0

	var dx, dy uint32 = 1, 1
	switch v.Sub % 5 {
	case 1:
		dx, dy = 0, 1 // C6
	case 2:
		dx, dy = 1, 0 // C6
	case 3:
		dx, dy = 2, 2
	case 4:
		dx, dy = 256, 1 // out of the 8-bit SIZ field
	}

	// The image offset is snapped to the sub-sampling factors: an unaligned
	// offset is rejected outright by the encoder (the two geometry derivations
	// disagree), so leaving it arbitrary would keep every sub-sampled vector out
	// of the encoder proper. Flags2 bit6 still produces inconsistent geometry.
	ax, ay := dx, dy
	if ax == 0 || ax > 32 {
		ax = 1
	}
	if ay == 0 || ay > 32 {
		ay = 1
	}
	x0 := uint32(v.Origin%3) * ax
	y0 := uint32((v.Origin>>2)%3) * ay

	cs := ColorSpaceGray
	if nc >= 3 {
		cs = ColorSpaceSRGB
	}

	// Component dimensions are derived from the reference grid exactly as the
	// encoder derives them (ceildiv(x1,dx)-ceildiv(x0,dx)), so that a valid
	// vector produces an image that actually encodes instead of one rejected for
	// an inconsistent geometry. The degenerate shapes stay reachable through the
	// dedicated knobs: Sub picks dx/dy of 0 or 256, Flags2 bit7 truncates the
	// sample slice, and Flags2 bit6 skews W/H away from the grid.
	x1, y1 := x0+w, y0+h
	gridDim := func(lo, hi, d uint32) uint32 {
		if d == 0 {
			return hi - lo
		}
		return (hi+d-1)/d - (lo+d-1)/d
	}
	comps := make([]Component, nc)
	for i := 0; i < nc; i++ {
		cdx, cdy := uint32(1), uint32(1)
		if i > 0 {
			cdx, cdy = dx, dy
		}
		cw := gridDim(x0, x1, cdx)
		ch := gridDim(y0, y1, cdy)
		if v.Flags2&0x40 != 0 {
			cw++ // deliberately inconsistent with the grid
		}
		n := int(cw) * int(ch)
		// Occasionally hand Encode a short sample slice (C21).
		if v.Flags2&0x80 != 0 && n > 0 {
			n /= 2
		}
		data := make([]int32, n)
		for k := range data {
			if len(payload) > 0 {
				data[k] = int32(payload[(k+i*17)%len(payload)])
			} else {
				data[k] = int32((k * 7) & 0xff)
			}
		}
		comps[i] = Component{
			Dx: cdx, Dy: cdy, W: cw, H: ch, Prec: prec, Sgnd: sgnd, Data: data,
		}
	}
	return NewImage(cs, x0, y0, x0+w, y0+h, comps)
}

// layerCounts is the rate/quality layer-count table (see buildEncodeOptions).
var layerCounts = []int{0, 1, 2, 3, 5, 8, 101, 129}

// buildEncodeOptions turns v into the option list. Counts are deliberately
// allowed past the fixed-array bounds the C parameter struct carries (100 rate
// layers, 32 POC records) because that mismatch is exactly what C1/C2 were.
func buildEncodeOptions(v encodeFuzzVec, payload []byte) []EncodeOption {
	at := func(i int) byte {
		if len(payload) == 0 {
			return byte(i * 31)
		}
		return payload[i%len(payload)]
	}

	opts := []EncodeOption{}
	if v.Flags&2 != 0 {
		opts = append(opts, WithIrreversible())
	} else {
		opts = append(opts, WithLossless())
	}
	if v.Flags&4 != 0 {
		opts = append(opts, WithEncodeFormat(FormatJP2))
	}
	if v.Flags&8 != 0 {
		opts = append(opts, WithSOP())
	}
	if v.Flags&0x10 != 0 {
		opts = append(opts, WithEPH())
	}
	if v.Flags&0x20 != 0 {
		opts = append(opts, WithPLT())
	}
	if v.Flags&0x40 != 0 {
		opts = append(opts, WithTLM())
	}
	if v.Flags&0x80 != 0 {
		opts = append(opts, WithTileParts([]byte{'R', 'L', 'C', 'X'}[int(at(0))%4]))
	}

	opts = append(opts, WithResolutions(int(v.NumRes)%12)) // 0..11, 0 is invalid
	cblkSizes := [][2]int{{64, 64}, {32, 32}, {4, 4}, {1024, 1}, {1024, 1024}, {3, 8}, {0, 0}, {8, 512}}
	cb := cblkSizes[int(v.Cblk)%len(cblkSizes)]
	opts = append(opts, WithCodeBlockSize(cb[0], cb[1]))

	switch int(v.Tile) % 8 {
	case 1:
		opts = append(opts, WithTileSize(8, 8))
	case 2:
		opts = append(opts, WithTileSize(16, 16))
	case 3:
		opts = append(opts, WithTileSize(0, 0))
	case 4:
		opts = append(opts, WithTileSize(-1, -1))
	case 5:
		opts = append(opts, WithTileSize(32, 16))
	case 6:
		opts = append(opts, WithTileSize(64, 64))
	case 7:
		opts = append(opts, WithTileSize(1, 1))
	}
	switch int(v.TileOrig) % 6 {
	case 1:
		opts = append(opts, WithTileOrigin(1, 1))
	case 2:
		opts = append(opts, WithTileOrigin(-1, -1))
	case 3:
		opts = append(opts, WithTileOrigin(0, 16)) // C5: degenerate tile grid
	case 4:
		opts = append(opts, WithTileOrigin(8, 8))
	case 5:
		opts = append(opts, WithTileOrigin(64, 64))
	}

	// Layer counts are drawn from a table rather than a modulus: the shapes that
	// matter are the small ones (which actually encode) and the ones past the
	// fixed [100] arrays (which must be rejected). Intermediate counts only make
	// each execution slower — rate allocation is O(layers x code-blocks x tiles).
	if n := layerCounts[int(v.NRates)%len(layerCounts)]; n > 0 { // >100 is C1
		rates := make([]float32, n)
		for i := range rates {
			rates[i] = float32(n-i) + float32(at(i)%4)
		}
		opts = append(opts, WithRates(rates...))
	}
	if n := layerCounts[int(v.NQuality)%len(layerCounts)]; n > 0 {
		q := make([]float32, n)
		for i := range q {
			q[i] = float32(20+i) + float32(at(i)%8)
		}
		opts = append(opts, WithQualityLayers(q...))
	}
	if v.NPrec != 0 || v.Mode&0x80 != 0 {
		n := int(v.NPrec) % 40 // 0 with the option applied is C3
		sizes := make([][2]int, n)
		for i := range sizes {
			sizes[i] = [2]int{1 << (int(at(i))%12 + 1), 1 << (int(at(i+1))%12 + 1)}
		}
		opts = append(opts, WithPrecincts(sizes...))
	}
	if n := int(v.NPOC) % 40; n > 0 { // >32 is C2
		pocs := make([]POCChange, n)
		for i := range pocs {
			pocs[i] = POCChange{
				Tile:      uint32(at(i)) % 4,
				ResStart:  uint32(at(i+1)) % 8,
				CompStart: uint32(at(i+2)) % 6,
				LayEnd:    uint32(at(i+3)) % 8,
				ResEnd:    uint32(at(i+4)) % 8,
				CompEnd:   uint32(at(i+5)) % 6,
				Order:     ProgressionOrder(int(at(i+6)) % 6),
			}
		}
		opts = append(opts, WithPOC(pocs...))
	}

	// Index 0 (the zero vector) must be a VALID order, otherwise most of the
	// option space would be rejected at setup and never reach the encoder; the
	// two invalid values sit at the end of the table.
	progs := []ProgressionOrder{
		ProgLRCP, ProgRLCP, ProgRPCL, ProgPCRL, ProgCPRL,
		ProgressionOrder(-1), ProgressionOrder(5),
	}
	opts = append(opts, WithProgressionOrder(progs[int(v.Prog)%len(progs)]))
	switch int(v.MCT) % 4 {
	case 0:
		// leave the default (resolved from the component count)
	case 1:
		opts = append(opts, WithMCT(0))
	case 2:
		opts = append(opts, WithMCT(2)) // mode 2 without a matrix: rejected
	case 3:
		n := int(v.NComps) % 6
		matrix := make([]float32, n*n)
		for i := range matrix {
			matrix[i] = float32(int(at(i))-128) / 64
		}
		shift := make([]int32, n)
		for i := range shift {
			shift[i] = int32(at(i)) - 128
		}
		opts = append(opts, WithCustomMCT(matrix, shift))
	}

	if v.ROI != 0 {
		opts = append(opts, WithROI(int(v.ROI%8)-1, int(v.ROI>>3)%40))
	}
	if v.Mode&0x7f != 0 {
		opts = append(opts, WithModeSwitches(int(v.Mode&0x3f)))
	}
	if g := int(v.Guard) % 12; g < 10 { // 8..9 are out of the documented [0,7]
		opts = append(opts, WithGuardBits(g))
	}
	switch int(v.MaxSize) % 5 {
	case 1:
		opts = append(opts, WithMaxCodestreamSize(1))
	case 2:
		opts = append(opts, WithMaxCodestreamSize(4096))
	case 3:
		opts = append(opts, WithMaxComponentSize(1))
	case 4:
		opts = append(opts, WithMaxComponentSize(-1))
	}

	if v.Flags2&1 != 0 {
		opts = append(opts, WithComment(string(payload)))
	}
	if v.Flags2&2 != 0 {
		opts = append(opts, WithCinema2K(int(at(0))%2*24+24))
	}
	if v.Flags2&4 != 0 {
		opts = append(opts, WithCinema4K())
	}
	if v.Flags2&8 != 0 {
		opts = append(opts, WithEncodeConcurrency(2))
	}
	if v.Flags2&0x10 != 0 {
		opts = append(opts, WithSubsampling(int(at(0))%3, int(at(1))%3))
	}
	return opts
}

// FuzzEncodeOptions drives Encode over an arbitrary option vector paired with an
// arbitrary (possibly degenerate) image. Encode is the single validation choke
// point for the encoder (T1): whatever the option/geometry combination, it must
// return an error or a codestream — never panic. C18: no fuzz target reached the
// encode-option surface at all, which is how C1-C6, C12, C21, C30 and C44 all
// shipped as public panics.
//
// The seed corpus below encodes every one of those panic shapes; they are fixed,
// so the seeds pass, and they keep passing only as long as the fixes hold.
func FuzzEncodeOptions(f *testing.F) {
	seed := func(v encodeFuzzVec, payload ...byte) {
		f.Add(append(v.encode(), payload...))
	}

	// --- Valid, ordinary configurations. ---
	seed(encodeFuzzVec{W: 32, H: 32, NComps: 1, Prec: 8, NumRes: 3, Cblk: 0}, 1, 2, 3, 4)
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 2, Cblk: 1, NRates: 3}, 9, 8, 7)
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 2, Flags: 0x04 | 0x02}, 5, 6, 7)
	seed(encodeFuzzVec{W: 24, H: 24, NComps: 1, Prec: 12, NumRes: 4, Tile: 1, Flags: 0x08 | 0x10}, 3, 1, 4)
	seed(encodeFuzzVec{W: 32, H: 8, NComps: 4, Prec: 8, NumRes: 1, NQuality: 2, Flags: 0x20 | 0x40}, 2, 7)
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, NPOC: 2, Prog: 3}, 1, 1, 2)
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, NPrec: 2}, 4, 5, 6)
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 2, Flags2: 0x08}, 1)

	// --- Panic-shaped combinations from the audit (all fixed in PR1-PR8). ---
	// C1: more rate / quality layers than the fixed [100] arrays hold.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, NRates: 101})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, NQuality: 129})
	// C2: more POC records than the fixed [32] array holds.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, NPOC: 33})
	// C3: WithPrecincts() with zero pairs (ResSpec=0 -> PrcwInit[-1]).
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, NPrec: 0, Mode: 0x80})
	// C4: zero-component image.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 0, Prec: 8, NumRes: 3})
	// C5: degenerate tile grid (origin at/past the image extent).
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Tile: 2, TileOrig: 3})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Tile: 3})
	// C6: zero / oversized component sub-sampling factors.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, Sub: 1})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, Sub: 2})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, Sub: 4})
	// C21: zero-sized planes, zero / oversized precision, short sample slices.
	seed(encodeFuzzVec{W: 0, H: 0, NComps: 1, Prec: 8, NumRes: 3})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 0, NumRes: 3})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 32, NumRes: 3})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Flags2: 0x80})
	// C44: negative tile size / origin.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Tile: 4})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, TileOrig: 2})
	// Impossible resolution counts, degenerate code-blocks, out-of-range ROI,
	// guard bits and size caps.
	seed(encodeFuzzVec{W: 1, H: 1, NComps: 1, Prec: 8, NumRes: 11})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 0})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Cblk: 6})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Cblk: 4})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, ROI: 0xff})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Guard: 9})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, MaxSize: 1})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, MaxSize: 4})
	// Custom / invalid MCT (C27), cinema profiles, tile-parts on a tiny image.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, MCT: 2})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, MCT: 3}, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, MCT: 3})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 12, NumRes: 3, Flags2: 0x02})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 12, NumRes: 3, Flags2: 0x04})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Flags: 0x80})
	// C14: WithSubsampling disagreeing with the components.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, Flags2: 0x10}, 2, 2)
	// --- Shapes this target found (fixed in this change). ---
	// An unrecognised progression order indexed the empty progression string in
	// pi.CreateEncode; it only bites once tile-parts put CreateEncode on the path.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Prog: 5, Flags: 0x80})
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Prog: 6, Flags: 0x80})
	// A tiling origin past the image extent underflowed X1-Tx0 into the tile
	// width: a ~4 GiB allocation with no explicit tile size.
	seed(encodeFuzzVec{W: 8, H: 8, NComps: 1, Prec: 8, NumRes: 2, TileOrig: 5})
	// A custom MCT over sub-sampled components walked every plane with
	// component 0's sample count.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 3, Prec: 8, NumRes: 3, Sub: 3, MCT: 3}, 1, 2, 3, 4)
	// MCT requested for fewer than 3 components dereferenced comps[1]/comps[2].
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 2, Prec: 8, NumRes: 3, MCT: 1})
	// A component whose grid extent is empty (1-wide image at dx=2) handed a
	// zero-length row to the 5/3 DWT.
	seed(encodeFuzzVec{W: 1, H: 15, NComps: 2, Prec: 14, Sub: 3, Origin: 1, NumRes: 2})
	// A declared geometry that disagrees with the reference grid.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Flags2: 0x40})
	// Re-encoding the same image (C12).
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, Origin: 0x80})
	// A tile whose higher resolutions are 1x0 ran the vertical DWT pass over a
	// zero-height resolution.
	seed(encodeFuzzVec{W: 15, H: 1, NComps: 1, Prec: 14, Origin: 2, NumRes: 5, Tile: 2})
	// A POC record with a zero end bound makes the tile-part count 0 while a
	// tile part is still written, under-sizing the TLM offsets buffer.
	seed(encodeFuzzVec{W: 16, H: 16, NComps: 1, Prec: 8, NumRes: 3, NPOC: 1,
		Flags: 0x40 | 0x80}, '1', '0', '0')
	// Empty input (all-zero vector).
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Encode panicked on vec %v: %v", data[:min(len(data), encodeVecLen)], r)
			}
		}()

		v := decodeEncodeVec(data)
		var payload []byte
		if len(data) > encodeVecLen {
			payload = data[encodeVecLen:]
		}

		img := buildEncodeImage(v, payload)
		opts := buildEncodeOptions(v, payload)
		if v.Flags2&0x20 != 0 {
			img.SetICCProfile(payload)
		}
		// Exercise the encoder's diagnostic emission path as well: a nil manager
		// short-circuits every event call site (C61 is the gate-side version of
		// the same gap).
		var diags int
		count := func(string) { diags++ }
		opts = append(opts,
			WithEncodeErrorHandler(count),
			WithEncodeWarningHandler(count),
			WithEncodeInfoHandler(count),
		)

		var buf bytes.Buffer
		err := Encode(img, &buf, opts...)
		if err != nil {
			// Rejecting a bad configuration is the expected outcome for most of
			// this space; only a panic is a bug.
			return
		}
		if buf.Len() == 0 {
			t.Fatalf("Encode reported success but produced no bytes")
		}
		// C12: the source image must survive a successful encode, so encoding it
		// a second time must also not panic (and should behave identically).
		if v.Origin&0x80 != 0 {
			var buf2 bytes.Buffer
			if err2 := Encode(img, &buf2, opts...); err2 == nil && buf2.Len() != buf.Len() {
				t.Fatalf("re-encoding the same image produced %d bytes, first pass produced %d",
					buf2.Len(), buf.Len())
			}
		}
		// The produced codestream must be decodable back (or cleanly rejected):
		// feed it to the decoder, which must not panic either.
		var decOpts []Option
		if v.Flags&4 != 0 {
			decOpts = append(decOpts, WithFormat(FormatJP2))
		} else {
			decOpts = append(decOpts, WithFormat(FormatJ2K))
		}
		_, _ = Decode(bytes.NewReader(buf.Bytes()), decOpts...)
		_ = diags
	})
}

// ---------------------------------------------------------------------------
// Colour pipeline (C18)
// ---------------------------------------------------------------------------

// addConvertSeeds seeds a (data, ctrl) target with the same corpus
// addRootFuzzSeeds uses, paired with every reduce level in [0,4] for the
// sub-sampled sYCC files. Reduce is what produces the degenerate chroma
// geometry behind C8, so it has to be part of the seed, not left to mutation.
func addConvertSeeds(f *testing.F) {
	f.Helper()

	seed := func(data []byte, ctrls ...uint8) {
		for _, c := range ctrls {
			f.Add(data, c)
		}
	}

	// Checked-in small seeds at reduce 0.
	if entries, err := os.ReadDir("testdata/fuzzseed"); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) == ".md" {
				continue
			}
			data, err := os.ReadFile(filepath.Join("testdata/fuzzseed", e.Name()))
			if err != nil {
				continue
			}
			seed(data, 0)
			// The three sYCC files are the colour-pipeline shapes: sweep reduce.
			// issue411-ycc420 at reduce>=1 is the exact C8 reproduction (a 4:2:0
			// chroma plane left one row short of ceil(luma_h/2)); it must stay a
			// seed so the fixed panic keeps being covered on every seed run.
			if strings.HasPrefix(e.Name(), "issue411-ycc") {
				seed(data, 1, 2, 3, 4)
			}
		}
	}
	// Curated oracle subset (skipped when the corpus is absent). CMYK, eYCC and
	// CIELab live only here; the synthetic FuzzColorPipeline target covers those
	// transforms when the corpus is missing.
	for _, rel := range append([]string{
		"nonregression/issue205.jp2",                // CMYK
		"nonregression/issue208.jp2",                // CMYK
		"nonregression/issue236-ESYCC-CDEF.jp2",     // eYCC
		"nonregression/issue559-eci-091-CIELab.jp2", // CIELab (colr meth 2)
		"nonregression/relax.jp2",                   // embedded ICC profile
	}, oracleSeedFiles...) {
		p := filepath.Join("oracle", "data", "input", rel)
		if data, err := os.ReadFile(p); err == nil && len(data) < 1<<20 {
			seed(data, 0, 2)
		}
	}
	seed([]byte{}, 0)
}

// FuzzDecodeConvert drives the FULL user pipeline, not just Decode: every
// successfully decoded image is run through ConvertToRGB, ApplyICCProfile and
// ToStandard, then through ConvertToRGB a second time (which must stay
// idempotent — C9). C18/T4: the colour stage had no fuzz coverage at all, which
// is precisely why the C8 (sycc420 out-of-bounds) and C9 (ICC re-apply) bugs
// survived sustained clean fuzz runs of a decode-only target.
//
// ctrl selects the resolution reduction and the call order; the reduction
// matters because reduce does not touch dx/dy, so it is what produces the
// degenerate sub-sampled chroma geometry the transforms have to survive.
func FuzzDecodeConvert(f *testing.F) {
	addConvertSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, ctrl uint8) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input (ctrl=%d): %v", len(data), ctrl, r)
			}
		}()

		info, _ := ReadInfo(bytes.NewReader(data))
		if !infoWithinCap(info) {
			return
		}

		// The reduce level comes from ctrl (appended last, so it wins over the
		// value decodeOptionMatrix derived from the input bytes).
		opts := append(decodeOptionMatrix(data), WithReduce(uint32(ctrl%5)))
		img, err := Decode(bytes.NewReader(data), opts...)
		if err != nil || img == nil {
			return
		}

		hadProfile := len(img.ICCProfile()) > 0
		if ctrl&0x20 != 0 && hadProfile {
			// Apply the profile before ConvertToRGB as well as through it: the
			// two orders reach different states of the same non-idempotent
			// machinery (T2).
			_ = img.ApplyICCProfile()
		}

		// The documented pipeline: colour-convert, then render.
		_ = img.ConvertToRGB()
		// Idempotency (C9): a second conversion must not panic and must not
		// re-apply a consumed profile.
		_ = img.ConvertToRGB()
		// An already-applied profile must now be gone (C9); a still-present one
		// means a second ConvertToRGB would transform transformed samples.
		if hadProfile && len(img.ICCProfile()) > 0 && img.ColorSpace() == ColorSpaceSRGB {
			t.Fatalf("ICC profile survived a successful ConvertToRGB (C9 regression)")
		}
		_ = img.ApplyICCProfile()

		// Every component must still describe its own buffer.
		for i := 0; i < img.NumComponents(); i++ {
			c := img.Component(i)
			if c.Data == nil {
				continue
			}
			if uint64(len(c.Data)) < uint64(c.W)*uint64(c.H) {
				t.Fatalf("comp %d holds %d samples after conversion, needs %dx%d",
					i, len(c.Data), c.W, c.H)
			}
		}

		if m, err := img.ToStandard(); err == nil && m != nil {
			// Touch a bounded sample of the rendered pixels.
			b := m.Bounds()
			stepX := 1 + b.Dx()/32
			stepY := 1 + b.Dy()/32
			for y := b.Min.Y; y < b.Max.Y; y += stepY {
				for x := b.Min.X; x < b.Max.X; x += stepX {
					_, _, _, _ = m.At(x, y).RGBA()
				}
			}
		}
	})
}

// colorFuzzVec is the compact colour-pipeline vector FuzzColorPipeline decodes
// from its first bytes. It is a struct (rather than ad-hoc byte indexing) so the
// seed corpus below reads as the shapes it is meant to pin.
type colorFuzzVec struct {
	Space  byte // colour-space selector
	NComps byte // component count selector (0..5, zero included: C4)
	W, H   byte // luma geometry selector
	Sub    byte // chroma sub-sampling selector (444 / 420 / 422 / odd)
	Prec   byte // precision selector
	Short  byte // how many samples to chop off the CHROMA planes (C8 shape)
	Flags  byte // bit0 signed, bit1 CIELab colr box, bit2 ICC profile,
	// bit3 alpha channel, bit4 ToStandard before ConvertToRGB
	X0 byte // reference-grid origin selector (odd origins take the offx/offy paths)
}

const colorVecLen = 10

func (v colorFuzzVec) encode() []byte {
	return []byte{v.Space, v.NComps, v.W, v.H, v.Sub, v.Prec, v.Short, v.Flags, v.X0, 0}
}

func decodeColorVec(data []byte) colorFuzzVec {
	var b [colorVecLen]byte
	copy(b[:], data)
	return colorFuzzVec{
		Space: b[0], NComps: b[1], W: b[2], H: b[3], Sub: b[4],
		Prec: b[5], Short: b[6], Flags: b[7], X0: b[8],
	}
}

// buildColorImage materialises the image described by v. Sample values come
// from payload (cycled), so the fuzzer can steer the arithmetic as well as the
// geometry. It returns nil only for a vector it cannot represent.
func buildColorImage(v colorFuzzVec, payload []byte) *Image {
	spaces := []ColorSpace{
		ColorSpaceUnknown, ColorSpaceUnspecified, ColorSpaceSRGB,
		ColorSpaceGray, ColorSpaceSYCC, ColorSpaceEYCC, ColorSpaceCMYK,
	}
	cs := spaces[int(v.Space)%len(spaces)]
	nc := int(v.NComps) % 6 // 0..5
	w := uint32(v.W%17) + 1 // 1..17: odd dims exercise the trailing-row/column walks
	h := uint32(v.H%17) + 1
	prec := uint32(v.Prec%18) + 1 // 1..18
	sgnd := v.Flags&1 != 0
	x0 := uint32(v.X0 % 3)
	y0 := uint32((v.X0 >> 2) % 3)

	// Chroma sub-sampling factors for components 1 and 2.
	var cdx, cdy uint32 = 1, 1
	switch v.Sub % 5 {
	case 1:
		cdx, cdy = 2, 2 // 4:2:0
	case 2:
		cdx, cdy = 2, 1 // 4:2:2
	case 3:
		cdx, cdy = 1, 2
	case 4:
		cdx, cdy = 4, 4
	}

	sampleAt := func(k int) int32 {
		if len(payload) == 0 {
			return int32(k) & 0xff
		}
		v := int32(payload[k%len(payload)])
		if prec > 8 {
			v |= int32(payload[(k*7+3)%len(payload)]) << 8
		}
		return v & ((1 << prec) - 1)
	}

	comps := make([]Component, nc)
	for i := 0; i < nc; i++ {
		dx, dy := uint32(1), uint32(1)
		if i == 1 || i == 2 {
			dx, dy = cdx, cdy
		}
		cw := (w + dx - 1) / dx
		ch := (h + dy - 1) / dy
		n := int(cw) * int(ch)
		// C8 shape: leave the chroma planes short of what the walker expects.
		if (i == 1 || i == 2) && v.Short != 0 {
			n -= int(v.Short) % (int(cw) + 1)
			if n < 0 {
				n = 0
			}
		}
		data := make([]int32, n)
		for k := range data {
			data[k] = sampleAt(k + i*31)
		}
		var alpha uint16
		if i == 3 && v.Flags&8 != 0 {
			alpha = 1
		}
		comps[i] = Component{
			Dx: dx, Dy: dy, W: cw, H: ch, X0: 0, Y0: 0,
			Prec: prec, Sgnd: sgnd, Alpha: alpha, Data: data,
		}
	}
	return NewImage(cs, x0, y0, x0+w, y0+h, comps)
}

// cielabColrBuf builds the packed CIELab parameter block internal/jp2 stores in
// ICCProfileBuf for a colr box with meth==2 and EnumCS==14 (nine big-endian
// uint32 words, the first being 14). Word 1 == "DEF\0" selects the default
// range/offset set; anything else uses the explicit ranges in words 2..7.
func cielabColrBuf(def bool, payload []byte) []byte {
	buf := make([]byte, 36)
	binary.BigEndian.PutUint32(buf[0:], 14)
	if def {
		binary.BigEndian.PutUint32(buf[4:], 0x44454600) // "DEF\0"
		return buf
	}
	for i := 1; i < 9; i++ {
		var v uint32
		for k := 0; k < 4; k++ {
			if idx := (i-1)*4 + k; idx < len(payload) {
				v = v<<8 | uint32(payload[idx])
			} else {
				v <<= 8
			}
		}
		binary.BigEndian.PutUint32(buf[i*4:], v)
	}
	return buf
}

// FuzzColorPipeline drives the whole colour surface directly, without needing a
// codestream: sYCC 4:4:4 / 4:2:2 / 4:2:0 (including short chroma planes and odd
// reference-grid origins), CMYK, eYCC, CIELab and embedded-ICC images, followed
// by ToStandard. C18: none of these transforms was reachable from any fuzz
// target before, which is how the C8 out-of-bounds read shipped.
//
// The contract is the library-wide one: never panic. A layout the transforms
// reject returns ErrColorConvert / ErrICCApply, which is fine.
func FuzzColorPipeline(f *testing.F) {
	seed := func(v colorFuzzVec, payload, profile []byte) {
		f.Add(append(v.encode(), payload...), profile)
	}

	// 4:2:0 sYCC, chroma one row short — the C8 reproduction shape, synthesised
	// (the codestream form of it is seeded into FuzzDecodeConvert).
	seed(colorFuzzVec{Space: 4, NComps: 3, W: 3, H: 3, Sub: 1, Prec: 8, Short: 2}, []byte{16, 90, 200, 45}, nil)
	// 4:2:0 / 4:2:2 / 4:4:4 with full chroma, odd dimensions.
	seed(colorFuzzVec{Space: 4, NComps: 3, W: 6, H: 4, Sub: 1, Prec: 8}, []byte{1, 2, 3, 4, 5}, nil)
	seed(colorFuzzVec{Space: 4, NComps: 3, W: 5, H: 5, Sub: 2, Prec: 8}, []byte{9, 8, 7, 6}, nil)
	seed(colorFuzzVec{Space: 4, NComps: 3, W: 4, H: 4, Sub: 0, Prec: 8}, []byte{200, 100, 50}, nil)
	// Odd reference-grid origin: takes the offx/offy pre-roll branches.
	seed(colorFuzzVec{Space: 4, NComps: 3, W: 4, H: 4, Sub: 1, Prec: 8, X0: 1 | (1 << 2)}, []byte{3, 1, 4, 1, 5}, nil)
	// The sYCC heuristic path: an sRGB-labelled image with sub-sampled chroma is
	// re-labelled sYCC by ConvertToRGB.
	seed(colorFuzzVec{Space: 2, NComps: 3, W: 8, H: 6, Sub: 1, Prec: 8}, []byte{7, 7, 7}, nil)
	// CMYK (4 components, 1:1) and eYCC (3 components, 1:1).
	seed(colorFuzzVec{Space: 6, NComps: 4, W: 4, H: 3, Sub: 0, Prec: 8}, []byte{10, 20, 30, 40}, nil)
	seed(colorFuzzVec{Space: 5, NComps: 3, W: 4, H: 3, Sub: 0, Prec: 8}, []byte{60, 120, 180}, nil)
	seed(colorFuzzVec{Space: 5, NComps: 3, W: 4, H: 3, Sub: 0, Prec: 12, Flags: 1}, []byte{0xff, 0x7f}, nil)
	// CIELab, default and explicit ranges.
	seed(colorFuzzVec{Space: 2, NComps: 3, W: 3, H: 3, Sub: 0, Prec: 8, Flags: 2}, []byte{50, 128, 128}, nil)
	seed(colorFuzzVec{Space: 2, NComps: 3, W: 3, H: 3, Sub: 0, Prec: 16, Flags: 2 | 16}, []byte{0x20, 0x40}, nil)
	// Greyscale, greyscale+alpha, 5 components, and the zero-component image (C4).
	seed(colorFuzzVec{Space: 3, NComps: 1, W: 4, H: 4, Prec: 8}, []byte{1, 2, 3}, nil)
	seed(colorFuzzVec{Space: 3, NComps: 2, W: 4, H: 4, Prec: 8, Flags: 8}, []byte{1, 2, 3}, nil)
	seed(colorFuzzVec{Space: 2, NComps: 5, W: 4, H: 4, Prec: 8}, []byte{1, 2, 3}, nil)
	seed(colorFuzzVec{Space: 0, NComps: 0, W: 4, H: 4, Prec: 8}, nil, nil)
	// With an ICC profile attached (bit2): malformed and header-shaped.
	iccHdr := make([]byte, 132)
	copy(iccHdr[36:], []byte("acsp"))
	seed(colorFuzzVec{Space: 2, NComps: 3, W: 4, H: 4, Prec: 8, Flags: 4}, []byte{1, 2, 3}, iccHdr)
	seed(colorFuzzVec{Space: 3, NComps: 1, W: 4, H: 4, Prec: 8, Flags: 4}, []byte{1, 2, 3}, []byte{0, 0, 0, 0})
	seed(colorFuzzVec{Space: 4, NComps: 3, W: 4, H: 4, Sub: 1, Prec: 8, Short: 1, Flags: 4}, []byte{1, 2}, iccHdr)

	f.Fuzz(func(t *testing.T, data, profile []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on vec %v profile %d bytes: %v",
					data[:min(len(data), colorVecLen)], len(profile), r)
			}
		}()

		v := decodeColorVec(data)
		var payload []byte
		if len(data) > colorVecLen {
			payload = data[colorVecLen:]
		}
		img := buildColorImage(v, payload)
		if img == nil {
			return
		}

		switch {
		case v.Flags&2 != 0:
			// CIELab enumerated colour space: a colr-box parameter block with a
			// zero profile length. SetICCProfile cannot express that (it sets the
			// length), so the internal fields are set directly, exactly as
			// internal/jp2 does when it reads a colr meth=2 box.
			img.img.ICCProfileBuf = cielabColrBuf(v.Flags&16 == 0, payload)
			img.img.ICCProfileLen = 0
		case v.Flags&4 != 0:
			img.SetICCProfile(profile)
		}

		if v.Flags&16 != 0 {
			if m, err := img.ToStandard(); err == nil && m != nil {
				_ = m.Bounds()
			}
		}

		// The user pipeline. Errors are expected for layouts the transforms
		// refuse; only a panic (or a corrupted component slice) is a bug.
		_ = img.ConvertToRGB()
		_ = img.ConvertToRGB()
		_ = img.ApplyICCProfile()

		for i := 0; i < img.NumComponents(); i++ {
			c := img.Component(i)
			if c.Data != nil && uint64(len(c.Data)) > uint64(c.W)*uint64(c.H)+uint64(c.W)+1 {
				// Guards against a transform resizing a plane without updating its
				// declared geometry (the 4:2:0/4:2:2 walkers rewrite both).
				t.Fatalf("comp %d: %d samples for declared %dx%d", i, len(c.Data), c.W, c.H)
			}
		}
		if m, err := img.ToStandard(); err == nil && m != nil {
			b := m.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					_, _, _, _ = m.At(x, y).RGBA()
				}
			}
		}
	})
}

// FuzzApplyICCProfile drives ApplyICCProfile (icc.go), and through it the
// pure-Go Little CMS profile parser and transform builder, over arbitrary
// profile bytes attached to a small fixed decoded image. golittlecms is
// documented to never panic on malformed profiles; this target enforces that
// end to end through our wiring, and exercises both the RGB and the grey->RGB
// component-expansion branches (selected by a control byte) so a mutated
// profile that opens but produces an odd transform cannot slip a panic through.
// It must never panic regardless of the profile bytes; a successful apply is
// not required.
func FuzzApplyICCProfile(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 128))
	// A byte pattern that looks vaguely like an ICC header (size + 'acsp').
	hdr := make([]byte, 132)
	copy(hdr[36:], []byte("acsp"))
	f.Add(hdr)

	f.Fuzz(func(t *testing.T, profile []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte profile: %v", len(profile), r)
			}
		}()

		// Build a tiny fixed image whose component count depends on the first
		// profile byte, so both the RGB (>2 comps) and grey (<=2 comps) branches
		// of ApplyICCProfile are reached. 2x2 samples per component.
		nc := 3
		if len(profile) > 0 {
			nc = int(profile[0]%4) + 1 // 1..4
		}
		const w, h = 2, 2
		comps := make([]Component, nc)
		for i := range comps {
			data := make([]int32, w*h)
			for k := range data {
				data[k] = int32((i*7 + k*3) & 0xff)
			}
			comps[i] = Component{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: data}
		}
		img := NewImage(ColorSpaceSRGB, 0, 0, w, h, comps)
		img.SetICCProfile(profile)
		// Best-effort: an inapplicable profile returns ErrICCApply, an applicable
		// one returns nil. Either way it must not panic and must not corrupt the
		// component slices' lengths.
		_ = img.ApplyICCProfile()
		for i := 0; i < img.NumComponents(); i++ {
			c := img.Component(i)
			if len(c.Data) != int(c.W)*int(c.H) {
				t.Fatalf("comp %d data length %d != %dx%d after apply",
					i, len(c.Data), c.W, c.H)
			}
		}
	})
}
