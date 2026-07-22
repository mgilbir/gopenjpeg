package j2k

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
)

// updateImageData ports opj_j2k_update_image_data: copy the decoded tile-component
// samples into the output image, honouring the region intersection and factor.
func (d *Decoder) updateImageData(out *image.Image) error {
	tcdObj := d.tcd
	src := tcdObj.Image
	for i := uint32(0); i < src.Numcomps; i++ {
		tilec := &tcdObj.TcdImage.Tiles[0].Comps[i]
		imgSrc := &src.Comps[i]
		dst := &out.Comps[i]

		dst.ResnoDecoded = imgSrc.ResnoDecoded

		var resX0, resY0, resX1, resY1 int32
		var srcStride uint32
		var srcData []int32
		if tcdObj.WholeTileDecoding {
			res := &tilec.Resolutions[imgSrc.ResnoDecoded]
			resX0, resY0, resX1, resY1 = res.X0, res.Y0, res.X1, res.Y1
			mr := &tilec.Resolutions[tilec.MinimumNumResolutions-1]
			srcStride = uint32(mr.X1 - mr.X0)
			srcData = tilec.Data
		} else {
			res := &tilec.Resolutions[imgSrc.ResnoDecoded]
			resX0, resY0 = int32(res.WinX0), int32(res.WinY0)
			resX1, resY1 = int32(res.WinX1), int32(res.WinY1)
			srcStride = res.WinX1 - res.WinX0
			srcData = tilec.DataWin
		}
		if srcData == nil {
			continue
		}

		widthSrc := uint32(resX1 - resX0)
		heightSrc := uint32(resY1 - resY0)

		x0Dest := uintCeildivpow2(dst.X0, dst.Factor)
		y0Dest := uintCeildivpow2(dst.Y0, dst.Factor)
		x1Dest := x0Dest + dst.W
		y1Dest := y0Dest + dst.H

		var startXDest, widthDest uint32
		var offX0Src, offX1Src int32
		if x0Dest < uint32(resX0) {
			startXDest = uint32(resX0) - x0Dest
			offX0Src = 0
			if x1Dest >= uint32(resX1) {
				widthDest = widthSrc
				offX1Src = 0
			} else {
				widthDest = x1Dest - uint32(resX0)
				offX1Src = int32(widthSrc - widthDest)
			}
		} else {
			startXDest = 0
			offX0Src = int32(x0Dest) - resX0
			if x1Dest >= uint32(resX1) {
				widthDest = widthSrc - uint32(offX0Src)
				offX1Src = 0
			} else {
				widthDest = dst.W
				offX1Src = resX1 - int32(x1Dest)
			}
		}

		var startYDest, heightDest uint32
		var offY0Src, offY1Src int32
		if y0Dest < uint32(resY0) {
			startYDest = uint32(resY0) - y0Dest
			offY0Src = 0
			if y1Dest >= uint32(resY1) {
				heightDest = heightSrc
				offY1Src = 0
			} else {
				heightDest = y1Dest - uint32(resY0)
				offY1Src = int32(heightSrc - heightDest)
			}
		} else {
			startYDest = 0
			offY0Src = int32(y0Dest) - resY0
			if y1Dest >= uint32(resY1) {
				heightDest = heightSrc - uint32(offY0Src)
				offY1Src = 0
			} else {
				heightDest = dst.H
				offY1Src = resY1 - int32(y1Dest)
			}
		}

		if offX0Src < 0 || offY0Src < 0 || offX1Src < 0 || offY1Src < 0 {
			return ErrDecodeFailed
		}
		if int32(widthDest) < 0 || int32(heightDest) < 0 {
			return ErrDecodeFailed
		}

		startOffsetSrc := int(offX0Src) + int(offY0Src)*int(srcStride)
		startOffsetDest := int(startXDest) + int(startYDest)*int(dst.W)

		if dst.Data == nil &&
			startOffsetSrc == 0 && startOffsetDest == 0 &&
			srcStride == dst.W && widthDest == dst.W && heightDest == dst.H {
			// Borrow the tile buffer directly.
			if tcdObj.WholeTileDecoding {
				dst.Data = tilec.Data
				tilec.Data = nil
			} else {
				dst.Data = tilec.DataWin
				tilec.DataWin = nil
			}
			continue
		} else if dst.Data == nil {
			w := uint64(dst.W)
			h := uint64(dst.H)
			if h == 0 || w > math.MaxUint64/h || w*h > math.MaxUint64/4 {
				return ErrDecodeFailed
			}
			dst.Data = make([]int32, w*h) // make() zero-fills
		}

		for j := uint32(0); j < heightDest; j++ {
			dOff := startOffsetDest + int(j)*int(dst.W)
			sOff := startOffsetSrc + int(j)*int(srcStride)
			copy(dst.Data[dOff:dOff+int(widthDest)], srcData[sOff:sOff+int(widthDest)])
		}
	}
	return nil
}

