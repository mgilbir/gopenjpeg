package tcd

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/mct"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	"github.com/mgilbir/gopenjpeg/internal/t2"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// aoiChecker implements t2.AOIChecker via opj_tcd_is_subband_area_of_interest,
// bound to a TCD instance.
type aoiChecker struct{ t *TCD }

func (a aoiChecker) IsSubbandAreaOfInterest(compno, resno, bandno, x0, y0, x1, y1 uint32) bool {
	return a.t.isSubbandAreaOfInterest(compno, resno, bandno, x0, y0, x1, y1)
}

// DecodeTile ports opj_tcd_decode_tile: decode the given tile from src into the
// working tile buffers. winX0..winY1 give the region of interest in grid
// reference coordinates (win==full image bounds means whole-tile decode).
// compsIndices, if non-nil, restricts decoding to those component indices.
func (t *TCD) DecodeTile(winX0, winY0, winX1, winY1 uint32, compsIndices []uint32,
	src []byte, maxLength, tileNo uint32, mgr *event.Manager) error {

	t.TcdTileno = tileNo
	t.TCP = &t.CP.Tcps[tileNo]
	t.WinX0, t.WinY0, t.WinX1, t.WinY1 = winX0, winY0, winX1, winY1
	t.WholeTileDecoding = true

	t.UsedComponent = nil
	if len(compsIndices) > 0 {
		used := make([]bool, t.Image.Numcomps)
		for _, ci := range compsIndices {
			if ci < uint32(len(used)) {
				used[ci] = true
			}
		}
		t.UsedComponent = used
	}

	for compno := uint32(0); compno < t.Image.Numcomps; compno++ {
		if t.UsedComponent != nil && !t.UsedComponent[compno] {
			continue
		}
		if !t.isWholeTilecompDecoding(compno) {
			t.WholeTileDecoding = false
			break
		}
	}

	tl := t.tile()

	if t.WholeTileDecoding {
		for compno := uint32(0); compno < t.Image.Numcomps; compno++ {
			tilec := &tl.Comps[compno]
			res := &tilec.Resolutions[tilec.MinimumNumResolutions-1]
			if t.UsedComponent != nil && !t.UsedComponent[compno] {
				continue
			}
			resW := uint64(res.X1 - res.X0)
			resH := uint64(res.Y1 - res.Y0)
			if resH > 0 && resW > math.MaxUint64/resH {
				mgr.Errorf("Size of tile data exceeds system limits\n")
				return errTileGeometry
			}
			dataSize := resW * resH
			if math.MaxUint64/4 < dataSize {
				mgr.Errorf("Size of tile data exceeds system limits\n")
				return errTileGeometry
			}
			dataSize *= 4
			tilec.DataSizeNeeded = dataSize
			if !allocTileComponentData(tilec) {
				mgr.Errorf("Size of tile data exceeds system limits\n")
				return errTileGeometry
			}
		}
	} else {
		for compno := uint32(0); compno < t.Image.Numcomps; compno++ {
			tilec := &tl.Comps[compno]
			imageComp := &t.Image.Comps[compno]
			if t.UsedComponent != nil && !t.UsedComponent[compno] {
				continue
			}
			tilec.WinX0 = opjmath.UintMax(uint32(tilec.X0), opjmath.UintCeildiv(t.WinX0, imageComp.Dx))
			tilec.WinY0 = opjmath.UintMax(uint32(tilec.Y0), opjmath.UintCeildiv(t.WinY0, imageComp.Dy))
			tilec.WinX1 = opjmath.UintMin(uint32(tilec.X1), opjmath.UintCeildiv(t.WinX1, imageComp.Dx))
			tilec.WinY1 = opjmath.UintMin(uint32(tilec.Y1), opjmath.UintCeildiv(t.WinY1, imageComp.Dy))
			if tilec.WinX1 < tilec.WinX0 || tilec.WinY1 < tilec.WinY0 {
				mgr.Errorf("Invalid tilec->win_xxx values\n")
				return errTileGeometry
			}
			for resno := uint32(0); resno < tilec.Numresolutions; resno++ {
				res := &tilec.Resolutions[resno]
				shift := tilec.Numresolutions - 1 - resno
				res.WinX0 = opjmath.UintCeildivpow2(tilec.WinX0, shift)
				res.WinY0 = opjmath.UintCeildivpow2(tilec.WinY0, shift)
				res.WinX1 = opjmath.UintCeildivpow2(tilec.WinX1, shift)
				res.WinY1 = opjmath.UintCeildivpow2(tilec.WinY1, shift)
			}
		}
	}

	// TIER-2
	if _, ok := t.t2Decode(src, maxLength, mgr); !ok {
		return errTierDecode
	}

	// TIER-1
	if err := t.t1Decode(mgr); err != nil {
		return err
	}

	// For subtile decoding, allocate the window output buffers now that
	// resno_decoded is known.
	if !t.WholeTileDecoding {
		for compno := uint32(0); compno < t.Image.Numcomps; compno++ {
			tilec := &tl.Comps[compno]
			imageComp := &t.Image.Comps[compno]
			res := &tilec.Resolutions[imageComp.ResnoDecoded]
			w := uint64(res.WinX1 - res.WinX0)
			h := uint64(res.WinY1 - res.WinY0)
			tilec.DataWin = nil
			if t.UsedComponent != nil && !t.UsedComponent[compno] {
				continue
			}
			if w > 0 && h > 0 {
				if w > math.MaxUint64/h {
					mgr.Errorf("Size of tile data exceeds system limits\n")
					return errTileGeometry
				}
				dataSize := w * h
				if dataSize > math.MaxUint64/4 {
					mgr.Errorf("Size of tile data exceeds system limits\n")
					return errTileGeometry
				}
				tilec.DataWin = make([]int32, dataSize)
			}
		}
	}

	// DWT
	if err := t.dwtDecode(mgr); err != nil {
		return err
	}

	// MCT
	if err := t.mctDecode(mgr); err != nil {
		return err
	}

	// DC level shift
	if err := t.dcLevelShiftDecode(); err != nil {
		return err
	}

	return nil
}

