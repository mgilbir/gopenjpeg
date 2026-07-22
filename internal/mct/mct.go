// Package mct implements the multi-component transforms of OpenJPEG:
// the reversible color transform (RCT), the irreversible color transform
// (ICT), and the arbitrary custom MCT matrix path. It is a faithful port of
// mct.c and invert.c from the OpenJPEG (JPEG 2000) reference implementation.
//
// Arithmetic is matched exactly to the C reference: the reversible path uses
// int32 integer arithmetic, the encode-real path uses int32 fixed-point via
// opj_int_fix_mul for the custom matrix and float32 for the RGB ICT, and the
// decode-real path uses float32 with the exact constant order-of-operations.
package mct

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/opjmath"
)

// opj_mct_norms — norms of the basis functions of the reversible MCT.
var mctNorms = [3]float64{1.732, .8292, .8292}

// opj_mct_norms_real — norms of the basis functions of the irreversible MCT.
var mctNormsReal = [3]float64{1.732, 1.805, 1.573}

// GetMctNorms is a port of opj_mct_get_mct_norms. It returns the norms of the
// basis functions of the reversible MCT.
func GetMctNorms() [3]float64 {
	return mctNorms
}

// GetMctNormsReal is a port of opj_mct_get_mct_norms_real. It returns the norms
// of the basis functions of the irreversible MCT.
func GetMctNormsReal() [3]float64 {
	return mctNormsReal
}

// Getnorm is a port of opj_mct_getnorm. It returns the norm of the reversible
// MCT basis function for the given component.
func Getnorm(compno uint32) float64 {
	return mctNorms[compno]
}

// GetnormReal is a port of opj_mct_getnorm_real. It returns the norm of the
// irreversible MCT basis function for the given component.
func GetnormReal(compno uint32) float64 {
	return mctNormsReal[compno]
}

// Encode is a port of opj_mct_encode (the scalar path). It applies the forward
// reversible multi-component (color) transform in place over the first n
// samples of the three component slices.
func Encode(c0, c1, c2 []int32, n int) {
	for i := 0; i < n; i++ {
		r := c0[i]
		g := c1[i]
		b := c2[i]
		y := (r + (g * 2) + b) >> 2
		u := b - g
		v := r - g
		c0[i] = y
		c1[i] = u
		c2[i] = v
	}
}

// Decode is a port of opj_mct_decode (the scalar path). It applies the inverse
// reversible multi-component (color) transform in place over the first n
// samples of the three component slices.
func Decode(c0, c1, c2 []int32, n int) {
	for i := 0; i < n; i++ {
		y := c0[i]
		u := c1[i]
		v := c2[i]
		g := y - ((u + v) >> 2)
		r := v + g
		b := u + g
		c0[i] = r
		c1[i] = g
		c2[i] = b
	}
}

// EncodeReal is a port of opj_mct_encode_real (the scalar path). It applies the
// forward irreversible (float) color transform in place over the first n
// samples. All intermediates are float32.
//
// Each channel is computed as first-term + (second-term + third-term): the two
// trailing products are summed before the leading one is added. The C source of
// opj_mct_encode_real (both the __SSE__ intrinsic block and the scalar tail)
// writes each channel as ((a*r + b*g) + c*b), i.e. left-associated. However, the
// stock OpenJPEG shared library that opj_compress links against (libopenjp2.so)
// is compiled with gcc's reassociating float optimizations (-ffast-math class),
// and gcc's -freassoc pass regroups each three-term sum as a + (b + c). Verified
// against the shipped binary: replaying its opj_mct_encode_real over random
// 12-bit RGB, the left-associated source order differs by 1-2 ULP on ~1/3 of
// samples, while a + (b + c) is bit-identical on all three channels (0/4096).
// This is the encode-side mirror of the DecodeReal green-term reassociation
// documented below (W12), and is required for irreversible/cinema encodes to be
// byte-identical with opj_compress (the divergence surfaces on 12-bit inputs; it
// was invisible on the 8-bit encode-gate cells).
func EncodeReal(c0, c1, c2 []float32, n int) {
	for i := 0; i < n; i++ {
		r := c0[i]
		g := c1[i]
		b := c2[i]
		y := 0.299*r + (0.587*g + 0.114*b)
		u := -0.16875*r + (-0.331260*g + 0.5*b)
		v := 0.5*r + (-0.41869*g - 0.08131*b)
		c0[i] = y
		c1[i] = u
		c2[i] = v
	}
}

