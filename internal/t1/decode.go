package t1

import (
	"fmt"
	"math"
)

// This file ports the tier-1 decoder: the significance, refinement and clean-up
// decoding passes (RAW and MQC variants), the per-code-block segment loop
// (opj_t1_decode_cblk), and the post-decode ROI de-scaling / dequantization
// that lives in opj_t1_clbl_decode_processor.
//
// Only the "generic" (arbitrary width/height) pass variants are ported. The C
// reference additionally ships hand-specialized 64x64 copies
// (opj_t1_dec_sigpass_mqc_64x64_*, _refpass_mqc_64x64, _clnpass_64x64_*) that
// are a pure speed optimization: they compute bit-identical results, so this
// port always takes the generic path. See the coordinator notes.

// ---------------------------------------------------------------------------
// Significance pass (decode)
// ---------------------------------------------------------------------------

// decSigpassStepRaw ports opj_t1_dec_sigpass_step_raw.
func (t *T1) decSigpassStepRaw(fp, dp int, oneplushalf int32, vsc, ci uint32) {
	flags := t.flags[fp]
	if flags&((t1SigmaThis|t1PiThis)<<(ci*3)) == 0 &&
		flags&(t1SigmaNeighbours<<(ci*3)) != 0 {
		if t.mqc.RawDecode() != 0 {
			v := t.mqc.RawDecode()
			if v != 0 {
				t.data[dp] = -oneplushalf
			} else {
				t.data[dp] = oneplushalf
			}
			t.updateFlags(fp, ci, v, t.w+2, vsc)
		}
		t.flags[fp] |= t1PiThis << (ci * 3)
	}
}

// decSigpassRaw ports opj_t1_dec_sigpass_raw.
func (t *T1) decSigpassRaw(bpno int32, cblksty uint32) {
	one := int32(1) << uint(bpno)
	half := one >> 1
	oneplushalf := one | half
	lw := int(t.w)
	stride := t.w + 2
	vsc := uint32(0)
	if cblksty&CblkstyVSC != 0 {
		vsc = 1
	}

	data := 0
	flagsp := int(stride) + 1
	var i, j, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			if t.flags[flagsp] != 0 {
				t.decSigpassStepRaw(flagsp, data, oneplushalf, vsc, 0)
				t.decSigpassStepRaw(flagsp, data+lw, oneplushalf, 0, 1)
				t.decSigpassStepRaw(flagsp, data+2*lw, oneplushalf, 0, 2)
				t.decSigpassStepRaw(flagsp, data+3*lw, oneplushalf, 0, 3)
			}
			flagsp++
			data++
		}
		flagsp += 2
		data += 3 * lw
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			for j = 0; j < t.h-k; j++ {
				t.decSigpassStepRaw(flagsp, data+int(j)*lw, oneplushalf, vsc, j)
			}
			flagsp++
			data++
		}
	}
}

// decSigpassStepMQC ports opj_t1_dec_sigpass_step_mqc_macro. center points to
// the caller's register-held copy of the centre flag word.
func (t *T1) decSigpassStepMQC(center *uint32, fp int, flagsStride uint32, dp, dataStride int, ci uint32, oneplushalf int32, vsc uint32) {
	f := *center
	if f&((t1SigmaThis|t1PiThis)<<(ci*3)) == 0 &&
		f&(t1SigmaNeighbours<<(ci*3)) != 0 {
		t.mqc.SetCurCtx(t.getctxnoZC(f >> (ci * 3)))
		v := t.mqc.Decode()
		if v != 0 {
			lu := getctxtnoScOrSpbIndex(f, t.flags[fp-1], t.flags[fp+1], ci)
			t.mqc.SetCurCtx(getctxnoSC(lu))
			v = t.mqc.Decode() ^ getspb(lu)
			if v != 0 {
				t.data[dp+int(ci)*dataStride] = -oneplushalf
			} else {
				t.data[dp+int(ci)*dataStride] = oneplushalf
			}
			t.updateFlagsCenter(center, fp, ci, v, flagsStride, vsc)
		}
		*center |= t1PiThis << (ci * 3)
	}
}