// t2Decode ports opj_tcd_t2_decode.
func (t *TCD) t2Decode(src []byte, maxLength uint32, mgr *event.Manager) (uint32, bool) {
	engine := t2.Create(t.Image, t.CP)
	return engine.DecodePackets(aoiChecker{t}, t.TcdTileno, t.tile(), src, maxLength, mgr)
}

// mctDecode ports opj_tcd_mct_decode.
func (t *TCD) mctDecode(mgr *event.Manager) error {
	tl := t.tile()
	tcp := t.TCP

	if tcp.MCT == 0 || t.UsedComponent != nil {
		return nil
	}

	var samples uint64
	tc0 := &tl.Comps[0]
	if t.WholeTileDecoding {
		resComp0 := &tc0.Resolutions[tc0.MinimumNumResolutions-1]
		samples = uint64(resComp0.X1-resComp0.X0) * uint64(resComp0.Y1-resComp0.Y0)
		if tl.Numcomps >= 3 {
			if tc0.MinimumNumResolutions != tl.Comps[1].MinimumNumResolutions ||
				tc0.MinimumNumResolutions != tl.Comps[2].MinimumNumResolutions {
				mgr.Errorf("Tiles don't all have the same dimension. Skip the MCT step.\n")
				return errMCT
			}
			resComp1 := &tl.Comps[1].Resolutions[tc0.MinimumNumResolutions-1]
			resComp2 := &tl.Comps[2].Resolutions[tc0.MinimumNumResolutions-1]
			if t.Image.Comps[0].ResnoDecoded != t.Image.Comps[1].ResnoDecoded ||
				t.Image.Comps[0].ResnoDecoded != t.Image.Comps[2].ResnoDecoded ||
				uint64(resComp1.X1-resComp1.X0)*uint64(resComp1.Y1-resComp1.Y0) != samples ||
				uint64(resComp2.X1-resComp2.X0)*uint64(resComp2.Y1-resComp2.Y0) != samples {
				mgr.Errorf("Tiles don't all have the same dimension. Skip the MCT step.\n")
				return errMCT
			}
		}
	} else {
		resComp0 := &tc0.Resolutions[t.Image.Comps[0].ResnoDecoded]
		samples = uint64(resComp0.WinX1-resComp0.WinX0) * uint64(resComp0.WinY1-resComp0.WinY0)
		if tl.Numcomps >= 3 {
			resComp1 := &tl.Comps[1].Resolutions[t.Image.Comps[1].ResnoDecoded]
			resComp2 := &tl.Comps[2].Resolutions[t.Image.Comps[2].ResnoDecoded]
			if t.Image.Comps[0].ResnoDecoded != t.Image.Comps[1].ResnoDecoded ||
				t.Image.Comps[0].ResnoDecoded != t.Image.Comps[2].ResnoDecoded ||
				uint64(resComp1.WinX1-resComp1.WinX0)*uint64(resComp1.WinY1-resComp1.WinY0) != samples ||
				uint64(resComp2.WinX1-resComp2.WinX0)*uint64(resComp2.WinY1-resComp2.WinY0) != samples {
				mgr.Errorf("Tiles don't all have the same dimension. Skip the MCT step.\n")
				return errMCT
			}
		}
	}

	if tl.Numcomps < 3 {
		mgr.Errorf("Number of components (%d) is inconsistent with a MCT. Skip the MCT step.\n", tl.Numcomps)
		return nil
	}

	n := int(samples)
	dataOf := func(c *tile.TileComp) []int32 {
		if t.WholeTileDecoding {
			return c.Data
		}
		return c.DataWin
	}

	if tcp.MCT == 2 {
		if tcp.MMctDecodingMatrix == nil {
			return nil
		}
		data := make([][]int32, tl.Numcomps)
		for i := uint32(0); i < tl.Numcomps; i++ {
			data[i] = dataOf(&tl.Comps[i])
		}
		if !decodeCustomMCT(tcp.MMctDecodingMatrix, n, data, tl.Numcomps, t.Image.Comps[0].Sgnd) {
			return errMCT
		}
		return nil
	}

	if tcp.TCCPs[0].Qmfbid == 1 {
		mct.Decode(dataOf(&tl.Comps[0]), dataOf(&tl.Comps[1]), dataOf(&tl.Comps[2]), n)
	} else {
		// The tile buffers are []int32 holding float32 bit patterns. mct.DecodeReal
		// operates on []float32, so bridge with a copy in/out (no unsafe aliasing).
		d0, d1, d2 := dataOf(&tl.Comps[0]), dataOf(&tl.Comps[1]), dataOf(&tl.Comps[2])
		c0, c1, c2 := bitsToFloat(d0, n), bitsToFloat(d1, n), bitsToFloat(d2, n)
		mct.DecodeReal(c0, c1, c2, n)
		floatToBits(c0, d0, n)
		floatToBits(c1, d1, n)
		floatToBits(c2, d2, n)
	}
	return nil
}

