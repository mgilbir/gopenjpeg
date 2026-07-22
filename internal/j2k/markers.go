package j2k

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// mreader is a big-endian cursor over a marker segment payload.
type mreader struct {
	data []byte
	pos  int
}

// u reads the next n bytes (big-endian) from the marker segment and advances
// the cursor. It is bounds-safe: bytes past the end of the segment read as 0.
//
// The C opj_read_bytes (used by opj_j2k_read_SQcd_SQcc and friends) reads its n
// bytes directly from the marker-segment scratch buffer *before* the running
// *p_header_size accounting is validated, so a short/truncated segment makes C
// over-read past the malloc'd buffer (a heap over-read ASan flags; tolerated
// only because the immediately following size check returns OPJ_FALSE and the
// over-read value is discarded). Zero-extending past the end reproduces that
// outcome without indexing out of range: the same size checks in the callers
// then reject the segment with an error. Faithful in result, panic-free.
func (r *mreader) u(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		var b uint32
		if idx := r.pos + i; idx >= 0 && idx < len(r.data) {
			b = uint32(r.data[idx])
		}
		v = (v << 8) | b
	}
	r.pos += n
	return v
}
func (r *mreader) rem() int { return len(r.data) - r.pos }

// readSIZ ports opj_j2k_read_siz.
func (d *Decoder) readSIZ(data []byte) error {
	img := d.privateImage
	cp := &d.CP

	if len(data) < 36 {
		d.mgr.Errorf("Error with SIZ marker size\n")
		return ErrBadSIZ
	}
	remainingSize := len(data) - 36
	nbComp := remainingSize / 3
	if remainingSize%3 != 0 {
		d.mgr.Errorf("Error with SIZ marker size\n")
		return ErrBadSIZ
	}

	r := &mreader{data: data}
	cp.Rsiz = uint16(r.u(2)) // Rsiz
	img.X1 = r.u(4)          // Xsiz
	img.Y1 = r.u(4)          // Ysiz
	img.X0 = r.u(4)          // X0siz
	img.Y0 = r.u(4)          // Y0siz
	cp.Tdx = r.u(4)          // XTsiz
	cp.Tdy = r.u(4)          // YTsiz
	cp.Tx0 = r.u(4)          // XT0siz
	cp.Ty0 = r.u(4)          // YT0siz
	csiz := r.u(2)           // Csiz
	if csiz < 16385 {
		img.Numcomps = csiz
	} else {
		d.mgr.Errorf("Error with SIZ marker: number of component is illegal -> %d\n", csiz)
		return ErrBadSIZ
	}
	if img.Numcomps != uint32(nbComp) {
		d.mgr.Errorf("Error with SIZ marker: number of component is not compatible with the remaining number of parameters (%d vs %d)\n", img.Numcomps, nbComp)
		return ErrBadSIZ
	}
	if img.X0 >= img.X1 || img.Y0 >= img.Y1 {
		d.mgr.Errorf("Error with SIZ marker: negative or zero image size\n")
		return ErrBadSIZ
	}
	if cp.Tdx == 0 || cp.Tdy == 0 {
		d.mgr.Errorf("Error with SIZ marker: invalid tile size (tdx: %d, tdy: %d)\n", cp.Tdx, cp.Tdy)
		return ErrBadSIZ
	}
	tx1 := uintAdds(cp.Tx0, cp.Tdx)
	ty1 := uintAdds(cp.Ty0, cp.Tdy)
	if cp.Tx0 > img.X0 || cp.Ty0 > img.Y0 || tx1 <= img.X0 || ty1 <= img.Y0 {
		d.mgr.Errorf("Error with SIZ marker: illegal tile offset\n")
		return ErrBadSIZ
	}
	sizW := img.X1 - img.X0
	sizH := img.Y1 - img.Y0
	if d.ihdrW > 0 && d.ihdrH > 0 && (d.ihdrW != sizW || d.ihdrH != sizH) {
		d.mgr.Errorf("Error with SIZ marker: IHDR w(%u) h(%u) vs. SIZ w(%u) h(%u)\n", d.ihdrW, d.ihdrH, sizW, sizH)
		return ErrBadSIZ
	}

	img.Comps = make([]image.Comp, img.Numcomps)
	var prec0, sgnd0 uint32
	for i := uint32(0); i < img.Numcomps; i++ {
		c := &img.Comps[i]
		tmp := r.u(1) // Ssiz_i
		c.Prec = (tmp & 0x7f) + 1
		c.Sgnd = tmp >> 7
		if i == 0 {
			prec0 = c.Prec
			sgnd0 = c.Sgnd
		} else if !cp.AllowDifferentBitDepthSign && (c.Prec != prec0 || c.Sgnd != sgnd0) {
			d.mgr.Warnf("Despite JP2 BPC!=255, precision and/or sgnd values for comp[%d] is different than comp[0]\n", i)
		}
		c.Dx = r.u(1) // XRsiz_i
		c.Dy = r.u(1) // YRsiz_i
		if c.Dx < 1 || c.Dx > 255 || c.Dy < 1 || c.Dy > 255 {
			d.mgr.Errorf("Invalid values for comp = %d : dx=%u dy=%u (should be between 1 and 255)\n", i, c.Dx, c.Dy)
			return ErrBadSIZ
		}
		if c.Prec > 31 {
			d.mgr.Errorf("Invalid values for comp = %d : prec=%u (OpenJpeg only supports up to 31)\n", i, c.Prec)
			return ErrBadSIZ
		}
		c.ResnoDecoded = 0
		c.Factor = cp.MDec.MReduce
	}

	cp.Tw = uintCeildiv(img.X1-cp.Tx0, cp.Tdx)
	cp.Th = uintCeildiv(img.Y1-cp.Ty0, cp.Tdy)
	if cp.Tw == 0 || cp.Th == 0 || cp.Tw > 65535/cp.Th {
		d.mgr.Errorf("Invalid number of tiles : %u x %u (maximum fixed by jpeg2000 norm is 65535 tiles)\n", cp.Tw, cp.Th)
		return ErrBadSIZ
	}
	nbTiles := cp.Tw * cp.Th

	if d.dec.discardTiles {
		d.dec.startTileX = (d.dec.startTileX - cp.Tx0) / cp.Tdx
		d.dec.startTileY = (d.dec.startTileY - cp.Ty0) / cp.Tdy
		d.dec.endTileX = uintCeildiv(d.dec.endTileX-cp.Tx0, cp.Tdx)
		d.dec.endTileY = uintCeildiv(d.dec.endTileY-cp.Ty0, cp.Tdy)
	} else {
		d.dec.startTileX = 0
		d.dec.startTileY = 0
		d.dec.endTileX = cp.Tw
		d.dec.endTileY = cp.Th
	}

	cp.Tcps = make([]cparams.TCP, nbTiles)
	d.dec.defaultTCP.TCCPs = make([]cparams.TCCP, img.Numcomps)
	d.dec.defaultTCP.MMctRecords = make([]cparams.MctData, mctDefaultNbRecords)
	d.dec.defaultTCP.MNbMaxMctRecords = mctDefaultNbRecords
	d.dec.defaultTCP.MccRecords = make([]cparams.MccData, mccDefaultNbRecords)
	d.dec.defaultTCP.MNbMaxMccRecords = mccDefaultNbRecords

	// Default DC level shift for unsigned components.
	for i := uint32(0); i < img.Numcomps; i++ {
		if img.Comps[i].Sgnd == 0 {
			d.dec.defaultTCP.TCCPs[i].MDcLevelShift = 1 << (img.Comps[i].Prec - 1)
		}
	}
	for i := uint32(0); i < nbTiles; i++ {
		cp.Tcps[i].TCCPs = make([]cparams.TCCP, img.Numcomps)
	}

	d.dec.state = stMH
	img.CompHeaderUpdate(&image.CompHeaderUpdateParams{
		Tx0: cp.Tx0, Ty0: cp.Ty0, Tdx: cp.Tdx, Tdy: cp.Tdy, Tw: cp.Tw, Th: cp.Th,
	})
	return nil
}

