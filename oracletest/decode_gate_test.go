package oracletest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
)

// This is the Phase-3 decode acceptance gate: the pure-Go decoder
// (internal/j2k -> internal/tcd -> t2/t1/dwt/mct) must produce output that is
// bit-exact with the C reference opj_decompress on raw codestreams, or error
// exactly where opj_decompress errors.
//
// There are ZERO exclusions. The five files that were previously excluded are
// now all handled; the root causes (documented at their handling sites) were:
//
//   - _00042.j2k, issue135.j2k (9/7 + ICT, qmfbid=0, mct=1): the green ICT
//     output deviated by <=1 LSB on a few samples. Root cause: OpenJPEG is
//     built -O3 -ffast-math, so the shipped libopenjp2.so that opj_decompress
//     links reassociates the green computation (y - u*0.34413) - v*0.71414 into
//     y - (u*0.34413 + v*0.71414). internal/mct.DecodeReal now matches the
//     shipped binary's association, so these decode bit-exact (see nonregPass).
//
//   - issue142.j2k, issue726.j2k (chroma sub-sampled, dx=(1,2,2)): the decoder
//     produces spec-native sub-sampled components (e.g. 960x1080), but the
//     opj_decompress CLI post-processes 3-component sub-sampled images through
//     its sYCC->RGB path (color.c), upsampling chroma to full size before
//     writing PGX. We mirror that CLI post-processing in the gate and then
//     compare bit-exact (see nonregSyccPass / applyCLIColorPost).
//
//   - v4dwt_interleave_h.gsr105.j2k: opj_decompress (default strict mode)
//     rejects this file with "segment too long (3107) with max (1706) ... r=31".
//     internal/t2 already ports that per-segment length check, gated on strict
//     mode. The gate's default decodeGo runs non-strict; opj_decompress's
//     default is strict, so this file is checked with a strict-mode Go decode
//     (see nonregStrictError / decodeGoStrict) for a faithful error parity.

// nonregPass are nonregression .j2k files that decode bit-exact vs opj_decompress.
var nonregPass = []string{
	"Bretagne2.j2k",
	"CT_Phillips_JPEG2K_Decompr_Problem.j2k",
	"Cannotreaddatawithnosizeknown.j2k",
	"MarkerIsNotCompliant.j2k",
	"buxI.j2k",
	"buxR.j2k",
	"cthead1.j2k",
	"illegalcolortransform.j2k",
	"issue228.j2k",
	"issue399.j2k",
	"issue979.j2k",
	"j2k32.j2k",
	"kakadu_v4-4_openjpegv2_broken.j2k",
	"movie_00000.j2k",
	"movie_00001.j2k",
	"movie_00002.j2k",
	"orb-blue10-lin-j2k.j2k",
	"orb-blue10-win-j2k.j2k",
	"pacs.ge.j2k",
	"test_lossless.j2k",
	// Irreversible 9/7 + ICT (qmfbid=0, mct=1). Bit-exact now that
	// internal/mct.DecodeReal reproduces the shipped -ffast-math green
	// association (see the file header comment).
	"_00042.j2k",
	"issue135.j2k",
	// HTJ2K (ITU-T T.814) codestreams, decoded via internal/ht.
	"htj2k/Bretagne1_ht.j2k",
	"htj2k/Bretagne1_ht_lossy.j2k",
}

// nonregSyccPass are 3-component chroma-sub-sampled nonregression files. The Go
// library decodes spec-native sub-sampled components; opj_decompress's CLI
// post-processes them through its sYCC->RGB path before writing PGX. We apply
// the same CLI post-processing (applyCLIColorPost) and then require bit-exact.
var nonregSyccPass = []string{
	"issue142.j2k",
	"issue726.j2k",
}

// nonregBothError are files that opj_decompress rejects; we must reject too.
var nonregBothError = []string{
	"issue1438.j2k",
	"issue1472-bigloop.j2k",
	"issue226.j2k",
	"issue775-2.j2k",
	"issue775.j2k",
}

// nonregStrictError are files opj_decompress rejects in its default (strict)
// mode but that Go's default non-strict decodeGo only warns on. The check is
// ported in internal/t2 and gated on strict mode; opj_decompress defaults to
// strict, so we decode these with a strict-mode Go decoder to match.
var nonregStrictError = []string{
	"v4dwt_interleave_h.gsr105.j2k",
}

