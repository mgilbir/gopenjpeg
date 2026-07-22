package j2k

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// getSotValues ports opj_j2k_get_sot_values.
func getSotValues(data []byte) (tileNo, totLen, curPart, numParts uint32, ok bool) {
	if len(data) != 8 {
		return 0, 0, 0, 0, false
	}
	r := &mreader{data: data}
	tileNo = r.u(2)   // Isot
	totLen = r.u(4)   // Psot
	curPart = r.u(1)  // TPsot
	numParts = r.u(1) // TNsot
	return tileNo, totLen, curPart, numParts, true
}

// readSOT ports opj_j2k_read_sot (index bookkeeping omitted).
func (d *Decoder) readSOT(data []byte) error {
	cp := &d.CP
	tileNo, totLen, curPart, numParts, ok := getSotValues(data)
	if !ok {
		d.mgr.Errorf("Error reading SOT marker\n")
		return ErrMarkerHandler
	}
	d.currentTileNumber = tileNo
	if tileNo >= cp.Tw*cp.Th {
		d.mgr.Errorf("Invalid tile number %d\n", tileNo)
		return ErrInvalidTile
	}
	tcp := &cp.Tcps[tileNo]
	tileX := tileNo % cp.Tw
	tileY := tileNo / cp.Tw

	if d.dec.tileIndToDec < 0 || tileNo == uint32(d.dec.tileIndToDec) {
		if tcp.MCurrentTilePartNumber+1 != int32(curPart) {
			d.mgr.Errorf("Invalid tile part index for tile number %d. Got %d, expected %d\n",
				tileNo, curPart, tcp.MCurrentTilePartNumber+1)
			return ErrMarkerHandler
		}
	}
	tcp.MCurrentTilePartNumber = int32(curPart)

	if totLen != 0 && totLen < 14 {
		if totLen == 12 {
			d.mgr.Warnf("Empty SOT marker detected: Psot=%d.\n", totLen)
		} else {
			d.mgr.Errorf("Psot value is not correct regards to the JPEG2000 norm: %d.\n", totLen)
			return ErrMarkerHandler
		}
	}
	if totLen == 0 {
		d.mgr.Infof("Psot value of the current tile-part is equal to zero, assuming last tile-part.\n")
		d.dec.lastTilePart = true
	}

	if tcp.MNbTileParts != 0 && curPart >= tcp.MNbTileParts {
		d.mgr.Errorf("In SOT marker, TPSot (%d) is not valid regards to the previous number of tile-part (%d)\n", curPart, tcp.MNbTileParts)
		d.dec.lastTilePart = true
		return ErrMarkerHandler
	}

	if numParts != 0 {
		numParts += d.dec.nbTilePartsCorrection
		if tcp.MNbTileParts != 0 {
			if curPart >= tcp.MNbTileParts {
				d.mgr.Errorf("In SOT marker, TPSot (%d) is not valid regards to the current number of tile-part (%d)\n", curPart, tcp.MNbTileParts)
				d.dec.lastTilePart = true
				return ErrMarkerHandler
			}
		}
		if curPart >= numParts {
			d.mgr.Errorf("In SOT marker, TPSot (%d) is not valid regards to the current number of tile-part (header) (%d)\n", curPart, numParts)
			d.dec.lastTilePart = true
			return ErrMarkerHandler
		}
		tcp.MNbTileParts = numParts
	}

	if tcp.MNbTileParts != 0 && tcp.MNbTileParts == curPart+1 {
		d.dec.canDecode = true
	}

	if !d.dec.lastTilePart {
		d.dec.sotLength = totLen - 12
	} else {
		d.dec.sotLength = 0
	}

	d.dec.state = stTPH

	if d.dec.tileIndToDec == -1 {
		d.dec.skipData = tileX < d.dec.startTileX || tileX >= d.dec.endTileX ||
			tileY < d.dec.startTileY || tileY >= d.dec.endTileY
	} else {
		d.dec.skipData = tileNo != uint32(d.dec.tileIndToDec)
	}
	return nil
}

