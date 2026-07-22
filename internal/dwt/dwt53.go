package dwt

// This file ports the reversible 5/3 integer transform of dwt.c: the scalar
// 1-D lifting kernels, the forward/inverse row and column passes, and the
// whole-tile 2-D drivers.

// maxResolution is a port of opj_dwt_max_resolution.
func maxResolution(res []Resolution, numres uint32) uint32 {
	var mr uint32
	// C: while (--i) { ++r; ... } starting from r = res[0], i = numres.
	for i := uint32(1); i < numres; i++ {
		r := &res[i]
		if w := uint32(r.X1 - r.X0); mr < w {
			mr = w
		}
		if w := uint32(r.Y1 - r.Y0); mr < w {
			mr = w
		}
	}
	return mr
}

// dwt53 is a port of opj_dwt_t: the scratch buffer plus band sizes and cas.
type dwt53 struct {
	mem    []int32
	dn, sn int32 // high-pass / low-pass element counts
	cas    int32 // 0 = start on even coord, 1 = start on odd coord
}

// ---- inverse 5/3 horizontal ----

// idwt53HCas0 is a port of opj_idwt53_h_cas0 (scalar, one-pass version).
func idwt53HCas0(tmp []int32, sn, length int32, tiledp []int32, off int) {
	inEven := func(i int32) int32 { return tiledp[off+int(i)] }
	inOdd := func(i int32) int32 { return tiledp[off+int(sn)+int(i)] }

	var d1c, d1n, s1n, s0c, s0n int32
	var i, j int32

	s1n = inEven(0)
	d1n = inOdd(0)
	s0n = s1n - ((d1n + 1) >> 1)

	for i, j = 0, 1; i < length-3; i, j = i+2, j+1 {
		d1c = d1n
		s0c = s0n

		s1n = inEven(j)
		d1n = inOdd(j)

		s0n = s1n - ((d1c + d1n + 2) >> 2)

		tmp[i] = s0c
		tmp[i+1] = d1c + ((s0c + s0n) >> 1)
	}

	tmp[i] = s0n

	if length&1 != 0 {
		tmp[length-1] = inEven((length-1)/2) - ((d1n + 1) >> 1)
		tmp[length-2] = d1n + ((s0n + tmp[length-1]) >> 1)
	} else {
		tmp[length-1] = d1n + s0n
	}

	copy(tiledp[off:off+int(length)], tmp[:length])
}

// idwt53HCas1 is a port of opj_idwt53_h_cas1 (scalar, one-pass version).
func idwt53HCas1(tmp []int32, sn, length int32, tiledp []int32, off int) {
	inEven := func(i int32) int32 { return tiledp[off+int(sn)+int(i)] }
	inOdd := func(i int32) int32 { return tiledp[off+int(i)] }

	var s1, s2, dc, dn int32
	var i, j int32

	s1 = inEven(1)
	dc = inOdd(0) - ((inEven(0) + s1 + 2) >> 2)
	tmp[0] = inEven(0) + dc

	evenAdj := int32(0)
	if length&1 == 0 {
		evenAdj = 1
	}
	for i, j = 1, 1; i < (length - 2 - evenAdj); i, j = i+2, j+1 {
		s2 = inEven(j + 1)

		dn = inOdd(j) - ((s1 + s2 + 2) >> 2)
		tmp[i] = dc
		tmp[i+1] = s1 + ((dn + dc) >> 1)

		dc = dn
		s1 = s2
	}

	tmp[i] = dc

	if length&1 == 0 {
		dn = inOdd(length/2-1) - ((s1 + 1) >> 1)
		tmp[length-2] = s1 + ((dn + dc) >> 1)
		tmp[length-1] = dn
	} else {
		tmp[length-1] = s1 + dc
	}

	copy(tiledp[off:off+int(length)], tmp[:length])
}

