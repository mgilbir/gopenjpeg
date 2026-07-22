package dwt

// This file ports the region/partial-decode paths of dwt.c: the windowed 5/3
// and 9/7 inverse transforms that consume a sparse array populated from decoded
// code-blocks.

import "github.com/mgilbir/gopenjpeg/internal/sparse"

// segmentGrow is a port of opj_dwt_segment_grow.
func segmentGrow(filterWidth, maxSize uint32, start, end *uint32) {
	*start = uintSubs(*start, filterWidth)
	*end = uintAdds(*end, filterWidth)
	*end = uintMin(*end, maxSize)
}

// getBandCoordinates is a port of opj_dwt_get_band_coordinates. It always
// computes all four coordinates; callers ignore the ones they do not need
// (the C code passes NULL for those).
func getBandCoordinates(tc *TileComponent, resno, bandno uint32,
	tcx0, tcy0, tcx1, tcy1 uint32) (tbx0, tby0, tbx1, tby1 uint32) {
	var nb uint32
	if resno == 0 {
		nb = tc.Numresolutions - 1
	} else {
		nb = tc.Numresolutions - resno
	}
	x0b := bandno & 1
	y0b := bandno >> 1

	coord := func(tc0, shift uint32) uint32 {
		if nb == 0 {
			return tc0
		}
		off := (uint32(1) << (nb - 1)) * shift
		if tc0 <= off {
			return 0
		}
		return uintCeildivpow2(tc0-off, nb)
	}
	tbx0 = coord(tcx0, x0b)
	tby0 = coord(tcy0, y0b)
	tbx1 = coord(tcx1, x0b)
	tby1 = coord(tcy1, y0b)
	return
}

// initSparseArray is a port of opj_dwt_init_sparse_array.
func initSparseArray(tc *TileComponent, numres uint32) *sparse.Array {
	trMax := &tc.Resolutions[numres-1]
	w := uint32(trMax.X1 - trMax.X0)
	h := uint32(trMax.Y1 - trMax.Y0)
	sa := sparse.New(w, h, uintMin(w, 64), uintMin(h, 64))
	if sa == nil {
		return nil
	}

	for resno := uint32(0); resno < numres; resno++ {
		res := &tc.Resolutions[resno]
		for bandno := uint32(0); bandno < res.Numbands; bandno++ {
			band := &res.Bands[bandno]
			for precno := uint32(0); precno < res.Pw*res.Ph; precno++ {
				precinct := &band.Precincts[precno]
				for cblkno := uint32(0); cblkno < precinct.Cw*precinct.Ch; cblkno++ {
					cblk := &precinct.Cblks[cblkno]
					if cblk.DecodedData != nil {
						x := uint32(cblk.X0 - band.X0)
						y := uint32(cblk.Y0 - band.Y0)
						cblkW := uint32(cblk.X1 - cblk.X0)
						cblkH := uint32(cblk.Y1 - cblk.Y0)

						if band.Bandno&1 != 0 {
							pres := &tc.Resolutions[resno-1]
							x += uint32(pres.X1 - pres.X0)
						}
						if band.Bandno&2 != 0 {
							pres := &tc.Resolutions[resno-1]
							y += uint32(pres.Y1 - pres.Y0)
						}

						if !sa.Write(x, y, x+cblkW, y+cblkH,
							cblk.DecodedData, 1, cblkW, true) {
							return nil
						}
					}
				}
			}
		}
	}
	return sa
}

// ---- 5/3 partial ----

// interleavePartialH is a port of opj_dwt_interleave_partial_h. dest is h.mem.
func interleavePartialH(dest []int32, cas int32, sa *sparse.Array, saLine, sn,
	winLX0, winLX1, winHX0, winHX1 uint32) {
	off := int(cas) + 2*int(winLX0)
	sa.Read(winLX0, saLine, winLX1, saLine+1, dest[off:], 2, 0, true)
	off = int(1-cas) + 2*int(winHX0)
	sa.Read(sn+winHX0, saLine, sn+winHX1, saLine+1, dest[off:], 2, 0, true)
}

// interleavePartialV is a port of opj_dwt_interleave_partial_v. dest is v.mem.
func interleavePartialV(dest []int32, cas int32, sa *sparse.Array, saCol, nbCols,
	sn, winLY0, winLY1, winHY0, winHY1 uint32) {
	off := int(cas)*4 + 2*4*int(winLY0)
	sa.Read(saCol, winLY0, saCol+nbCols, winLY1, dest[off:], 1, 2*4, true)
	off = int(1-cas)*4 + 2*4*int(winHY0)
	sa.Read(saCol, sn+winHY0, saCol+nbCols, sn+winHY1, dest[off:], 1, 2*4, true)
}

