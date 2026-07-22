package dwt

// This file ports the irreversible 9/7 float transform of dwt.c: the scalar
// 1-D lifting kernels, the forward/inverse row and column passes, and the
// whole-tile 2-D drivers. All arithmetic is float32 in the exact order of the
// C source; results are bit-exact with the C library.

// two_invK: historic value for 2 / opj_invK used by the inverse transform
// (see the BUG_WEIRD_TWO_INVK note in dwt.c / tcd.c).
const dwtTwoInvK float32 = 1.625732422

// ---- forward 9/7 horizontal ----

// encodeStep2 is a port of opj_dwt_encode_step2. fl and fw are offsets into the
// same slice w.
func encodeStep2(w []float32, flOff, fwOff int, end, m uint32, c float32) {
	imax := uintMin(end, m)
	if imax > 0 {
		w[fwOff-1] += (w[flOff] + w[fwOff]) * c
		fwOff += 2
		for i := uint32(1); i < imax; i++ {
			w[fwOff-1] += (w[fwOff-2] + w[fwOff]) * c
			fwOff += 2
		}
	}
	if m < end {
		w[fwOff-1] += (2 * w[fwOff-2]) * c
	}
}

// encodeStep1Combined is a port of opj_dwt_encode_step1_combined (scalar).
func encodeStep1Combined(w []float32, fwOff int, itersC1, itersC2 uint32, c1, c2 float32) {
	itersCommon := uintMin(itersC1, itersC2)
	var i uint32
	for i = 0; i < itersCommon; i++ {
		w[fwOff] *= c1
		w[fwOff+1] *= c2
		fwOff += 2
	}
	if i < itersC1 {
		w[fwOff] *= c1
	} else if i < itersC2 {
		w[fwOff+1] *= c2
	}
}

// encode1Real is a port of opj_dwt_encode_1_real.
func encode1Real(w []float32, wOff int, dn, sn, cas int32) {
	var a, b int32
	if cas == 0 {
		a, b = 0, 1
	} else {
		a, b = 1, 0
	}
	encodeStep2(w, wOff+int(a), wOff+int(b)+1, uint32(dn), uint32(intMin(dn, sn-b)), dwtAlpha)
	encodeStep2(w, wOff+int(b), wOff+int(a)+1, uint32(sn), uint32(intMin(sn, dn-a)), dwtBeta)
	encodeStep2(w, wOff+int(a), wOff+int(b)+1, uint32(dn), uint32(intMin(dn, sn-b)), dwtGamma)
	encodeStep2(w, wOff+int(b), wOff+int(a)+1, uint32(sn), uint32(intMin(sn, dn-a)), dwtDelta)
	if a == 0 {
		encodeStep1Combined(w, wOff, uint32(sn), uint32(dn), dwtInvK, dwtK)
	} else {
		encodeStep1Combined(w, wOff, uint32(dn), uint32(sn), dwtK, dwtInvK)
	}
}

// deinterleaveHF is the float32 form of opj_dwt_deinterleave_h.
func deinterleaveHF(a, b []float32, dn, sn, cas int32) {
	ld := 0
	ls := int(cas)
	for i := int32(0); i < sn; i++ {
		b[ld] = a[ls]
		ld++
		ls += 2
	}
	ld = int(sn)
	ls = int(1 - cas)
	for i := int32(0); i < dn; i++ {
		b[ld] = a[ls]
		ld++
		ls += 2
	}
}

// encodeAndDeinterleaveHOneRowReal is a port of
// opj_dwt_encode_and_deinterleave_h_one_row_real (9/7).
func encodeAndDeinterleaveHOneRowReal(row, tmp []float32, width uint32, even bool) {
	var sn int32
	if even {
		sn = int32((width + 1) >> 1)
	} else {
		sn = int32(width >> 1)
	}
	dn := int32(width) - sn
	if width == 1 {
		return
	}
	copy(tmp[:width], row[:width])
	cas := int32(1)
	if even {
		cas = 0
	}
	encode1Real(tmp, 0, dn, sn, cas)
	deinterleaveHF(tmp, row, dn, sn, cas)
}