// readUpTo reads up to len(buf) bytes from the stream, returning the count.
func readUpTo(s *cio.Stream, buf []byte, mgr *event.Manager) int {
	got := 0
	for got < len(buf) {
		m, err := s.Read(buf[got:], mgr)
		if m > 0 {
			got += m
		}
		if err != nil || m == 0 {
			break
		}
	}
	return got
}

// readSOD ports opj_j2k_read_sod (index bookkeeping omitted).
func (d *Decoder) readSOD(s *cio.Stream) error {
	cp := &d.CP
	tcp := &cp.Tcps[d.currentTileNumber]

	if d.dec.lastTilePart {
		d.dec.sotLength = uint32(s.NumberByteLeft() - 2)
	} else {
		if d.dec.sotLength >= 2 {
			d.dec.sotLength -= 2
		}
	}

	sotLen := d.dec.sotLength
	pbDetected := false
	if sotLen != 0 {
		if int64(sotLen) > s.NumberByteLeft() {
			if cp.Strict {
				d.mgr.Errorf("Tile part length size inconsistent with stream length\n")
				return ErrStreamTooShort
			}
			d.mgr.Warnf("Tile part length size inconsistent with stream length\n")
		}
		if sotLen > math.MaxUint32-cblkExtra {
			d.mgr.Errorf("sot_length too large\n")
			return ErrMarkerHandler
		}
		if tcp.MDataSize > math.MaxUint32-cblkExtra-sotLen {
			d.mgr.Errorf("tile data too large\n")
			return ErrMarkerHandler
		}
		newLen := tcp.MDataSize + sotLen
		buf := make([]byte, newLen+cblkExtra)
		copy(buf, tcp.MData[:tcp.MDataSize])
		tcp.MData = buf
	} else {
		pbDetected = true
	}

	var read uint32
	if !pbDetected {
		read = uint32(readUpTo(s, tcp.MData[tcp.MDataSize:tcp.MDataSize+sotLen], d.mgr))
	}

	if read != sotLen {
		d.dec.state = stNEOC
	} else {
		d.dec.state = stTPHSOT
	}
	tcp.MDataSize += read
	return nil
}