// decodePartial1 is a port of opj_dwt_decode_partial_1.
func decodePartial1(a []int32, dn, sn, cas int32, winLX0, winLX1, winHX0, winHX1 int32) {
	s := func(i int32) int32 { return a[i*2] }
	setS := func(i, v int32) { a[i*2] = v }
	d := func(i int32) int32 { return a[1+i*2] }
	setD := func(i, v int32) { a[1+i*2] = v }
	sClamp := func(i int32) int32 {
		if i < 0 {
			return s(0)
		}
		if i >= sn {
			return s(sn - 1)
		}
		return s(i)
	}
	dClamp := func(i int32) int32 {
		if i < 0 {
			return d(0)
		}
		if i >= dn {
			return d(dn - 1)
		}
		return d(i)
	}
	ssClamp := func(i int32) int32 { // OPJ_SS_
		if i < 0 {
			return s(0)
		}
		if i >= dn {
			return s(dn - 1)
		}
		return s(i)
	}
	ddClamp := func(i int32) int32 { // OPJ_DD_
		if i < 0 {
			return d(0)
		}
		if i >= sn {
			return d(sn - 1)
		}
		return d(i)
	}

	if cas == 0 {
		if dn > 0 || sn > 1 {
			i := winLX0
			if i < winLX1 {
				setS(i, s(i)-((dClamp(i-1)+dClamp(i)+2)>>2))
				i++
				iMax := winLX1
				if iMax > dn {
					iMax = dn
				}
				for ; i < iMax; i++ {
					setS(i, s(i)-((d(i-1)+d(i)+2)>>2))
				}
				for ; i < winLX1; i++ {
					setS(i, s(i)-((dClamp(i-1)+dClamp(i)+2)>>2))
				}
			}
			i = winHX0
			if i < winHX1 {
				iMax := winHX1
				if iMax >= sn {
					iMax = sn - 1
				}
				for ; i < iMax; i++ {
					setD(i, d(i)+((s(i)+s(i+1))>>1))
				}
				for ; i < winHX1; i++ {
					setD(i, d(i)+((sClamp(i)+sClamp(i+1))>>1))
				}
			}
		}
	} else {
		if sn == 0 && dn == 1 {
			setS(0, s(0)/2)
		} else {
			for i := winLX0; i < winLX1; i++ {
				setD(i, d(i)-((ssClamp(i)+ssClamp(i+1)+2)>>2))
			}
			for i := winHX0; i < winHX1; i++ {
				setS(i, s(i)+((ddClamp(i)+ddClamp(i-1))>>1))
			}
		}
	}
}

