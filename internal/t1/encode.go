package t1

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/dwt"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
)

// This file ports the tier-1 encoder: the significance, refinement and clean-up
// encoding passes, the nmsedec / weighted-MSE distortion accounting, and the
// pass-management state machine (opj_t1_encode_cblk) with all termination and
// mode-switch decisions (TERMALL, LAZY bypass, RESET, PTERM, SEGSYM).

// ---------------------------------------------------------------------------
// Significance pass (encode)
// ---------------------------------------------------------------------------

// encSigpassStep ports opj_t1_enc_sigpass_step_macro. flags is the current
// centre word value (read once); centre-word and neighbour updates are written
// straight to the array via updateFlags.
func (t *T1) encSigpassStep(fp, dp int, bpno, one int32, nmsedec *int32, typ int, ci, vsc uint32) {
	flags := t.flags[fp]
	if flags&((t1SigmaThis|t1PiThis)<<(ci*3)) == 0 &&
		flags&(t1SigmaNeighbours<<(ci*3)) != 0 {
		ctxt1 := t.getctxnoZC(flags >> (ci * 3))
		var v uint32
		if smrAbs(t.data[dp])&uint32(one) != 0 {
			v = 1
		}
		t.mqc.SetCurCtx(ctxt1)
		if typ == t1TypeRAW {
			t.mqc.BypassEnc(v)
		} else {
			t.mqc.Encode(v)
		}
		if v != 0 {
			lu := getctxtnoScOrSpbIndex(flags, t.flags[fp-1], t.flags[fp+1], ci)
			ctxt2 := getctxnoSC(lu)
			v = smrSign(t.data[dp])
			*nmsedec += getnmsedecSig(smrAbs(t.data[dp]), uint32(bpno))
			t.mqc.SetCurCtx(ctxt2)
			if typ == t1TypeRAW {
				t.mqc.BypassEnc(v)
			} else {
				t.mqc.Encode(v ^ getspb(lu))
			}
			t.updateFlags(fp, ci, v, t.w+2, vsc)
		}
		t.flags[fp] |= t1PiThis << (ci * 3)
	}
}

// encSigpass ports opj_t1_enc_sigpass.
func (t *T1) encSigpass(bpno int32, nmsedec *int32, typ int, cblksty uint32) {
	one := int32(1) << uint(bpno+t1NmsedecFracbits)
	f := int(t.w+2) + 1
	datap := 0
	vscBlk := uint32(0)
	if cblksty&CblkstyVSC != 0 {
		vscBlk = 1
	}
	*nmsedec = 0
	var i, j, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			if t.flags[f] == 0 {
				f++
				datap += 4
				continue
			}
			t.encSigpassStep(f, datap+0, bpno, one, nmsedec, typ, 0, vscBlk)
			t.encSigpassStep(f, datap+1, bpno, one, nmsedec, typ, 1, 0)
			t.encSigpassStep(f, datap+2, bpno, one, nmsedec, typ, 2, 0)
			t.encSigpassStep(f, datap+3, bpno, one, nmsedec, typ, 3, 0)
			f++
			datap += 4
		}
		f += 2
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			if t.flags[f] == 0 {
				datap += int(t.h - k)
				f++
				continue
			}
			for j = k; j < t.h; j++ {
				vsc := uint32(0)
				if j == k && cblksty&CblkstyVSC != 0 {
					vsc = 1
				}
				t.encSigpassStep(f, datap, bpno, one, nmsedec, typ, j-k, vsc)
				datap++
			}
			f++
		}
	}
}

// ---------------------------------------------------------------------------
// Refinement pass (encode)
// ---------------------------------------------------------------------------

// encRefpassStep ports opj_t1_enc_refpass_step_macro.
func (t *T1) encRefpassStep(flags uint32, flagsUpdated *uint32, dp int, bpno, one int32, nmsedec *int32, typ int, ci uint32) {
	if flags&((t1SigmaThis|t1PiThis)<<(ci*3)) == (t1SigmaThis << (ci * 3)) {
		ctxt := getctxnoMag(flags >> (ci * 3))
		absData := smrAbs(t.data[dp])
		*nmsedec += getnmsedecRef(absData, uint32(bpno))
		var v uint32
		if int32(absData)&one != 0 {
			v = 1
		}
		t.mqc.SetCurCtx(ctxt)
		if typ == t1TypeRAW {
			t.mqc.BypassEnc(v)
		} else {
			t.mqc.Encode(v)
		}
		*flagsUpdated |= t1MuThis << (ci * 3)
	}
}