// DecodeReal is a port of opj_mct_decode_real. It applies the inverse
// irreversible (float) color transform in place over the first n samples. All
// intermediates are float32.
//
// Green is computed as y - (u*0.34413 + v*0.71414), i.e. the two chroma
// products are summed before the single subtraction. The C source of
// opj_mct_decode_real (both the __SSE__ intrinsic and scalar paths) writes it
// as (y - u*0.34413) - v*0.71414 — two separate subtractions. However, the
// STOCK OpenJPEG shared library shipped on x86-64 (libopenjp2.so, which
// opj_decompress links against — our parity target) is compiled with gcc's
// reassociating float optimizations, and gcc folds the two subtractions into
// add-then-subtract in the SSE code it emits (verified in the .so disassembly:
// `addps` of the two products, then one `subps`). The static libopenjp2.a and
// the literal source order both use the two-subtraction form and differ from
// the shipped binary by <=1 LSB on a handful of green samples (4/2.07M on
// _00042.j2k, 73/3.19M on issue135.j2k) — exactly the samples where the two
// associations round to different integers. We reproduce the shipped binary's
// arithmetic here so whole-image decodes are bit-exact with opj_decompress.
// (r and b are single multiply-adds with no reassociation freedom, so they are
// identical under either build and need no adjustment.)
func DecodeReal(c0, c1, c2 []float32, n int) {
	for i := 0; i < n; i++ {
		y := c0[i]
		u := c1[i]
		v := c2[i]
		r := y + (v * 1.402)
		g := y - (u*0.34413 + v*0.71414)
		b := y + (u * 1.772)
		c0[i] = r
		c1[i] = g
		c2[i] = b
	}
}

// EncodeCustom is a port of opj_mct_encode_custom. It applies an arbitrary
// forward NxN component transform to integer sample data.
//
// matrix holds nbComp*nbComp float32 coefficients in row-major order. Each
// coefficient is converted to a 1<<13 fixed-point int32 (C truncation toward
// zero) before the per-sample matrix multiply with opj_int_fix_mul. data holds
// nbComp component slices, each with at least n samples; it is updated in place.
func EncodeCustom(matrix []float32, n int, data [][]int32, nbComp uint32) {
	const multiplicator = 1 << 13
	nbMatCoeff := nbComp * nbComp

	currentMatrix := make([]int32, nbMatCoeff)
	for i := uint32(0); i < nbMatCoeff; i++ {
		currentMatrix[i] = int32(matrix[i] * float32(multiplicator))
	}

	currentData := make([]int32, nbComp)
	for i := 0; i < n; i++ {
		mctPtr := 0
		for j := uint32(0); j < nbComp; j++ {
			currentData[j] = data[j][i]
		}
		for j := uint32(0); j < nbComp; j++ {
			var acc int32
			for k := uint32(0); k < nbComp; k++ {
				acc += opjmath.IntFixMul(currentMatrix[mctPtr], currentData[k])
				mctPtr++
			}
			data[j][i] = acc
		}
	}
}

// DecodeCustom is a port of opj_mct_decode_custom. It applies an arbitrary
// inverse NxN component transform to float sample data.
//
// matrix holds nbComp*nbComp float32 coefficients in row-major order, applied
// directly (no fixed-point conversion). data holds nbComp component slices,
// each with at least n samples; it is updated in place. All arithmetic is
// float32, matching the C order of operations.
func DecodeCustom(matrix []float32, n int, data [][]float32, nbComp uint32) {
	currentData := make([]float32, nbComp)
	currentResult := make([]float32, nbComp)
	for i := 0; i < n; i++ {
		mctPtr := 0
		for j := uint32(0); j < nbComp; j++ {
			currentData[j] = data[j][i]
		}
		for j := uint32(0); j < nbComp; j++ {
			currentResult[j] = 0
			for k := uint32(0); k < nbComp; k++ {
				currentResult[j] += matrix[mctPtr] * currentData[k]
				mctPtr++
			}
			data[j][i] = currentResult[j]
		}
	}
}

// CalculateNorms is a port of opj_calculate_norms. It computes, for each of the
// nbComps columns of the row-major matrix, the Euclidean norm of that column
// and stores it into norms. The accumulation uses float64 with the values read
// as float32, matching the C reference.
func CalculateNorms(norms []float64, nbComps uint32, matrix []float32) {
	for i := uint32(0); i < nbComps; i++ {
		norms[i] = 0
		index := i
		for j := uint32(0); j < nbComps; j++ {
			currentValue := matrix[index]
			index += nbComps
			norms[i] += float64(currentValue) * float64(currentValue)
		}
		norms[i] = math.Sqrt(norms[i])
	}
}