// decodePartial1Parallel is a port of opj_dwt_decode_partial_1_parallel (scalar
// path). a holds 4 interleaved lanes (offset 0..3).
func decodePartial1Parallel(a []int32, nbCols uint32, dn, sn, cas int32,
	winLX0, winLX1, winHX0, winHX1 int32) {
	_ = nbCols
	sOff := func(i, off int32) int32 { return a[i*2*4+off] }
	setSOff := func(i, off, v int32) { a[i*2*4+off] = v }
	dOff := func(i, off int32) int32 { return a[(1+i*2)*4+off] }
	setDOff := func(i, off, v int32) { a[(1+i*2)*4+off] = v }
	sClamp := func(i, off int32) int32 { // OPJ_S__off
		if i < 0 {
			return sOff(0, off)
		}
		if i >= sn {
			return sOff(sn-1, off)
		}
		return sOff(i, off)
	}
	dClamp := func(i, off int32) int32 { // OPJ_D__off
		if i < 0 {
			return dOff(0, off)
		}
		if i >= dn {
			return dOff(dn-1, off)
		}
		return dOff(i, off)
	}
	ssClamp := func(i, off int32) int32 { // OPJ_SS__off
		if i < 0 {
			return sOff(0, off)
		}
		if i >= dn {
			return sOff(dn-1, off)
		}
		return sOff(i, off)
	}
	ddClamp := func(i, off int32) int32 { // OPJ_DD__off
		if i < 0 {
			return dOff(0, off)
		}
		if i >= sn {
			return dOff(sn-1, off)
		}
		return dOff(i, off)
	}

	if cas == 0 {
		if dn > 0 || sn > 1 {
			i := winLX0
			if i < winLX1 {
				for off := int32(0); off < 4; off++ {
					setSOff(i, off, sOff(i, off)-((dClamp(i-1, off)+dClamp(i, off)+2)>>2))
				}
				i++
				iMax := winLX1
				if iMax > dn {
					iMax = dn
				}
				for ; i < iMax; i++ {
					for off := int32(0); off < 4; off++ {
						setSOff(i, off, sOff(i, off)-((dOff(i-1, off)+dOff(i, off)+2)>>2))
					}
				}
				for ; i < winLX1; i++ {
					for off := int32(0); off < 4; off++ {
						setSOff(i, off, sOff(i, off)-((dClamp(i-1, off)+dClamp(i, off)+2)>>2))
					}
				}
			}
			i = winHX0
			if i < winHX1 {
				iMax := winHX1
				if iMax >= sn {
					iMax = sn - 1
				}
				for ; i < iMax; i++ {
					for off := int32(0); off < 4; off++ {
						setDOff(i, off, dOff(i, off)+((sOff(i, off)+sOff(i+1, off))>>1))
					}
				}
				for ; i < winHX1; i++ {
					for off := int32(0); off < 4; off++ {
						setDOff(i, off, dOff(i, off)+((sClamp(i, off)+sClamp(i+1, off))>>1))
					}
				}
			}
		}
	} else {
		if sn == 0 && dn == 1 {
			for off := int32(0); off < 4; off++ {
				setSOff(0, off, sOff(0, off)/2)
			}
		} else {
			for i := winLX0; i < winLX1; i++ {
				for off := int32(0); off < 4; off++ {
					setDOff(i, off, dOff(i, off)-((ssClamp(i, off)+ssClamp(i+1, off)+2)>>2))
				}
			}
			for i := winHX0; i < winHX1; i++ {
				for off := int32(0); off < 4; off++ {
					setSOff(i, off, sOff(i, off)+((ddClamp(i, off)+ddClamp(i-1, off))>>1))
				}
			}
		}
	}
}