// encRefpass ports opj_t1_enc_refpass.
func (t *T1) encRefpass(bpno int32, nmsedec *int32, typ int) {
	one := int32(1) << uint(bpno+t1NmsedecFracbits)
	f := int(t.w+2) + 1
	datap := 0
	*nmsedec = 0
	const sigMask = t1Sigma4 | t1Sigma7 | t1Sigma10 | t1Sigma13
	var i, j, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			flags := t.flags[f]
			flagsUpdated := flags
			if flags&sigMask == 0 {
				f++
				datap += 4
				continue
			}
			if flags&t1PiAll == t1PiAll {
				f++
				datap += 4
				continue
			}
			t.encRefpassStep(flags, &flagsUpdated, datap+0, bpno, one, nmsedec, typ, 0)
			t.encRefpassStep(flags, &flagsUpdated, datap+1, bpno, one, nmsedec, typ, 1)
			t.encRefpassStep(flags, &flagsUpdated, datap+2, bpno, one, nmsedec, typ, 2)
			t.encRefpassStep(flags, &flagsUpdated, datap+3, bpno, one, nmsedec, typ, 3)
			t.flags[f] = flagsUpdated
			f++
			datap += 4
		}
		f += 2
	}
	if k < t.h {
		remaining := t.h - k
		for i = 0; i < t.w; i++ {
			if t.flags[f]&sigMask == 0 {
				datap += int(remaining)
				f++
				continue
			}
			for j = 0; j < remaining; j++ {
				cur := t.flags[f]
				t.encRefpassStep(cur, &t.flags[f], datap, bpno, one, nmsedec, typ, j)
				datap++
			}
			f++
		}
	}
}

// ---------------------------------------------------------------------------
// Clean-up pass (encode)
// ---------------------------------------------------------------------------

// encClnpassStep ports opj_t1_enc_clnpass_step_macro (the [runlen, lim) range).
func (t *T1) encClnpassStep(fp, datapStart int, bpno, one int32, nmsedec *int32, agg, runlen, lim, cblksty uint32) {
	const check = t1Sigma4 | t1Sigma7 | t1Sigma10 | t1Sigma13 | t1Pi0 | t1Pi1 | t1Pi2 | t1Pi3
	if t.flags[fp]&check == check {
		switch runlen {
		case 0:
			t.flags[fp] &^= t1PiAll
		case 1:
			t.flags[fp] &^= t1Pi1 | t1Pi2 | t1Pi3
		case 2:
			t.flags[fp] &^= t1Pi2 | t1Pi3
		case 3:
			t.flags[fp] &^= t1Pi3
		}
		return
	}
	ldatap := datapStart
	for ci := runlen; ci < lim; ci++ {
		gotoPartial := false
		if agg != 0 && ci == runlen {
			gotoPartial = true
		} else if t.flags[fp]&((t1SigmaThis|t1PiThis)<<(ci*3)) == 0 {
			t.mqc.SetCurCtx(t.getctxnoZC(t.flags[fp] >> (ci * 3)))
			var v uint32
			if smrAbs(t.data[ldatap])&uint32(one) != 0 {
				v = 1
			}
			t.mqc.Encode(v)
			if v != 0 {
				gotoPartial = true
			}
		}
		if gotoPartial {
			lu := getctxtnoScOrSpbIndex(t.flags[fp], t.flags[fp-1], t.flags[fp+1], ci)
			*nmsedec += getnmsedecSig(smrAbs(t.data[ldatap]), uint32(bpno))
			t.mqc.SetCurCtx(getctxnoSC(lu))
			v := smrSign(t.data[ldatap])
			t.mqc.Encode(v ^ getspb(lu))
			vsc := uint32(0)
			if cblksty&CblkstyVSC != 0 && ci == 0 {
				vsc = 1
			}
			t.updateFlags(fp, ci, v, t.w+2, vsc)
		}
		t.flags[fp] &^= t1PiThis << (3 * ci)
		ldatap++
	}
}