// readCOM ports opj_j2k_read_com (ignored).
func (d *Decoder) readCOM(data []byte) error { return nil }

// readCRG ports opj_j2k_read_crg.
func (d *Decoder) readCRG(data []byte) error {
	if len(data) != int(d.privateImage.Numcomps*4) {
		d.mgr.Errorf("Error reading CRG marker\n")
		return ErrMarkerHandler
	}
	return nil
}

// readCAP / readCPF ports the empty CAP/CPF handlers.
func (d *Decoder) readCAP(data []byte) error { return nil }
func (d *Decoder) readCPF(data []byte) error { return nil }

// readTLM/readPLM/readPLT: parse-and-ignore. These are index hints only; the
// decode result is identical whether or not they are used, and this port reads
// the codestream sequentially, so we simply validate nothing and continue.
func (d *Decoder) readTLM(data []byte) error { return nil }
func (d *Decoder) readPLM(data []byte) error { return nil }
func (d *Decoder) readPLT(data []byte) error { return nil }

// readCOD ports opj_j2k_read_cod.
func (d *Decoder) readCOD(data []byte) error {
	img := d.privateImage
	cp := &d.CP
	tcp := d.tcpAt()
	tcp.Cod = true

	if len(data) < 5 {
		d.mgr.Errorf("Error reading COD marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	tcp.Csty = r.u(1) // Scod
	if tcp.Csty&^uint32(cpCstyPRT|cpCstySOP|cpCstyEPH) != 0 {
		d.mgr.Errorf("Unknown Scod value in COD marker\n")
		return ErrMarkerHandler
	}
	prg := r.u(1) // SGcod (A)
	tcp.Prg = cparams.ProgOrder(prg)
	if tcp.Prg > cparams.CPRL {
		d.mgr.Errorf("Unknown progression order in COD marker\n")
		tcp.Prg = cparams.ProgUnknown
	}
	tcp.Numlayers = r.u(2) // SGcod (B)
	if tcp.Numlayers < 1 || tcp.Numlayers > 65535 {
		d.mgr.Errorf("Invalid number of layers in COD marker : %d not in range [1-65535]\n", tcp.Numlayers)
		return ErrMarkerHandler
	}
	if cp.MDec.MLayer != 0 {
		tcp.NumLayersToDecode = cp.MDec.MLayer
	} else {
		tcp.NumLayersToDecode = tcp.Numlayers
	}
	tcp.MCT = r.u(1) // SGcod (C)
	if tcp.MCT > 1 {
		d.mgr.Errorf("Invalid multiple component transformation\n")
		return ErrMarkerHandler
	}
	for i := uint32(0); i < img.Numcomps; i++ {
		tcp.TCCPs[i].Csty = tcp.Csty & ccpCstyPRT
	}
	hs := len(data) - 5
	if err := d.readSPCodSPCoc(tcp, 0, data[r.pos:], &hs); err != nil {
		d.mgr.Errorf("Error reading COD marker\n")
		return err
	}
	if hs != 0 {
		d.mgr.Errorf("Error reading COD marker\n")
		return ErrMarkerHandler
	}
	d.copyTileComponentParameters(tcp)
	return nil
}

// readCOC ports opj_j2k_read_coc.
func (d *Decoder) readCOC(data []byte) error {
	img := d.privateImage
	tcp := d.tcpAt()
	compRoom := 1
	if img.Numcomps > 256 {
		compRoom = 2
	}
	if len(data) < compRoom+1 {
		d.mgr.Errorf("Error reading COC marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	compNo := r.u(compRoom) // Ccoc
	if compNo >= img.Numcomps {
		d.mgr.Errorf("Error reading COC marker (bad number of components)\n")
		return ErrMarkerHandler
	}
	tcp.TCCPs[compNo].Csty = r.u(1) // Scoc
	hs := len(data) - compRoom - 1
	if err := d.readSPCodSPCoc(tcp, compNo, data[r.pos:], &hs); err != nil {
		d.mgr.Errorf("Error reading COC marker\n")
		return err
	}
	if hs != 0 {
		d.mgr.Errorf("Error reading COC marker\n")
		return ErrMarkerHandler
	}
	return nil
}

// readSPCodSPCoc ports opj_j2k_read_SPCod_SPCoc. data is the payload after the
// component/style bytes; *hs is the remaining header size, decremented in place.
func (d *Decoder) readSPCodSPCoc(tcp *cparams.TCP, compno uint32, data []byte, hs *int) error {
	cp := &d.CP
	tccp := &tcp.TCCPs[compno]
	if *hs < 5 {
		d.mgr.Errorf("Error reading SPCod SPCoc element\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	tccp.Numresolutions = r.u(1) + 1
	if tccp.Numresolutions > maxRLvls {
		d.mgr.Errorf("Invalid value for numresolutions : %d, max value is %d\n", tccp.Numresolutions, maxRLvls)
		return ErrMarkerHandler
	}
	if cp.MDec.MReduce >= tccp.Numresolutions {
		d.mgr.Errorf("Error decoding component %d. The number of resolutions to remove (%d) is greater or equal than the number of resolutions (%d)\n", compno, cp.MDec.MReduce, tccp.Numresolutions)
		d.dec.state |= stErr
		return ErrMarkerHandler
	}
	tccp.Cblkw = r.u(1) + 2
	tccp.Cblkh = r.u(1) + 2
	if tccp.Cblkw > 10 || tccp.Cblkh > 10 || tccp.Cblkw+tccp.Cblkh > 12 {
		d.mgr.Errorf("Error reading SPCod SPCoc element, Invalid cblkw/cblkh combination\n")
		return ErrMarkerHandler
	}
	tccp.Cblksty = r.u(1)
	if tccp.Cblksty&ccpCblkStyHTMixed != 0 {
		d.mgr.Errorf("Error reading SPCod SPCoc element. Unsupported Mixed HT code-block style found\n")
		return ErrMarkerHandler
	}
	tccp.Qmfbid = r.u(1)
	if tccp.Qmfbid > 1 {
		d.mgr.Errorf("Error reading SPCod SPCoc element, Invalid transformation found\n")
		return ErrMarkerHandler
	}
	*hs -= 5
	if tccp.Csty&ccpCstyPRT != 0 {
		if *hs < int(tccp.Numresolutions) {
			d.mgr.Errorf("Error reading SPCod SPCoc element\n")
			return ErrMarkerHandler
		}
		for i := uint32(0); i < tccp.Numresolutions; i++ {
			tmp := r.u(1)
			if i != 0 && ((tmp&0xf) == 0 || (tmp>>4) == 0) {
				d.mgr.Errorf("Invalid precinct size\n")
				return ErrMarkerHandler
			}
			tccp.Prcw[i] = tmp & 0xf
			tccp.Prch[i] = tmp >> 4
		}
		*hs -= int(tccp.Numresolutions)
	} else {
		for i := uint32(0); i < tccp.Numresolutions; i++ {
			tccp.Prcw[i] = 15
			tccp.Prch[i] = 15
		}
	}
	return nil
}

// copyTileComponentParameters ports opj_j2k_copy_tile_component_parameters.
func (d *Decoder) copyTileComponentParameters(tcp *cparams.TCP) {
	ref := &tcp.TCCPs[0]
	for i := uint32(1); i < d.privateImage.Numcomps; i++ {
		c := &tcp.TCCPs[i]
		c.Numresolutions = ref.Numresolutions
		c.Cblkw = ref.Cblkw
		c.Cblkh = ref.Cblkh
		c.Cblksty = ref.Cblksty
		c.Qmfbid = ref.Qmfbid
		c.Prcw = ref.Prcw
		c.Prch = ref.Prch
	}
}

// readQCD ports opj_j2k_read_qcd.
func (d *Decoder) readQCD(data []byte) error {
	tcp := d.tcpAt()
	hs := len(data)
	if err := d.readSQcdSQcc(tcp, 0, data, &hs); err != nil {
		d.mgr.Errorf("Error reading QCD marker\n")
		return err
	}
	if hs != 0 {
		d.mgr.Errorf("Error reading QCD marker\n")
		return ErrMarkerHandler
	}
	d.copyTileQuantizationParameters(tcp)
	return nil
}

// readQCC ports opj_j2k_read_qcc.
func (d *Decoder) readQCC(data []byte) error {
	img := d.privateImage
	tcp := d.tcpAt()
	r := &mreader{data: data}
	var compNo uint32
	if img.Numcomps <= 256 {
		if len(data) < 1 {
			d.mgr.Errorf("Error reading QCC marker\n")
			return ErrMarkerHandler
		}
		compNo = r.u(1)
	} else {
		if len(data) < 2 {
			d.mgr.Errorf("Error reading QCC marker\n")
			return ErrMarkerHandler
		}
		compNo = r.u(2)
	}
	if compNo >= img.Numcomps {
		d.mgr.Errorf("Invalid component number: %d, regarding the number of components %d\n", compNo, img.Numcomps)
		return ErrMarkerHandler
	}
	hs := len(data) - r.pos
	if err := d.readSQcdSQcc(tcp, compNo, data[r.pos:], &hs); err != nil {
		d.mgr.Errorf("Error reading QCC marker\n")
		return err
	}
	if hs != 0 {
		d.mgr.Errorf("Error reading QCC marker\n")
		return ErrMarkerHandler
	}
	return nil
}

// readSQcdSQcc ports opj_j2k_read_SQcd_SQcc.
func (d *Decoder) readSQcdSQcc(tcp *cparams.TCP, compno uint32, data []byte, hs *int) error {
	tccp := &tcp.TCCPs[compno]
	if *hs < 1 {
		d.mgr.Errorf("Error reading SQcd or SQcc element\n")
		return ErrMarkerHandler
	}
	*hs--
	r := &mreader{data: data}
	tmp := r.u(1) // Sqcx
	tccp.Qntsty = tmp & 0x1f
	tccp.Numgbits = tmp >> 5
	var numBand uint32
	if tccp.Qntsty == ccpQntStySiQnt {
		numBand = 1
	} else {
		if tccp.Qntsty == ccpQntStyNoQnt {
			numBand = uint32(*hs)
		} else {
			numBand = uint32(*hs) / 2
		}
		if numBand > maxBands {
			d.mgr.Warnf("While reading CCP_QNTSTY element, number of subbands (%d) is greater than OPJ_J2K_MAXBANDS (%d).\n", numBand, maxBands)
		}
	}

	if tccp.Qntsty == ccpQntStyNoQnt {
		for bandNo := uint32(0); bandNo < numBand; bandNo++ {
			t := r.u(1) // SPqcx_i
			if bandNo < maxBands {
				tccp.Stepsizes[bandNo].Expn = int32(t >> 3)
				tccp.Stepsizes[bandNo].Mant = 0
			}
		}
		if *hs < int(numBand) {
			return ErrMarkerHandler
		}
		*hs -= int(numBand)
	} else {
		for bandNo := uint32(0); bandNo < numBand; bandNo++ {
			t := r.u(2) // SPqcx_i
			if bandNo < maxBands {
				tccp.Stepsizes[bandNo].Expn = int32(t >> 11)
				tccp.Stepsizes[bandNo].Mant = int32(t & 0x7ff)
			}
		}
		if *hs < int(2*numBand) {
			return ErrMarkerHandler
		}
		*hs -= int(2 * numBand)
	}

	// Scalar derived: compute other stepsizes.
	if tccp.Qntsty == ccpQntStySiQnt {
		for bandNo := uint32(1); bandNo < maxBands; bandNo++ {
			e := tccp.Stepsizes[0].Expn - int32((bandNo-1)/3)
			if e < 0 {
				e = 0
			}
			tccp.Stepsizes[bandNo].Expn = e
			tccp.Stepsizes[bandNo].Mant = tccp.Stepsizes[0].Mant
		}
	}
	return nil
}

// copyTileQuantizationParameters ports opj_j2k_copy_tile_quantization_parameters.
func (d *Decoder) copyTileQuantizationParameters(tcp *cparams.TCP) {
	ref := &tcp.TCCPs[0]
	for i := uint32(1); i < d.privateImage.Numcomps; i++ {
		c := &tcp.TCCPs[i]
		c.Qntsty = ref.Qntsty
		c.Numgbits = ref.Numgbits
		c.Stepsizes = ref.Stepsizes
	}
}

// readRGN ports opj_j2k_read_rgn.
func (d *Decoder) readRGN(data []byte) error {
	img := d.privateImage
	tcp := d.tcpAt()
	compRoom := 1
	if img.Numcomps > 256 {
		compRoom = 2
	}
	if len(data) != 2+compRoom {
		d.mgr.Errorf("Error reading RGN marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	compNo := r.u(compRoom) // Crgn
	_ = r.u(1)              // Srgn
	if compNo >= img.Numcomps {
		d.mgr.Errorf("bad component number in RGN (%d when there are only %d)\n", compNo, img.Numcomps)
		return ErrMarkerHandler
	}
	tcp.TCCPs[compNo].Roishift = int32(r.u(1)) // SPrgn
	return nil
}

// readPOC ports opj_j2k_read_poc.
func (d *Decoder) readPOC(data []byte) error {
	img := d.privateImage
	tcp := d.tcpAt()
	nbComp := img.Numcomps
	compRoom := 1
	if nbComp > 256 {
		compRoom = 2
	}
	chunkSize := 5 + 2*compRoom
	currentPocNb := len(data) / chunkSize
	if currentPocNb <= 0 || len(data)%chunkSize != 0 {
		d.mgr.Errorf("Error reading POC marker\n")
		return ErrMarkerHandler
	}
	var oldPocNb uint32
	if tcp.POC != 0 {
		oldPocNb = tcp.Numpocs + 1
	}
	total := uint32(currentPocNb) + oldPocNb
	if total >= maxPocs {
		d.mgr.Errorf("Too many POCs %d\n", total)
		return ErrMarkerHandler
	}
	tcp.POC = 1
	r := &mreader{data: data}
	for i := oldPocNb; i < total; i++ {
		p := &tcp.Pocs[i]
		p.Resno0 = r.u(1)                          // RSpoc_i
		p.Compno0 = r.u(compRoom)                  // CSpoc_i
		p.Layno1 = uintMin(r.u(2), tcp.Numlayers)  // LYEpoc_i (clamped)
		p.Resno1 = r.u(1)                          // REpoc_i
		p.Compno1 = uintMin(r.u(compRoom), nbComp) // CEpoc_i (clamped)
		p.Prg = cparams.ProgOrder(r.u(1))          // Ppoc_i
	}
	tcp.Numpocs = total - 1
	return nil
}

// readPPM ports opj_j2k_read_ppm.
func (d *Decoder) readPPM(data []byte) error {
	cp := &d.CP
	if len(data) < 2 {
		d.mgr.Errorf("Error reading PPM marker\n")
		return ErrMarkerHandler
	}
	cp.Ppm = 1
	zppm := uint32(data[0])
	payload := data[1:]

	if cp.PpmMarkers == nil {
		cp.PpmMarkers = make([]cparams.Ppx, zppm+1)
		cp.PpmMarkersCount = zppm + 1
	} else if cp.PpmMarkersCount <= zppm {
		grown := make([]cparams.Ppx, zppm+1)
		copy(grown, cp.PpmMarkers)
		cp.PpmMarkers = grown
		cp.PpmMarkersCount = zppm + 1
	}
	if cp.PpmMarkers[zppm].Data != nil {
		d.mgr.Errorf("Zppm %u already read\n", zppm)
		return ErrMarkerHandler
	}
	cp.PpmMarkers[zppm].Data = append([]byte(nil), payload...)
	return nil
}

// mergePPM ports opj_j2k_merge_ppm.
func (d *Decoder) mergePPM() error {
	cp := &d.CP
	if cp.Ppm == 0 {
		return nil
	}
	// First pass: compute total size, with Nppm length checks (CVE surface).
	var total uint64
	var remaining uint64
	for i := uint32(0); i < cp.PpmMarkersCount; i++ {
		data := cp.PpmMarkers[i].Data
		if data == nil {
			continue
		}
		dataSize := uint64(len(data))
		off := uint64(0)
		if remaining >= dataSize {
			remaining -= dataSize
			dataSize = 0
		} else {
			off = remaining
			dataSize -= remaining
			remaining = 0
		}
		for dataSize > 0 {
			if dataSize < 4 {
				d.mgr.Errorf("Not enough bytes to read Nppm\n")
				return ErrMarkerHandler
			}
			nppm := uint64(cio.ReadBytes(data[off:], 4))
			off += 4
			dataSize -= 4
			if total > 0xffffffff-nppm {
				d.mgr.Errorf("Too large value for Nppm\n")
				return ErrMarkerHandler
			}
			total += nppm
			if dataSize >= nppm {
				dataSize -= nppm
				off += nppm
			} else {
				remaining = nppm - dataSize
				dataSize = 0
			}
		}
	}
	if remaining != 0 {
		d.mgr.Errorf("Corrupted PPM markers\n")
		return ErrMarkerHandler
	}

	buf := make([]byte, total)
	var w uint64
	remaining = 0
	for i := uint32(0); i < cp.PpmMarkersCount; i++ {
		data := cp.PpmMarkers[i].Data
		if data == nil {
			continue
		}
		off := uint64(0)
		dataSize := uint64(len(data))
		if remaining >= dataSize {
			copy(buf[w:], data[off:off+dataSize])
			w += dataSize
			remaining -= dataSize
			dataSize = 0
		} else {
			copy(buf[w:], data[off:off+remaining])
			w += remaining
			off += remaining
			dataSize -= remaining
			remaining = 0
		}
		for dataSize > 0 {
			if dataSize < 4 {
				d.mgr.Errorf("Not enough bytes to read Nppm\n")
				return ErrMarkerHandler
			}
			nppm := uint64(cio.ReadBytes(data[off:], 4))
			off += 4
			dataSize -= 4
			if dataSize >= nppm {
				copy(buf[w:], data[off:off+nppm])
				w += nppm
				dataSize -= nppm
				off += nppm
			} else {
				copy(buf[w:], data[off:off+dataSize])
				w += dataSize
				remaining = nppm - dataSize
				dataSize = 0
			}
		}
		cp.PpmMarkers[i].Data = nil
	}

	cp.PpmBuffer = buf
	cp.PpmData = buf
	cp.PpmLen = uint32(total)
	cp.PpmMarkersCount = 0
	cp.PpmMarkers = nil
	return nil
}

// readPPT ports opj_j2k_read_ppt.
func (d *Decoder) readPPT(data []byte) error {
	cp := &d.CP
	if len(data) < 2 {
		d.mgr.Errorf("Error reading PPT marker\n")
		return ErrMarkerHandler
	}
	if cp.Ppm != 0 {
		d.mgr.Errorf("Error reading PPT marker: packet header have been previously found in the main header (PPM marker).\n")
		return ErrMarkerHandler
	}
	tcp := &cp.Tcps[d.currentTileNumber]
	tcp.Ppt = 1
	zppt := uint32(data[0])
	payload := data[1:]
	if tcp.PptMarkers == nil {
		tcp.PptMarkers = make([]cparams.Ppx, zppt+1)
		tcp.PptMarkersCount = zppt + 1
	} else if tcp.PptMarkersCount <= zppt {
		grown := make([]cparams.Ppx, zppt+1)
		copy(grown, tcp.PptMarkers)
		tcp.PptMarkers = grown
		tcp.PptMarkersCount = zppt + 1
	}
	if tcp.PptMarkers[zppt].Data != nil {
		d.mgr.Errorf("Zppt %u already read\n", zppt)
		return ErrMarkerHandler
	}
	tcp.PptMarkers[zppt].Data = append([]byte(nil), payload...)
	return nil
}

// mergePPT ports opj_j2k_merge_ppt.
func (d *Decoder) mergePPT(tcp *cparams.TCP) error {
	if tcp.PptBuffer != nil {
		d.mgr.Errorf("opj_j2k_merge_ppt() has already been called\n")
		return ErrMarkerHandler
	}
	if tcp.Ppt == 0 {
		return nil
	}
	var total int
	for i := uint32(0); i < tcp.PptMarkersCount; i++ {
		total += len(tcp.PptMarkers[i].Data)
	}
	buf := make([]byte, 0, total)
	for i := uint32(0); i < tcp.PptMarkersCount; i++ {
		buf = append(buf, tcp.PptMarkers[i].Data...)
		tcp.PptMarkers[i].Data = nil
	}
	tcp.PptMarkersCount = 0
	tcp.PptMarkers = nil
	tcp.PptBuffer = buf
	tcp.PptData = buf
	tcp.PptLen = uint32(len(buf))
	return nil
}

// readCBD ports opj_j2k_read_cbd.
func (d *Decoder) readCBD(data []byte) error {
	img := d.privateImage
	if len(data) != int(img.Numcomps+2) {
		d.mgr.Errorf("Error reading CBD marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	nbComp := r.u(2) // Ncbd
	if nbComp != img.Numcomps {
		d.mgr.Errorf("Error reading CBD marker\n")
		return ErrMarkerHandler
	}
	for i := uint32(0); i < img.Numcomps; i++ {
		def := r.u(1)
		img.Comps[i].Sgnd = (def >> 7) & 1
		img.Comps[i].Prec = (def & 0x7f) + 1
		if img.Comps[i].Prec > 31 {
			d.mgr.Errorf("Invalid values for comp = %d : prec=%u\n", i, img.Comps[i].Prec)
			return ErrMarkerHandler
		}
	}
	return nil
}

// helpers mirroring opj_uint_* used by the marker parsers.
func uintAdds(a, b uint32) uint32 {
	s := uint64(a) + uint64(b)
	if s > 0xffffffff {
		return 0xffffffff
	}
	return uint32(s)
}
func uintCeildiv(a, b uint32) uint32 { return uint32((uint64(a) + uint64(b) - 1) / uint64(b)) }
func uintMin(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