// decSigpassMQC ports opj_t1_dec_sigpass_mqc (generic path).
func (t *T1) decSigpassMQC(bpno int32, cblksty uint32) {
	one := int32(1) << uint(bpno)
	half := one >> 1
	oneplushalf := one | half
	lw := int(t.w)
	stride := t.w + 2
	vsc := uint32(0)
	if cblksty&CblkstyVSC != 0 {
		vsc = 1
	}

	data := 0
	flagsp := int(stride) + 1
	var i, j, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			flags := t.flags[flagsp]
			if flags != 0 {
				t.decSigpassStepMQC(&flags, flagsp, stride, data, lw, 0, oneplushalf, vsc)
				t.decSigpassStepMQC(&flags, flagsp, stride, data, lw, 1, oneplushalf, 0)
				t.decSigpassStepMQC(&flags, flagsp, stride, data, lw, 2, oneplushalf, 0)
				t.decSigpassStepMQC(&flags, flagsp, stride, data, lw, 3, oneplushalf, 0)
				t.flags[flagsp] = flags
			}
			flagsp++
			data++
		}
		flagsp += 2
		data += 3 * lw
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			for j = 0; j < t.h-k; j++ {
				t.decSigpassStepMQC(&t.flags[flagsp], flagsp, stride, data+int(j)*lw, 0, j, oneplushalf, vsc)
			}
			flagsp++
			data++
		}
	}
}

// ---------------------------------------------------------------------------
// Refinement pass (decode)
// ---------------------------------------------------------------------------

// decRefpassStepRaw ports opj_t1_dec_refpass_step_raw.
func (t *T1) decRefpassStepRaw(fp, dp int, poshalf int32, ci uint32) {
	if t.flags[fp]&((t1SigmaThis|t1PiThis)<<(ci*3)) == (t1SigmaThis << (ci * 3)) {
		v := t.mqc.RawDecode()
		neg := uint32(0)
		if t.data[dp] < 0 {
			neg = 1
		}
		if v^neg != 0 {
			t.data[dp] += poshalf
		} else {
			t.data[dp] -= poshalf
		}
		t.flags[fp] |= t1MuThis << (ci * 3)
	}
}

// decRefpassRaw ports opj_t1_dec_refpass_raw.
func (t *T1) decRefpassRaw(bpno int32) {
	one := int32(1) << uint(bpno)
	poshalf := one >> 1
	lw := int(t.w)
	stride := t.w + 2

	data := 0
	flagsp := int(stride) + 1
	var i, j, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			if t.flags[flagsp] != 0 {
				t.decRefpassStepRaw(flagsp, data, poshalf, 0)
				t.decRefpassStepRaw(flagsp, data+lw, poshalf, 1)
				t.decRefpassStepRaw(flagsp, data+2*lw, poshalf, 2)
				t.decRefpassStepRaw(flagsp, data+3*lw, poshalf, 3)
			}
			flagsp++
			data++
		}
		flagsp += 2
		data += 3 * lw
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			for j = 0; j < t.h-k; j++ {
				t.decRefpassStepRaw(flagsp, data+int(j)*lw, poshalf, j)
			}
			flagsp++
			data++
		}
	}
}

// decRefpassStepMQC ports opj_t1_dec_refpass_step_mqc_macro.
func (t *T1) decRefpassStepMQC(center *uint32, dp, dataStride int, ci uint32, poshalf int32) {
	f := *center
	if f&((t1SigmaThis|t1PiThis)<<(ci*3)) == (t1SigmaThis << (ci * 3)) {
		t.mqc.SetCurCtx(getctxnoMag(f >> (ci * 3)))
		v := t.mqc.Decode()
		idx := dp + int(ci)*dataStride
		neg := uint32(0)
		if t.data[idx] < 0 {
			neg = 1
		}
		if v^neg != 0 {
			t.data[idx] += poshalf
		} else {
			t.data[idx] -= poshalf
		}
		*center |= t1MuThis << (ci * 3)
	}
}