// DecodePartialTile is a port of opj_dwt_decode_partial_tile: region-constrained
// inverse 5/3 transform. It fills tc.DataWin with the window of interest.
func DecodePartialTile(tc *TileComponent, numres uint32) bool {
	const filterWidth = 2

	tr := tc.Resolutions
	ti := 0
	trMax := &tc.Resolutions[numres-1]

	rw := uint32(tr[0].X1 - tr[0].X0)
	rh := uint32(tr[0].Y1 - tr[0].Y0)

	winTcx0 := tc.WinX0
	winTcy0 := tc.WinY0
	winTcx1 := tc.WinX1
	winTcy1 := tc.WinY1

	if trMax.X0 == trMax.X1 || trMax.Y0 == trMax.Y1 {
		return true
	}

	sa := initSparseArray(tc, numres)
	if sa == nil {
		return false
	}

	if numres == 1 {
		sa.Read(trMax.WinX0-uint32(trMax.X0), trMax.WinY0-uint32(trMax.Y0),
			trMax.WinX1-uint32(trMax.X0), trMax.WinY1-uint32(trMax.Y0),
			tc.DataWin, 1, trMax.WinX1-trMax.WinX0, true)
		return true
	}

	hMem := maxResolution(tr, numres)
	mem := make([]int32, uint64(hMem)*4)

	var hcas, vcas int32
	var hsn, hdn, vsn, vdn int32

	for resno := uint32(1); resno < numres; resno++ {
		ti++

		hsn = int32(rw)
		vsn = int32(rh)

		rw = uint32(tr[ti].X1 - tr[ti].X0)
		rh = uint32(tr[ti].Y1 - tr[ti].Y0)

		hdn = int32(rw) - hsn
		hcas = tr[ti].X0 % 2
		vdn = int32(rh) - vsn
		vcas = tr[ti].Y0 % 2

		winLLX0, winLLY0, winLLX1, winLLY1 := getBandCoordinates(tc, resno, 0,
			winTcx0, winTcy0, winTcx1, winTcy1)
		winHLX0, _, winHLX1, _ := getBandCoordinates(tc, resno, 1,
			winTcx0, winTcy0, winTcx1, winTcy1)
		_, winLHY0, _, winLHY1 := getBandCoordinates(tc, resno, 2,
			winTcx0, winTcy0, winTcx1, winTcy1)

		// Beware: band index for non-LL0 resolution are 0=HL, 1=LH, 2=HH.
		trLLX0 := uint32(tr[ti].Bands[1].X0)
		trLLY0 := uint32(tr[ti].Bands[0].Y0)
		trHLX0 := uint32(tr[ti].Bands[0].X0)
		trLHY0 := uint32(tr[ti].Bands[1].Y0)

		winLLX0 = uintSubs(winLLX0, trLLX0)
		winLLY0 = uintSubs(winLLY0, trLLY0)
		winLLX1 = uintSubs(winLLX1, trLLX0)
		winLLY1 = uintSubs(winLLY1, trLLY0)
		winHLX0 = uintSubs(winHLX0, trHLX0)
		winHLX1 = uintSubs(winHLX1, trHLX0)
		winLHY0 = uintSubs(winLHY0, trLHY0)
		winLHY1 = uintSubs(winLHY1, trLHY0)

		segmentGrow(filterWidth, uint32(hsn), &winLLX0, &winLLX1)
		segmentGrow(filterWidth, uint32(hdn), &winHLX0, &winHLX1)
		segmentGrow(filterWidth, uint32(vsn), &winLLY0, &winLLY1)
		segmentGrow(filterWidth, uint32(vdn), &winLHY0, &winLHY1)

		var winTrX0, winTrX1, winTrY0, winTrY1 uint32
		if hcas == 0 {
			winTrX0 = uintMin(2*winLLX0, 2*winHLX0+1)
			winTrX1 = uintMin(uintMax(2*winLLX1, 2*winHLX1+1), rw)
		} else {
			winTrX0 = uintMin(2*winHLX0, 2*winLLX0+1)
			winTrX1 = uintMin(uintMax(2*winHLX1, 2*winLLX1+1), rw)
		}
		if vcas == 0 {
			winTrY0 = uintMin(2*winLLY0, 2*winLHY0+1)
			winTrY1 = uintMin(uintMax(2*winLLY1, 2*winLHY1+1), rh)
		} else {
			winTrY0 = uintMin(2*winLHY0, 2*winLLY0+1)
			winTrY1 = uintMin(uintMax(2*winLHY1, 2*winLLY1+1), rh)
		}

		d := &dwt53{mem: mem, sn: hsn, dn: hdn, cas: hcas}
		for j := uint32(0); j < rh; j++ {
			if (j >= winLLY0 && j < winLLY1) ||
				(j >= winLHY0+uint32(vsn) && j < winLHY1+uint32(vsn)) {

				if winTrX1 >= 1 && winTrX1 < rw {
					d.mem[winTrX1-1] = 0
				}
				if winTrX1 < rw {
					d.mem[winTrX1] = 0
				}

				interleavePartialH(d.mem, hcas, sa, j, uint32(hsn),
					winLLX0, winLLX1, winHLX0, winHLX1)
				decodePartial1(d.mem, hdn, hsn, hcas,
					int32(winLLX0), int32(winLLX1), int32(winHLX0), int32(winHLX1))
				if !sa.Write(winTrX0, j, winTrX1, j+1,
					d.mem[winTrX0:], 1, 0, true) {
					return false
				}
			}
		}

		for i := winTrX0; i < winTrX1; {
			nbCols := uintMin(4, winTrX1-i)
			interleavePartialV(mem, vcas, sa, i, nbCols, uint32(vsn),
				winLLY0, winLLY1, winLHY0, winLHY1)
			decodePartial1Parallel(mem, nbCols, vdn, vsn, vcas,
				int32(winLLY0), int32(winLLY1), int32(winLHY0), int32(winLHY1))
			if !sa.Write(i, winTrY0, i+nbCols, winTrY1,
				mem[4*int(winTrY0):], 1, 4, true) {
				return false
			}
			i += nbCols
		}
	}

	sa.Read(trMax.WinX0-uint32(trMax.X0), trMax.WinY0-uint32(trMax.Y0),
		trMax.WinX1-uint32(trMax.X0), trMax.WinY1-uint32(trMax.Y0),
		tc.DataWin, 1, trMax.WinX1-trMax.WinX0, true)
	return true
}

// ---- 9/7 partial ----