// readTileHeader ports opj_j2k_read_tile_header (TLM seek and intersecting
// tile-part optimizations omitted; sequential reading is used).
func (d *Decoder) readTileHeader(s *cio.Stream) (tileIndex uint32, goOn bool, err error) {
	cp := &d.CP
	nbTiles := cp.Tw * cp.Th
	curMarker := uint32(msSOT)
	if d.dec.state == stEOC {
		curMarker = msEOC
	} else if d.dec.state != stTPHSOT {
		return 0, false, ErrDecodeFailed
	}

	for !d.dec.canDecode && curMarker != msEOC {
		for curMarker != msSOD {
			if s.NumberByteLeft() == 0 {
				d.dec.state = stNEOC
				break
			}
			sizeBuf, e := readExact(s, 2, d.mgr)
			if e != nil {
				d.mgr.Errorf("Stream too short\n")
				return 0, false, e
			}
			markerSize := cio.ReadBytes(sizeBuf, 2)
			if markerSize < 2 {
				d.mgr.Errorf("Inconsistent marker size\n")
				return 0, false, ErrInvalidMarker
			}
			if curMarker == 0x8080 && s.NumberByteLeft() == 0 {
				d.dec.state = stNEOC
				break
			}
			if d.dec.state&stTPH != 0 && d.dec.sotLength != 0 {
				if d.dec.sotLength < markerSize+2 {
					d.mgr.Errorf("Sot length is less than marker size + marker ID\n")
					return 0, false, ErrMarkerHandler
				}
				d.dec.sotLength -= markerSize + 2
			}
			markerSize -= 2
			mh := getMarkerHandler(curMarker)
			if d.dec.state&mh.states == 0 {
				d.mgr.Errorf("Marker is not compliant with its position\n")
				return 0, false, ErrMarkerPosition
			}
			if int64(markerSize) > s.NumberByteLeft() {
				d.mgr.Errorf("Marker size inconsistent with stream length\n")
				return 0, false, ErrStreamTooShort
			}
			seg, re := readExact(s, int(markerSize), d.mgr)
			if re != nil {
				d.mgr.Errorf("Stream too short\n")
				return 0, false, re
			}
			if mh.handler == nil && mh.id != msSOP {
				d.mgr.Errorf("Not sure how that happened.\n")
				return 0, false, ErrMarkerHandler
			}
			if mh.handler != nil {
				if he := mh.handler(d, seg); he != nil {
					d.mgr.Errorf("Fail to read the current marker segment (%x)\n", curMarker)
					return 0, false, he
				}
			}

			if d.dec.skipData {
				if _, se := s.Skip(int64(d.dec.sotLength), d.mgr); se != nil {
					d.mgr.Errorf("Stream too short\n")
					return 0, false, ErrStreamTooShort
				}
				curMarker = msSOD
			} else {
				nb, ne := readExact(s, 2, d.mgr)
				if ne != nil {
					d.mgr.Errorf("Stream too short\n")
					return 0, false, ne
				}
				curMarker = cio.ReadBytes(nb, 2)
			}
		}
		if s.NumberByteLeft() == 0 && d.dec.state == stNEOC {
			break
		}

		if !d.dec.skipData {
			if se := d.readSOD(s); se != nil {
				return 0, false, se
			}
			if d.dec.canDecode && !d.dec.nbTilePartsCorrectionChecked {
				d.dec.nbTilePartsCorrectionChecked = true
				if cp.Tcps[d.currentTileNumber].MNbTileParts == 1 {
					// skip correction check for single declared tile-part
				} else {
					correction, ce := d.needNbTilePartsCorrection(s, d.currentTileNumber)
					if ce != nil {
						d.mgr.Errorf("opj_j2k_apply_nb_tile_parts_correction error\n")
						return 0, false, ce
					}
					if correction {
						d.dec.canDecode = false
						d.dec.nbTilePartsCorrection = 1
						for t := uint32(0); t < nbTiles; t++ {
							if cp.Tcps[t].MNbTileParts != 0 {
								cp.Tcps[t].MNbTileParts++
							}
						}
						d.mgr.Warnf("Non conformant codestream TPsot==TNsot.\n")
					}
				}
			}
		} else {
			d.dec.skipData = false
			d.dec.canDecode = false
			d.dec.state = stTPHSOT
		}

		if !d.dec.canDecode {
			nb, ne := readExact(s, 2, d.mgr)
			if ne != nil {
				// SPOT6 fallback: last row of tiles with TPsot==0 and TNsot==0.
				if d.currentTileNumber+1 == nbTiles {
					for t := uint32(0); t < nbTiles; t++ {
						if cp.Tcps[t].MCurrentTilePartNumber == 0 && cp.Tcps[t].MNbTileParts == 0 {
							d.currentTileNumber = t
							curMarker = msEOC
							d.dec.state = stEOC
							goto afterInner
						}
					}
				}
				d.mgr.Errorf("Stream too short\n")
				return 0, false, ne
			}
			curMarker = cio.ReadBytes(nb, 2)
		}
	}
afterInner:

	if curMarker == msEOC {
		if d.dec.state != stEOC {
			d.currentTileNumber = 0
			d.dec.state = stEOC
		}
	}

	if !d.dec.canDecode {
		for d.currentTileNumber < nbTiles && cp.Tcps[d.currentTileNumber].MData == nil {
			d.currentTileNumber++
		}
		if d.currentTileNumber == nbTiles {
			return 0, false, nil
		}
	}

	if me := d.mergePPT(&cp.Tcps[d.currentTileNumber]); me != nil {
		d.mgr.Errorf("Failed to merge PPT data\n")
		return 0, false, me
	}
	if ie := d.tcd.InitDecodeTile(d.currentTileNumber, d.mgr); ie != nil {
		d.mgr.Errorf("Cannot decode tile, memory error\n")
		return 0, false, ie
	}

	tileIndex = d.currentTileNumber
	d.dec.state |= stData
	return tileIndex, true, nil
}