// decRefpassMQC ports opj_t1_dec_refpass_mqc (generic path).
func (t *T1) decRefpassMQC(bpno int32) {
	one := int32(1) << uint(bpno)
	poshalf := one >> 1
	lw := int(t.w)
	stride := t.w + 2

	data := 0
	flagsp := int(stride) + 1
	var i, j, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			flags := t.flags[flagsp]
			if flags != 0 {
				t.decRefpassStepMQC(&flags, data, lw, 0, poshalf)
				t.decRefpassStepMQC(&flags, data, lw, 1, poshalf)
				t.decRefpassStepMQC(&flags, data, lw, 2, poshalf)
				t.decRefpassStepMQC(&flags, data, lw, 3, poshalf)
				t.flags[flagsp] = flags
			}
			flagsp++
			data++
		}
		flagsp += 2
		data += 3 * lw
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			for j = 0; j < t.h-k; j++ {
				t.decRefpassStepMQC(&t.flags[flagsp], data+int(j)*lw, 0, j, poshalf)
			}
			flagsp++
			data++
		}
	}
}

// ---------------------------------------------------------------------------
// Clean-up pass (decode)
// ---------------------------------------------------------------------------

// decClnpassStep ports opj_t1_dec_clnpass_step_macro. checkFlags/partial mirror
// the compile-time flags of the C macro.
func (t *T1) decClnpassStep(center *uint32, fp int, flagsStride uint32, dp, dataStride int, ci uint32, oneplushalf int32, vsc uint32, checkFlags, partial bool) {
	f := *center
	if checkFlags && f&((t1SigmaThis|t1PiThis)<<(ci*3)) != 0 {
		return
	}
	if !partial {
		t.mqc.SetCurCtx(t.getctxnoZC(f >> (ci * 3)))
		if t.mqc.Decode() == 0 {
			return
		}
	}
	lu := getctxtnoScOrSpbIndex(f, t.flags[fp-1], t.flags[fp+1], ci)
	t.mqc.SetCurCtx(getctxnoSC(lu))
	v := t.mqc.Decode() ^ getspb(lu)
	idx := dp + int(ci)*dataStride
	if v != 0 {
		t.data[idx] = -oneplushalf
	} else {
		t.data[idx] = oneplushalf
	}
	t.updateFlagsCenter(center, fp, ci, v, flagsStride, vsc)
}

const t1PiAll = t1Pi0 | t1Pi1 | t1Pi2 | t1Pi3

// decClnpass ports opj_t1_dec_clnpass (generic path + segsym check).
func (t *T1) decClnpass(bpno int32, cblksty uint32) {
	one := int32(1) << uint(bpno)
	half := one >> 1
	oneplushalf := one | half
	lw := int(t.w)
	stride := t.w + 2
	vsc := uint32(0)
	if cblksty&CblkstyVSC != 0 {
		vsc = 1
	}

	data := 0
	flagsp := int(stride) + 1
	var i, j, k, runlen uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			flags := t.flags[flagsp]
			if flags == 0 {
				t.mqc.SetCurCtx(t1CtxnoAgg)
				if t.mqc.Decode() == 0 {
					flagsp++
					data++
					continue
				}
				t.mqc.SetCurCtx(t1CtxnoUni)
				runlen = t.mqc.Decode()
				runlen = (runlen << 1) | t.mqc.Decode()

				partial := true
				switch runlen {
				case 0:
					t.decClnpassStep(&flags, flagsp, stride, data, lw, 0, oneplushalf, vsc, false, partial)
					partial = false
					fallthrough
				case 1:
					t.decClnpassStep(&flags, flagsp, stride, data, lw, 1, oneplushalf, 0, false, partial)
					partial = false
					fallthrough
				case 2:
					t.decClnpassStep(&flags, flagsp, stride, data, lw, 2, oneplushalf, 0, false, partial)
					partial = false
					fallthrough
				default: // case 3
					t.decClnpassStep(&flags, flagsp, stride, data, lw, 3, oneplushalf, 0, false, partial)
				}
			} else {
				t.decClnpassStep(&flags, flagsp, stride, data, lw, 0, oneplushalf, vsc, true, false)
				t.decClnpassStep(&flags, flagsp, stride, data, lw, 1, oneplushalf, 0, true, false)
				t.decClnpassStep(&flags, flagsp, stride, data, lw, 2, oneplushalf, 0, true, false)
				t.decClnpassStep(&flags, flagsp, stride, data, lw, 3, oneplushalf, 0, true, false)
			}
			t.flags[flagsp] = flags &^ t1PiAll
			flagsp++
			data++
		}
		flagsp += 2
		data += 3 * lw
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			for j = 0; j < t.h-k; j++ {
				t.decClnpassStep(&t.flags[flagsp], flagsp, stride, data+int(j)*lw, 0, j, oneplushalf, vsc, true, false)
			}
			t.flags[flagsp] &^= t1PiAll
			flagsp++
			data++
		}
	}

	// opj_t1_dec_clnpass_check_segsym
	if cblksty&CblkstySegsym != 0 {
		t.mqc.SetCurCtx(t1CtxnoUni)
		t.mqc.Decode()
		t.mqc.Decode()
		t.mqc.Decode()
		t.mqc.Decode()
	}
}

