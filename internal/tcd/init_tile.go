package tcd

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	"github.com/mgilbir/gopenjpeg/internal/tgt"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// initTile ports opj_tcd_init_tile: computes the tile / tile-component /
// resolution / band / precinct / code-block geometry and allocates the
// corresponding structures. isEncoder selects the encode vs decode arms.
//
// Every overflow and bounds guard of the C reference is preserved.
func (t *TCD) initTile(tileNo uint32, isEncoder bool, mgr *event.Manager) error {
	cp := t.CP
	tcp := &cp.Tcps[tileNo]
	tl := t.tile()
	img := t.Image

	p := tileNo % cp.Tw // tile coordinates
	q := tileNo / cp.Tw

	// 4 borders of the tile rescale on the image if necessary.
	tx0 := cp.Tx0 + p*cp.Tdx
	tl.X0 = int32(opjmath.UintMax(tx0, img.X0))
	tl.X1 = int32(opjmath.UintMin(opjmath.UintAdds(tx0, cp.Tdx), img.X1))
	if tl.X0 < 0 || tl.X1 <= tl.X0 {
		mgr.Errorf("Tile X coordinates are not supported\n")
		return errTileGeometry
	}
	ty0 := cp.Ty0 + q*cp.Tdy
	tl.Y0 = int32(opjmath.UintMax(ty0, img.Y0))
	tl.Y1 = int32(opjmath.UintMin(opjmath.UintAdds(ty0, cp.Tdy), img.Y1))
	if tl.Y0 < 0 || tl.Y1 <= tl.Y0 {
		mgr.Errorf("Tile Y coordinates are not supported\n")
		return errTileGeometry
	}

	// testcase 1888.pdf.asan.35.988
	if tcp.TCCPs[0].Numresolutions == 0 {
		mgr.Errorf("tiles require at least one resolution\n")
		return errTileGeometry
	}

	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		tccp := &tcp.TCCPs[compno]
		tilec := &tl.Comps[compno]
		imageComp := &img.Comps[compno]

		imageComp.ResnoDecoded = 0
		tilec.X0 = opjmath.IntCeildiv(tl.X0, int32(imageComp.Dx))
		tilec.Y0 = opjmath.IntCeildiv(tl.Y0, int32(imageComp.Dy))
		tilec.X1 = opjmath.IntCeildiv(tl.X1, int32(imageComp.Dx))
		tilec.Y1 = opjmath.IntCeildiv(tl.Y1, int32(imageComp.Dy))
		tilec.Compno = compno

		tilec.Numresolutions = tccp.Numresolutions
		if tccp.Numresolutions < cp.MDec.MReduce {
			tilec.MinimumNumResolutions = 1
		} else {
			tilec.MinimumNumResolutions = tccp.Numresolutions - cp.MDec.MReduce
		}

		if isEncoder {
			w := uint64(tilec.X1 - tilec.X0)
			h := uint64(tilec.Y1 - tilec.Y0)
			if h > 0 && w > math.MaxUint64/h {
				mgr.Errorf("Size of tile data exceeds system limits\n")
				return errTileGeometry
			}
			dataSize := w * h
			if math.MaxUint64/4 < dataSize {
				mgr.Errorf("Size of tile data exceeds system limits\n")
				return errTileGeometry
			}
			tilec.DataSizeNeeded = dataSize * 4
		}

		// (Re)allocate the resolutions array.
		tilec.DataWin = nil
		tilec.WinX0, tilec.WinY0, tilec.WinX1, tilec.WinY1 = 0, 0, 0, 0
		if uint32(len(tilec.Resolutions)) < tilec.Numresolutions {
			tilec.Resolutions = make([]tile.Resolution, tilec.Numresolutions)
		} else {
			// Zero the reused entries, matching the memset in the C realloc path.
			for i := range tilec.Resolutions[:tilec.Numresolutions] {
				tilec.Resolutions[i] = tile.Resolution{}
			}
		}
		tilec.ResolutionsSize = tilec.Numresolutions * 4

		levelNo := tilec.Numresolutions
		stepIdx := 0 // index into tccp.Stepsizes

		for resno := uint32(0); resno < tilec.Numresolutions; resno++ {
			res := &tilec.Resolutions[resno]
			levelNo--

			res.X0 = opjmath.IntCeildivpow2(tilec.X0, int32(levelNo))
			res.Y0 = opjmath.IntCeildivpow2(tilec.Y0, int32(levelNo))
			res.X1 = opjmath.IntCeildivpow2(tilec.X1, int32(levelNo))
			res.Y1 = opjmath.IntCeildivpow2(tilec.Y1, int32(levelNo))

			pdx := tccp.Prcw[resno]
			pdy := tccp.Prch[resno]
			tlPrcXStart := opjmath.IntFloordivpow2(res.X0, int32(pdx)) << pdx
			tlPrcYStart := opjmath.IntFloordivpow2(res.Y0, int32(pdy)) << pdy
			var brPrcXEnd, brPrcYEnd int32
			{
				tmp := uint32(opjmath.IntCeildivpow2(res.X1, int32(pdx))) << pdx
				if tmp > uint32(math.MaxInt32) {
					mgr.Errorf("Integer overflow\n")
					return errIntegerOverflow
				}
				brPrcXEnd = int32(tmp)
			}
			{
				tmp := uint32(opjmath.IntCeildivpow2(res.Y1, int32(pdy))) << pdy
				if tmp > uint32(math.MaxInt32) {
					mgr.Errorf("Integer overflow\n")
					return errIntegerOverflow
				}
				brPrcYEnd = int32(tmp)
			}

			if res.X0 == res.X1 {
				res.Pw = 0
			} else {
				res.Pw = uint32((brPrcXEnd - tlPrcXStart) >> pdx)
			}
			if res.Y0 == res.Y1 {
				res.Ph = 0
			} else {
				res.Ph = uint32((brPrcYEnd - tlPrcYStart) >> pdy)
			}

			if res.Pw != 0 && (^uint32(0)/res.Pw) < res.Ph {
				mgr.Errorf("Size of tile data exceeds system limits\n")
				return errTileGeometry
			}
			nbPrecincts := res.Pw * res.Ph

			var tlcbgxstart, tlcbgystart int32
			var cbgwidthexpn, cbgheightexpn uint32
			if resno == 0 {
				tlcbgxstart = tlPrcXStart
				tlcbgystart = tlPrcYStart
				cbgwidthexpn = pdx
				cbgheightexpn = pdy
				res.Numbands = 1
			} else {
				tlcbgxstart = opjmath.IntCeildivpow2(tlPrcXStart, 1)
				tlcbgystart = opjmath.IntCeildivpow2(tlPrcYStart, 1)
				cbgwidthexpn = pdx - 1
				cbgheightexpn = pdy - 1
				res.Numbands = 3
			}

			cblkwidthexpn := opjmath.UintMin(tccp.Cblkw, cbgwidthexpn)
			cblkheightexpn := opjmath.UintMin(tccp.Cblkh, cbgheightexpn)

			for bandno := uint32(0); bandno < res.Numbands; bandno++ {
				band := &res.Bands[bandno]
				stepSize := &tccp.Stepsizes[stepIdx]

				if resno == 0 {
					band.Bandno = 0
					band.X0 = opjmath.IntCeildivpow2(tilec.X0, int32(levelNo))
					band.Y0 = opjmath.IntCeildivpow2(tilec.Y0, int32(levelNo))
					band.X1 = opjmath.IntCeildivpow2(tilec.X1, int32(levelNo))
					band.Y1 = opjmath.IntCeildivpow2(tilec.Y1, int32(levelNo))
				} else {
					band.Bandno = bandno + 1
					x0b := int32(band.Bandno & 1)
					y0b := int32(band.Bandno >> 1)
					band.X0 = opjmath.Int64Ceildivpow2(int64(tilec.X0)-(int64(x0b)<<levelNo), int32(levelNo+1))
					band.Y0 = opjmath.Int64Ceildivpow2(int64(tilec.Y0)-(int64(y0b)<<levelNo), int32(levelNo+1))
					band.X1 = opjmath.Int64Ceildivpow2(int64(tilec.X1)-(int64(x0b)<<levelNo), int32(levelNo+1))
					band.Y1 = opjmath.Int64Ceildivpow2(int64(tilec.Y1)-(int64(y0b)<<levelNo), int32(levelNo+1))
				}

				if isEncoder && tile.IsBandEmpty(band) {
					stepIdx++
					continue
				}

				// Table E-1 sub-band gains; see BUG_WEIRD_TWO_INVK in dwt.c.
				var log2Gain int32
				switch {
				case !isEncoder && tccp.Qmfbid == 0:
					log2Gain = 0
				case band.Bandno == 0:
					log2Gain = 0
				case band.Bandno == 3:
					log2Gain = 2
				default:
					log2Gain = 1
				}
				rb := int32(imageComp.Prec) + log2Gain
				band.Stepsize = float32((1.0 + float64(stepSize.Mant)/2048.0) *
					math.Pow(2.0, float64(rb-stepSize.Expn)))
				band.Numbps = stepSize.Expn + int32(tccp.Numgbits) - 1

				// Allocate precincts.
				if uint32(len(band.Precincts)) < nbPrecincts {
					band.Precincts = make([]tile.Precinct, nbPrecincts)
				} else {
					for i := range band.Precincts[:nbPrecincts] {
						band.Precincts[i] = tile.Precinct{}
					}
				}
				band.PrecinctsDataSize = nbPrecincts

				for precno := uint32(0); precno < nbPrecincts; precno++ {
					prc := &band.Precincts[precno]
					cbgxstart := tlcbgxstart + int32(precno%res.Pw)*(1<<cbgwidthexpn)
					cbgystart := tlcbgystart + int32(precno/res.Pw)*(1<<cbgheightexpn)
					cbgxend := cbgxstart + (1 << cbgwidthexpn)
					cbgyend := cbgystart + (1 << cbgheightexpn)

					prc.X0 = opjmath.IntMax(cbgxstart, band.X0)
					prc.Y0 = opjmath.IntMax(cbgystart, band.Y0)
					prc.X1 = opjmath.IntMin(cbgxend, band.X1)
					prc.Y1 = opjmath.IntMin(cbgyend, band.Y1)

					tlcblkxstart := opjmath.IntFloordivpow2(prc.X0, int32(cblkwidthexpn)) << cblkwidthexpn
					tlcblkystart := opjmath.IntFloordivpow2(prc.Y0, int32(cblkheightexpn)) << cblkheightexpn
					brcblkxend := opjmath.IntCeildivpow2(prc.X1, int32(cblkwidthexpn)) << cblkwidthexpn
					brcblkyend := opjmath.IntCeildivpow2(prc.Y1, int32(cblkheightexpn)) << cblkheightexpn
					prc.Cw = uint32((brcblkxend - tlcblkxstart) >> cblkwidthexpn)
					prc.Ch = uint32((brcblkyend - tlcblkystart) >> cblkheightexpn)

					nbCodeBlocks := prc.Cw * prc.Ch
					sizeofBlock := uint32(sizeofCblkDec)
					if isEncoder {
						sizeofBlock = sizeofCblkEnc
					}
					if (^uint32(0) / sizeofBlock) < nbCodeBlocks {
						mgr.Errorf("Size of code block data exceeds system limits\n")
						return errTileGeometry
					}

					if isEncoder {
						if uint32(len(prc.CblksEnc)) < nbCodeBlocks {
							prc.CblksEnc = make([]tile.CblkEnc, nbCodeBlocks)
						}
					} else {
						if uint32(len(prc.CblksDec)) < nbCodeBlocks {
							prc.CblksDec = make([]tile.CblkDec, nbCodeBlocks)
						} else {
							for i := range prc.CblksDec[:nbCodeBlocks] {
								prc.CblksDec[i] = tile.CblkDec{}
							}
						}
					}
					prc.BlockSize = nbCodeBlocks * sizeofBlock

					// Tag trees. In C, opj_tgt_create returns NULL for a zero-leaf
					// precinct (cw==0 or ch==0), which only occurs in empty bands
					// that tier-2 skips. Go's tier-2 unconditionally calls
					// tree.Reset(), which is not nil-safe, so we always create a
					// (degenerate) tree. Such a tree is only ever Reset (never
					// SetValue/Decode) for a 0-code-block precinct, so this does
					// not affect the decoded result.
					cwLeafs := prc.Cw
					if cwLeafs == 0 {
						cwLeafs = 1
					}
					chLeafs := prc.Ch
					if chLeafs == 0 {
						chLeafs = 1
					}
					if it, err := tgt.Create(cwLeafs, chLeafs, mgr); err == nil {
						prc.Incltree = it
					}
					if it, err := tgt.Create(cwLeafs, chLeafs, mgr); err == nil {
						prc.Imsbtree = it
					}

					for cblkno := uint32(0); cblkno < nbCodeBlocks; cblkno++ {
						cblkxstart := tlcblkxstart + int32(cblkno%prc.Cw)*(1<<cblkwidthexpn)
						cblkystart := tlcblkystart + int32(cblkno/prc.Cw)*(1<<cblkheightexpn)
						cblkxend := cblkxstart + (1 << cblkwidthexpn)
						cblkyend := cblkystart + (1 << cblkheightexpn)

						if isEncoder {
							cb := &prc.CblksEnc[cblkno]
							if !encCblkAllocate(cb) {
								return errAlloc
							}
							cb.X0 = opjmath.IntMax(cblkxstart, prc.X0)
							cb.Y0 = opjmath.IntMax(cblkystart, prc.Y0)
							cb.X1 = opjmath.IntMin(cblkxend, prc.X1)
							cb.Y1 = opjmath.IntMin(cblkyend, prc.Y1)
							if !encCblkAllocateData(cb) {
								return errAlloc
							}
						} else {
							cb := &prc.CblksDec[cblkno]
							decCblkAllocate(cb)
							cb.X0 = opjmath.IntMax(cblkxstart, prc.X0)
							cb.Y0 = opjmath.IntMax(cblkystart, prc.Y0)
							cb.X1 = opjmath.IntMin(cblkxend, prc.X1)
							cb.Y1 = opjmath.IntMin(cblkyend, prc.Y1)
						}
					}
				}
				stepIdx++
			}
		}
	}
	return nil
}