// updateImageDimensions ports opj_j2k_update_image_dimensions.
func (d *Decoder) updateImageDimensions(img *image.Image) error {
	for it := uint32(0); it < img.Numcomps; it++ {
		c := &img.Comps[it]
		if img.X0 > math.MaxInt32 || img.Y0 > math.MaxInt32 ||
			img.X1 > math.MaxInt32 || img.Y1 > math.MaxInt32 {
			d.mgr.Errorf("Image coordinates above INT_MAX are not supported\n")
			return ErrBadParams
		}
		c.X0 = uintCeildiv(img.X0, c.Dx)
		c.Y0 = uintCeildiv(img.Y0, c.Dy)
		compX1 := opjmath.IntCeildiv(int32(img.X1), int32(c.Dx))
		compY1 := opjmath.IntCeildiv(int32(img.Y1), int32(c.Dy))
		w := opjmath.IntCeildivpow2(compX1, int32(c.Factor)) - opjmath.IntCeildivpow2(int32(c.X0), int32(c.Factor))
		if w < 0 {
			d.mgr.Errorf("Size x of the decoded component image is incorrect (comp[%d].w=%d).\n", it, w)
			return ErrBadParams
		}
		c.W = uint32(w)
		h := opjmath.IntCeildivpow2(compY1, int32(c.Factor)) - opjmath.IntCeildivpow2(int32(c.Y0), int32(c.Factor))
		if h < 0 {
			d.mgr.Errorf("Size y of the decoded component image is incorrect (comp[%d].h=%d).\n", it, h)
			return ErrBadParams
		}
		c.H = uint32(h)
	}
	return nil
}

// SetDecodeArea ports opj_j2k_set_decode_area.
func (d *Decoder) SetDecodeArea(out *image.Image, startX, startY, endX, endY int32) error {
	cp := &d.CP
	limg := d.privateImage

	single := cp.Tw == 1 && cp.Th == 1 && cp.Tcps != nil && cp.Tcps[0].MData != nil
	if !single && d.dec.state != stTPHSOT {
		d.mgr.Errorf("Need to decode the main header before begin to decode the remaining codestream.\n")
		return ErrHeaderNotRead
	}

	for it := uint32(0); it < out.Numcomps; it++ {
		out.Comps[it].Factor = cp.MDec.MReduce
	}

	if startX == 0 && startY == 0 && endX == 0 && endY == 0 {
		d.mgr.Infof("No decoded area parameters, set the decoded area to the whole image\n")
		d.dec.startTileX, d.dec.startTileY = 0, 0
		d.dec.endTileX, d.dec.endTileY = cp.Tw, cp.Th
		out.X0, out.Y0, out.X1, out.Y1 = limg.X0, limg.Y0, limg.X1, limg.Y1
		return d.updateImageDimensions(out)
	}

	// Left
	if startX < 0 {
		d.mgr.Errorf("Left position of the decoded area (region_x0=%d) should be >= 0.\n", startX)
		return ErrBadParams
	} else if uint32(startX) > limg.X1 {
		d.mgr.Errorf("Left position of the decoded area (region_x0=%d) is outside the image area.\n", startX)
		return ErrBadParams
	} else if uint32(startX) < limg.X0 {
		d.mgr.Warnf("Left position of the decoded area (region_x0=%d) is outside the image area.\n", startX)
		d.dec.startTileX = 0
		out.X0 = limg.X0
	} else {
		d.dec.startTileX = (uint32(startX) - cp.Tx0) / cp.Tdx
		out.X0 = uint32(startX)
	}

	// Up
	if startY < 0 {
		d.mgr.Errorf("Up position of the decoded area (region_y0=%d) should be >= 0.\n", startY)
		return ErrBadParams
	} else if uint32(startY) > limg.Y1 {
		d.mgr.Errorf("Up position of the decoded area (region_y0=%d) is outside the image area.\n", startY)
		return ErrBadParams
	} else if uint32(startY) < limg.Y0 {
		d.mgr.Warnf("Up position of the decoded area (region_y0=%d) is outside the image area.\n", startY)
		d.dec.startTileY = 0
		out.Y0 = limg.Y0
	} else {
		d.dec.startTileY = (uint32(startY) - cp.Ty0) / cp.Tdy
		out.Y0 = uint32(startY)
	}

	// Right
	if endX <= 0 {
		d.mgr.Errorf("Right position of the decoded area (region_x1=%d) should be > 0.\n", endX)
		return ErrBadParams
	} else if uint32(endX) < limg.X0 {
		d.mgr.Errorf("Right position of the decoded area (region_x1=%d) is outside the image area.\n", endX)
		return ErrBadParams
	} else if uint32(endX) > limg.X1 {
		d.mgr.Warnf("Right position of the decoded area (region_x1=%d) is outside the image area.\n", endX)
		d.dec.endTileX = cp.Tw
		out.X1 = limg.X1
	} else {
		d.dec.endTileX = uintCeildiv(uint32(endX)-cp.Tx0, cp.Tdx)
		out.X1 = uint32(endX)
	}

	// Bottom
	if endY <= 0 {
		d.mgr.Errorf("Bottom position of the decoded area (region_y1=%d) should be > 0.\n", endY)
		return ErrBadParams
	} else if uint32(endY) < limg.Y0 {
		d.mgr.Errorf("Bottom position of the decoded area (region_y1=%d) is outside the image area.\n", endY)
		return ErrBadParams
	}
	if uint32(endY) > limg.Y1 {
		d.mgr.Warnf("Bottom position of the decoded area (region_y1=%d) is outside the image area.\n", endY)
		d.dec.endTileY = cp.Th
		out.Y1 = limg.Y1
	} else {
		d.dec.endTileY = uintCeildiv(uint32(endY)-cp.Ty0, cp.Tdy)
		out.Y1 = uint32(endY)
	}

	d.dec.discardTiles = true
	return d.updateImageDimensions(out)
}