// ---- forward 9/7 vertical ----

func fetchColsVerticalPassF(arr []float32, arrOff int, tmp []float32, height, strideWidth, cols uint32) {
	for k := uint32(0); k < height; k++ {
		var c uint32
		for c = 0; c < cols; c++ {
			tmp[nbEltsV8*k+c] = arr[arrOff+int(c+k*strideWidth)]
		}
		for ; c < nbEltsV8; c++ {
			tmp[nbEltsV8*k+c] = 0
		}
	}
}

func deinterleaveVColsF(src, dst []float32, dstOff int, dn, sn int32, strideWidth uint32, cas int32, cols uint32) {
	i := sn
	ldest := dstOff
	lsrc := int(cas) * nbEltsV8
	for k := 0; k < 2; k++ {
		for ; i > 0; i-- {
			for c := uint32(0); c < cols; c++ {
				dst[ldest+int(c)] = src[lsrc+int(c)]
			}
			ldest += int(strideWidth)
			lsrc += 2 * nbEltsV8
		}
		ldest = dstOff + int(sn)*int(strideWidth)
		lsrc = int(1-cas) * nbEltsV8
		i = dn
	}
}

// encodeV8Step1 is a port of opj_v8dwt_encode_step1 (scalar). base is a float
// offset into tmp.
func encodeV8Step1(tmp []float32, base int, end uint32, cst float32) {
	for i := uint32(0); i < end; i++ {
		for c := 0; c < nbEltsV8; c++ {
			tmp[base+int(i)*2*nbEltsV8+c] *= cst
		}
	}
}

// encodeV8Step2 is a port of opj_v8dwt_encode_step2 (scalar). flOff/fwOff are
// float offsets into tmp.
func encodeV8Step2(tmp []float32, flOff, fwOff int, end, m uint32, cst float32) {
	imax := uintMin(end, m)
	if imax > 0 {
		for c := 0; c < nbEltsV8; c++ {
			tmp[fwOff-nbEltsV8+c] += (tmp[flOff+c] + tmp[fwOff+c]) * cst
		}
		fwOff += 2 * nbEltsV8
		for i := uint32(1); i < imax; i++ {
			for c := 0; c < nbEltsV8; c++ {
				tmp[fwOff-nbEltsV8+c] += (tmp[fwOff-2*nbEltsV8+c] + tmp[fwOff+c]) * cst
			}
			fwOff += 2 * nbEltsV8
		}
	}
	if m < end {
		for c := 0; c < nbEltsV8; c++ {
			tmp[fwOff-nbEltsV8+c] += (2 * tmp[fwOff-2*nbEltsV8+c]) * cst
		}
	}
}

// encodeAndDeinterleaveVReal is a port of
// opj_dwt_encode_and_deinterleave_v_real (9/7).
func encodeAndDeinterleaveVReal(arr []float32, arrOff int, tmp []float32, height uint32, even bool, strideWidth, cols uint32) {
	var sn int32
	if even {
		sn = int32((height + 1) >> 1)
	} else {
		sn = int32(height >> 1)
	}
	dn := int32(height) - sn

	if height == 1 {
		return
	}

	fetchColsVerticalPassF(arr, arrOff, tmp, height, strideWidth, cols)

	var a, b int32
	if even {
		a, b = 0, 1
	} else {
		a, b = 1, 0
	}
	encodeV8Step2(tmp, int(a)*nbEltsV8, int(b+1)*nbEltsV8, uint32(dn), uint32(intMin(dn, sn-b)), dwtAlpha)
	encodeV8Step2(tmp, int(b)*nbEltsV8, int(a+1)*nbEltsV8, uint32(sn), uint32(intMin(sn, dn-a)), dwtBeta)
	encodeV8Step2(tmp, int(a)*nbEltsV8, int(b+1)*nbEltsV8, uint32(dn), uint32(intMin(dn, sn-b)), dwtGamma)
	encodeV8Step2(tmp, int(b)*nbEltsV8, int(a+1)*nbEltsV8, uint32(sn), uint32(intMin(sn, dn-a)), dwtDelta)
	encodeV8Step1(tmp, int(b)*nbEltsV8, uint32(dn), dwtK)
	encodeV8Step1(tmp, int(a)*nbEltsV8, uint32(sn), dwtInvK)

	casv := int32(0)
	if !even {
		casv = 1
	}
	deinterleaveVColsF(tmp, arr, arrOff, dn, sn, strideWidth, casv, cols)
}