// needNbTilePartsCorrection ports opj_j2k_need_nb_tile_parts_correction.
func (d *Decoder) needNbTilePartsCorrection(s *cio.Stream, tileNo uint32) (bool, error) {
	if !s.HasSeek() {
		return false, nil
	}
	backup := s.Tell()
	if backup < 0 {
		return false, nil
	}
	for {
		buf, e := readExact(s, 2, d.mgr)
		if e != nil {
			if se := s.SeekTo(backup, d.mgr); se != nil {
				return false, se
			}
			return false, nil
		}
		if cio.ReadBytes(buf, 2) != msSOT {
			if se := s.SeekTo(backup, d.mgr); se != nil {
				return false, se
			}
			return false, nil
		}
		sizeBuf, se := readExact(s, 2, d.mgr)
		if se != nil {
			d.mgr.Errorf("Stream too short\n")
			return false, se
		}
		markerSize := cio.ReadBytes(sizeBuf, 2)
		if markerSize != 10 {
			d.mgr.Errorf("Inconsistent marker size\n")
			return false, ErrInvalidMarker
		}
		markerSize -= 2
		seg, re := readExact(s, int(markerSize), d.mgr)
		if re != nil {
			d.mgr.Errorf("Stream too short\n")
			return false, re
		}
		lTileNo, lTotLen, lCurPart, lNumParts, ok := getSotValues(seg)
		if !ok {
			return false, ErrMarkerHandler
		}
		if lTileNo == tileNo {
			if se := s.SeekTo(backup, d.mgr); se != nil {
				return false, se
			}
			return lCurPart == lNumParts, nil
		}
		if lTotLen < 14 {
			if se := s.SeekTo(backup, d.mgr); se != nil {
				return false, se
			}
			return false, nil
		}
		lTotLen -= 12
		if n, _ := s.Skip(int64(lTotLen), d.mgr); n != int64(lTotLen) {
			if se := s.SeekTo(backup, d.mgr); se != nil {
				return false, se
			}
			return false, nil
		}
	}
}

// decodeTile ports opj_j2k_decode_tile (whole-tile-into-tcd path; p_data==nil).
func (d *Decoder) decodeTile(s *cio.Stream, tileIndex uint32) error {
	cp := &d.CP
	if d.dec.state&stData == 0 || tileIndex != d.currentTileNumber {
		return ErrDecodeFailed
	}
	tcp := &cp.Tcps[tileIndex]
	if tcp.MData == nil {
		return ErrDecodeFailed
	}

	bounds := d.outputImage
	if bounds == nil {
		bounds = d.privateImage
	}
	if err := d.tcd.DecodeTile(bounds.X0, bounds.Y0, bounds.X1, bounds.Y1,
		d.dec.compsIndicesToDec, tcp.MData, tcp.MDataSize, tileIndex, d.mgr); err != nil {
		d.dec.state |= stErr
		d.mgr.Errorf("Failed to decode.\n")
		return err
	}

	d.dec.canDecode = false
	d.dec.state &^= stData

	if s.NumberByteLeft() == 0 && d.dec.state == stNEOC {
		return nil
	}
	if d.dec.state != stEOC {
		buf := make([]byte, 2)
		n := readUpTo(s, buf, d.mgr)
		if n != 2 {
			if cp.Strict {
				d.mgr.Errorf("Stream too short\n")
				return ErrStreamTooShort
			}
			d.mgr.Warnf("Stream too short\n")
			return nil
		}
		m := cio.ReadBytes(buf, 2)
		if m == msEOC {
			d.currentTileNumber = 0
			d.dec.state = stEOC
		} else if m != msSOT {
			if s.NumberByteLeft() == 0 {
				d.dec.state = stNEOC
				d.mgr.Warnf("Stream does not end with EOC\n")
				return nil
			}
			d.mgr.Errorf("Stream too short, expected SOT\n")
			return ErrStreamTooShort
		}
	}
	return nil
}