// TestDecodeConformanceP0P1 decodes every class-0/class-1 ETS raw codestream and
// requires bit-exactness against opj_decompress.
func TestDecodeConformanceP0P1(t *testing.T) {
	Require(t)
	files, _ := filepath.Glob(DataDir("input", "conformance", "p[01]_*.j2k"))
	sort.Strings(files)
	if len(files) == 0 {
		t.Skip("no conformance p0_/p1_ files found")
	}
	for _, in := range files {
		in := in
		t.Run(filepath.Base(in), func(t *testing.T) {
			comps, err := oracleDecodePGX(t, in)
			if err != nil {
				// If the oracle itself errors on a class-0/1 file, require we do too.
				if _, gerr := decodeGo(t, in, 0, 0, nil); gerr == nil {
					t.Fatalf("oracle errored but Go decoded: oracle=%v", err)
				}
				return
			}
			img, err := decodeGo(t, in, 0, 0, nil)
			if err != nil {
				t.Fatalf("Go decode failed: %v", err)
			}
			if err := compareComps(t, img, comps); err != nil {
				t.Fatalf("not bit-exact: %v", err)
			}
		})
	}
}

// TestDecodeNonregression decodes the curated nonregression list bit-exact and
// asserts error-parity on the files opj_decompress rejects.
func TestDecodeNonregression(t *testing.T) {
	Require(t)
	for _, name := range nonregPass {
		name := name
		t.Run("exact/"+name, func(t *testing.T) {
			in := DataDir("input", "nonregression", name)
			comps, err := oracleDecodePGX(t, in)
			if err != nil {
				t.Fatalf("oracle decode failed unexpectedly: %v", err)
			}
			img, err := decodeGo(t, in, 0, 0, nil)
			if err != nil {
				t.Fatalf("Go decode failed: %v", err)
			}
			if err := compareComps(t, img, comps); err != nil {
				t.Fatalf("not bit-exact: %v", err)
			}
		})
	}
	for _, name := range nonregSyccPass {
		name := name
		t.Run("sycc/"+name, func(t *testing.T) {
			in := DataDir("input", "nonregression", name)
			comps, err := oracleDecodePGX(t, in)
			if err != nil {
				t.Fatalf("oracle decode failed unexpectedly: %v", err)
			}
			img, err := decodeGo(t, in, 0, 0, nil)
			if err != nil {
				t.Fatalf("Go decode failed: %v", err)
			}
			// Mirror the opj_decompress CLI's sYCC->RGB post-processing that
			// upsamples sub-sampled chroma before writing PGX.
			applyCLIColorPost(img)
			if err := compareComps(t, img, comps); err != nil {
				t.Fatalf("not bit-exact after sYCC post-processing: %v", err)
			}
		})
	}
	for _, name := range nonregBothError {
		name := name
		t.Run("error/"+name, func(t *testing.T) {
			in := DataDir("input", "nonregression", name)
			if _, err := oracleDecodePGX(t, in); err == nil {
				t.Skipf("oracle unexpectedly succeeded on %s", name)
			}
			if _, err := decodeGo(t, in, 0, 0, nil); err == nil {
				t.Fatalf("oracle rejected %s but Go decoded it", name)
			}
		})
	}
	for _, name := range nonregStrictError {
		name := name
		t.Run("strict-error/"+name, func(t *testing.T) {
			in := DataDir("input", "nonregression", name)
			if _, err := oracleDecodePGX(t, in); err == nil {
				t.Skipf("oracle unexpectedly succeeded on %s", name)
			}
			if _, err := decodeGoStrict(t, in); err == nil {
				t.Fatalf("oracle (strict) rejected %s but strict Go decoded it", name)
			}
		})
	}
}

// TestDecodeReduce checks resolution-reduction (-r) parity.
func TestDecodeReduce(t *testing.T) {
	Require(t)
	files := []string{"p0_01.j2k", "p0_03.j2k", "p1_01.j2k"}
	for _, f := range files {
		for _, r := range []uint32{1, 2} {
			f, r := f, r
			t.Run(f+"/r"+itoa(int(r)), func(t *testing.T) {
				in := DataDir("input", "conformance", f)
				comps, err := oracleDecodePGX(t, in, "-r", itoa(int(r)))
				if err != nil {
					t.Skipf("oracle -r %d failed: %v", r, err)
				}
				img, err := decodeGo(t, in, r, 0, nil)
				if err != nil {
					t.Fatalf("Go -r %d failed: %v", r, err)
				}
				if err := compareComps(t, img, comps); err != nil {
					t.Fatalf("-r %d not bit-exact: %v", r, err)
				}
			})
		}
	}
}