// moveDataFromCodecToOutputImage ports opj_j2k_move_data_from_codec_to_output_image.
func (d *Decoder) moveDataFromCodecToOutputImage(out *image.Image) error {
	if d.dec.numcompsToDecode > 0 {
		newcomps := make([]image.Comp, d.dec.numcompsToDecode)
		for c := uint32(0); c < out.Numcomps; c++ {
			out.Comps[c].Data = nil
		}
		for c := uint32(0); c < d.dec.numcompsToDecode; c++ {
			srcCompno := d.dec.compsIndicesToDec[c]
			newcomps[c] = d.outputImage.Comps[srcCompno]
			newcomps[c].Data = d.outputImage.Comps[srcCompno].Data
			d.outputImage.Comps[srcCompno].Data = nil
		}
		out.Numcomps = d.dec.numcompsToDecode
		out.Comps = newcomps
	} else {
		for c := uint32(0); c < out.Numcomps; c++ {
			out.Comps[c].ResnoDecoded = d.outputImage.Comps[c].ResnoDecoded
			out.Comps[c].Data = d.outputImage.Comps[c].Data
			d.outputImage.Comps[c].Data = nil
		}
	}
	return nil
}

// Decode ports opj_j2k_decode: decode the whole codestream into out.
func (d *Decoder) Decode(s *cio.Stream, out *image.Image, mgr *event.Manager) error {
	if out == nil {
		return ErrBadParams
	}
	d.mgr = mgr

	if d.CP.MDec.MReduce > 0 && d.privateImage != nil && d.privateImage.Numcomps > 0 &&
		d.privateImage.Comps[0].Factor == d.CP.MDec.MReduce &&
		out.Numcomps > 0 && out.Comps[0].Factor == 0 && out.Comps[0].Data == nil {
		for it := uint32(0); it < out.Numcomps; it++ {
			out.Comps[it].Factor = d.CP.MDec.MReduce
		}
		if err := d.updateImageDimensions(out); err != nil {
			return err
		}
	}

	if d.outputImage == nil {
		d.outputImage = image.Create0()
	}
	image.CopyHeader(out, d.outputImage)

	if err := d.decodeTiles(s); err != nil {
		return err
	}
	return d.moveDataFromCodecToOutputImage(out)
}

