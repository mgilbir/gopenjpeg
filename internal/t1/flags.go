// Package t1 is a pure-Go, bit-exact port of the OpenJPEG EBCOT tier-1 coder
// (t1.c, t1.h, t1_luts.h): the code-block entropy coder that sits on top of the
// MQ arithmetic coder (internal/mqc). It implements the JPEG 2000 significance,
// magnitude-refinement and clean-up coding passes for both encoding and
// decoding, including all code-block style switches (LAZY/bypass, RESET, VSC,
// SEGSYM, TERMALL, PTERM) and the post-decode ROI de-scaling / dequantization.
//
// The port mirrors the C control flow closely; every function carries the name
// of the C construct it corresponds to in its doc comment. Integer widths match
// the C source (OPJ_INT32 -> int32, OPJ_UINT32 -> uint32, opj_flag_t ->
// uint32). This is the hottest code in the codec: the inner passes are kept
// allocation-free and call the concrete *mqc.MQC methods directly (no interface
// indirection).
//
// HTJ2K (High Throughput, ht_dec.c / opj_t1_ht_decode_cblk) is intentionally
// out of scope; see the seam in DecodeCblk.
package t1

// Flag-word bit layout (t1.h). One 32-bit opj_flag_t word holds the state of a
// 4-row-high column of the code-block: 3 columns of SIGMA (significance) for 6
// rows, plus CHI (sign), MU (refinement-visited) and PI (sigpass-visited).
const (
	t1Sigma0  = 1 << 0
	t1Sigma1  = 1 << 1
	t1Sigma2  = 1 << 2
	t1Sigma3  = 1 << 3
	t1Sigma4  = 1 << 4
	t1Sigma5  = 1 << 5
	t1Sigma6  = 1 << 6
	t1Sigma7  = 1 << 7
	t1Sigma8  = 1 << 8
	t1Sigma9  = 1 << 9
	t1Sigma10 = 1 << 10
	t1Sigma11 = 1 << 11
	t1Sigma12 = 1 << 12
	t1Sigma13 = 1 << 13
	t1Sigma14 = 1 << 14
	t1Sigma15 = 1 << 15
	t1Sigma16 = 1 << 16
	t1Sigma17 = 1 << 17

	t1Chi0  = 1 << 18
	t1Chi0I = 18
	t1Chi1  = 1 << 19
	t1Chi1I = 19
	t1Mu0   = 1 << 20
	t1Pi0   = 1 << 21
	t1Chi2  = 1 << 22
	t1Chi2I = 22
	t1Mu1   = 1 << 23
	t1Pi1   = 1 << 24
	t1Chi3  = 1 << 25
	t1Mu2   = 1 << 26
	t1Pi2   = 1 << 27
	t1Chi4  = 1 << 28
	t1Mu3   = 1 << 29
	t1Pi3   = 1 << 30
	t1Chi5  = 1 << 31
	t1Chi5I = 31
)

// Semantic aliases for data-point 0 within a flag word (t1.h).
const (
	t1SigmaThis = t1Sigma4
	t1ChiThis   = t1Chi1
	t1ChiThisI  = t1Chi1I
	t1MuThis    = t1Mu0
	t1PiThis    = t1Pi0

	// t1SigmaNeighbours is the union of the 8 neighbour significance bits
	// (everything except THIS).
	t1SigmaNeighbours = t1Sigma0 | t1Sigma1 | t1Sigma2 | t1Sigma3 |
		t1Sigma5 | t1Sigma6 | t1Sigma7 | t1Sigma8
)

// LUT index bits used by getctxtnoScOrSpbIndex (t1.h).
const (
	t1LutSgnW = 1 << 0
	t1LutSigN = 1 << 1
	t1LutSgnE = 1 << 2
	t1LutSigW = 1 << 3
	t1LutSgnN = 1 << 4
	t1LutSigE = 1 << 5
	t1LutSgnS = 1 << 6
	t1LutSigS = 1 << 7
)

// nmsedec fixed-point constants (t1.h).
const (
	t1NmsedecBits     = 7
	t1NmsedecFracbits = t1NmsedecBits - 1
)

// Context numbers (t1.h). These index the 19 MQ contexts.
const (
	t1NumctxsZC  = 9
	t1NumctxsSC  = 5
	t1NumctxsMag = 3

	t1CtxnoZC  = 0
	t1CtxnoSC  = t1CtxnoZC + t1NumctxsZC   // 9
	t1CtxnoMag = t1CtxnoSC + t1NumctxsSC   // 14
	t1CtxnoAgg = t1CtxnoMag + t1NumctxsMag // 17
	t1CtxnoUni = t1CtxnoAgg + 1            // 18
	t1Numctxs  = t1CtxnoUni + 1            // 19
)

// Coding-pass entropy-coder type (t1.h).
const (
	t1TypeMQ  = 0 // normal MQ arithmetic coding
	t1TypeRAW = 1 // raw / bypass coding (LAZY mode)
)

// Code-block style switches (J2K_CCP_CBLKSTY_* in j2k.h).
const (
	CblkstyLazy    = 0x01 // selective arithmetic coding bypass
	CblkstyReset   = 0x02 // reset context probabilities on pass boundaries
	CblkstyTermall = 0x04 // termination on each coding pass
	CblkstyVSC     = 0x08 // vertically stripe-causal context
	CblkstyPterm   = 0x10 // predictable termination
	CblkstySegsym  = 0x20 // segmentation symbols
	CblkstyHT      = 0x40 // high-throughput (HTJ2K) code-blocks
)