// bitsToFloat reinterprets the first n int32 words as float32 bit patterns.
func bitsToFloat(src []int32, n int) []float32 {
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(uint32(src[i]))
	}
	return out
}

// floatToBits stores n float32 values back into the int32 slots as bit patterns.
func floatToBits(src []float32, dst []int32, n int) {
	for i := 0; i < n; i++ {
		dst[i] = int32(math.Float32bits(src[i]))
	}
}

// decodeCustomMCT ports opj_mct_decode_custom, bridging the []int32 tile buffers
// (float32 bit patterns) to the []float32 API of package mct.
func decodeCustomMCT(matrix []float32, n int, data [][]int32, nbComp uint32, signed uint32) bool {
	fdata := make([][]float32, nbComp)
	for j := uint32(0); j < nbComp; j++ {
		fdata[j] = bitsToFloat(data[j], n)
	}
	mct.DecodeCustom(matrix, n, fdata, nbComp)
	for j := uint32(0); j < nbComp; j++ {
		floatToBits(fdata[j], data[j], n)
	}
	return true
}

// dcLevelShiftDecode ports opj_tcd_dc_level_shift_decode.
func (t *TCD) dcLevelShiftDecode() error {
	tl := t.tile()
	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		imgComp := &t.Image.Comps[compno]
		tccp := &t.TCP.TCCPs[compno]
		tilec := &tl.Comps[compno]

		if t.UsedComponent != nil && !t.UsedComponent[compno] {
			continue
		}

		res := &tilec.Resolutions[imgComp.ResnoDecoded]
		var width, height, stride uint32
		var cur []int32
		if !t.WholeTileDecoding {
			width = res.WinX1 - res.WinX0
			height = res.WinY1 - res.WinY0
			stride = 0
			cur = tilec.DataWin
		} else {
			width = uint32(res.X1 - res.X0)
			height = uint32(res.Y1 - res.Y0)
			minRes := &tilec.Resolutions[tilec.MinimumNumResolutions-1]
			stride = uint32(minRes.X1-minRes.X0) - width
			cur = tilec.Data
		}

		var lmin, lmax int32
		if imgComp.Sgnd != 0 {
			lmin = -(1 << (imgComp.Prec - 1))
			lmax = (1 << (imgComp.Prec - 1)) - 1
		} else {
			lmin = 0
			lmax = int32((uint32(1) << imgComp.Prec) - 1)
		}

		if width == 0 || height == 0 {
			continue
		}

		dcShift := tccp.MDcLevelShift
		idx := 0
		if tccp.Qmfbid == 1 {
			for j := uint32(0); j < height; j++ {
				for i := uint32(0); i < width; i++ {
					cur[idx] = opjmath.IntClamp(cur[idx]+dcShift, lmin, lmax)
					idx++
				}
				idx += int(stride)
			}
		} else {
			for j := uint32(0); j < height; j++ {
				for i := uint32(0); i < width; i++ {
					val := math.Float32frombits(uint32(cur[idx]))
					switch {
					case val > float32(math.MaxInt32):
						cur[idx] = lmax
					case val < math.MinInt32:
						cur[idx] = lmin
					default:
						valInt := int64(lrintf(val))
						cur[idx] = int32(opjmath.Int64Clamp(valInt+int64(dcShift), int64(lmin), int64(lmax)))
					}
					idx++
				}
				idx += int(stride)
			}
		}
	}
	return nil
}