// encClnpass ports opj_t1_enc_clnpass.
func (t *T1) encClnpass(bpno int32, nmsedec *int32, cblksty uint32) {
	one := int32(1) << uint(bpno+t1NmsedecFracbits)
	datap := 0
	f := int(t.w+2) + 1
	*nmsedec = 0
	var i, k uint32
	for k = 0; k < (t.h &^ 3); k += 4 {
		for i = 0; i < t.w; i++ {
			var agg, runlen uint32
			if t.flags[f] == 0 {
				agg = 1
			}
			if agg != 0 {
				for runlen = 0; runlen < 4; runlen++ {
					if smrAbs(t.data[datap])&uint32(one) != 0 {
						break
					}
					datap++
				}
				t.mqc.SetCurCtx(t1CtxnoAgg)
				var b uint32
				if runlen != 4 {
					b = 1
				}
				t.mqc.Encode(b)
				if runlen == 4 {
					f++
					continue
				}
				t.mqc.SetCurCtx(t1CtxnoUni)
				t.mqc.Encode(runlen >> 1)
				t.mqc.Encode(runlen & 1)
			} else {
				runlen = 0
			}
			t.encClnpassStep(f, datap, bpno, one, nmsedec, agg, runlen, 4, cblksty)
			datap += int(4 - runlen)
			f++
		}
		f += 2
	}
	if k < t.h {
		for i = 0; i < t.w; i++ {
			t.encClnpassStep(f, datap, bpno, one, nmsedec, 0, 0, t.h-k, cblksty)
			datap += int(t.h - k)
			f++
		}
	}
}

// ---------------------------------------------------------------------------
// Distortion accounting
// ---------------------------------------------------------------------------

// getwmsedec ports opj_t1_getwmsedec: convert a pass's nmsedec into the weighted
// MSE reduction that feeds tile->distotile. mctNorms may be nil; numcomps is
// unused (kept for signature parity with the C reference).
func getwmsedec(nmsedec int32, compno, level, orient uint32, bpno int32, qmfbid uint32, stepsize float64, numcomps uint32, mctNorms []float64, mctNumcomps uint32) float64 {
	_ = numcomps
	w1 := 1.0
	if mctNorms != nil && compno < mctNumcomps {
		w1 = mctNorms[compno]
	}
	var w2 float64
	if qmfbid == 1 {
		w2 = dwt.Getnorm(level, orient)
	} else {
		log2gain := 0
		if orient == 3 {
			log2gain = 2
		} else if orient != 0 {
			log2gain = 1
		}
		w2 = dwt.GetnormReal(level, orient)
		// Not sure this is right. But preserves past behaviour (C comment).
		stepsize /= float64(int64(1) << uint(log2gain))
	}
	wmsedec := w1 * w2 * stepsize * float64(int64(1)<<uint(bpno))
	wmsedec *= wmsedec * float64(nmsedec) / 8192.0
	return wmsedec
}