// idwt53H is a port of opj_idwt53_h: inverse 5/3 transform of one row.
func idwt53H(d *dwt53, tiledp []int32, off int) {
	sn := d.sn
	length := sn + d.dn
	if d.cas == 0 {
		if length > 1 {
			idwt53HCas0(d.mem, sn, length, tiledp, off)
		}
		// else: unmodified value
	} else {
		switch {
		case length == 1:
			tiledp[off] /= 2
		case length == 2:
			out := d.mem
			inEven := tiledp[off+int(sn)]
			inOdd := tiledp[off]
			out[1] = inOdd - ((inEven + 1) >> 1)
			out[0] = inEven + out[1]
			copy(tiledp[off:off+int(length)], d.mem[:length])
		case length > 2:
			idwt53HCas1(d.mem, sn, length, tiledp, off)
		}
	}
}

// ---- inverse 5/3 vertical (per-column scalar) ----

// idwt3VCas0 is a port of opj_idwt3_v_cas0.
func idwt3VCas0(tmp []int32, sn, length int32, tiledp []int32, colOff int, stride int) {
	col := func(i int32) int32 { return tiledp[colOff+int(i)*stride] }
	setCol := func(i int32, v int32) { tiledp[colOff+int(i)*stride] = v }

	var d1c, d1n, s1n, s0c, s0n int32
	var i, j int32

	s1n = col(0)
	d1n = col(sn)
	s0n = s1n - ((d1n + 1) >> 1)

	for i, j = 0, 0; i < length-3; i, j = i+2, j+1 {
		d1c = d1n
		s0c = s0n

		s1n = col(j + 1)
		d1n = col(sn + j + 1)

		s0n = s1n - ((d1c + d1n + 2) >> 2)

		tmp[i] = s0c
		tmp[i+1] = d1c + ((s0c + s0n) >> 1)
	}

	tmp[i] = s0n

	if length&1 != 0 {
		tmp[length-1] = col((length-1)/2) - ((d1n + 1) >> 1)
		tmp[length-2] = d1n + ((s0n + tmp[length-1]) >> 1)
	} else {
		tmp[length-1] = d1n + s0n
	}

	for i = 0; i < length; i++ {
		setCol(i, tmp[i])
	}
}

// idwt3VCas1 is a port of opj_idwt3_v_cas1.
func idwt3VCas1(tmp []int32, sn, length int32, tiledp []int32, colOff int, stride int) {
	inEven := func(i int32) int32 { return tiledp[colOff+int(sn+i)*stride] }
	inOdd := func(i int32) int32 { return tiledp[colOff+int(i)*stride] }

	var s1, s2, dc, dn int32
	var i, j int32

	s1 = inEven(1)
	dc = inOdd(0) - ((inEven(0) + s1 + 2) >> 2)
	tmp[0] = inEven(0) + dc

	evenAdj := int32(0)
	if length&1 == 0 {
		evenAdj = 1
	}
	for i, j = 1, 1; i < (length - 2 - evenAdj); i, j = i+2, j+1 {
		s2 = inEven(j + 1)

		dn = inOdd(j) - ((s1 + s2 + 2) >> 2)
		tmp[i] = dc
		tmp[i+1] = s1 + ((dn + dc) >> 1)

		dc = dn
		s1 = s2
	}
	tmp[i] = dc
	if length&1 == 0 {
		dn = inOdd(length/2-1) - ((s1 + 1) >> 1)
		tmp[length-2] = s1 + ((dn + dc) >> 1)
		tmp[length-1] = dn
	} else {
		tmp[length-1] = s1 + dc
	}

	for i = 0; i < length; i++ {
		tiledp[colOff+int(i)*stride] = tmp[i]
	}
}