// decodeTiles ports opj_j2k_decode_tiles.
func (d *Decoder) decodeTiles(s *cio.Stream) error {
	cp := &d.CP
	out := d.outputImage

	// Whole single-tile fast path.
	if cp.Tw == 1 && cp.Th == 1 && cp.Tx0 == 0 && cp.Ty0 == 0 &&
		out.X0 == 0 && out.Y0 == 0 && out.X1 == cp.Tdx && out.Y1 == cp.Tdy {
		tileNo, goOn, err := d.readTileHeader(s)
		if err != nil {
			return err
		}
		if !goOn {
			d.mgr.Errorf("Failed to decode tile 1/1\n")
			return ErrDecodeFailed
		}
		if err := d.decodeTile(s, tileNo); err != nil {
			d.mgr.Errorf("Failed to decode tile 1/1\n")
			return err
		}
		for i := uint32(0); i < out.Numcomps; i++ {
			out.Comps[i].Data = d.tcd.TcdImage.Tiles[0].Comps[i].Data
			out.Comps[i].ResnoDecoded = d.tcd.Image.Comps[i].ResnoDecoded
			d.tcd.TcdImage.Tiles[0].Comps[i].Data = nil
		}
		return nil
	}

	nrTiles := uint32(0)
	for {
		var tileNo uint32
		if cp.Tw == 1 && cp.Th == 1 && cp.Tcps[0].MData != nil {
			tileNo = 0
			d.currentTileNumber = 0
			d.dec.state |= stData
		} else {
			var goOn bool
			var err error
			tileNo, goOn, err = d.readTileHeader(s)
			if err != nil {
				return err
			}
			if !goOn {
				break
			}
		}
		if err := d.decodeTile(s, tileNo); err != nil {
			d.mgr.Errorf("Failed to decode tile %d/%d\n", tileNo+1, cp.Th*cp.Tw)
			return err
		}
		if err := d.updateImageData(out); err != nil {
			return err
		}
		if cp.Tw == 1 && cp.Th == 1 &&
			!(out.X0 == d.privateImage.X0 && out.Y0 == d.privateImage.Y0 &&
				out.X1 == d.privateImage.X1 && out.Y1 == d.privateImage.Y1) {
			// keep current tcp data
		} else {
			cp.Tcps[tileNo].MData = nil
			cp.Tcps[tileNo].MDataSize = 0
		}
		if s.NumberByteLeft() == 0 && d.dec.state == stNEOC {
			break
		}
		nrTiles++
		if nrTiles == cp.Th*cp.Tw {
			break
		}
	}
	return d.areAllUsedComponentsDecoded()
}

// areAllUsedComponentsDecoded ports opj_j2k_are_all_used_components_decoded.
func (d *Decoder) areAllUsedComponentsDecoded() error {
	ok := true
	if d.dec.numcompsToDecode != 0 {
		for _, c := range d.dec.compsIndicesToDec {
			if d.outputImage.Comps[c].Data == nil {
				d.mgr.Warnf("Failed to decode component %d\n", c)
				ok = false
			}
		}
	} else {
		for c := uint32(0); c < d.outputImage.Numcomps; c++ {
			if d.outputImage.Comps[c].Data == nil {
				d.mgr.Warnf("Failed to decode component %d\n", c)
				ok = false
			}
		}
	}
	if !ok {
		d.mgr.Errorf("Failed to decode all used components\n")
		return ErrDecodeFailed
	}
	return nil
}

func uintCeildivpow2(a, b uint32) uint32 {
	return uint32((uint64(a) + (uint64(1) << b) - 1) >> b)
}