// v8dwtInterleavePartialH is a port of opj_v8dwt_interleave_partial_h. raw is
// the int32 view of the wavelet buffer.
func v8dwtInterleavePartialH(d *v8dwt, raw []int32, sa *sparse.Array, saLine, remainingHeight uint32) {
	for i := uint32(0); i < remainingHeight; i++ {
		off := (int(d.cas) + 2*int(d.winLX0)) * nbEltsV8
		sa.Read(d.winLX0, saLine+i, d.winLX1, saLine+i+1,
			raw[off+int(i):], 2*nbEltsV8, 0, true)
		off = (int(1-d.cas) + 2*int(d.winHX0)) * nbEltsV8
		sa.Read(uint32(d.sn)+d.winHX0, saLine+i, uint32(d.sn)+d.winHX1, saLine+i+1,
			raw[off+int(i):], 2*nbEltsV8, 0, true)
	}
}

// v8dwtInterleavePartialV is a port of opj_v8dwt_interleave_partial_v.
func v8dwtInterleavePartialV(d *v8dwt, raw []int32, sa *sparse.Array, saCol, nbEltsRead uint32) {
	off := (int(d.cas) + 2*int(d.winLX0)) * nbEltsV8
	sa.Read(saCol, d.winLX0, saCol+nbEltsRead, d.winLX1,
		raw[off:], 1, 2*nbEltsV8, true)
	off = (int(1-d.cas) + 2*int(d.winHX0)) * nbEltsV8
	sa.Read(saCol, uint32(d.sn)+d.winHX0, saCol+nbEltsRead, uint32(d.sn)+d.winHX1,
		raw[off:], 1, 2*nbEltsV8, true)
}