// idwt53V is a port of opj_idwt53_v (scalar per-column path) for nbCols columns.
func idwt53V(d *dwt53, tiledp []int32, colOff int, stride int, nbCols int32) {
	sn := d.sn
	length := sn + d.dn
	if d.cas == 0 {
		if length > 1 {
			for c := int32(0); c < nbCols; c++ {
				idwt3VCas0(d.mem, sn, length, tiledp, colOff+int(c), stride)
			}
		}
		// else len == 1: unmodified value
	} else {
		switch {
		case length == 1:
			for c := int32(0); c < nbCols; c++ {
				tiledp[colOff+int(c)] /= 2
			}
		case length == 2:
			out := d.mem
			for c := int32(0); c < nbCols; c++ {
				cc := colOff + int(c)
				inEven := tiledp[cc+int(sn)*stride]
				inOdd := tiledp[cc]
				out[1] = inOdd - ((inEven + 1) >> 1)
				out[0] = inEven + out[1]
				for i := int32(0); i < length; i++ {
					tiledp[cc+int(i)*stride] = out[i]
				}
			}
		case length > 2:
			for c := int32(0); c < nbCols; c++ {
				idwt3VCas1(d.mem, sn, length, tiledp, colOff+int(c), stride)
			}
		}
	}
}

// DecodeTile is a port of opj_dwt_decode_tile: whole-tile inverse 5/3 transform.
// It reconstructs tc.Data in place for numres resolution levels. numThreads>1
// fans the per-level horizontal (row) and vertical (column) passes across that
// many goroutines, mirroring the opj_thread_pool jobs of opj_dwt_decode_tile;
// the result is identical to the sequential decode.
func DecodeTile(tc *TileComponent, numres uint32, numThreads int) bool {
	tr := tc.Resolutions
	rw := uint32(tr[0].X1 - tr[0].X0)
	rh := uint32(tr[0].Y1 - tr[0].Y0)

	w := uint32(tc.Resolutions[tc.MinimumNumResolutions-1].X1 -
		tc.Resolutions[tc.MinimumNumResolutions-1].X0)

	if numres == 1 || w == 0 {
		return true
	}

	hMem := maxResolution(tr, numres)

	ti := 0 // index into tr
	for numres--; numres > 0; numres-- {
		tiledp := tc.Data
		ti++
		snH := int32(rw)
		snV := int32(rh)

		rw = uint32(tr[ti].X1 - tr[ti].X0)
		rh = uint32(tr[ti].Y1 - tr[ti].Y0)

		dnH := int32(rw) - snH
		casH := tr[ti].X0 % 2

		// Horizontal pass: one independent inverse transform per row.
		parChunksI32(numThreads, int(rh), int(hMem), func(mem []int32, js, je uint32) {
			d := &dwt53{mem: mem, sn: snH, dn: dnH, cas: casH}
			for j := js; j < je; j++ {
				idwt53H(d, tiledp, int(j*w))
			}
		})

		dnV := int32(rh) - snV
		casV := tr[ti].Y0 % 2

		// Vertical pass: one independent inverse transform per column.
		parChunksI32(numThreads, int(rw), int(hMem), func(mem []int32, js, je uint32) {
			d := &dwt53{mem: mem, sn: snV, dn: dnV, cas: casV}
			for j := js; j < je; j++ {
				idwt53V(d, tiledp, int(j), int(w), 1)
			}
		})
	}
	return true
}

// ---- forward 5/3 ----