// TestDecodeLayers checks quality-layer truncation (-l) parity.
func TestDecodeLayers(t *testing.T) {
	Require(t)
	cases := []struct {
		f string
		l uint32
	}{
		{"nonregression/Bretagne2.j2k", 1},
		{"nonregression/Bretagne2.j2k", 2},
		{"nonregression/buxR.j2k", 1},
	}
	for _, c := range cases {
		c := c
		t.Run(filepath.Base(c.f)+"/l"+itoa(int(c.l)), func(t *testing.T) {
			in := DataDir("input", filepath.FromSlash(c.f))
			comps, err := oracleDecodePGX(t, in, "-l", itoa(int(c.l)))
			if err != nil {
				t.Skipf("oracle -l %d failed: %v", c.l, err)
			}
			img, err := decodeGo(t, in, 0, c.l, nil)
			if err != nil {
				t.Fatalf("Go -l %d failed: %v", c.l, err)
			}
			if err := compareComps(t, img, comps); err != nil {
				t.Fatalf("-l %d not bit-exact: %v", c.l, err)
			}
		})
	}
}

// TestDecodeArea checks decode-window (-d x0,y0,x1,y1) parity.
func TestDecodeArea(t *testing.T) {
	Require(t)
	cases := []struct {
		f    string
		area [4]int32
	}{
		{"p0_01.j2k", [4]int32{32, 32, 96, 96}},
		{"p0_01.j2k", [4]int32{10, 20, 100, 110}},
		{"p1_01.j2k", [4]int32{0, 0, 64, 64}},
	}
	for _, c := range cases {
		c := c
		name := c.f + itoa(int(c.area[0])) + "_" + itoa(int(c.area[1])) + "_" + itoa(int(c.area[2])) + "_" + itoa(int(c.area[3]))
		t.Run(name, func(t *testing.T) {
			in := DataDir("input", "conformance", c.f)
			spec := itoa(int(c.area[0])) + "," + itoa(int(c.area[1])) + "," + itoa(int(c.area[2])) + "," + itoa(int(c.area[3]))
			comps, err := oracleDecodePGX(t, in, "-d", spec)
			if err != nil {
				t.Skipf("oracle -d %s failed: %v", spec, err)
			}
			area := c.area
			img, err := decodeGo(t, in, 0, 0, &area)
			if err != nil {
				t.Fatalf("Go -d %s failed: %v", spec, err)
			}
			if err := compareComps(t, img, comps); err != nil {
				t.Fatalf("-d %s not bit-exact: %v", spec, err)
			}
		})
	}
}

// decodeGoStrict decodes a raw codestream with the pure-Go j2k decoder in
// strict mode (SetStrictMode(true)), matching opj_decompress's default. It
// mirrors decodeGo (decode_test.go) except for the strict flag; it is used for
// error-parity on files that opj_decompress rejects only because of a
// strict-mode-gated check (see nonregStrictError).
func decodeGoStrict(t *testing.T, path string) (*image.Image, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := cio.NewMemoryInputStream(data)
	var mgr *event.Manager
	d := j2k.CreateDecompress()
	d.SetupDecoder(0, 0)
	d.SetStrictMode(true)
	img, err := d.ReadHeader(s, mgr)
	if err != nil {
		return nil, fmt.Errorf("ReadHeader: %w", err)
	}
	if err := d.SetDecodeArea(img, 0, 0, 0, 0); err != nil {
		return nil, fmt.Errorf("SetDecodeArea: %w", err)
	}
	if err := d.Decode(s, img, mgr); err != nil {
		return nil, fmt.Errorf("Decode: %w", err)
	}
	return img, nil
}

// applyCLIColorPost mirrors the color post-processing that the opj_decompress
// CLI applies between opj_decode() and writing PGX (opj_decompress.c lines
// ~1620-1635 + color.c). The pure-Go library, like libopenjp2's opj_decode,
// produces spec-native (possibly sub-sampled) components; the CLI heuristically
// treats a 3-component image whose chroma is sub-sampled as sYCC and runs
// color_sycc_to_rgb, upsampling chroma to full resolution. We reproduce that
// here so gate comparisons against the CLI's PGX are apples-to-apples.
func applyCLIColorPost(img *image.Image) {
	// opj_decompress.c: if color_space != SYCC && numcomps == 3 &&
	// comp[0].dx == comp[0].dy && comp[1].dx != 1 -> treat as SYCC.
	if img.Numcomps != 3 {
		return
	}
	c0, c1 := &img.Comps[0], &img.Comps[1]
	if !(c0.Dx == c0.Dy && c1.Dx != 1) {
		return
	}
	syccToRGB(img)
}

