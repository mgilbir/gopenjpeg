package tcd

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/t1"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// t1Decode ports opj_tcd_t1_decode: decode all code-blocks of the tile via
// tier-1, for every component that must be decoded.
func (t *TCD) t1Decode(mgr *event.Manager) error {
	tl := t.tile()
	tcp := t.TCP

	checkPterm := false
	if tcp.NumLayersToDecode == tcp.Numlayers &&
		(tcp.TCCPs[0].Cblksty&cparams.CCPCblkStyPterm) != 0 {
		checkPterm = true
	}

	// A single reusable T1 state (the C reference uses a per-thread pool; the
	// scalar port decodes sequentially with one handle).
	state := t1.New(false)

	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		if t.UsedComponent != nil && !t.UsedComponent[compno] {
			continue
		}
		if err := t.t1DecodeComponent(state, compno, checkPterm, mgr); err != nil {
			return err
		}
	}
	return nil
}

// t1DecodeComponent ports opj_t1_decode_cblks for one tile-component.
func (t *TCD) t1DecodeComponent(state *t1.T1, compno uint32, checkPterm bool, mgr *event.Manager) error {
	tilec := &t.tile().Comps[compno]
	tccp := &t.TCP.TCCPs[compno]

	for resno := uint32(0); resno < tilec.MinimumNumResolutions; resno++ {
		res := &tilec.Resolutions[resno]
		for bandno := uint32(0); bandno < res.Numbands; bandno++ {
			band := &res.Bands[bandno]
			for precno := uint32(0); precno < res.Pw*res.Ph; precno++ {
				prec := &band.Precincts[precno]
				if !t.isSubbandAreaOfInterest(compno, resno, band.Bandno,
					uint32(prec.X0), uint32(prec.Y0), uint32(prec.X1), uint32(prec.Y1)) {
					for cblkno := uint32(0); cblkno < prec.Cw*prec.Ch; cblkno++ {
						prec.CblksDec[cblkno].DecodedData = nil
					}
					continue
				}
				for cblkno := uint32(0); cblkno < prec.Cw*prec.Ch; cblkno++ {
					cblk := &prec.CblksDec[cblkno]
					if !t.isSubbandAreaOfInterest(compno, resno, band.Bandno,
						uint32(cblk.X0), uint32(cblk.Y0), uint32(cblk.X1), uint32(cblk.Y1)) {
						cblk.DecodedData = nil
						continue
					}
					if !t.WholeTileDecoding {
						cblkW := uint32(cblk.X1 - cblk.X0)
						cblkH := uint32(cblk.Y1 - cblk.Y0)
						if cblk.DecodedData != nil {
							continue
						}
						if cblkW == 0 || cblkH == 0 {
							continue
						}
					}
					if err := t.t1DecodeBlock(state, resno, tilec, tccp, band, cblk, checkPterm, mgr); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// t1DecodeBlock ports opj_t1_clbl_decode_processor for a single code-block.
func (t *TCD) t1DecodeBlock(state *t1.T1, resno uint32, tilec *tile.TileComp,
	tccp *cparams.TCCP, band *tile.Band, cblk *tile.CblkDec, checkPterm bool, mgr *event.Manager) error {

	if !t.WholeTileDecoding {
		cblkW := uint32(cblk.X1 - cblk.X0)
		cblkH := uint32(cblk.Y1 - cblk.Y0)
		cblk.DecodedData = make([]int32, cblkW*cblkH)
	} else if cblk.DecodedData != nil {
		cblk.DecodedData = nil
	}

	tileW := uint32(tilec.Resolutions[tilec.MinimumNumResolutions-1].X1 -
		tilec.Resolutions[tilec.MinimumNumResolutions-1].X0)

	t1cblk := mapCblkDec(cblk)

	roishift := uint32(tccp.Roishift)
	if (tccp.Cblksty & cparams.CCPCblkStyHT) != 0 {
		if HTDecodeCblk == nil {
			mgr.Errorf("HTJ2K support not wired\n")
			return errHTNotWired
		}
		if ok, err := HTDecodeCblk(state, t1cblk, band.Bandno, roishift, tccp.Cblksty, checkPterm); err != nil || !ok {
			if err != nil {
				return err
			}
			return errTierDecode
		}
	} else {
		if ok, err := state.DecodeCblk(t1cblk, band.Bandno, roishift, tccp.Cblksty, checkPterm); err != nil || !ok {
			if err != nil {
				return err
			}
			return errTierDecode
		}
	}
	// Propagate any decoded_data buffer the t1 state populated.
	cblk.DecodedData = t1cblk.DecodedData

	x := cblk.X0 - band.X0
	y := cblk.Y0 - band.Y0
	if band.Bandno&1 != 0 {
		pres := &tilec.Resolutions[resno-1]
		x += pres.X1 - pres.X0
	}
	if band.Bandno&2 != 0 {
		pres := &tilec.Resolutions[resno-1]
		y += pres.Y1 - pres.Y0
	}

	var datap []int32
	if cblk.DecodedData != nil {
		datap = cblk.DecodedData
	} else {
		datap = state.Data()
	}
	cblkW := state.W()
	cblkH := state.H()

	// ROI de-scaling.
	if tccp.Roishift != 0 {
		t1.RoiShift(datap, cblkW, cblkH, roishift)
	}

	if cblk.DecodedData != nil {
		t1.Dequantize(datap, cblkW, cblkH, tccp.Qmfbid, band.Stepsize)
		return nil
	}

	// Whole-tile: place directly into tilec.Data at (x,y) with stride tileW.
	if tccp.Qmfbid == 1 {
		base := int(y)*int(tileW) + int(x)
		for j := uint32(0); j < cblkH; j++ {
			row := base + int(j)*int(tileW)
			src := int(j) * int(cblkW)
			for i := uint32(0); i < cblkW; i++ {
				tilec.Data[row+int(i)] = datap[src+int(i)] / 2
			}
		}
	} else {
		stepsize := 0.5 * band.Stepsize
		base := int(y)*int(tileW) + int(x)
		si := 0
		for j := uint32(0); j < cblkH; j++ {
			row := base + int(j)*int(tileW)
			for i := uint32(0); i < cblkW; i++ {
				tmp := float32(datap[si]) * stepsize
				tilec.Data[row+int(i)] = int32(math.Float32bits(tmp))
				si++
			}
		}
	}
	return nil
}

// mapCblkDec maps a tile.CblkDec (tcd's data model) to the t1.CodeBlockDec type
// that package t1 consumes, aliasing the chunk/segment/decoded backing slices.
func mapCblkDec(cblk *tile.CblkDec) *t1.CodeBlockDec {
	out := &t1.CodeBlockDec{
		X0:          cblk.X0,
		Y0:          cblk.Y0,
		X1:          cblk.X1,
		Y1:          cblk.Y1,
		Numbps:      cblk.Numbps,
		NumChunks:   cblk.Numchunks,
		RealNumSegs: cblk.RealNumSegs,
		Corrupted:   cblk.Corrupted,
		DecodedData: cblk.DecodedData,
	}
	if cblk.Numchunks > 0 {
		out.Chunks = make([]t1.Chunk, cblk.Numchunks)
		for i := uint32(0); i < cblk.Numchunks; i++ {
			out.Chunks[i] = t1.Chunk{Data: cblk.Chunks[i].Data, Len: cblk.Chunks[i].Len}
		}
	}
	if cblk.RealNumSegs > 0 {
		out.Segs = make([]t1.Seg, cblk.RealNumSegs)
		for i := uint32(0); i < cblk.RealNumSegs; i++ {
			out.Segs[i] = t1.Seg{Len: cblk.Segs[i].Len, RealNumPasses: cblk.Segs[i].RealNumPasses}
		}
	}
	return out
}