// encodeAndDeinterleaveHOneRow is a port of
// opj_dwt_encode_and_deinterleave_h_one_row (5/3). data is the full tile buffer
// and off the row start (C: aj = tiledp + j*w); the kernel indexes data[off+k],
// so that for an empty (width 0) resolution the tail block that reads the
// neighbouring coefficient behaves exactly as the C code relying on the row
// buffer's over-allocation.
func encodeAndDeinterleaveHOneRow(data []int32, off int, tmp []int32, width uint32, even bool) {
	row := func(k int32) int32 { return data[off+int(k)] }
	setRow := func(k, v int32) { data[off+int(k)] = v }
	var sn, dn int32
	if even {
		sn = int32((width + 1) >> 1)
	} else {
		sn = int32(width >> 1)
	}
	dn = int32(width) - sn

	if even {
		if width > 1 {
			var i int32
			for i = 0; i < sn-1; i++ {
				tmp[sn+i] = row(2*i+1) - ((row(i*2) + row((i+1)*2)) >> 1)
			}
			if width%2 == 0 {
				tmp[sn+i] = row(2*i+1) - row(i*2)
			}
			setRow(0, row(0)+((tmp[sn]+tmp[sn]+2)>>2))
			for i = 1; i < dn; i++ {
				setRow(i, row(2*i)+((tmp[sn+(i-1)]+tmp[sn+i]+2)>>2))
			}
			if width%2 == 1 {
				setRow(i, row(2*i)+((tmp[sn+(i-1)]+tmp[sn+(i-1)]+2)>>2))
			}
			for k := int32(0); k < dn; k++ {
				setRow(sn+k, tmp[sn+k])
			}
		}
	} else {
		if width == 1 {
			setRow(0, row(0)*2)
		} else {
			var i int32
			tmp[sn+0] = row(0) - row(1)
			for i = 1; i < sn; i++ {
				tmp[sn+i] = row(2*i) - ((row(2*i+1) + row(2*(i-1)+1)) >> 1)
			}
			if width%2 == 1 {
				tmp[sn+i] = row(2*i) - row(2*(i-1)+1)
			}
			for i = 0; i < dn-1; i++ {
				setRow(i, row(2*i+1)+((tmp[sn+i]+tmp[sn+i+1]+2)>>2))
			}
			if width%2 == 0 {
				setRow(i, row(2*i+1)+((tmp[sn+i]+tmp[sn+i]+2)>>2))
			}
			for k := int32(0); k < dn; k++ {
				setRow(sn+k, tmp[sn+k])
			}
		}
	}
}