// syccToRGB ports color_sycc_to_rgb's dispatch (color.c). Only the 4:2:2
// (horizontal-only sub-sample) and 4:4:4 and 4:2:0 cases are defined by the
// reference; anything else is left untouched (the C code prints CAN NOT CONVERT
// and returns without modifying the image).
func syccToRGB(img *image.Image) {
	c := img.Comps
	dx0, dx1, dx2 := c[0].Dx, c[1].Dx, c[2].Dx
	dy0, dy1, dy2 := c[0].Dy, c[1].Dy, c[2].Dy
	switch {
	case dx0 == 1 && dx1 == 2 && dx2 == 2 && dy0 == 1 && dy1 == 1 && dy2 == 1:
		sycc422ToRGB(img) // 4:2:2, horizontal sub-sample only
	case dx0 == 1 && dx1 == 1 && dx2 == 1 && dy0 == 1 && dy1 == 1 && dy2 == 1:
		sycc444ToRGB(img) // 4:4:4, no sub-sample
		// NOTE: color.c also handles 4:2:0 (dy=(1,2,2)) via sycc420_to_rgb; no
		// such file exists in the corpus, so it is intentionally not ported
		// here. If one is added to nonregSyccPass the gate will fail loudly,
		// signalling that sycc420_to_rgb needs porting.
	}
}

func syccClamp(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// syccPix ports the scalar sycc_to_rgb (color.c). offset and upb derive from
// comp[0].prec. The multiply constants are C double literals, so we accumulate
// in float64 and truncate toward zero, exactly like the C `(int)(...)` casts.
func syccPix(offset, upb, y, cb, cr int32) (r, g, b int32) {
	cb -= offset
	cr -= offset
	r = syccClamp(y+int32(1.402*float64(cr)), 0, upb)
	g = syccClamp(y-int32(0.344*float64(cb)+0.714*float64(cr)), 0, upb)
	b = syccClamp(y+int32(1.772*float64(cb)), 0, upb)
	return
}

func syccParams(img *image.Image) (offset, upb int32) {
	prec := img.Comps[0].Prec
	upb = int32((uint32(1) << prec) - 1)
	offset = int32(uint32(1) << (prec - 1))
	return
}

// setChromaFull marks comps 1 and 2 as full-resolution (post-conversion),
// mirroring the tail of the sycc*_to_rgb functions.
func setChromaFull(img *image.Image) {
	c := img.Comps
	c[1].W, c[2].W = c[0].W, c[0].W
	c[1].H, c[2].H = c[0].H, c[0].H
	c[1].Dx, c[2].Dx = c[0].Dx, c[0].Dx
	c[1].Dy, c[2].Dy = c[0].Dy, c[0].Dy
}

// sycc444ToRGB ports color.c sycc444_to_rgb.
func sycc444ToRGB(img *image.Image) {
	offset, upb := syccParams(img)
	c := img.Comps
	maxw, maxh := int(c[0].W), int(c[0].H)
	max := maxw * maxh
	y, cb, cr := c[0].Data, c[1].Data, c[2].Data
	r := make([]int32, max)
	g := make([]int32, max)
	b := make([]int32, max)
	for i := 0; i < max; i++ {
		r[i], g[i], b[i] = syccPix(offset, upb, y[i], cb[i], cr[i])
	}
	c[0].Data, c[1].Data, c[2].Data = r, g, b
}

// sycc422ToRGB ports color.c sycc422_to_rgb (horizontal sub-sample only).
func sycc422ToRGB(img *image.Image) {
	offset, upb := syccParams(img)
	c := img.Comps
	maxw, maxh := int(c[0].W), int(c[0].H)
	comp12w := int(c[1].W)
	max := maxw * maxh
	y, cb, cr := c[0].Data, c[1].Data, c[2].Data
	r := make([]int32, max)
	g := make([]int32, max)
	b := make([]int32, max)
	offx := int(img.X0 & 1)
	loopmaxw := maxw - offx
	yi, oi := 0, 0
	cbRowStart, crRowStart := 0, 0
	for i := 0; i < maxh; i++ {
		cbi, cri := cbRowStart, crRowStart
		if offx > 0 {
			r[oi], g[oi], b[oi] = syccPix(offset, upb, y[yi], 0, 0)
			yi++
			oi++
		}
		j := 0
		for ; j < (loopmaxw &^ 1); j += 2 {
			r[oi], g[oi], b[oi] = syccPix(offset, upb, y[yi], cb[cbi], cr[cri])
			yi++
			oi++
			r[oi], g[oi], b[oi] = syccPix(offset, upb, y[yi], cb[cbi], cr[cri])
			yi++
			oi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				r[oi], g[oi], b[oi] = syccPix(offset, upb, y[yi], 0, 0)
			} else {
				r[oi], g[oi], b[oi] = syccPix(offset, upb, y[yi], cb[cbi], cr[cri])
			}
			yi++
			oi++
		}
		cbRowStart += comp12w
		crRowStart += comp12w
	}
	c[0].Data, c[1].Data, c[2].Data = r, g, b
	setChromaFull(img)
}