// encodeProcedure97 is the 9/7 specialization of opj_dwt_encode_procedure.
func encodeProcedure97(fdata []float32, tc *TileComponent) bool {
	w := uint32(tc.X1 - tc.X0)
	l := int32(tc.Numresolutions) - 1
	curi := int(l)

	dataSize := maxResolution(tc.Resolutions, tc.Numresolutions)
	bj := make([]float32, uint64(dataSize)*nbEltsV8)

	for i := l; i > 0; i-- {
		lasti := curi - 1
		rw := uint32(tc.Resolutions[curi].X1 - tc.Resolutions[curi].X0)
		rh := uint32(tc.Resolutions[curi].Y1 - tc.Resolutions[curi].Y0)

		casRow := tc.Resolutions[curi].X0 & 1
		casCol := tc.Resolutions[curi].Y0 & 1

		var j uint32
		for j = 0; j+nbEltsV8-1 < rw; j += nbEltsV8 {
			encodeAndDeinterleaveVReal(fdata, int(j), bj, rh, casCol == 0, w, nbEltsV8)
		}
		if j < rw {
			encodeAndDeinterleaveVReal(fdata, int(j), bj, rh, casCol == 0, w, rw-j)
		}

		for j = 0; j < rh; j++ {
			encodeAndDeinterleaveHOneRowReal(fdata[j*w:], bj, rw, casRow == 0)
		}

		curi = lasti
	}
	return true
}

// EncodeReal is a port of opj_dwt_encode_real: whole-tile forward 9/7 transform.
// tc.Data holds float32 bit patterns and is transformed in place.
func EncodeReal(tc *TileComponent) bool {
	fdata := make([]float32, len(tc.Data))
	for i, v := range tc.Data {
		fdata[i] = f32frombits(v)
	}
	ok := encodeProcedure97(fdata, tc)
	for i, v := range fdata {
		tc.Data[i] = f32bits(v)
	}
	return ok
}

// ---- inverse 9/7 ----

// v8dwt is a port of opj_v8dwt_t. wavelet holds n groups of NB_ELTS_V8 floats
// (opj_v8_t), stored flat as []float32 of length n*NB_ELTS_V8.
type v8dwt struct {
	wavelet                        []float32
	dn, sn, cas                    int32
	winLX0, winLX1, winHX0, winHX1 uint32
}

// v8dwtDecodeStep1 is a port of opj_v8dwt_decode_step1 (scalar). wUnitOff is in
// opj_v8_t units.
func v8dwtDecodeStep1(w []float32, wUnitOff int, start, end uint32, c float32) {
	base := wUnitOff * nbEltsV8
	for i := start; i < end; i++ {
		for e := 0; e < nbEltsV8; e++ {
			w[base+int(i)*2*nbEltsV8+e] *= c
		}
	}
}