// DecodePartial97 is a port of opj_dwt_decode_partial_97: region-constrained
// inverse 9/7 transform. tc.Data holds float32 bit patterns; the window of
// interest is written to tc.DataWin (also float32 bit patterns).
func DecodePartial97(tc *TileComponent, numres uint32) bool {
	const filterWidth = 4

	tr := tc.Resolutions
	ti := 0
	trMax := &tc.Resolutions[numres-1]

	rw := uint32(tr[0].X1 - tr[0].X0)
	rh := uint32(tr[0].Y1 - tr[0].Y0)

	winTcx0 := tc.WinX0
	winTcy0 := tc.WinY0
	winTcx1 := tc.WinX1
	winTcy1 := tc.WinY1

	if trMax.X0 == trMax.X1 || trMax.Y0 == trMax.Y1 {
		return true
	}

	sa := initSparseArray(tc, numres)
	if sa == nil {
		return false
	}

	if numres == 1 {
		sa.Read(trMax.WinX0-uint32(trMax.X0), trMax.WinY0-uint32(trMax.Y0),
			trMax.WinX1-uint32(trMax.X0), trMax.WinY1-uint32(trMax.Y0),
			tc.DataWin, 1, trMax.WinX1-trMax.WinX0, true)
		return true
	}

	dataSize := maxResolution(tr, numres)
	var h, v v8dwt
	h.wavelet = make([]float32, uint64(dataSize)*nbEltsV8)
	v.wavelet = h.wavelet
	raw := make([]int32, uint64(dataSize)*nbEltsV8)

	// decode wraps: convert raw->float, run v8dwtDecode, convert float->raw.
	decode := func(d *v8dwt) {
		for i := range d.wavelet {
			d.wavelet[i] = f32frombits(raw[i])
		}
		v8dwtDecode(d)
		for i := range d.wavelet {
			raw[i] = f32bits(d.wavelet[i])
		}
	}

	for resno := uint32(1); resno < numres; resno++ {
		ti++

		h.sn = int32(rw)
		v.sn = int32(rh)

		rw = uint32(tr[ti].X1 - tr[ti].X0)
		rh = uint32(tr[ti].Y1 - tr[ti].Y0)

		h.dn = int32(rw) - h.sn
		h.cas = tr[ti].X0 % 2
		v.dn = int32(rh) - v.sn
		v.cas = tr[ti].Y0 % 2

		winLLX0, winLLY0, winLLX1, winLLY1 := getBandCoordinates(tc, resno, 0,
			winTcx0, winTcy0, winTcx1, winTcy1)
		winHLX0, _, winHLX1, _ := getBandCoordinates(tc, resno, 1,
			winTcx0, winTcy0, winTcx1, winTcy1)
		_, winLHY0, _, winLHY1 := getBandCoordinates(tc, resno, 2,
			winTcx0, winTcy0, winTcx1, winTcy1)

		trLLX0 := uint32(tr[ti].Bands[1].X0)
		trLLY0 := uint32(tr[ti].Bands[0].Y0)
		trHLX0 := uint32(tr[ti].Bands[0].X0)
		trLHY0 := uint32(tr[ti].Bands[1].Y0)

		winLLX0 = uintSubs(winLLX0, trLLX0)
		winLLY0 = uintSubs(winLLY0, trLLY0)
		winLLX1 = uintSubs(winLLX1, trLLX0)
		winLLY1 = uintSubs(winLLY1, trLLY0)
		winHLX0 = uintSubs(winHLX0, trHLX0)
		winHLX1 = uintSubs(winHLX1, trHLX0)
		winLHY0 = uintSubs(winLHY0, trLHY0)
		winLHY1 = uintSubs(winLHY1, trLHY0)

		segmentGrow(filterWidth, uint32(h.sn), &winLLX0, &winLLX1)
		segmentGrow(filterWidth, uint32(h.dn), &winHLX0, &winHLX1)
		segmentGrow(filterWidth, uint32(v.sn), &winLLY0, &winLLY1)
		segmentGrow(filterWidth, uint32(v.dn), &winLHY0, &winLHY1)

		var winTrX0, winTrX1, winTrY0, winTrY1 uint32
		if h.cas == 0 {
			winTrX0 = uintMin(2*winLLX0, 2*winHLX0+1)
			winTrX1 = uintMin(uintMax(2*winLLX1, 2*winHLX1+1), rw)
		} else {
			winTrX0 = uintMin(2*winHLX0, 2*winLLX0+1)
			winTrX1 = uintMin(uintMax(2*winHLX1, 2*winLLX1+1), rw)
		}
		if v.cas == 0 {
			winTrY0 = uintMin(2*winLLY0, 2*winLHY0+1)
			winTrY1 = uintMin(uintMax(2*winLLY1, 2*winLHY1+1), rh)
		} else {
			winTrY0 = uintMin(2*winLHY0, 2*winLLY0+1)
			winTrY1 = uintMin(uintMax(2*winLHY1, 2*winLLY1+1), rh)
		}

		h.winLX0 = winLLX0
		h.winLX1 = winLLX1
		h.winHX0 = winHLX0
		h.winHX1 = winHLX1
		var j uint32
		for j = 0; j+(nbEltsV8-1) < rh; j += nbEltsV8 {
			if (j+(nbEltsV8-1) >= winLLY0 && j < winLLY1) ||
				(j+(nbEltsV8-1) >= winLHY0+uint32(v.sn) && j < winLHY1+uint32(v.sn)) {
				v8dwtInterleavePartialH(&h, raw, sa, j, uintMin(nbEltsV8, rh-j))
				decode(&h)
				if !sa.Write(winTrX0, j, winTrX1, j+nbEltsV8,
					raw[int(winTrX0)*nbEltsV8:], nbEltsV8, 1, true) {
					return false
				}
			}
		}
		if j < rh &&
			((j+(nbEltsV8-1) >= winLLY0 && j < winLLY1) ||
				(j+(nbEltsV8-1) >= winLHY0+uint32(v.sn) && j < winLHY1+uint32(v.sn))) {
			v8dwtInterleavePartialH(&h, raw, sa, j, rh-j)
			decode(&h)
			if !sa.Write(winTrX0, j, winTrX1, rh,
				raw[int(winTrX0)*nbEltsV8:], nbEltsV8, 1, true) {
				return false
			}
		}

		v.winLX0 = winLLY0
		v.winLX1 = winLLY1
		v.winHX0 = winLHY0
		v.winHX1 = winLHY1
		for j = winTrX0; j < winTrX1; j += nbEltsV8 {
			nbElts := uintMin(nbEltsV8, winTrX1-j)
			v8dwtInterleavePartialV(&v, raw, sa, j, nbElts)
			decode(&v)
			if !sa.Write(j, winTrY0, j+nbElts, winTrY1,
				raw[int(winTrY0)*nbEltsV8:], 1, nbEltsV8, true) {
				return false
			}
		}
	}

	sa.Read(trMax.WinX0-uint32(trMax.X0), trMax.WinY0-uint32(trMax.Y0),
		trMax.WinX1-uint32(trMax.X0), trMax.WinY1-uint32(trMax.Y0),
		tc.DataWin, 1, trMax.WinX1-trMax.WinX0, true)
	return true
}