// GetTile ports opj_j2k_get_tile: decode a single tile into out.
func (d *Decoder) GetTile(s *cio.Stream, out *image.Image, tileIndex uint32, mgr *event.Manager) error {
	if out == nil {
		mgr.Errorf("We need an image previously created.\n")
		return ErrBadParams
	}
	d.mgr = mgr
	if out.Numcomps < d.privateImage.Numcomps {
		mgr.Errorf("Image has less components than codestream.\n")
		return ErrBadParams
	}
	if tileIndex >= d.CP.Tw*d.CP.Th {
		mgr.Errorf("Tile index provided by the user is incorrect %d (max = %d)\n", tileIndex, d.CP.Tw*d.CP.Th-1)
		return ErrInvalidTile
	}

	tileX := tileIndex % d.CP.Tw
	tileY := tileIndex / d.CP.Tw
	out.X0 = tileX*d.CP.Tdx + d.CP.Tx0
	if out.X0 < d.privateImage.X0 {
		out.X0 = d.privateImage.X0
	}
	out.X1 = (tileX+1)*d.CP.Tdx + d.CP.Tx0
	if out.X1 > d.privateImage.X1 {
		out.X1 = d.privateImage.X1
	}
	out.Y0 = tileY*d.CP.Tdy + d.CP.Ty0
	if out.Y0 < d.privateImage.Y0 {
		out.Y0 = d.privateImage.Y0
	}
	out.Y1 = (tileY+1)*d.CP.Tdy + d.CP.Ty0
	if out.Y1 > d.privateImage.Y1 {
		out.Y1 = d.privateImage.Y1
	}

	for compno := uint32(0); compno < d.privateImage.Numcomps; compno++ {
		c := &out.Comps[compno]
		c.Factor = d.privateImage.Comps[compno].Factor
		c.X0 = uintCeildiv(out.X0, c.Dx)
		c.Y0 = uintCeildiv(out.Y0, c.Dy)
		compX1 := opjmath.IntCeildiv(int32(out.X1), int32(c.Dx))
		compY1 := opjmath.IntCeildiv(int32(out.Y1), int32(c.Dy))
		c.W = uint32(opjmath.IntCeildivpow2(compX1, int32(c.Factor)) - opjmath.IntCeildivpow2(int32(c.X0), int32(c.Factor)))
		c.H = uint32(opjmath.IntCeildivpow2(compY1, int32(c.Factor)) - opjmath.IntCeildivpow2(int32(c.Y0), int32(c.Factor)))
	}
	if out.Numcomps > d.privateImage.Numcomps {
		for compno := d.privateImage.Numcomps; compno < out.Numcomps; compno++ {
			out.Comps[compno].Data = nil
		}
		out.Numcomps = d.privateImage.Numcomps
	}

	d.outputImage = image.Create0()
	image.CopyHeader(out, d.outputImage)
	d.dec.tileIndToDec = int32(tileIndex)

	if err := d.decodeOneTile(s); err != nil {
		return err
	}
	return d.moveDataFromCodecToOutputImage(out)
}

// decodeOneTile ports opj_j2k_decode_one_tile (sequential, no TLM seek).
func (d *Decoder) decodeOneTile(s *cio.Stream) error {
	cp := &d.CP
	tileNoToDec := uint32(d.dec.tileIndToDec)

	// Seek back to the start of the codestream tile-parts.
	if s.HasSeek() {
		if err := s.SeekTo(d.mainHeadEnd+2, d.mgr); err != nil {
			d.mgr.Errorf("Problem with seek function\n")
			return err
		}
		if d.dec.state == stEOC {
			d.dec.state = stTPHSOT
		}
	}
	nbTiles := cp.Tw * cp.Th
	for i := uint32(0); i < nbTiles; i++ {
		cp.Tcps[i].MCurrentTilePartNumber = -1
	}

	for {
		tileNo, goOn, err := d.readTileHeader(s)
		if err != nil {
			return err
		}
		if !goOn {
			break
		}
		if err := d.decodeTile(s, tileNo); err != nil {
			return err
		}
		if err := d.updateImageData(d.outputImage); err != nil {
			return err
		}
		cp.Tcps[tileNo].MData = nil
		cp.Tcps[tileNo].MDataSize = 0
		if tileNo == tileNoToDec {
			if s.HasSeek() {
				if err := s.SeekTo(d.mainHeadEnd+2, d.mgr); err != nil {
					d.mgr.Errorf("Problem with seek function\n")
					return err
				}
			}
			break
		}
		d.mgr.Warnf("Tile read, decoded and updated is not the desired one (%d vs %d).\n", tileNo+1, tileNoToDec+1)
	}
	return d.areAllUsedComponentsDecoded()
}