// v8dwtDecodeStep2 is a port of opj_v8dwt_decode_step2 (scalar). lUnitOff and
// wUnitOff are in opj_v8_t units.
func v8dwtDecodeStep2(w []float32, lUnitOff, wUnitOff int, start, end, m uint32, c float32) {
	imax := uintMin(end, m)
	wpos := wUnitOff * nbEltsV8
	lpos := lUnitOff * nbEltsV8
	if start > 0 {
		wpos += 2 * nbEltsV8 * int(start)
		lpos = wpos - 2*nbEltsV8
	}
	for i := start; i < imax; i++ {
		for e := 0; e < nbEltsV8; e++ {
			w[wpos-nbEltsV8+e] = w[wpos-nbEltsV8+e] + (w[lpos+e]+w[wpos+e])*c
		}
		lpos = wpos
		wpos += 2 * nbEltsV8
	}
	if m < end {
		c2 := c + c
		for e := 0; e < nbEltsV8; e++ {
			w[wpos-nbEltsV8+e] = w[wpos-nbEltsV8+e] + w[lpos+e]*c2
		}
	}
}

// v8dwtDecode is a port of opj_v8dwt_decode: inverse 9/7 transform in 1-D.
func v8dwtDecode(d *v8dwt) {
	var a, b int32
	if d.cas == 0 {
		if !((d.dn > 0) || (d.sn > 1)) {
			return
		}
		a, b = 0, 1
	} else {
		if !((d.sn > 0) || (d.dn > 1)) {
			return
		}
		a, b = 1, 0
	}
	w := d.wavelet
	v8dwtDecodeStep1(w, int(a), d.winLX0, d.winLX1, dwtK)
	v8dwtDecodeStep1(w, int(b), d.winHX0, d.winHX1, dwtTwoInvK)
	v8dwtDecodeStep2(w, int(b), int(a+1), d.winLX0, d.winLX1,
		uint32(intMin(d.sn, d.dn-a)), -dwtDelta)
	v8dwtDecodeStep2(w, int(a), int(b+1), d.winHX0, d.winHX1,
		uint32(intMin(d.dn, d.sn-b)), -dwtGamma)
	v8dwtDecodeStep2(w, int(b), int(a+1), d.winLX0, d.winLX1,
		uint32(intMin(d.sn, d.dn-a)), -dwtBeta)
	v8dwtDecodeStep2(w, int(a), int(b+1), d.winHX0, d.winHX1,
		uint32(intMin(d.dn, d.sn-b)), -dwtAlpha)
}

// v8dwtInterleaveH is a port of opj_v8dwt_interleave_h. aOff is a float offset
// into a.
func v8dwtInterleaveH(d *v8dwt, a []float32, aOff int, width, remainingHeight uint32) {
	casF := int(d.cas)
	biBase := casF * nbEltsV8
	x0 := d.winLX0
	x1 := d.winLX1
	for k := 0; k < 2; k++ {
		for i := x0; i < x1; i++ {
			j := aOff + int(i)
			dst := biBase + int(i)*2*nbEltsV8
			for r := uint32(0); r < remainingHeight; r++ {
				d.wavelet[dst+int(r)] = a[j]
				j += int(width)
			}
		}
		biBase = int(1-d.cas) * nbEltsV8
		aOff += int(d.sn)
		x0 = d.winHX0
		x1 = d.winHX1
	}
}

// v8dwtInterleaveV is a port of opj_v8dwt_interleave_v. aOff is a float offset
// into a.
func v8dwtInterleaveV(d *v8dwt, a []float32, aOff int, width, nbEltsRead uint32) {
	biBase := int(d.cas)
	for i := d.winLX0; i < d.winLX1; i++ {
		dst := (biBase + int(i)*2) * nbEltsV8
		src := aOff + int(i)*int(width)
		for e := uint32(0); e < nbEltsRead; e++ {
			d.wavelet[dst+int(e)] = a[src+int(e)]
		}
	}
	aOff += int(d.sn) * int(width)
	biBase = int(1 - d.cas)
	for i := d.winHX0; i < d.winHX1; i++ {
		dst := (biBase + int(i)*2) * nbEltsV8
		src := aOff + int(i)*int(width)
		for e := uint32(0); e < nbEltsRead; e++ {
			d.wavelet[dst+int(e)] = a[src+int(e)]
		}
	}
}

