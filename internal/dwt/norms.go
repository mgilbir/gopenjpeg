package dwt

import "math"

// This file ports the gain/norm helpers and explicit stepsize computation of
// dwt.c.

// dwtNorms holds the norms of the 5/3 wavelets for different bands
// (opj_dwt_norms).
var dwtNorms = [4][10]float64{
	{1.000, 1.500, 2.750, 5.375, 10.68, 21.34, 42.67, 85.33, 170.7, 341.3},
	{1.038, 1.592, 2.919, 5.703, 11.33, 22.64, 45.25, 90.48, 180.9},
	{1.038, 1.592, 2.919, 5.703, 11.33, 22.64, 45.25, 90.48, 180.9},
	{.7186, .9218, 1.586, 3.043, 6.019, 12.01, 24.00, 47.97, 95.93},
}

// dwtNormsReal holds the norms of the 9/7 wavelets for different bands
// (opj_dwt_norms_real).
var dwtNormsReal = [4][10]float64{
	{1.000, 1.965, 4.177, 8.403, 16.90, 33.84, 67.69, 135.3, 270.6, 540.9},
	{2.022, 3.989, 8.355, 17.04, 34.27, 68.63, 137.3, 274.6, 549.0},
	{2.022, 3.989, 8.355, 17.04, 34.27, 68.63, 137.3, 274.6, 549.0},
	{2.080, 3.865, 8.307, 17.18, 34.71, 69.59, 139.3, 278.6, 557.2},
}

// Getnorm is a port of opj_dwt_getnorm (norm of the 5/3 wavelet).
func Getnorm(level, orient uint32) float64 {
	// Band-aid clamp against buffer overflow, matching the C source.
	if orient == 0 && level >= 10 {
		level = 9
	} else if orient > 0 && level >= 9 {
		level = 8
	}
	return dwtNorms[orient][level]
}

// GetnormReal is a port of opj_dwt_getnorm_real (norm of the 9/7 wavelet).
func GetnormReal(level, orient uint32) float64 {
	if orient == 0 && level >= 10 {
		level = 9
	} else if orient > 0 && level >= 9 {
		level = 8
	}
	return dwtNormsReal[orient][level]
}

// Getgain reconstructs the (reversible 5/3) sub-band analysis gain. The current
// OpenJPEG dwt.c no longer exposes opj_dwt_getgain as a standalone function;
// the gain is computed inline in opj_dwt_calc_explicit_stepsizes. This helper
// reproduces that logic (0 for LL, 1 for HL/LH, 2 for HH) for callers that need
// it. See CalcExplicitStepsizes for the in-context computation.
func Getgain(orient uint32) uint32 {
	if orient == 0 {
		return 0
	}
	if orient == 1 || orient == 2 {
		return 1
	}
	return 2
}

// GetgainReal reconstructs the irreversible 9/7 sub-band analysis gain, which is
// always 0 (the 9/7 filter is normalized). Not present as a standalone function
// in the current dwt.c; see Getgain for context.
func GetgainReal(orient uint32) uint32 {
	_ = orient
	return 0
}

// Stepsize is a port of opj_stepsize_t.
type Stepsize struct {
	Expn int32 // exponent
	Mant int32 // mantissa
}

// Tccp mirrors the fields of opj_tccp_t read by opj_dwt_calc_explicit_stepsizes.
type Tccp struct {
	Numresolutions uint32
	Qmfbid         uint32 // 1 = reversible 5/3, 0 = irreversible 9/7
	Qntsty         uint32 // J2K_CCP_QNTSTY_* (0 = NOQNT)
	Stepsizes      []Stepsize
}

// qntstyNoQnt is a port of J2K_CCP_QNTSTY_NOQNT.
const qntstyNoQnt = 0

// encodeStepsize is a port of opj_dwt_encode_stepsize.
func encodeStepsize(stepsize, numbps int32, bandnoStepsize *Stepsize) {
	p := intFloorlog2(stepsize) - 13
	n := 11 - intFloorlog2(stepsize)
	if n < 0 {
		bandnoStepsize.Mant = (stepsize >> -n) & 0x7ff
	} else {
		bandnoStepsize.Mant = (stepsize << n) & 0x7ff
	}
	bandnoStepsize.Expn = numbps - p
}

// CalcExplicitStepsizes is a port of opj_dwt_calc_explicit_stepsizes.
func CalcExplicitStepsizes(tccp *Tccp, prec uint32) {
	numbands := 3*tccp.Numresolutions - 2
	for bandno := uint32(0); bandno < numbands; bandno++ {
		var stepsize float64
		var resno, level, orient, gain uint32

		if bandno == 0 {
			resno = 0
		} else {
			resno = (bandno-1)/3 + 1
		}
		if bandno == 0 {
			orient = 0
		} else {
			orient = (bandno-1)%3 + 1
		}
		level = tccp.Numresolutions - 1 - resno
		// C: gain = (qmfbid == 0) ? 0 : orient-based.
		if tccp.Qmfbid == 0 {
			gain = 0
		} else if orient == 0 {
			gain = 0
		} else if orient == 1 || orient == 2 {
			gain = 1
		} else {
			gain = 2
		}
		if tccp.Qntsty == qntstyNoQnt {
			stepsize = 1.0
		} else {
			norm := GetnormReal(level, orient)
			stepsize = float64(int64(1)<<gain) / norm
		}
		encodeStepsize(int32(math.Floor(stepsize*8192.0)), int32(prec+gain),
			&tccp.Stepsizes[bandno])
	}
}
