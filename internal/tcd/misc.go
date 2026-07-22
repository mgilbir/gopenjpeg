package tcd

import (
	"math"
)

// GetDecodedTileSize ports opj_tcd_get_decoded_tile_size: the number of bytes a
// decoded tile occupies once packed into per-component samples. Returns
// math.MaxUint32 on overflow (mirroring the C UINT_MAX sentinel).
func (t *TCD) GetDecodedTileSize(takeIntoAccountPartialDecoding bool) uint32 {
	dataSize := uint32(0)
	tl := t.tile()
	for i := uint32(0); i < t.Image.Numcomps; i++ {
		imgComp := &t.Image.Comps[i]
		tilec := &tl.Comps[i]
		sizeComp := imgComp.Prec >> 3
		if imgComp.Prec&7 != 0 {
			sizeComp++
		}
		if sizeComp == 3 {
			sizeComp = 4
		}
		res := &tilec.Resolutions[tilec.MinimumNumResolutions-1]
		var w, h uint32
		if takeIntoAccountPartialDecoding && !t.WholeTileDecoding {
			w = res.WinX1 - res.WinX0
			h = res.WinY1 - res.WinY0
		} else {
			w = uint32(res.X1 - res.X0)
			h = uint32(res.Y1 - res.Y0)
		}
		if h > 0 && math.MaxUint32/w < h {
			return math.MaxUint32
		}
		tmp := w * h
		if sizeComp != 0 && math.MaxUint32/sizeComp < tmp {
			return math.MaxUint32
		}
		tmp *= sizeComp
		if tmp > math.MaxUint32-dataSize {
			return math.MaxUint32
		}
		dataSize += tmp
	}
	return dataSize
}

// UpdateTileData ports opj_tcd_update_tile_data: pack the decoded tile-component
// samples into dest using the per-component byte size, honouring signedness and
// the whole-tile vs windowed stride. Used by the tile-based decode API.
func (t *TCD) UpdateTileData(dest []byte) error {
	dataSize := t.GetDecodedTileSize(true)
	if dataSize == math.MaxUint32 || uint64(dataSize) > uint64(len(dest)) {
		return errTileGeometry
	}

	tl := t.tile()
	pos := 0
	for i := uint32(0); i < t.Image.Numcomps; i++ {
		imgComp := &t.Image.Comps[i]
		tilec := &tl.Comps[i]
		sizeComp := imgComp.Prec >> 3
		res := &tilec.Resolutions[imgComp.ResnoDecoded]

		var width, height, stride uint32
		var src []int32
		if t.WholeTileDecoding {
			width = uint32(res.X1 - res.X0)
			height = uint32(res.Y1 - res.Y0)
			mr := &tilec.Resolutions[tilec.MinimumNumResolutions-1]
			stride = uint32(mr.X1-mr.X0) - width
			src = tilec.Data
		} else {
			width = res.WinX1 - res.WinX0
			height = res.WinY1 - res.WinY0
			stride = 0
			src = tilec.DataWin
		}

		if imgComp.Prec&7 != 0 {
			sizeComp++
		}
		if sizeComp == 3 {
			sizeComp = 4
		}

		si := 0
		switch sizeComp {
		case 1:
			for j := uint32(0); j < height; j++ {
				for k := uint32(0); k < width; k++ {
					dest[pos] = byte(src[si])
					si++
					pos++
				}
				si += int(stride)
			}
		case 2:
			for j := uint32(0); j < height; j++ {
				for k := uint32(0); k < width; k++ {
					v := uint16(src[si])
					dest[pos] = byte(v)
					dest[pos+1] = byte(v >> 8)
					si++
					pos += 2
				}
				si += int(stride)
			}
		case 4:
			for j := uint32(0); j < height; j++ {
				for k := uint32(0); k < width; k++ {
					v := uint32(src[si])
					dest[pos] = byte(v)
					dest[pos+1] = byte(v >> 8)
					dest[pos+2] = byte(v >> 16)
					dest[pos+3] = byte(v >> 24)
					si++
					pos += 4
				}
				si += int(stride)
			}
		}
	}
	return nil
}