// DecodeTile97 is a port of opj_dwt_decode_tile_97: whole-tile inverse 9/7
// transform. tc.Data holds float32 bit patterns and is reconstructed in place.
func DecodeTile97(tc *TileComponent, numres uint32) bool {
	if numres == 1 {
		return true
	}

	fdata := make([]float32, len(tc.Data))
	for i, v := range tc.Data {
		fdata[i] = f32frombits(v)
	}
	ok := decodeTile97(fdata, tc, numres)
	for i, v := range fdata {
		tc.Data[i] = f32bits(v)
	}
	return ok
}

func decodeTile97(fdata []float32, tc *TileComponent, numres uint32) bool {
	res := tc.Resolutions
	ri := 0
	rw := uint32(res[0].X1 - res[0].X0)
	rh := uint32(res[0].Y1 - res[0].Y0)

	w := uint32(tc.Resolutions[tc.MinimumNumResolutions-1].X1 -
		tc.Resolutions[tc.MinimumNumResolutions-1].X0)

	dataSize := maxResolution(res, numres)
	var h, v v8dwt
	h.wavelet = make([]float32, uint64(dataSize)*nbEltsV8)
	v.wavelet = h.wavelet

	for numres--; numres > 0; numres-- {
		aOff := 0
		h.sn = int32(rw)
		v.sn = int32(rh)

		ri++
		rw = uint32(res[ri].X1 - res[ri].X0)
		rh = uint32(res[ri].Y1 - res[ri].Y0)

		h.dn = int32(rw) - h.sn
		h.cas = res[ri].X0 % 2
		h.winLX0 = 0
		h.winLX1 = uint32(h.sn)
		h.winHX0 = 0
		h.winHX1 = uint32(h.dn)

		var j uint32
		for j = 0; j+(nbEltsV8-1) < rh; j += nbEltsV8 {
			v8dwtInterleaveH(&h, fdata, aOff, w, nbEltsV8)
			v8dwtDecode(&h)
			for k := uint32(0); k < rw; k++ {
				for l := 0; l < nbEltsV8; l++ {
					fdata[aOff+int(k)+int(w)*l] = h.wavelet[int(k)*nbEltsV8+l]
				}
			}
			aOff += int(w) * nbEltsV8
		}
		if j < rh {
			v8dwtInterleaveH(&h, fdata, aOff, w, rh-j)
			v8dwtDecode(&h)
			for k := uint32(0); k < rw; k++ {
				for l := uint32(0); l < rh-j; l++ {
					fdata[aOff+int(k)+int(w)*int(l)] = h.wavelet[int(k)*nbEltsV8+int(l)]
				}
			}
		}

		v.dn = int32(rh) - v.sn
		v.cas = res[ri].Y0 % 2
		v.winLX0 = 0
		v.winLX1 = uint32(v.sn)
		v.winHX0 = 0
		v.winHX1 = uint32(v.dn)

		aOff = 0
		for j = rw; j > (nbEltsV8 - 1); j -= nbEltsV8 {
			v8dwtInterleaveV(&v, fdata, aOff, w, nbEltsV8)
			v8dwtDecode(&v)
			for k := uint32(0); k < rh; k++ {
				for e := 0; e < nbEltsV8; e++ {
					fdata[aOff+int(k)*int(w)+e] = v.wavelet[int(k)*nbEltsV8+e]
				}
			}
			aOff += nbEltsV8
		}
		if rw&(nbEltsV8-1) != 0 {
			j = rw & (nbEltsV8 - 1)
			v8dwtInterleaveV(&v, fdata, aOff, w, j)
			v8dwtDecode(&v)
			for k := uint32(0); k < rh; k++ {
				for e := uint32(0); e < j; e++ {
					fdata[aOff+int(k)*int(w)+int(e)] = v.wavelet[int(k)*nbEltsV8+int(e)]
				}
			}
		}
	}
	return true
}
