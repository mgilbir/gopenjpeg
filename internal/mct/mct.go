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

import "math"

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
// samples. All intermediates are float32, matching the C order of operations.
func EncodeReal(c0, c1, c2 []float32, n int) {
	for i := 0; i < n; i++ {
		r := c0[i]
		g := c1[i]
		b := c2[i]
		y := 0.299*r + 0.587*g + 0.114*b
		u := -0.16875*r - 0.331260*g + 0.5*b
		v := 0.5*r - 0.41869*g - 0.08131*b
		c0[i] = y
		c1[i] = u
		c2[i] = v
	}
}

// DecodeReal is a port of opj_mct_decode_real (the scalar path). It applies the
// inverse irreversible (float) color transform in place over the first n
// samples. All intermediates are float32, matching the C order of operations.
func DecodeReal(c0, c1, c2 []float32, n int) {
	for i := 0; i < n; i++ {
		y := c0[i]
		u := c1[i]
		v := c2[i]
		r := y + (v * 1.402)
		g := y - (u * 0.34413) - (v * 0.71414)
		b := y + (u * 1.772)
		c0[i] = r
		c1[i] = g
		c2[i] = b
	}
}

// intFixMul is a port of opj_int_fix_mul from opj_intmath.h. It multiplies two
// fixed-point rationals with 13 fractional bits and rounding.
//
// TODO: switch to internal/opjmath once that package lands.
func intFixMul(a, b int32) int32 {
	temp := int64(a) * int64(b)
	temp += 4096
	return int32(temp >> 13)
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
				acc += intFixMul(currentMatrix[mctPtr], currentData[k])
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