// getctxnoZC ports opj_t1_getctxno_zc: significance context for a flag word,
// using the orient-selected slice of lutCtxnoZC installed by DecodeCblk /
// EncodeCblk. f is the (already ci-shifted) flag word.
func (t *T1) getctxnoZC(f uint32) uint32 {
	return uint32(t.lutCtxnoZCOrient[f&t1SigmaNeighbours])
}

// getctxtnoScOrSpbIndex ports opj_t1_getctxtno_sc_or_spb_index: build the 8-bit
// index into lutCtxnoSC / lutSPB from the sign/significance neighbourhood.
func getctxtnoScOrSpbIndex(fX, pfX, nfX, ci uint32) uint32 {
	lu := (fX >> (ci * 3)) & (t1Sigma1 | t1Sigma3 | t1Sigma5 | t1Sigma7)

	lu |= (pfX >> (t1ChiThisI + (ci * 3))) & (1 << 0)
	lu |= (nfX >> (t1ChiThisI - 2 + (ci * 3))) & (1 << 2)
	if ci == 0 {
		lu |= (fX >> (t1Chi0I - 4)) & (1 << 4)
	} else {
		lu |= (fX >> (t1Chi1I - 4 + ((ci - 1) * 3))) & (1 << 4)
	}
	lu |= (fX >> (t1Chi2I - 6 + (ci * 3))) & (1 << 6)
	return lu
}

// getctxnoSC ports opj_t1_getctxno_sc.
func getctxnoSC(lu uint32) uint32 { return uint32(lutCtxnoSC[lu]) }

// getspb ports opj_t1_getspb.
func getspb(lu uint32) uint32 { return uint32(lutSPB[lu]) }

// getctxnoMag ports opj_t1_getctxno_mag. f is the (already ci-shifted) flag word.
func getctxnoMag(f uint32) uint32 {
	tmp := uint32(t1CtxnoMag)
	if f&t1SigmaNeighbours != 0 {
		tmp = t1CtxnoMag + 1
	}
	if f&t1Mu0 != 0 {
		return t1CtxnoMag + 2
	}
	return tmp
}

// getnmsedecSig ports opj_t1_getnmsedec_sig.
func getnmsedecSig(x, bitpos uint32) int32 {
	if bitpos > 0 {
		return int32(lutNmsedecSig[(x>>bitpos)&((1<<t1NmsedecBits)-1)])
	}
	return int32(lutNmsedecSig0[x&((1<<t1NmsedecBits)-1)])
}

// getnmsedecRef ports opj_t1_getnmsedec_ref.
func getnmsedecRef(x, bitpos uint32) int32 {
	if bitpos > 0 {
		return int32(lutNmsedecRef[(x>>bitpos)&((1<<t1NmsedecBits)-1)])
	}
	return int32(lutNmsedecRef0[x&((1<<t1NmsedecBits)-1)])
}

// Sign-magnitude representation helpers (t1.c: opj_smr_abs/sign/to_smr).
func smrAbs(x int32) uint32  { return uint32(x) & 0x7FFFFFFF }
func smrSign(x int32) uint32 { return uint32(x) >> 31 }
func toSmr(x int32) uint32 {
	if x >= 0 {
		return uint32(x)
	}
	return uint32(-x) | 0x80000000
}

// updateFlags ports opj_t1_update_flags / opj_t1_update_flags_macro writing the
// centre word directly to the flags array (used by the raw and remainder
// paths). fp is the index of the centre word; s is the sign of the coefficient.
func (t *T1) updateFlags(fp int, ci, s, stride, vsc uint32) {
	t.flags[fp-1] |= t1Sigma5 << (3 * ci)                  // east
	t.flags[fp] |= ((s << t1Chi1I) | t1Sigma4) << (3 * ci) // this
	t.flags[fp+1] |= t1Sigma3 << (3 * ci)                  // west
	if ci == 0 && vsc == 0 {
		north := fp - int(stride)
		t.flags[north] |= (s << t1Chi5I) | t1Sigma16
		t.flags[north-1] |= t1Sigma17
		t.flags[north+1] |= t1Sigma15
	}
	if ci == 3 {
		south := fp + int(stride)
		t.flags[south] |= (s << t1Chi0I) | t1Sigma1
		t.flags[south-1] |= t1Sigma2
		t.flags[south+1] |= t1Sigma0
	}
}

// updateFlagsCenter is like updateFlags but accumulates the centre-word update
// into a caller-held local (register) copy, matching the C macro's use of a
// local flags variable inside the optimized MQC passes. Neighbour words are
// still updated directly in the array.
func (t *T1) updateFlagsCenter(center *uint32, fp int, ci, s, stride, vsc uint32) {
	t.flags[fp-1] |= t1Sigma5 << (3 * ci)              // east
	*center |= ((s << t1Chi1I) | t1Sigma4) << (3 * ci) // this
	t.flags[fp+1] |= t1Sigma3 << (3 * ci)              // west
	if ci == 0 && vsc == 0 {
		north := fp - int(stride)
		t.flags[north] |= (s << t1Chi5I) | t1Sigma16
		t.flags[north-1] |= t1Sigma17
		t.flags[north+1] |= t1Sigma15
	}
	if ci == 3 {
		south := fp + int(stride)
		t.flags[south] |= (s << t1Chi0I) | t1Sigma1
		t.flags[south-1] |= t1Sigma2
		t.flags[south+1] |= t1Sigma0
	}
}
