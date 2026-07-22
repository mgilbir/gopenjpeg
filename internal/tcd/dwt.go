package tcd

import (
	"github.com/mgilbir/gopenjpeg/internal/dwt"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// dwtDecode ports opj_tcd_dwt_decode: run the inverse wavelet transform on every
// tile-component that must be decoded, dispatching on the reversible (5/3) vs
// irreversible (9/7) filter and on whole-tile vs windowed decoding.
func (t *TCD) dwtDecode(mgr *event.Manager) error {
	tl := t.tile()
	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		if t.UsedComponent != nil && !t.UsedComponent[compno] {
			continue
		}
		tilec := &tl.Comps[compno]
		imgComp := &t.Image.Comps[compno]
		tccp := &t.TCP.TCCPs[compno]
		numres := imgComp.ResnoDecoded + 1

		// Guard against the degenerate geometry (e.g. a 1x1 tile with more than
		// one resolution level) where the C reference performs an out-of-bounds
		// read (undefined behaviour). We validate that the coefficient buffer is
		// large enough for every level the transform will touch, and fail the
		// tile decode with an error rather than risk a Go bounds panic.
		if !t.dwtBufferSufficient(tilec, numres) {
			mgr.Errorf("Invalid tile-component geometry for inverse DWT\n")
			return errTileGeometry
		}

		dc := t.mapTileCompToDWT(tilec)
		var ok bool
		if tccp.Qmfbid == 1 {
			if t.WholeTileDecoding {
				ok = dwt.DecodeTile(dc, numres)
			} else {
				ok = dwt.DecodePartialTile(dc, numres)
			}
		} else {
			if t.WholeTileDecoding {
				ok = dwt.DecodeTile97(dc, numres)
			} else {
				ok = dwt.DecodePartial97(dc, numres)
			}
		}
		if !ok {
			return errTierDecode
		}
		// The partial paths allocate/replace tilec.DataWin via the mapped struct.
		if !t.WholeTileDecoding {
			tilec.DataWin = dc.DataWin
		}
	}
	return nil
}

// dwtBufferSufficient checks that the whole-tile coefficient buffer covers the
// largest resolution the inverse transform will access.
func (t *TCD) dwtBufferSufficient(tilec *tile.TileComp, numres uint32) bool {
	if numres == 0 || numres > tilec.Numresolutions {
		return false
	}
	if !t.WholeTileDecoding {
		return true // partial path reads the (separately-sized) window buffer
	}
	res := &tilec.Resolutions[numres-1]
	w := uint32(0)
	if tilec.MinimumNumResolutions > 0 {
		mr := &tilec.Resolutions[tilec.MinimumNumResolutions-1]
		w = uint32(mr.X1 - mr.X0)
	}
	h := uint32(res.Y1 - res.Y0)
	rw := uint32(res.X1 - res.X0)
	if w == 0 || h == 0 || rw == 0 {
		return true // transform is a no-op for empty resolutions
	}
	needed := uint64(h-1)*uint64(w) + uint64(rw)
	return needed <= uint64(len(tilec.Data))
}

// mapTileCompToDWT builds the dwt.TileComponent view of a tile-component,
// aliasing the coefficient buffers so the transform writes back in place.
func (t *TCD) mapTileCompToDWT(tilec *tile.TileComp) *dwt.TileComponent {
	out := &dwt.TileComponent{
		X0:                    tilec.X0,
		Y0:                    tilec.Y0,
		X1:                    tilec.X1,
		Y1:                    tilec.Y1,
		Numresolutions:        tilec.Numresolutions,
		MinimumNumResolutions: tilec.MinimumNumResolutions,
		Data:                  tilec.Data,
		DataWin:               tilec.DataWin,
		WinX0:                 tilec.WinX0,
		WinY0:                 tilec.WinY0,
		WinX1:                 tilec.WinX1,
		WinY1:                 tilec.WinY1,
	}
	out.Resolutions = make([]dwt.Resolution, len(tilec.Resolutions))
	for ri := range tilec.Resolutions {
		src := &tilec.Resolutions[ri]
		dst := &out.Resolutions[ri]
		dst.X0, dst.Y0, dst.X1, dst.Y1 = src.X0, src.Y0, src.X1, src.Y1
		dst.Pw, dst.Ph = src.Pw, src.Ph
		dst.Numbands = src.Numbands
		dst.WinX0, dst.WinY0, dst.WinX1, dst.WinY1 = src.WinX0, src.WinY0, src.WinX1, src.WinY1
		for bi := uint32(0); bi < src.Numbands; bi++ {
			sb := &src.Bands[bi]
			db := &dst.Bands[bi]
			db.X0, db.Y0, db.X1, db.Y1 = sb.X0, sb.Y0, sb.X1, sb.Y1
			db.Bandno = sb.Bandno
			if len(sb.Precincts) > 0 {
				db.Precincts = make([]dwt.Precinct, len(sb.Precincts))
				for pi := range sb.Precincts {
					sp := &sb.Precincts[pi]
					dp := &db.Precincts[pi]
					dp.Cw, dp.Ch = sp.Cw, sp.Ch
					if len(sp.CblksDec) > 0 {
						dp.Cblks = make([]dwt.CblkDec, len(sp.CblksDec))
						for ci := range sp.CblksDec {
							sc := &sp.CblksDec[ci]
							dp.Cblks[ci] = dwt.CblkDec{
								X0: sc.X0, Y0: sc.Y0, X1: sc.X1, Y1: sc.Y1,
								DecodedData: sc.DecodedData,
							}
						}
					}
				}
			}
		}
	}
	return out
}
