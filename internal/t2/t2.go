// Package t2 ports t2.c/t2.h: tier-2 coding, i.e. the packetization of
// code-block data into (and out of) the codestream. It writes and reads packet
// headers (using the bit-I/O of package bio and the tag trees of package tgt)
// and the packet bodies, driving the packet order with the iterator of package
// pi.
//
// The decoder deliberately preserves the C tolerance behaviour: which malformed
// conditions produce a warning and continue (non-strict mode) versus a hard
// error (strict mode). That distinction is load-bearing for decoder robustness
// parity, so it is reproduced exactly.
package t2

import (
	"github.com/mgilbir/gopenjpeg/internal/bio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	"github.com/mgilbir/gopenjpeg/internal/pi"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// T2 ports opj_t2_t: a tier-2 handle bound to an image and its coding
// parameters.
type T2 struct {
	// Image ports opj_t2_t.image (src image for encoding, dst image for decoding).
	Image *image.Image
	// CP ports opj_t2_t.cp.
	CP *cparams.CP
}

// Create ports opj_t2_create.
func Create(img *image.Image, cp *cparams.CP) *T2 {
	return &T2{Image: img, CP: cp}
}

// PacketInfo ports the subset of opj_packet_info_t touched by t2.
type PacketInfo struct {
	StartPos int32   // start_pos
	EndPos   int32   // end_pos
	EndPhPos int32   // end_ph_pos
	Disto    float64 // disto
}

// TileInfo ports the subset of opj_tile_info_t touched by t2 (encoder index).
type TileInfo struct {
	EndHeader int32        // end_header
	Packet    []PacketInfo // packet
}

// CodestreamInfo ports the subset of opj_codestream_info_t touched by t2's
// encoder index-writing paths. It is optional (nil disables index writing).
type CodestreamInfo struct {
	IndexWrite bool       // index_write
	Tile       []TileInfo // tile
	Packno     uint32     // packno
	DMax       float64    // D_max
}

// AOIChecker abstracts opj_tcd_is_subband_area_of_interest, which
// opj_t2_decode_packets calls on its tcd handle to decide whether a packet's
// precinct intersects the decode window. The tcd package (W7) will implement it;
// for whole-tile decoding every band intersects, so WholeTileAOI returns true.
type AOIChecker interface {
	IsSubbandAreaOfInterest(compno, resno, bandno, x0, y0, x1, y1 uint32) bool
}

// WholeTileAOI is an AOIChecker that always reports intersection, matching the
// behaviour of opj_tcd_is_subband_area_of_interest when the decode window covers
// the whole tile.
type WholeTileAOI struct{}

// IsSubbandAreaOfInterest always returns true.
func (WholeTileAOI) IsSubbandAreaOfInterest(_, _, _, _, _, _, _ uint32) bool {
	return true
}

/* ----------------------------------------------------------------------- */

// putcommacode ports opj_t2_putcommacode.
func putcommacode(b *bio.BIO, n int32) {
	for {
		n--
		if n < 0 {
			break
		}
		b.PutBit(1)
	}
	b.PutBit(0)
}

// getcommacode ports opj_t2_getcommacode.
func getcommacode(b *bio.BIO) uint32 {
	n := uint32(0)
	for b.Read(1) != 0 {
		n++
	}
	return n
}

// putnumpasses ports opj_t2_putnumpasses.
func putnumpasses(b *bio.BIO, n uint32) {
	switch {
	case n == 1:
		b.PutBit(0)
	case n == 2:
		b.Write(2, 2)
	case n <= 5:
		b.Write(0xc|(n-3), 4)
	case n <= 36:
		b.Write(0x1e0|(n-6), 9)
	case n <= 164:
		b.Write(0xff80|(n-37), 16)
	}
}

// getnumpasses ports opj_t2_getnumpasses.
func getnumpasses(b *bio.BIO) uint32 {
	if b.Read(1) == 0 {
		return 1
	}
	if b.Read(1) == 0 {
		return 2
	}
	if n := b.Read(2); n != 3 {
		return 3 + n
	}
	if n := b.Read(5); n != 31 {
		return 6 + n
	}
	return 37 + b.Read(7)
}

/* ----------------------------------------------------------------------- */

// EncodePackets ports opj_t2_encode_packets. It writes the packets of tile
// tileno into dest (whose usable length is maxLen), returning the number of
// bytes written and whether encoding succeeded.
//
// In THRESH_CALC mode it simulates packet writing to size the layer; in
// FINAL_PASS it writes the real bytes and, if markerInfo.NeedPLT is set, records
// each packet size.
func (t2 *T2) EncodePackets(tileno uint32, t *tile.Tile, maxlayers uint32, dest []byte, maxLen uint32,
	cstrInfo *CodestreamInfo, markerInfo *tile.MarkerInfo, tpNum uint32, tpPos int32, pino uint32,
	t2Mode cparams.T2Mode, manager *event.Manager) (dataWritten uint32, ok bool) {

	cp := t2.CP
	img := t2.Image
	tcp := &cp.Tcps[tileno]
	pocno := uint32(1)
	if cp.Rsiz == cparams.ProfileCinema4K {
		pocno = 2
	}
	maxComp := uint32(1)
	if cp.MEnc.MMaxCompSize > 0 {
		maxComp = img.Numcomps
	}

	pis := pi.InitialiseEncode(img, cp, tileno, t2Mode, manager)
	if pis == nil {
		return 0, false
	}

	dataWritten = 0
	curOff := uint32(0)

	if t2Mode == cparams.ThreshCalc { // calculating threshold
		for compno := uint32(0); compno < maxComp; compno++ {
			compLen := uint32(0)
			for poc := uint32(0); poc < pocno; poc++ {
				tpNumLocal := compno
				pi.CreateEncode(pis, cp, tileno, poc, tpNumLocal, tpPos, t2Mode)
				cur := &pis[poc]
				if cur.Prg() == cparams.ProgUnknown {
					return dataWritten, false
				}
				for cur.Next() {
					if cur.Layno() < maxlayers {
						nb, packOK := t2.encodePacket(tileno, t, tcp, cur, dest[curOff:], maxLen, cstrInfo, t2Mode, manager)
						if !packOK {
							return dataWritten, false
						}
						compLen += nb
						curOff += nb
						maxLen -= nb
						dataWritten += nb
					}
				}
				if cp.MEnc.MMaxCompSize != 0 {
					if compLen > cp.MEnc.MMaxCompSize {
						return dataWritten, false
					}
				}
			}
		}
		return dataWritten, true
	}

	// FINAL_PASS
	pi.CreateEncode(pis, cp, tileno, pino, tpNum, tpPos, t2Mode)
	cur := &pis[pino]
	if cur.Prg() == cparams.ProgUnknown {
		return dataWritten, false
	}

	if markerInfo != nil && markerInfo.NeedPLT {
		// One time use intended.
		markerInfo.PacketSize = make([]uint32, pi.GetEncodingPacketCount(img, cp, tileno))
	}

	for cur.Next() {
		if cur.Layno() < maxlayers {
			nb, packOK := t2.encodePacket(tileno, t, tcp, cur, dest[curOff:], maxLen, cstrInfo, t2Mode, manager)
			if !packOK {
				return dataWritten, false
			}
			curOff += nb
			maxLen -= nb
			dataWritten += nb

			if markerInfo != nil && markerInfo.NeedPLT {
				markerInfo.PacketSize[markerInfo.PacketCount] = nb
				markerInfo.PacketCount++
			}

			// INDEX
			if cstrInfo != nil {
				if cstrInfo.IndexWrite {
					infoTL := &cstrInfo.Tile[tileno]
					infoPK := &infoTL.Packet[cstrInfo.Packno]
					if cstrInfo.Packno == 0 {
						infoPK.StartPos = infoTL.EndHeader + 1
					} else {
						if (cp.MEnc.MTpOn != 0 || tcp.POC != 0) && infoPK.StartPos != 0 {
							// keep infoPK.StartPos
						} else {
							infoPK.StartPos = infoTL.Packet[cstrInfo.Packno-1].EndPos + 1
						}
					}
					infoPK.EndPos = infoPK.StartPos + int32(nb) - 1
					infoPK.EndPhPos += infoPK.StartPos - 1
				}
				cstrInfo.Packno++
			}
			t.Packno++
		}
	}

	return dataWritten, true
}

// encodePacket ports opj_t2_encode_packet. dest is the current output position;
// length is the space remaining there. It returns the number of bytes written
// for this packet.
func (t2 *T2) encodePacket(tileno uint32, t *tile.Tile, tcp *cparams.TCP, p *pi.Iterator,
	dest []byte, length uint32, cstrInfo *CodestreamInfo, t2Mode cparams.T2Mode, manager *event.Manager) (uint32, bool) {

	compno := p.Compno()
	resno := p.Resno()
	precno := p.Precno()
	layno := p.Layno()

	tilec := &t.Comps[compno]
	res := &tilec.Resolutions[resno]

	cOff := uint32(0)
	packetEmpty := false // ENABLE_EMPTY_PACKET_OPTIMIZATION disabled

	// <SOP 0xff91>
	if tcp.Csty&cparams.CPCstySOP != 0 {
		if length < 6 {
			if t2Mode == cparams.FinalPass {
				errorf(manager, "opj_t2_encode_packet(): only %d bytes remaining in output buffer. %d needed.\n", length, 6)
			}
			return 0, false
		}
		dest[0] = 255
		dest[1] = 145
		dest[2] = 0
		dest[3] = 4
		dest[4] = byte((t.Packno >> 8) & 0xff)
		dest[5] = byte(t.Packno & 0xff)
		cOff += 6
		length -= 6
	}
	// </SOP>

	if layno == 0 {
		for bandno := uint32(0); bandno < res.Numbands; bandno++ {
			band := &res.Bands[bandno]
			if tile.IsBandEmpty(band) {
				continue
			}
			// Avoid out of bounds access (issue 1294).
			if precno >= res.Pw*res.Ph {
				errorf(manager, "opj_t2_encode_packet(): accessing precno=%d >= %d\n", precno, res.Pw*res.Ph)
				return 0, false
			}
			prc := &band.Precincts[precno]
			prc.Incltree.Reset()
			prc.Imsbtree.Reset()

			nbBlocks := prc.Cw * prc.Ch
			for cblkno := uint32(0); cblkno < nbBlocks; cblkno++ {
				cblk := &prc.CblksEnc[cblkno]
				cblk.Numpasses = 0
				prc.Imsbtree.SetValue(cblkno, band.Numbps-int32(cblk.Numbps))
			}
		}
	}

	b := bio.NewEncoder(dest[cOff : cOff+length])
	b.PutBit(boolToBit(!packetEmpty)) // empty header bit

	// Writing Packet header
	for bandno := uint32(0); !packetEmpty && bandno < res.Numbands; bandno++ {
		band := &res.Bands[bandno]
		if tile.IsBandEmpty(band) {
			continue
		}
		// Avoid out of bounds access (issue 1297).
		if precno >= res.Pw*res.Ph {
			errorf(manager, "opj_t2_encode_packet(): accessing precno=%d >= %d\n", precno, res.Pw*res.Ph)
			return 0, false
		}
		prc := &band.Precincts[precno]
		nbBlocks := prc.Cw * prc.Ch

		for cblkno := uint32(0); cblkno < nbBlocks; cblkno++ {
			cblk := &prc.CblksEnc[cblkno]
			layer := &cblk.Layers[layno]
			if cblk.Numpasses == 0 && layer.Numpasses != 0 {
				prc.Incltree.SetValue(cblkno, int32(layno))
			}
		}

		for cblkno := uint32(0); cblkno < nbBlocks; cblkno++ {
			cblk := &prc.CblksEnc[cblkno]
			layer := &cblk.Layers[layno]
			increment := uint32(0)
			nump := uint32(0)
			length2 := uint32(0)

			// cblk inclusion bits
			if cblk.Numpasses == 0 {
				prc.Incltree.Encode(b, cblkno, int32(layno+1))
			} else {
				b.PutBit(boolToBit(layer.Numpasses != 0))
			}

			// if cblk not included, go to the next cblk
			if layer.Numpasses == 0 {
				continue
			}

			// if first instance of cblk --> zero bit-planes information
			if cblk.Numpasses == 0 {
				cblk.Numlenbits = 3
				prc.Imsbtree.Encode(b, cblkno, 999)
			}

			// number of coding passes included
			putnumpasses(b, layer.Numpasses)
			nbPasses := cblk.Numpasses + layer.Numpasses

			// computation of the increase of the length indicator
			for passno := cblk.Numpasses; passno < nbPasses; passno++ {
				pass := &cblk.Passes[passno]
				nump++
				length2 += pass.Len
				if pass.Term || passno == (cblk.Numpasses+layer.Numpasses)-1 {
					increment = uint32(opjmath.IntMax(int32(increment),
						opjmath.IntFloorlog2(int32(length2))+1-(int32(cblk.Numlenbits)+opjmath.IntFloorlog2(int32(nump)))))
					length2 = 0
					nump = 0
				}
			}
			putcommacode(b, int32(increment))

			// computation of the new length indicator
			cblk.Numlenbits += increment

			// insertion of the codeword segment length
			for passno := cblk.Numpasses; passno < nbPasses; passno++ {
				pass := &cblk.Passes[passno]
				nump++
				length2 += pass.Len
				if pass.Term || passno == (cblk.Numpasses+layer.Numpasses)-1 {
					b.Write(length2, cblk.Numlenbits+uint32(opjmath.IntFloorlog2(int32(nump))))
					length2 = 0
					nump = 0
				}
			}
		}
	}

	if !b.Flush() {
		return 0, false // modified to eliminate longjmp
	}

	nbBytes := uint32(b.NumBytes())
	cOff += nbBytes
	length -= nbBytes

	// <EPH 0xff92>
	if tcp.Csty&cparams.CPCstyEPH != 0 {
		if length < 2 {
			if t2Mode == cparams.FinalPass {
				errorf(manager, "opj_t2_encode_packet(): only %d bytes remaining in output buffer. %d needed.\n", length, 2)
			}
			return 0, false
		}
		dest[cOff] = 255
		dest[cOff+1] = 146
		cOff += 2
		length -= 2
	}
	// </EPH>

	// INDEX: end of packet header position
	if cstrInfo != nil && cstrInfo.IndexWrite {
		infoPK := &cstrInfo.Tile[tileno].Packet[cstrInfo.Packno]
		infoPK.EndPhPos = int32(cOff)
	}

	// Writing the packet body
	for bandno := uint32(0); !packetEmpty && bandno < res.Numbands; bandno++ {
		band := &res.Bands[bandno]
		if tile.IsBandEmpty(band) {
			continue
		}
		prc := &band.Precincts[precno]
		nbBlocks := prc.Cw * prc.Ch

		for cblkno := uint32(0); cblkno < nbBlocks; cblkno++ {
			cblk := &prc.CblksEnc[cblkno]
			layer := &cblk.Layers[layno]
			if layer.Numpasses == 0 {
				continue
			}
			if layer.Len > length {
				if t2Mode == cparams.FinalPass {
					errorf(manager, "opj_t2_encode_packet(): only %d bytes remaining in output buffer. %d needed.\n", length, layer.Len)
				}
				return 0, false
			}
			if t2Mode == cparams.FinalPass {
				copy(dest[cOff:cOff+layer.Len], layer.Data[:layer.Len])
			}
			cblk.Numpasses += layer.Numpasses
			cOff += layer.Len
			length -= layer.Len

			// INDEX
			if cstrInfo != nil && cstrInfo.IndexWrite {
				infoPK := &cstrInfo.Tile[tileno].Packet[cstrInfo.Packno]
				infoPK.Disto += layer.Disto
				if cstrInfo.DMax < infoPK.Disto {
					cstrInfo.DMax = infoPK.Disto
				}
			}
		}
	}

	return cOff, true
}

// DecodePackets ports opj_t2_decode_packets. It reads the packets of tile
// tileno from src (usable length maxLen), returning the number of bytes read and
// whether decoding succeeded. Packets whose layer is beyond
// num_layers_to_decode, whose resolution is beyond minimum_num_resolutions, or
// whose precincts do not intersect the area of interest (per aoi) are skipped.
func (t2 *T2) DecodePackets(aoi AOIChecker, tileno uint32, t *tile.Tile, src []byte, maxLen uint32, manager *event.Manager) (dataRead uint32, ok bool) {
	img := t2.Image
	cp := t2.CP
	tcp := &cp.Tcps[tileno]

	pis := pi.CreateDecode(img, cp, tileno, manager)
	if pis == nil {
		return 0, false
	}

	curOff := uint32(0)

	for pino := uint32(0); pino <= tcp.Numpocs; pino++ {
		cur := &pis[pino]

		if cur.Prg() == cparams.ProgUnknown {
			return 0, false
		}

		firstPassFailed := make([]bool, img.Numcomps)
		for i := range firstPassFailed {
			firstPassFailed[i] = true
		}

		for cur.Next() {
			skipPacket := false

			// If the packet layer is >= the maximum number of layers, skip.
			if cur.Layno() >= tcp.NumLayersToDecode {
				skipPacket = true
			} else if cur.Resno() >= t.Comps[cur.Compno()].MinimumNumResolutions {
				// If the packet resolution number is beyond the minimum, skip.
				skipPacket = true
			} else {
				// If no precinct of any band intersects the area of interest, skip.
				tilec := &t.Comps[cur.Compno()]
				res := &tilec.Resolutions[cur.Resno()]
				skipPacket = true
				for bandno := uint32(0); bandno < res.Numbands; bandno++ {
					band := &res.Bands[bandno]
					prec := &band.Precincts[cur.Precno()]
					if aoi.IsSubbandAreaOfInterest(cur.Compno(), cur.Resno(), band.Bandno,
						uint32(prec.X0), uint32(prec.Y0), uint32(prec.X1), uint32(prec.Y1)) {
						skipPacket = false
						break
					}
				}
			}

			var nbBytesRead uint32
			if !skipPacket {
				firstPassFailed[cur.Compno()] = false
				n, packOK := t2.decodePacket(t, tcp, cur, src[curOff:], maxLen, manager)
				if !packOK {
					return 0, false
				}
				nbBytesRead = n
				imgComp := &img.Comps[cur.Compno()]
				imgComp.ResnoDecoded = opjmath.UintMax(cur.Resno(), imgComp.ResnoDecoded)
			} else {
				n, packOK := t2.skipPacket(t, tcp, cur, src[curOff:], maxLen, manager)
				if !packOK {
					return 0, false
				}
				nbBytesRead = n
			}

			if firstPassFailed[cur.Compno()] {
				imgComp := &img.Comps[cur.Compno()]
				if imgComp.ResnoDecoded == 0 {
					imgComp.ResnoDecoded = t.Comps[cur.Compno()].MinimumNumResolutions - 1
				}
			}

			curOff += nbBytesRead
			maxLen -= nbBytesRead
		}
	}

	dataRead = curOff
	return dataRead, true
}

// decodePacket ports opj_t2_decode_packet.
func (t2 *T2) decodePacket(t *tile.Tile, tcp *cparams.TCP, p *pi.Iterator, src []byte, maxLength uint32, manager *event.Manager) (uint32, bool) {
	var totalRead uint32

	readData, nbBytesRead, ok := t2.readPacketHeader(t, tcp, p, src, maxLength, nil, manager)
	if !ok {
		return 0, false
	}
	totalRead += nbBytesRead
	maxLength -= nbBytesRead

	if readData {
		n, dataOK := t2.readPacketData(t, p, src[nbBytesRead:], maxLength, manager)
		if !dataOK {
			return 0, false
		}
		totalRead += n
	}
	return totalRead, true
}

// skipPacket ports opj_t2_skip_packet.
func (t2 *T2) skipPacket(t *tile.Tile, tcp *cparams.TCP, p *pi.Iterator, src []byte, maxLength uint32, manager *event.Manager) (uint32, bool) {
	var totalRead uint32

	readData, nbBytesRead, ok := t2.readPacketHeader(t, tcp, p, src, maxLength, nil, manager)
	if !ok {
		return 0, false
	}
	totalRead += nbBytesRead
	maxLength -= nbBytesRead

	if readData {
		n, dataOK := t2.skipPacketData(t, p, maxLength, manager)
		if !dataOK {
			return 0, false
		}
		totalRead += n
	}
	return totalRead, true
}

// readPacketHeader ports opj_t2_read_packet_header. It returns whether packet
// body data follows, the number of codestream bytes consumed, and success.
//
// The packet header source is selected per the C code: PPM (cp.PpmData), PPT
// (tcp.PptData), or, in the normal case, the codestream itself. In the PPM/PPT
// cases the header bytes are consumed from those buffers (advancing them), while
// the codestream position only advances past any SOP marker.
func (t2 *T2) readPacketHeader(t *tile.Tile, tcp *cparams.TCP, p *pi.Iterator, src []byte, maxLength uint32, packInfo *PacketInfo, manager *event.Manager) (dataPresent bool, dataRead uint32, ok bool) {
	cp := t2.CP
	res := &t.Comps[p.Compno()].Resolutions[p.Resno()]

	// curData is the codestream cursor (offset into src). Only SOP advances it in
	// the PPM/PPT cases.
	curData := uint32(0)

	if p.Layno() == 0 {
		for bandno := uint32(0); bandno < res.Numbands; bandno++ {
			band := &res.Bands[bandno]
			if !tile.IsBandEmpty(band) {
				// Precinct index validity. The C code checks
				//   p_pi->precno < band->precincts_data_size / sizeof(opj_tcd_precinct_t)
				// i.e. precno must be within the allocated precinct array. In this
				// port the allocated array is band.Precincts, so its length is the
				// exact equivalent (and it also prevents the Go index panic that a
				// bare C pointer computation would not trigger).
				if !(uint64(p.Precno()) < uint64(len(band.Precincts))) {
					errorf(manager, "Invalid precinct\n")
					return false, 0, false
				}
				prc := &band.Precincts[p.Precno()]
				prc.Incltree.Reset()
				prc.Imsbtree.Reset()
				nbCodeBlocks := prc.Cw * prc.Ch
				for cblkno := uint32(0); cblkno < nbCodeBlocks; cblkno++ {
					prc.CblksDec[cblkno].Numsegs = 0
					prc.CblksDec[cblkno].RealNumSegs = 0
				}
			}
		}
	}

	// SOP markers
	if tcp.Csty&cparams.CPCstySOP != 0 {
		if maxLength < 6 {
			warnf(manager, "Not enough space for expected SOP marker\n")
		} else if src[curData] != 0xff || src[curData+1] != 0x91 {
			warnf(manager, "Expected SOP marker\n")
		} else {
			curData += 6
		}
		// TODO: check the Nsop value
	}

	// Header source selection (PPM / PPT / normal).
	var (
		headerData []byte // the header buffer at *l_header_data_start
		modLen     uint32 // *l_modified_length_ptr
	)
	const (
		srcNormal = iota
		srcPPM
		srcPPT
	)
	srcKind := srcNormal
	if cp.Ppm == 1 { // PPM
		srcKind = srcPPM
		headerData = cp.PpmData
		modLen = cp.PpmLen
	} else if tcp.Ppt == 1 { // PPT
		srcKind = srcPPT
		headerData = tcp.PptData
		modLen = tcp.PptLen
	} else { // Normal case
		srcKind = srcNormal
		headerData = src[curData:]
		modLen = maxLength - curData // p_src_data + p_max_length - l_header_data
	}

	b := bio.NewDecoder(headerData[:modLen])

	present := b.Read(1)
	if present == 0 {
		// no data present. C ignores opj_bio_inalign's result in this branch.
		b.InAlign()
		headerConsumed := uint32(b.NumBytes())

		// EPH markers (required when signalled)
		if tcp.Csty&cparams.CPCstyEPH != 0 {
			if modLen-headerConsumed < 2 {
				errorf(manager, "Not enough space for required EPH marker\n")
				return false, 0, false
			} else if headerData[headerConsumed] != 0xff || headerData[headerConsumed+1] != 0x92 {
				errorf(manager, "Expected EPH marker\n")
				return false, 0, false
			}
			headerConsumed += 2
		}

		headerLength := headerConsumed
		commitHeader(cp, tcp, srcKind, headerLength, &curData)

		if packInfo != nil {
			packInfo.EndPhPos = int32(curData)
		}
		return false, curData, true
	}

	for bandno := uint32(0); bandno < res.Numbands; bandno++ {
		band := &res.Bands[bandno]
		if tile.IsBandEmpty(band) {
			continue
		}
		prc := &band.Precincts[p.Precno()]
		nbCodeBlocks := prc.Cw * prc.Ch
		for cblkno := uint32(0); cblkno < nbCodeBlocks; cblkno++ {
			cblk := &prc.CblksDec[cblkno]
			var included, increment, segno uint32

			// if cblk not yet included --> inclusion tagtree, else one bit
			if cblk.Numsegs == 0 {
				included = prc.Incltree.Decode(b, cblkno, int32(p.Layno()+1))
			} else {
				included = b.Read(1)
			}

			// if cblk not included
			if included == 0 {
				cblk.Numnewpasses = 0
				continue
			}

			// if cblk not yet included --> zero-bitplane tagtree
			if cblk.Numsegs == 0 {
				i := uint32(0)
				for prc.Imsbtree.Decode(b, cblkno, int32(i)) == 0 {
					i++
				}
				cblk.Mb = uint32(band.Numbps)
				if uint32(band.Numbps)+1 < i {
					// Avoids the integer overflow of PR 1488 while keeping the
					// regression suite happy.
					cblk.Numbps = uint32(band.Numbps + 1 - int32(i))
				} else {
					cblk.Numbps = uint32(band.Numbps) + 1 - i
				}
				cblk.Numlenbits = 3
			}

			// number of coding passes
			cblk.Numnewpasses = getnumpasses(b)
			increment = getcommacode(b)

			// length indicator increment
			cblk.Numlenbits += increment
			segno = 0

			if cblk.Numsegs == 0 {
				if !initSeg(cblk, segno, tcp.TCCPs[p.Compno()].Cblksty, 1) {
					return false, 0, false
				}
			} else {
				segno = cblk.Numsegs - 1
				if cblk.Segs[segno].Numpasses == cblk.Segs[segno].Maxpasses {
					segno++
					if !initSeg(cblk, segno, tcp.TCCPs[p.Compno()].Cblksty, 0) {
						return false, 0, false
					}
				}
			}
			n := int32(cblk.Numnewpasses)

			if tcp.TCCPs[p.Compno()].Cblksty&cparams.CCPCblkStyHT != 0 {
				for {
					if segno == 0 {
						cblk.Segs[segno].Numnewpasses = 1
					} else {
						cblk.Segs[segno].Numnewpasses = uint32(n)
					}
					bitNumber := cblk.Numlenbits + opjmath.UintFloorlog2(cblk.Segs[segno].Numnewpasses)
					if bitNumber > 32 {
						errorf(manager, "Invalid bit number %d in opj_t2_read_packet_header()\n", bitNumber)
						return false, 0, false
					}
					cblk.Segs[segno].Newlen = b.Read(bitNumber)
					n -= int32(cblk.Segs[segno].Numnewpasses)
					if n > 0 {
						segno++
						if !initSeg(cblk, segno, tcp.TCCPs[p.Compno()].Cblksty, 0) {
							return false, 0, false
						}
					}
					if n <= 0 {
						break
					}
				}
			} else {
				for {
					cblk.Segs[segno].Numnewpasses = uint32(opjmath.IntMin(
						int32(cblk.Segs[segno].Maxpasses-cblk.Segs[segno].Numpasses), n))
					bitNumber := cblk.Numlenbits + opjmath.UintFloorlog2(cblk.Segs[segno].Numnewpasses)
					if bitNumber > 32 {
						errorf(manager, "Invalid bit number %d in opj_t2_read_packet_header()\n", bitNumber)
						return false, 0, false
					}
					cblk.Segs[segno].Newlen = b.Read(bitNumber)
					n -= int32(cblk.Segs[segno].Numnewpasses)
					if n > 0 {
						segno++
						if !initSeg(cblk, segno, tcp.TCCPs[p.Compno()].Cblksty, 0) {
							return false, 0, false
						}
					}
					if n <= 0 {
						break
					}
				}
			}
		}
	}

	if !b.InAlign() {
		return false, 0, false
	}
	headerConsumed := uint32(b.NumBytes())

	// EPH markers (required when signalled)
	if tcp.Csty&cparams.CPCstyEPH != 0 {
		if modLen-headerConsumed < 2 {
			errorf(manager, "Not enough space for required EPH marker\n")
			return false, 0, false
		} else if headerData[headerConsumed] != 0xff || headerData[headerConsumed+1] != 0x92 {
			errorf(manager, "Expected EPH marker\n")
			return false, 0, false
		}
		headerConsumed += 2
	}

	headerLength := headerConsumed
	if headerLength == 0 {
		return false, 0, false
	}
	commitHeader(cp, tcp, srcKind, headerLength, &curData)

	if packInfo != nil {
		packInfo.EndPhPos = int32(curData)
	}
	return true, curData, true
}

// commitHeader applies the consumption of headerLength bytes from the selected
// header source: for the normal (codestream) case it advances the codestream
// cursor; for PPM/PPT it advances (and shortens) the packed-header buffers.
func commitHeader(cp *cparams.CP, tcp *cparams.TCP, srcKind int, headerLength uint32, curData *uint32) {
	switch srcKind {
	case 1: // PPM
		cp.PpmData = cp.PpmData[headerLength:]
		cp.PpmLen -= headerLength
	case 2: // PPT
		tcp.PptData = tcp.PptData[headerLength:]
		tcp.PptLen -= headerLength
	default: // normal: header source aliases the codestream cursor
		*curData += headerLength
	}
}

// readPacketData ports opj_t2_read_packet_data.
func (t2 *T2) readPacketData(t *tile.Tile, p *pi.Iterator, src []byte, maxLength uint32, manager *event.Manager) (uint32, bool) {
	res := &t.Comps[p.Compno()].Resolutions[p.Resno()]
	curData := uint32(0)
	partialBuffer := false

	for bandno := uint32(0); bandno < res.Numbands; bandno++ {
		band := &res.Bands[bandno]
		prc := &band.Precincts[p.Precno()]

		if (band.X1-band.X0 == 0) || (band.Y1-band.Y0 == 0) {
			continue
		}

		nbCodeBlocks := prc.Cw * prc.Ch
		for cblkno := uint32(0); cblkno < nbCodeBlocks; cblkno++ {
			cblk := &prc.CblksDec[cblkno]

			if cblk.Numnewpasses == 0 {
				continue
			}

			if partialBuffer || cblk.Corrupted {
				// A previous segment in this packet couldn't be decoded, or this
				// code block was corrupted in a previous layer: mark corrupted.
				cblk.Numchunks = 0
				cblk.Corrupted = true
				continue
			}

			var segIdx uint32
			if cblk.Numsegs == 0 {
				segIdx = 0
				cblk.Numsegs++
			} else {
				segIdx = cblk.Numsegs - 1
				if cblk.Segs[segIdx].Numpasses == cblk.Segs[segIdx].Maxpasses {
					segIdx++
					cblk.Numsegs++
				}
			}

			for {
				seg := &cblk.Segs[segIdx]
				// Check size (and possible overflow) then partial_buffer.
				if uint64(curData)+uint64(seg.Newlen) > uint64(maxLength) || partialBuffer {
					if t2.CP.Strict {
						errorf(manager, "read: segment too long (%d) with max (%d) for codeblock %d (p=%d, b=%d, r=%d, c=%d)\n",
							seg.Newlen, maxLength, cblkno, p.Precno(), bandno, p.Resno(), p.Compno())
						return 0, false
					}
					warnf(manager, "read: segment too long (%d) with max (%d) for codeblock %d (p=%d, b=%d, r=%d, c=%d)\n",
						seg.Newlen, maxLength, cblkno, p.Precno(), bandno, p.Resno(), p.Compno())
					// Skip this codeblock (and following ones in this packet).
					partialBuffer = true
					cblk.Corrupted = true
					cblk.Numchunks = 0
					break
				}

				cblk.Chunks = append(cblk.Chunks[:cblk.Numchunks], tile.SegDataChunk{
					Data: src[curData : curData+seg.Newlen],
					Len:  seg.Newlen,
				})
				cblk.Numchunks++
				if uint32(len(cblk.Chunks)) > cblk.Numchunksalloc {
					cblk.Numchunksalloc = uint32(len(cblk.Chunks))
				}

				curData += seg.Newlen
				seg.Len += seg.Newlen
				seg.Numpasses += seg.Numnewpasses
				cblk.Numnewpasses -= seg.Numnewpasses
				seg.RealNumPasses = seg.Numpasses

				if cblk.Numnewpasses > 0 {
					segIdx++
					cblk.Numsegs++
				}
				if cblk.Numnewpasses == 0 {
					break
				}
			}

			cblk.RealNumSegs = cblk.Numsegs
		}
	}

	if partialBuffer {
		return maxLength, true
	}
	return curData, true
}

// skipPacketData ports opj_t2_skip_packet_data.
func (t2 *T2) skipPacketData(t *tile.Tile, p *pi.Iterator, maxLength uint32, manager *event.Manager) (uint32, bool) {
	res := &t.Comps[p.Compno()].Resolutions[p.Resno()]
	var dataRead uint32

	for bandno := uint32(0); bandno < res.Numbands; bandno++ {
		band := &res.Bands[bandno]
		prc := &band.Precincts[p.Precno()]

		if (band.X1-band.X0 == 0) || (band.Y1-band.Y0 == 0) {
			continue
		}

		nbCodeBlocks := prc.Cw * prc.Ch
		for cblkno := uint32(0); cblkno < nbCodeBlocks; cblkno++ {
			cblk := &prc.CblksDec[cblkno]

			if cblk.Numnewpasses == 0 {
				continue
			}

			var segIdx uint32
			if cblk.Numsegs == 0 {
				segIdx = 0
				cblk.Numsegs++
			} else {
				segIdx = cblk.Numsegs - 1
				if cblk.Segs[segIdx].Numpasses == cblk.Segs[segIdx].Maxpasses {
					segIdx++
					cblk.Numsegs++
				}
			}

			for {
				seg := &cblk.Segs[segIdx]
				// Check size (and possible overflow).
				if uint64(dataRead)+uint64(seg.Newlen) > uint64(maxLength) {
					if t2.CP.Strict {
						errorf(manager, "skip: segment too long (%d) with max (%d) for codeblock %d (p=%d, b=%d, r=%d, c=%d)\n",
							seg.Newlen, maxLength, cblkno, p.Precno(), bandno, p.Resno(), p.Compno())
						return 0, false
					}
					warnf(manager, "skip: segment too long (%d) with max (%d) for codeblock %d (p=%d, b=%d, r=%d, c=%d)\n",
						seg.Newlen, maxLength, cblkno, p.Precno(), bandno, p.Resno(), p.Compno())
					return maxLength, true
				}

				dataRead += seg.Newlen
				seg.Numpasses += seg.Numnewpasses
				cblk.Numnewpasses -= seg.Numnewpasses
				if cblk.Numnewpasses > 0 {
					segIdx++
					cblk.Numsegs++
				}
				if cblk.Numnewpasses == 0 {
					break
				}
			}
		}
	}

	return dataRead, true
}

// initSeg ports opj_t2_init_seg. It (re)initialises segment index for a
// code-block, growing the segment slice by DefaultNbSegs when needed and setting
// maxpasses from the code-block style.
func initSeg(cblk *tile.CblkDec, index, cblksty, first uint32) bool {
	nbSegs := index + 1
	if nbSegs > cblk.MCurrentMaxSegs {
		newMax := cblk.MCurrentMaxSegs + cparams.DefaultNbSegs
		newSegs := make([]tile.Seg, newMax)
		copy(newSegs, cblk.Segs)
		cblk.Segs = newSegs
		cblk.MCurrentMaxSegs = newMax
	}

	seg := &cblk.Segs[index]
	tile.ReinitSegment(seg)

	switch {
	case cblksty&cparams.CCPCblkStyTermall != 0:
		seg.Maxpasses = 1
	case cblksty&cparams.CCPCblkStyLazy != 0:
		if first != 0 {
			seg.Maxpasses = 10
		} else {
			prev := cblk.Segs[index-1].Maxpasses
			if prev == 1 || prev == 10 {
				seg.Maxpasses = 2
			} else {
				seg.Maxpasses = 1
			}
		}
	default:
		// See B.10.6 "Number of coding passes": (Mb-1)*3+1 with Mb=37 => 109.
		seg.Maxpasses = 109
	}
	return true
}

// boolToBit returns 1 for true and 0 for false, matching the C idiom of writing
// a boolean expression into a single bit.
func boolToBit(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

// errorf emits an EVT_ERROR message through the (optional) event manager.
func errorf(manager *event.Manager, format string, args ...any) {
	if manager != nil {
		manager.Errorf(format, args...)
	}
}

// warnf emits an EVT_WARNING message through the (optional) event manager.
func warnf(manager *event.Manager, format string, args ...any) {
	if manager != nil {
		manager.Warnf(format, args...)
	}
}