// encIsTermPass ports opj_t1_enc_is_term_pass.
func encIsTermPass(numbps, cblksty uint32, bpno int32, passtype uint32) bool {
	if passtype == 2 && bpno == 0 {
		return true
	}
	if cblksty&CblkstyTermall != 0 {
		return true
	}
	if cblksty&CblkstyLazy != 0 {
		if bpno == int32(numbps)-4 && passtype == 2 {
			return true
		}
		if bpno < int32(numbps)-4 && passtype > 0 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Code-block encode
// ---------------------------------------------------------------------------

// EncodeCblk ports opj_t1_encode_cblk. t1.data must already be filled (see
// SetData) with the code-block coefficients in T1 "zigzag" order and fixed-point
// scaled exactly as opj_t1_cblk_encode_processor produces them. It converts the
// data to sign-magnitude in place, runs the coding passes, and fills cblk's
// Data / Passes / Numbps / Totalpasses. Returns the cumulative weighted MSE
// reduction (the value the C reference adds to tile->distotile).
func (t *T1) EncodeCblk(cblk *CodeBlockEnc, orient, compno, level, qmfbid uint32, stepsize float64, cblksty, numcomps uint32, mctNorms []float64, mctNumcomps uint32) float64 {
	cumwmsedec := 0.0
	t.lutCtxnoZCOrient = lutCtxnoZC[orient<<9:]

	// Compute max magnitude and convert data to sign-magnitude in place.
	max := int32(0)
	n := int(t.w * t.h)
	for i := 0; i < n; i++ {
		tmp := t.data[i]
		if tmp < 0 {
			if tmp == math.MinInt32 {
				// Avoid UB negating INT_MIN; input exceeds supported bit depth.
				tmp = math.MinInt32 + 1
			}
			max = opjmath.IntMax(max, -tmp)
			t.data[i] = int32(toSmr(tmp))
		} else {
			max = opjmath.IntMax(max, tmp)
		}
	}

	if max != 0 {
		cblk.Numbps = uint32((opjmath.IntFloorlog2(max) + 1) - t1NmsedecFracbits)
	} else {
		cblk.Numbps = 0
	}
	if cblk.Numbps == 0 {
		cblk.Totalpasses = 0
		return cumwmsedec
	}

	bpno := int32(cblk.Numbps - 1)
	passtype := uint32(2)

	t.mqc.ResetStates()
	t.mqc.SetState(t1CtxnoUni, 0, 46)
	t.mqc.SetState(t1CtxnoAgg, 0, 3)
	t.mqc.SetState(t1CtxnoZC, 0, 4)
	t.mqc.InitEnc()

	var nmsedec int32
	passno := uint32(0)
	for bpno >= 0 {
		if int(passno) >= len(cblk.Passes) {
			cblk.Passes = append(cblk.Passes, Pass{})
		}
		typ := t1TypeMQ
		if bpno < int32(cblk.Numbps)-4 && passtype < 2 && cblksty&CblkstyLazy != 0 {
			typ = t1TypeRAW
		}

		// If the previous pass was terminating, reset the encoder.
		if passno > 0 && cblk.Passes[passno-1].Term != 0 {
			if typ == t1TypeRAW {
				t.mqc.BypassInitEnc()
			} else {
				t.mqc.RestartInitEnc()
			}
		}

		switch passtype {
		case 0:
			t.encSigpass(bpno, &nmsedec, typ, cblksty)
		case 1:
			t.encRefpass(bpno, &nmsedec, typ)
		case 2:
			t.encClnpass(bpno, &nmsedec, cblksty)
			if cblksty&CblkstySegsym != 0 {
				t.mqc.SegmarkEnc()
			}
		}

		tempwmsedec := getwmsedec(nmsedec, compno, level, orient, bpno, qmfbid,
			stepsize, numcomps, mctNorms, mctNumcomps)
		cumwmsedec += tempwmsedec

		pass := &cblk.Passes[passno]
		pass.DistortionDec = cumwmsedec

		if encIsTermPass(cblk.Numbps, cblksty, bpno, passtype) {
			if typ == t1TypeRAW {
				t.mqc.BypassFlushEnc(cblksty&CblkstyPterm != 0)
			} else {
				if cblksty&CblkstyPterm != 0 {
					t.mqc.ErtermEnc()
				} else {
					t.mqc.Flush()
				}
			}
			pass.Term = 1
			pass.Rate = t.mqc.NumBytes()
		} else {
			var rateExtra uint32
			if typ == t1TypeRAW {
				rateExtra = t.mqc.BypassGetExtraBytes(cblksty&CblkstyPterm != 0)
			} else {
				rateExtra = 3
			}
			pass.Term = 0
			pass.Rate = t.mqc.NumBytes() + rateExtra
		}

		passtype++
		if passtype == 3 {
			passtype = 0
			bpno--
		}

		// Code-switch RESET.
		if cblksty&CblkstyReset != 0 {
			t.mqc.ResetEnc()
		}
		passno++
	}

	cblk.Totalpasses = passno

	data := t.mqc.Bytes()

	if cblk.Totalpasses != 0 {
		// Make sure that pass rates are increasing.
		lastPassRate := t.mqc.NumBytes()
		for p := cblk.Totalpasses; p > 0; {
			p--
			pass := &cblk.Passes[p]
			if pass.Rate > lastPassRate {
				pass.Rate = lastPassRate
			} else {
				lastPassRate = pass.Rate
			}
		}
	}

	for p := uint32(0); p < cblk.Totalpasses; p++ {
		pass := &cblk.Passes[p]
		// Prevent generation of FF as last data byte of a pass.
		if data[pass.Rate-1] == 0xFF {
			pass.Rate--
		}
		if p == 0 {
			pass.Len = pass.Rate
		} else {
			pass.Len = pass.Rate - cblk.Passes[p-1].Rate
		}
	}

	// Publish the coded byte stream (cblk->data equivalent).
	cblk.Data = append(cblk.Data[:0], data...)

	return cumwmsedec
}