// ---------------------------------------------------------------------------
// Code-block decode
// ---------------------------------------------------------------------------

// DecodeCblk ports opj_t1_decode_cblk: decode one code-block into t1.Data()
// (raster order), or into cblk.DecodedData when that field is set. orient is the
// sub-band orientation (band->bandno, 0..3), roishift the ROI up-shift, cblksty
// the code-block style bitmask, and checkPterm requests the predictable
// termination warning checks (recorded in t1.PtermWarning).
//
// Returns ok=false with an error only for the unsupported bpno_plus_one>=31
// case, mirroring the single hard failure of the C function; all other paths
// return ok=true (a corrupted or empty code-block leaves Data() zeroed).
//
// SEAM: HTJ2K code-blocks (cblksty&CblkstyHT) are handled by a separate worker
// (ht_dec.c / opj_t1_ht_decode_cblk). The C dispatch lives in
// opj_t1_clbl_decode_processor, not here; callers must route HT blocks
// elsewhere before reaching DecodeCblk.
func (t *T1) DecodeCblk(cblk *CodeBlockDec, orient, roishift, cblksty uint32, checkPterm bool) (bool, error) {
	t.lutCtxnoZCOrient = lutCtxnoZC[orient<<9:]
	t.PtermWarning = ""

	t.allocateBuffers(uint32(cblk.X1-cblk.X0), uint32(cblk.Y1-cblk.Y0))

	bpnoPlusOne := int32(roishift + cblk.Numbps)
	if bpnoPlusOne >= 31 {
		return false, fmt.Errorf("t1: unsupported bpno_plus_one = %d >= 31", bpnoPlusOne)
	}
	passtype := uint32(2)

	t.mqc.ResetStates()
	t.mqc.SetState(t1CtxnoUni, 0, 46)
	t.mqc.SetState(t1CtxnoAgg, 0, 3)
	t.mqc.SetState(t1CtxnoZC, 0, 4)

	if cblk.Corrupted {
		return true, nil
	}
	if cblk.NumChunks == 0 {
		return true, nil
	}

	// Concatenate all chunks plus 2 trailing scratch bytes for the MQ synthetic
	// end-of-stream marker (OPJ_COMMON_CBLK_DATA_EXTRA).
	var cblkLen uint32
	for i := uint32(0); i < cblk.NumChunks; i++ {
		cblkLen += cblk.Chunks[i].Len
	}
	if uint32(cap(t.cblkdatabuffer)) < cblkLen+2 {
		t.cblkdatabuffer = make([]byte, cblkLen+2)
	}
	cblkdata := t.cblkdatabuffer[:cblkLen+2]
	off := uint32(0)
	for i := uint32(0); i < cblk.NumChunks; i++ {
		copy(cblkdata[off:], cblk.Chunks[i].Data[:cblk.Chunks[i].Len])
		off += cblk.Chunks[i].Len
	}
	// Zero the 2 trailing scratch bytes (the mqc overwrites them with a
	// synthetic 0xFF 0xFF marker and restores the originals afterwards).
	cblkdata[cblkLen] = 0
	cblkdata[cblkLen+1] = 0

	// Sub-tile decoding: decode directly into the code-block's own buffer.
	var originalData []int32
	if cblk.DecodedData != nil {
		originalData = t.data
		t.data = cblk.DecodedData
	}

	cblkdataindex := uint32(0)
	var lastSegLen uint32
	for segno := uint32(0); segno < cblk.RealNumSegs; segno++ {
		seg := &cblk.Segs[segno]

		typ := t1TypeMQ
		if bpnoPlusOne <= int32(cblk.Numbps)-4 && passtype < 2 && cblksty&CblkstyLazy != 0 {
			typ = t1TypeRAW
		}

		buf := cblkdata[cblkdataindex:]
		if typ == t1TypeRAW {
			t.mqc.RawInitDec(buf, int(seg.Len))
		} else {
			t.mqc.InitDec(buf, int(seg.Len))
		}
		cblkdataindex += seg.Len
		lastSegLen = seg.Len

		for passno := uint32(0); passno < seg.RealNumPasses && bpnoPlusOne >= 1; passno++ {
			switch passtype {
			case 0:
				if typ == t1TypeRAW {
					t.decSigpassRaw(bpnoPlusOne, cblksty)
				} else {
					t.decSigpassMQC(bpnoPlusOne, cblksty)
				}
			case 1:
				if typ == t1TypeRAW {
					t.decRefpassRaw(bpnoPlusOne)
				} else {
					t.decRefpassMQC(bpnoPlusOne)
				}
			case 2:
				t.decClnpass(bpnoPlusOne, cblksty)
			}

			if cblksty&CblkstyReset != 0 && typ == t1TypeMQ {
				t.mqc.ResetStates()
				t.mqc.SetState(t1CtxnoUni, 0, 46)
				t.mqc.SetState(t1CtxnoAgg, 0, 3)
				t.mqc.SetState(t1CtxnoZC, 0, 4)
			}
			passtype++
			if passtype == 3 {
				passtype = 0
				bpnoPlusOne--
			}
		}

		t.mqc.FinishDec()
	}

	if checkPterm {
		// bp/end are not exposed by internal/mqc; the "remaining bytes" test
		// bp+2 < end is equivalent to NumBytes()+2 < seg->len of the last seg.
		if t.mqc.NumBytes()+2 < lastSegLen {
			t.PtermWarning = fmt.Sprintf(
				"PTERM check failure: %d remaining bytes in code block",
				int(lastSegLen)-int(t.mqc.NumBytes())-2)
		} else if t.mqc.EndOfByteStreamCounter() > 2 {
			t.PtermWarning = fmt.Sprintf(
				"PTERM check failure: %d synthesized 0xFF markers read",
				t.mqc.EndOfByteStreamCounter())
		}
	}

	if cblk.DecodedData != nil {
		t.data = originalData
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Post-decode (ROI de-scaling + dequantization)
// ---------------------------------------------------------------------------

// RoiShift ports the ROI de-scaling block of opj_t1_clbl_decode_processor: undo
// the region-of-interest up-shift on the decoded coefficients in place. datap
// holds w*h decoded coefficients (raster order).
func RoiShift(datap []int32, w, h, roishift uint32) {
	if roishift == 0 {
		return
	}
	n := int(w * h)
	if roishift >= 31 {
		for i := 0; i < n; i++ {
			datap[i] = 0
		}
		return
	}
	thresh := int32(1) << roishift
	for i := 0; i < n; i++ {
		val := datap[i]
		mag := val
		if mag < 0 {
			mag = -mag
		}
		if mag >= thresh {
			mag >>= roishift
			if val < 0 {
				datap[i] = -mag
			} else {
				datap[i] = mag
			}
		}
	}
}

// Dequantize ports the in-place (cblk->decoded_data) dequantization branch of
// opj_t1_clbl_decode_processor. For qmfbid==1 (reversible 5/3) each coefficient
// is halved; for qmfbid==0 (irreversible 9/7) each is scaled by 0.5*bandStepsize
// and the resulting float32 is stored, reinterpreted as its int32 bit pattern
// (exactly as the C code memcpy's an OPJ_FLOAT32 over the OPJ_INT32 slot). The
// caller (tcd) reinterprets the slot as float32 when qmfbid==0.
func Dequantize(datap []int32, w, h, qmfbid uint32, bandStepsize float32) {
	n := int(w * h)
	if qmfbid == 1 {
		for i := 0; i < n; i++ {
			datap[i] /= 2
		}
		return
	}
	stepsize := 0.5 * bandStepsize
	for i := 0; i < n; i++ {
		tmp := float32(datap[i]) * stepsize
		datap[i] = int32(math.Float32bits(tmp))
	}
}
