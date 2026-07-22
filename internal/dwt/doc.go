// Package dwt is a faithful pure-Go port of OpenJPEG's dwt.c: the discrete
// wavelet transform module (both the reversible 5/3 integer transform and the
// irreversible 9/7 floating-point transform), forward and inverse, in full-tile
// and region/partial-decode variants.
//
// # Fidelity
//
// The port mirrors the C control flow and arithmetic exactly:
//
//   - C integer widths are preserved (OPJ_INT32 -> int32, OPJ_UINT32 -> uint32).
//     Go's two's-complement wraparound for signed integers matches the C code's
//     opj_int_add_no_overflow / opj_int_sub_no_overflow helpers, and arithmetic
//     right shifts match.
//   - The 9/7 transform uses float32 throughout, with the exact constants and
//     order of operations of the C source. No float64 intermediates are used
//     where C uses float, and no expressions are re-associated. This is a
//     correctness requirement: results are compared bit-for-bit against the C
//     oracle.
//   - Only the scalar code paths are ported. The C file additionally contains
//     SSE/AVX/NEON intrinsic paths and an opj_thread pool; these are omitted.
//     They are pure performance variants that produce identical results, so the
//     scalar port is bit-exact with the C library. The per-row / per-column
//     kernels are kept as separate package-level functions so a caller can
//     later parallelize them per row/column.
//
// # Geometry input types
//
// dwt.c operates on opj_tcd_tilecomp_t / opj_tcd_resolution_t from tcd.c, which
// is not yet ported. This package defines minimal local geometry types
// (TileComponent, Resolution, Band, Precinct, CblkDec) mirroring only the fields
// dwt actually reads, so the future tcd port can adapt trivially. Coordinate
// fields keep their C signedness (int32 for x0/y0/x1/y1, uint32 for window
// coordinates). TileComponent.Data and DataWin are []int32 exactly as
// tilec->data is OPJ_INT32*; the 9/7 paths reinterpret those words as float32
// bit patterns, just as the C code casts the buffer to OPJ_FLOAT32*.
package dwt

import "math"

// nbEltsV8 mirrors NB_ELTS_V8: the number of rows/columns the 9/7 (and the
// forward 5/3 vertical) pass processes as a group. It is part of the algorithm
// structure, not a SIMD width, so it is retained in the scalar port.
const nbEltsV8 = 8

// From table F.4 of the standard; float32 exactly as in dwt.c.
const (
	dwtAlpha float32 = -1.586134342
	dwtBeta  float32 = -0.052980118
	dwtGamma float32 = 0.882911075
	dwtDelta float32 = 0.443506852
	dwtK     float32 = 1.230174105
)

// dwtInvK is (OPJ_FLOAT32)(1.0 / 1.230174105): the division is performed in
// double precision then narrowed to float32, exactly as the C source does.
var dwtInvK = float32(1.0 / 1.230174105)

// CblkDec mirrors the fields of opj_tcd_cblk_dec_t read by dwt.c.
type CblkDec struct {
	X0, Y0, X1, Y1 int32
	// DecodedData is the code-block's decoded coefficients, or nil if the
	// block was not decoded. For 9/7 these int32 words are float32 bit
	// patterns. Length must be (X1-X0)*(Y1-Y0) when non-nil.
	DecodedData []int32
}

// Precinct mirrors the fields of opj_tcd_precinct_t read by dwt.c.
type Precinct struct {
	Cw, Ch uint32
	Cblks  []CblkDec
}

// Band mirrors the fields of opj_tcd_band_t read by dwt.c.
type Band struct {
	X0, Y0, X1, Y1 int32
	Bandno         uint32
	Precincts      []Precinct
}

// Resolution mirrors the fields of opj_tcd_resolution_t read by dwt.c.
type Resolution struct {
	X0, Y0, X1, Y1 int32
	Pw, Ph         uint32
	Numbands       uint32
	Bands          [3]Band

	// Window of interest, in tile-resolution coordinates (partial decode only).
	WinX0, WinY0, WinX1, WinY1 uint32
}

// TileComponent mirrors the fields of opj_tcd_tilecomp_t read by dwt.c.
type TileComponent struct {
	X0, Y0, X1, Y1 int32

	Numresolutions        uint32
	MinimumNumResolutions uint32
	Resolutions           []Resolution

	// Data is the full tile-component coefficient buffer (whole-tile decode /
	// encode). For 9/7 the words are float32 bit patterns.
	Data []int32

	// DataWin is the window-of-interest output buffer (partial decode only).
	DataWin []int32

	// Window of interest, in tile-component coordinates (partial decode only).
	WinX0, WinY0, WinX1, WinY1 uint32
}

// ---- integer helpers (ports of opj_intmath.h) ----

func intMin(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func uintMin(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func uintMax(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// uintAdds is a port of opj_uint_adds (saturating add).
func uintAdds(a, b uint32) uint32 {
	sum := uint64(a) + uint64(b)
	return uint32(-int32(sum>>32)) | uint32(sum)
}

// uintSubs is a port of opj_uint_subs (saturating subtract).
func uintSubs(a, b uint32) uint32 {
	if a >= b {
		return a - b
	}
	return 0
}

// uintCeildivpow2 is a port of opj_uint_ceildivpow2.
func uintCeildivpow2(a, b uint32) uint32 {
	return uint32((uint64(a) + (uint64(1) << b) - 1) >> b)
}

// intFloorlog2 is a port of opj_int_floorlog2.
func intFloorlog2(a int32) int32 {
	var l int32
	for ; a > 1; l++ {
		a >>= 1
	}
	return l
}

// f32bits / f32frombits reinterpret an int32 tile word as a float32 and back,
// mirroring the C code's pointer casts between OPJ_INT32* and OPJ_FLOAT32*.
func f32frombits(v int32) float32 { return math.Float32frombits(uint32(v)) }
func f32bits(f float32) int32     { return int32(math.Float32bits(f)) }