// decCblkAllocate ports opj_tcd_code_block_dec_allocate: reset a decode
// code-block, keeping any previously-allocated segment/chunk backing.
func decCblkAllocate(cb *tile.CblkDec) {
	segs := cb.Segs
	maxSegs := cb.MCurrentMaxSegs
	chunks := cb.Chunks
	numchunksalloc := cb.Numchunksalloc
	if segs == nil {
		cb.Segs = make([]tile.Seg, cparamsDefaultNbSegs)
		cb.MCurrentMaxSegs = cparamsDefaultNbSegs
		return
	}
	*cb = tile.CblkDec{}
	for i := range segs[:maxSegs] {
		segs[i] = tile.Seg{}
	}
	cb.Segs = segs
	cb.MCurrentMaxSegs = maxSegs
	cb.Chunks = chunks
	cb.Numchunksalloc = numchunksalloc
}

const cparamsDefaultNbSegs = 10 // OPJ_J2K_DEFAULT_NB_SEGS

// encCblkAllocate ports opj_tcd_code_block_enc_allocate (layers/passes only).
func encCblkAllocate(cb *tile.CblkEnc) bool {
	if cb.Layers == nil {
		cb.Layers = make([]tile.Layer, 100)
	}
	if cb.Passes == nil {
		cb.Passes = make([]tile.Pass, 100)
	}
	return true
}

// encCblkAllocateData ports opj_tcd_code_block_enc_allocate_data.
func encCblkAllocateData(cb *tile.CblkEnc) bool {
	dataSize := uint32(74) + uint32((cb.X1-cb.X0)*(cb.Y1-cb.Y0))*4
	if dataSize > cb.DataSize {
		cb.Data = make([]byte, dataSize)
		cb.DataSize = dataSize
	}
	return true
}