// isSubbandAreaOfInterest ports opj_tcd_is_subband_area_of_interest.
func (t *TCD) isSubbandAreaOfInterest(compno, resno, bandno, bandX0, bandY0, bandX1, bandY1 uint32) bool {
	filterMargin := uint32(3)
	if t.TCP.TCCPs[compno].Qmfbid == 1 {
		filterMargin = 2
	}
	tilec := &t.tile().Comps[compno]
	imageComp := &t.Image.Comps[compno]
	tcx0 := opjmath.UintMax(uint32(tilec.X0), opjmath.UintCeildiv(t.WinX0, imageComp.Dx))
	tcy0 := opjmath.UintMax(uint32(tilec.Y0), opjmath.UintCeildiv(t.WinY0, imageComp.Dy))
	tcx1 := opjmath.UintMin(uint32(tilec.X1), opjmath.UintCeildiv(t.WinX1, imageComp.Dx))
	tcy1 := opjmath.UintMin(uint32(tilec.Y1), opjmath.UintCeildiv(t.WinY1, imageComp.Dy))

	var nb uint32
	if resno == 0 {
		nb = tilec.Numresolutions - 1
	} else {
		nb = tilec.Numresolutions - resno
	}
	x0b := bandno & 1
	y0b := bandno >> 1

	subOff := func(tc, b uint32) uint32 {
		if nb == 0 {
			return tc
		}
		off := (uint32(1) << (nb - 1)) * b
		if tc <= off {
			return 0
		}
		return opjmath.UintCeildivpow2(tc-off, nb)
	}
	tbx0 := subOff(tcx0, x0b)
	tby0 := subOff(tcy0, y0b)
	tbx1 := subOff(tcx1, x0b)
	tby1 := subOff(tcy1, y0b)

	if tbx0 < filterMargin {
		tbx0 = 0
	} else {
		tbx0 -= filterMargin
	}
	if tby0 < filterMargin {
		tby0 = 0
	} else {
		tby0 -= filterMargin
	}
	tbx1 = opjmath.UintAdds(tbx1, filterMargin)
	tby1 = opjmath.UintAdds(tby1, filterMargin)

	return bandX0 < tbx1 && bandY0 < tby1 && bandX1 > tbx0 && bandY1 > tby0
}

// isWholeTilecompDecoding ports opj_tcd_is_whole_tilecomp_decoding.
func (t *TCD) isWholeTilecompDecoding(compno uint32) bool {
	tilec := &t.tile().Comps[compno]
	imageComp := &t.Image.Comps[compno]
	tcx0 := opjmath.UintMax(uint32(tilec.X0), opjmath.UintCeildiv(t.WinX0, imageComp.Dx))
	tcy0 := opjmath.UintMax(uint32(tilec.Y0), opjmath.UintCeildiv(t.WinY0, imageComp.Dy))
	tcx1 := opjmath.UintMin(uint32(tilec.X1), opjmath.UintCeildiv(t.WinX1, imageComp.Dx))
	tcy1 := opjmath.UintMin(uint32(tilec.Y1), opjmath.UintCeildiv(t.WinY1, imageComp.Dy))
	shift := tilec.Numresolutions - tilec.MinimumNumResolutions
	return tcx0 >= uint32(tilec.X0) &&
		tcy0 >= uint32(tilec.Y0) &&
		tcx1 <= uint32(tilec.X1) &&
		tcy1 <= uint32(tilec.Y1) &&
		(shift >= 32 ||
			(((tcx0-uint32(tilec.X0))>>shift) == 0 &&
				((tcy0-uint32(tilec.Y0))>>shift) == 0 &&
				((uint32(tilec.X1)-tcx1)>>shift) == 0 &&
				((uint32(tilec.Y1)-tcy1)>>shift) == 0))
}

// lrintf rounds to nearest, ties to even, matching opj_lrintf (rintf).
func lrintf(x float32) int64 {
	return int64(math.RoundToEven(float64(x)))
}