// fetchColsVerticalPass is a port of opj_dwt_fetch_cols_vertical_pass (int32).
func fetchColsVerticalPass(arr []int32, arrOff int, tmp []int32, height, strideWidth, cols uint32) {
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

// deinterleaveVCols is a port of opj_dwt_deinterleave_v_cols.
func deinterleaveVCols(src, dst []int32, dstOff int, dn, sn int32, strideWidth uint32, cas int32, cols uint32) {
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

// encodeAndDeinterleaveV is a port of opj_dwt_encode_and_deinterleave_v (5/3,
// scalar path).
func encodeAndDeinterleaveV(arr []int32, arrOff int, tmp []int32, height uint32, even bool, strideWidth, cols uint32) {
	var sn uint32
	if even {
		sn = (height + 1) >> 1
	} else {
		sn = height >> 1
	}
	dn := height - sn

	fetchColsVerticalPass(arr, arrOff, tmp, height, strideWidth, cols)

	sIdx := func(i int32, c int) int { return int(i)*2*nbEltsV8 + c }
	dIdx := func(i int32, c int) int { return (1+int(i)*2)*nbEltsV8 + c }

	if even {
		if height > 1 {
			var i int32
			for i = 0; i+1 < int32(sn); i++ {
				for c := 0; c < nbEltsV8; c++ {
					tmp[dIdx(i, c)] -= (tmp[sIdx(i, c)] + tmp[sIdx(i+1, c)]) >> 1
				}
			}
			if height%2 == 0 {
				for c := 0; c < nbEltsV8; c++ {
					tmp[dIdx(i, c)] -= tmp[sIdx(i, c)]
				}
			}
			for c := 0; c < nbEltsV8; c++ {
				tmp[sIdx(0, c)] += (tmp[dIdx(0, c)] + tmp[dIdx(0, c)] + 2) >> 2
			}
			for i = 1; i < int32(dn); i++ {
				for c := 0; c < nbEltsV8; c++ {
					tmp[sIdx(i, c)] += (tmp[dIdx(i-1, c)] + tmp[dIdx(i, c)] + 2) >> 2
				}
			}
			if height%2 == 1 {
				for c := 0; c < nbEltsV8; c++ {
					tmp[sIdx(i, c)] += (tmp[dIdx(i-1, c)] + tmp[dIdx(i-1, c)] + 2) >> 2
				}
			}
		}
	} else {
		if height == 1 {
			for c := 0; c < nbEltsV8; c++ {
				tmp[sIdx(0, c)] *= 2
			}
		} else {
			var i int32
			for c := 0; c < nbEltsV8; c++ {
				tmp[sIdx(0, c)] -= tmp[dIdx(0, c)]
			}
			for i = 1; i < int32(sn); i++ {
				for c := 0; c < nbEltsV8; c++ {
					tmp[sIdx(i, c)] -= (tmp[dIdx(i, c)] + tmp[dIdx(i-1, c)]) >> 1
				}
			}
			if height%2 == 1 {
				for c := 0; c < nbEltsV8; c++ {
					tmp[sIdx(i, c)] -= tmp[dIdx(i-1, c)]
				}
			}
			for i = 0; i+1 < int32(dn); i++ {
				for c := 0; c < nbEltsV8; c++ {
					tmp[dIdx(i, c)] += (tmp[sIdx(i, c)] + tmp[sIdx(i+1, c)] + 2) >> 2
				}
			}
			if height%2 == 0 {
				for c := 0; c < nbEltsV8; c++ {
					tmp[dIdx(i, c)] += (tmp[sIdx(i, c)] + tmp[sIdx(i, c)] + 2) >> 2
				}
			}
		}
	}

	casv := int32(0)
	if !even {
		casv = 1
	}
	deinterleaveVCols(tmp, arr, arrOff, int32(dn), int32(sn), strideWidth, casv, cols)
}

// encodeProcedure53 is the 5/3 specialization of opj_dwt_encode_procedure.
func encodeProcedure53(tc *TileComponent) bool {
	w := uint32(tc.X1 - tc.X0)
	l := int32(tc.Numresolutions) - 1

	curi := int(l) // index of current resolution
	tiledp := tc.Data

	dataSize := maxResolution(tc.Resolutions, tc.Numresolutions)
	bj := make([]int32, uint64(dataSize)*nbEltsV8)

	for i := l; i > 0; i-- {
		lasti := curi - 1
		rw := uint32(tc.Resolutions[curi].X1 - tc.Resolutions[curi].X0)
		rh := uint32(tc.Resolutions[curi].Y1 - tc.Resolutions[curi].Y0)
		rw1 := uint32(tc.Resolutions[lasti].X1 - tc.Resolutions[lasti].X0)
		rh1 := uint32(tc.Resolutions[lasti].Y1 - tc.Resolutions[lasti].Y0)

		casRow := tc.Resolutions[curi].X0 & 1
		casCol := tc.Resolutions[curi].Y0 & 1

		// Vertical pass
		var j uint32
		for j = 0; j+nbEltsV8-1 < rw; j += nbEltsV8 {
			encodeAndDeinterleaveV(tiledp, int(j), bj, rh, casCol == 0, w, nbEltsV8)
		}
		if j < rw {
			encodeAndDeinterleaveV(tiledp, int(j), bj, rh, casCol == 0, w, rw-j)
		}

		// Horizontal pass
		_ = rh1
		for j = 0; j < rh; j++ {
			encodeAndDeinterleaveHOneRow(tiledp, int(j*w), bj, rw, casRow == 0)
		}
		_ = rw1

		curi = lasti
	}
	return true
}

// Encode is a port of opj_dwt_encode: whole-tile forward 5/3 transform,
// transforming tc.Data in place.
func Encode(tc *TileComponent) bool {
	return encodeProcedure53(tc)
}
