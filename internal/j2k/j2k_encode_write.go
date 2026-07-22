package j2k

// Compress flow (start/encode/end) and marker writers, port of the encode-side
// procedure list in j2k.c.

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	"github.com/mgilbir/gopenjpeg/internal/pi"
	"github.com/mgilbir/gopenjpeg/internal/tcd"
)

// StartCompress ports opj_j2k_start_compress: bind the image, validate, and
// write the codestream main header.
func (e *Encoder) StartCompress(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	e.privateImage = image.Create0()
	image.CopyHeader(img, e.privateImage)
	// Move the component data pointers into the private image (as C does).
	for i := uint32(0); i < img.Numcomps; i++ {
		if img.Comps[i].Data != nil {
			e.privateImage.Comps[i].Data = img.Comps[i].Data
			img.Comps[i].Data = nil
		}
	}

	if err := e.encodingValidation(mgr); err != nil {
		return err
	}
	return e.writeHeader(stream, mgr)
}

// encodingValidation ports opj_j2k_encoding_validation.
func (e *Encoder) encodingValidation(mgr *event.Manager) error {
	numres := e.CP.Tcps[0].TCCPs[0].Numresolutions
	if numres == 0 || numres > 32 {
		mgr.Errorf("Number of resolutions is too high in comparison to the size of tiles\n")
		return ErrEncodeSetup
	}
	if e.CP.Tdx < uint32(1)<<(numres-1) {
		mgr.Errorf("Number of resolutions is too high in comparison to the size of tiles\n")
		return ErrEncodeSetup
	}
	if e.CP.Tdy < uint32(1)<<(numres-1) {
		mgr.Errorf("Number of resolutions is too high in comparison to the size of tiles\n")
		return ErrEncodeSetup
	}
	return nil
}

// writeHeader runs the header-writing procedure list.
func (e *Encoder) writeHeader(stream *cio.Stream, mgr *event.Manager) error {
	// init_info -> calculate_tp
	if err := e.calculateTP(mgr); err != nil {
		return err
	}
	if err := e.writeSOC(stream, mgr); err != nil {
		return err
	}
	if err := e.writeSIZ(stream, mgr); err != nil {
		return err
	}
	if err := e.writeCOD(stream, mgr); err != nil {
		return err
	}
	if err := e.writeQCD(stream, mgr); err != nil {
		return err
	}
	if err := e.writeAllCOC(stream, mgr); err != nil {
		return err
	}
	if err := e.writeAllQCC(stream, mgr); err != nil {
		return err
	}
	if e.enc.tlm {
		if err := e.writeTLM(stream, mgr); err != nil {
			return err
		}
		if e.CP.Rsiz == cparams.ProfileCinema4K {
			if err := e.writePOC(stream, mgr); err != nil {
				return err
			}
		}
	}
	if err := e.writeRegions(stream, mgr); err != nil {
		return err
	}
	if e.CP.Comment != "" {
		if err := e.writeCOM(stream, mgr); err != nil {
			return err
		}
	}
	// DEVELOPER CORNER: Part-2 custom MCT marker group.
	if e.CP.Rsiz&(cparams.ProfilePart2|cparams.ExtensionMCT) ==
		(cparams.ProfilePart2 | cparams.ExtensionMCT) {
		if err := e.writeMctDataGroup(stream, mgr); err != nil {
			return err
		}
	}
	// create_tcd
	e.tcd = tcd.Create(false)
	e.tcd.SetNumThreads(e.numThreads)
	if !e.tcd.Init(e.privateImage, &e.CP) {
		mgr.Errorf("Cannot instantiate the tile coder\n")
		return ErrEncodeWrite
	}
	// update_rates
	if err := e.updateRates(stream, mgr); err != nil {
		return err
	}
	return nil
}

func writeToStream(stream *cio.Stream, data []byte, mgr *event.Manager) error {
	n, err := stream.Write(data, mgr)
	if err != nil || n != len(data) {
		if err != nil {
			return err
		}
		return ErrEncodeWrite
	}
	return nil
}

// writeSOC ports opj_j2k_write_soc.
func (e *Encoder) writeSOC(stream *cio.Stream, mgr *event.Manager) error {
	buf := make([]byte, 2)
	cio.WriteBytes(buf, msSOC, 2)
	return writeToStream(stream, buf, mgr)
}

// writeSIZ ports opj_j2k_write_siz.
func (e *Encoder) writeSIZ(stream *cio.Stream, mgr *event.Manager) error {
	img := e.privateImage
	cp := &e.CP
	sizeLen := 40 + 3*img.Numcomps
	buf := make([]byte, sizeLen)
	p := buf
	cio.WriteBytes(p, msSIZ, 2)
	p = p[2:]
	cio.WriteBytes(p, sizeLen-2, 2)
	p = p[2:]
	cio.WriteBytes(p, uint32(cp.Rsiz), 2)
	p = p[2:]
	cio.WriteBytes(p, img.X1, 4)
	p = p[4:]
	cio.WriteBytes(p, img.Y1, 4)
	p = p[4:]
	cio.WriteBytes(p, img.X0, 4)
	p = p[4:]
	cio.WriteBytes(p, img.Y0, 4)
	p = p[4:]
	cio.WriteBytes(p, cp.Tdx, 4)
	p = p[4:]
	cio.WriteBytes(p, cp.Tdy, 4)
	p = p[4:]
	cio.WriteBytes(p, cp.Tx0, 4)
	p = p[4:]
	cio.WriteBytes(p, cp.Ty0, 4)
	p = p[4:]
	cio.WriteBytes(p, img.Numcomps, 2)
	p = p[2:]
	for i := uint32(0); i < img.Numcomps; i++ {
		c := &img.Comps[i]
		cio.WriteBytes(p, c.Prec-1+(c.Sgnd<<7), 1)
		p = p[1:]
		cio.WriteBytes(p, c.Dx, 1)
		p = p[1:]
		cio.WriteBytes(p, c.Dy, 1)
		p = p[1:]
	}
	return writeToStream(stream, buf, mgr)
}

// writeCOD ports opj_j2k_write_cod.
func (e *Encoder) writeCOD(stream *cio.Stream, mgr *event.Manager) error {
	tcp := &e.CP.Tcps[e.currentTileNumber]
	codeSize := 9 + e.getSPCodSPCocSize(e.currentTileNumber, 0)
	buf := make([]byte, codeSize)
	p := buf
	cio.WriteBytes(p, msCOD, 2)
	p = p[2:]
	cio.WriteBytes(p, codeSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, tcp.Csty, 1)
	p = p[1:]
	cio.WriteBytes(p, uint32(tcp.Prg), 1)
	p = p[1:]
	cio.WriteBytes(p, tcp.Numlayers, 2)
	p = p[2:]
	cio.WriteBytes(p, tcp.MCT, 1)
	p = p[1:]
	remaining := codeSize - 9
	if !e.writeSPCodSPCoc(e.currentTileNumber, 0, p, &remaining, mgr) || remaining != 0 {
		mgr.Errorf("Error writing COD marker\n")
		return ErrEncodeWrite
	}
	return writeToStream(stream, buf, mgr)
}

// getSPCodSPCocSize ports opj_j2k_get_SPCod_SPCoc_size.
func (e *Encoder) getSPCodSPCocSize(tileno, compno uint32) uint32 {
	tccp := &e.CP.Tcps[tileno].TCCPs[compno]
	if tccp.Csty&cparams.CCPCstyPRT != 0 {
		return 5 + tccp.Numresolutions
	}
	return 5
}

// writeSPCodSPCoc ports opj_j2k_write_SPCod_SPCoc.
func (e *Encoder) writeSPCodSPCoc(tileno, compno uint32, data []byte, headerSize *uint32, mgr *event.Manager) bool {
	tccp := &e.CP.Tcps[tileno].TCCPs[compno]
	if *headerSize < 5 {
		mgr.Errorf("Error writing SPCod SPCoc element\n")
		return false
	}
	p := data
	cio.WriteBytes(p, tccp.Numresolutions-1, 1)
	p = p[1:]
	cio.WriteBytes(p, tccp.Cblkw-2, 1)
	p = p[1:]
	cio.WriteBytes(p, tccp.Cblkh-2, 1)
	p = p[1:]
	cio.WriteBytes(p, tccp.Cblksty, 1)
	p = p[1:]
	cio.WriteBytes(p, tccp.Qmfbid, 1)
	p = p[1:]
	*headerSize -= 5
	if tccp.Csty&cparams.CCPCstyPRT != 0 {
		if *headerSize < tccp.Numresolutions {
			mgr.Errorf("Error writing SPCod SPCoc element\n")
			return false
		}
		for i := uint32(0); i < tccp.Numresolutions; i++ {
			cio.WriteBytes(p, tccp.Prcw[i]+(tccp.Prch[i]<<4), 1)
			p = p[1:]
		}
		*headerSize -= tccp.Numresolutions
	}
	return true
}

// writeQCD ports opj_j2k_write_qcd.
func (e *Encoder) writeQCD(stream *cio.Stream, mgr *event.Manager) error {
	qcdSize := 4 + e.getSQcdSQccSize(e.currentTileNumber, 0)
	buf := make([]byte, qcdSize)
	p := buf
	cio.WriteBytes(p, msQCD, 2)
	p = p[2:]
	cio.WriteBytes(p, qcdSize-2, 2)
	p = p[2:]
	remaining := qcdSize - 4
	if !e.writeSQcdSQcc(e.currentTileNumber, 0, p, &remaining, mgr) || remaining != 0 {
		mgr.Errorf("Error writing QCD marker\n")
		return ErrEncodeWrite
	}
	return writeToStream(stream, buf, mgr)
}

// getSQcdSQccSize ports opj_j2k_get_SQcd_SQcc_size.
func (e *Encoder) getSQcdSQccSize(tileno, compno uint32) uint32 {
	tccp := &e.CP.Tcps[tileno].TCCPs[compno]
	var numBands uint32
	if tccp.Qntsty == cparams.CCPQntStySiQnt {
		numBands = 1
	} else {
		numBands = tccp.Numresolutions*3 - 2
	}
	if tccp.Qntsty == cparams.CCPQntStyNoQnt {
		return 1 + numBands
	}
	return 1 + 2*numBands
}

// writeSQcdSQcc ports opj_j2k_write_SQcd_SQcc.
func (e *Encoder) writeSQcdSQcc(tileno, compno uint32, data []byte, headerSize *uint32, mgr *event.Manager) bool {
	tccp := &e.CP.Tcps[tileno].TCCPs[compno]
	var numBands uint32
	if tccp.Qntsty == cparams.CCPQntStySiQnt {
		numBands = 1
	} else {
		numBands = tccp.Numresolutions*3 - 2
	}
	p := data
	if tccp.Qntsty == cparams.CCPQntStyNoQnt {
		hs := 1 + numBands
		if *headerSize < hs {
			mgr.Errorf("Error writing SQcd SQcc element\n")
			return false
		}
		cio.WriteBytes(p, tccp.Qntsty+(tccp.Numgbits<<5), 1)
		p = p[1:]
		for b := uint32(0); b < numBands; b++ {
			expn := uint32(tccp.Stepsizes[b].Expn)
			cio.WriteBytes(p, expn<<3, 1)
			p = p[1:]
		}
		*headerSize -= hs
	} else {
		hs := 1 + 2*numBands
		if *headerSize < hs {
			mgr.Errorf("Error writing SQcd SQcc element\n")
			return false
		}
		cio.WriteBytes(p, tccp.Qntsty+(tccp.Numgbits<<5), 1)
		p = p[1:]
		for b := uint32(0); b < numBands; b++ {
			expn := uint32(tccp.Stepsizes[b].Expn)
			mant := uint32(tccp.Stepsizes[b].Mant)
			cio.WriteBytes(p, (expn<<11)+mant, 2)
			p = p[2:]
		}
		*headerSize -= hs
	}
	return true
}

// writeAllCOC ports opj_j2k_write_all_coc.
func (e *Encoder) writeAllCOC(stream *cio.Stream, mgr *event.Manager) error {
	for compno := uint32(1); compno < e.privateImage.Numcomps; compno++ {
		if !e.compareCOC(0, compno) {
			if err := e.writeCOC(compno, stream, mgr); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeAllQCC ports opj_j2k_write_all_qcc.
func (e *Encoder) writeAllQCC(stream *cio.Stream, mgr *event.Manager) error {
	for compno := uint32(1); compno < e.privateImage.Numcomps; compno++ {
		if !e.compareQCC(0, compno) {
			if err := e.writeQCC(compno, stream, mgr); err != nil {
				return err
			}
		}
	}
	return nil
}

// compareCOC ports opj_j2k_compare_coc.
func (e *Encoder) compareCOC(a, b uint32) bool {
	tcp := &e.CP.Tcps[e.currentTileNumber]
	if tcp.TCCPs[a].Csty != tcp.TCCPs[b].Csty {
		return false
	}
	return e.compareSPCodSPCoc(e.currentTileNumber, a, b)
}

func (e *Encoder) compareSPCodSPCoc(tileno, a, b uint32) bool {
	t0 := &e.CP.Tcps[tileno].TCCPs[a]
	t1 := &e.CP.Tcps[tileno].TCCPs[b]
	if t0.Numresolutions != t1.Numresolutions || t0.Cblkw != t1.Cblkw ||
		t0.Cblkh != t1.Cblkh || t0.Cblksty != t1.Cblksty || t0.Qmfbid != t1.Qmfbid {
		return false
	}
	if (t0.Csty & cparams.CCPCstyPRT) != (t1.Csty & cparams.CCPCstyPRT) {
		return false
	}
	for i := uint32(0); i < t0.Numresolutions; i++ {
		if t0.Prcw[i] != t1.Prcw[i] || t0.Prch[i] != t1.Prch[i] {
			return false
		}
	}
	return true
}

// compareQCC ports opj_j2k_compare_qcc.
func (e *Encoder) compareQCC(a, b uint32) bool {
	tileno := e.currentTileNumber
	t0 := &e.CP.Tcps[tileno].TCCPs[a]
	t1 := &e.CP.Tcps[tileno].TCCPs[b]
	if t0.Qntsty != t1.Qntsty || t0.Numgbits != t1.Numgbits {
		return false
	}
	var numBands uint32
	if t0.Qntsty == cparams.CCPQntStySiQnt {
		numBands = 1
	} else {
		numBands = t0.Numresolutions*3 - 2
		if numBands != t1.Numresolutions*3-2 {
			return false
		}
	}
	for b := uint32(0); b < numBands; b++ {
		if t0.Stepsizes[b].Expn != t1.Stepsizes[b].Expn {
			return false
		}
	}
	if t0.Qntsty != cparams.CCPQntStyNoQnt {
		for b := uint32(0); b < numBands; b++ {
			if t0.Stepsizes[b].Mant != t1.Stepsizes[b].Mant {
				return false
			}
		}
	}
	return true
}

// writeCOC ports opj_j2k_write_coc.
func (e *Encoder) writeCOC(compno uint32, stream *cio.Stream, mgr *event.Manager) error {
	compRoom := uint32(1)
	if e.privateImage.Numcomps > 256 {
		compRoom = 2
	}
	cocSize := 5 + compRoom + e.getSPCodSPCocSize(e.currentTileNumber, compno)
	buf := make([]byte, cocSize)
	written := uint32(0)
	e.writeCOCInMemory(compno, buf, &written, mgr)
	return writeToStream(stream, buf, mgr)
}

// writeCOCInMemory ports opj_j2k_write_coc_in_memory.
func (e *Encoder) writeCOCInMemory(compno uint32, data []byte, written *uint32, mgr *event.Manager) {
	tcp := &e.CP.Tcps[e.currentTileNumber]
	compRoom := uint32(1)
	if e.privateImage.Numcomps > 256 {
		compRoom = 2
	}
	cocSize := 5 + compRoom + e.getSPCodSPCocSize(e.currentTileNumber, compno)
	p := data
	cio.WriteBytes(p, msCOC, 2)
	p = p[2:]
	cio.WriteBytes(p, cocSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, compno, compRoom)
	p = p[compRoom:]
	cio.WriteBytes(p, tcp.TCCPs[compno].Csty, 1)
	p = p[1:]
	remaining := cocSize - (5 + compRoom)
	// NOTE: faithful to C, which passes comp_no 0 here (reproduces its quirk).
	e.writeSPCodSPCoc(e.currentTileNumber, 0, p, &remaining, mgr)
	*written = cocSize
}

// writeQCC ports opj_j2k_write_qcc.
func (e *Encoder) writeQCC(compno uint32, stream *cio.Stream, mgr *event.Manager) error {
	qccSize := 5 + e.getSQcdSQccSize(e.currentTileNumber, compno)
	if e.privateImage.Numcomps > 256 {
		qccSize++
	}
	buf := make([]byte, qccSize)
	written := uint32(0)
	e.writeQCCInMemory(compno, buf, &written, mgr)
	return writeToStream(stream, buf, mgr)
}

// writeQCCInMemory ports opj_j2k_write_qcc_in_memory.
func (e *Encoder) writeQCCInMemory(compno uint32, data []byte, written *uint32, mgr *event.Manager) {
	qccSize := 6 + e.getSQcdSQccSize(e.currentTileNumber, compno)
	p := data
	cio.WriteBytes(p, msQCC, 2)
	p = p[2:]
	if e.privateImage.Numcomps <= 256 {
		qccSize--
		cio.WriteBytes(p, qccSize-2, 2)
		p = p[2:]
		cio.WriteBytes(p, compno, 1)
		p = p[1:]
	} else {
		cio.WriteBytes(p, qccSize-2, 2)
		p = p[2:]
		cio.WriteBytes(p, compno, 2)
		p = p[2:]
	}
	remaining := qccSize
	e.writeSQcdSQcc(e.currentTileNumber, compno, p, &remaining, mgr)
	*written = qccSize
}

// writeRegions ports opj_j2k_write_regions.
func (e *Encoder) writeRegions(stream *cio.Stream, mgr *event.Manager) error {
	tccps := e.CP.Tcps[0].TCCPs
	for compno := uint32(0); compno < e.privateImage.Numcomps; compno++ {
		if tccps[compno].Roishift != 0 {
			if err := e.writeRGN(0, compno, e.privateImage.Numcomps, stream, mgr); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeRGN ports opj_j2k_write_rgn.
func (e *Encoder) writeRGN(tileno, compno, nbComps uint32, stream *cio.Stream, mgr *event.Manager) error {
	tccp := &e.CP.Tcps[tileno].TCCPs[compno]
	compRoom := uint32(1)
	if nbComps > 256 {
		compRoom = 2
	}
	rgnSize := 6 + compRoom
	buf := make([]byte, rgnSize)
	p := buf
	cio.WriteBytes(p, msRGN, 2)
	p = p[2:]
	cio.WriteBytes(p, rgnSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, compno, compRoom)
	p = p[compRoom:]
	cio.WriteBytes(p, 0, 1)
	p = p[1:]
	cio.WriteBytes(p, uint32(tccp.Roishift), 1)
	return writeToStream(stream, buf, mgr)
}

// writeCOM ports opj_j2k_write_com.
func (e *Encoder) writeCOM(stream *cio.Stream, mgr *event.Manager) error {
	comment := []byte(e.CP.Comment)
	total := uint32(len(comment)) + 6
	buf := make([]byte, total)
	p := buf
	cio.WriteBytes(p, msCOM, 2)
	p = p[2:]
	cio.WriteBytes(p, total-2, 2)
	p = p[2:]
	cio.WriteBytes(p, 1, 2)
	p = p[2:]
	copy(p, comment)
	return writeToStream(stream, buf, mgr)
}

// writePOC ports opj_j2k_write_poc.
func (e *Encoder) writePOC(stream *cio.Stream, mgr *event.Manager) error {
	tcp := &e.CP.Tcps[e.currentTileNumber]
	nbComp := e.privateImage.Numcomps
	nbPoc := 1 + tcp.Numpocs
	pocRoom := uint32(1)
	if nbComp > 256 {
		pocRoom = 2
	}
	pocSize := 4 + (5+2*pocRoom)*nbPoc
	buf := make([]byte, pocSize)
	written := uint32(0)
	e.writePOCInMemory(buf, &written, mgr)
	return writeToStream(stream, buf, mgr)
}

// writePOCInMemory ports opj_j2k_write_poc_in_memory.
func (e *Encoder) writePOCInMemory(data []byte, written *uint32, mgr *event.Manager) {
	tcp := &e.CP.Tcps[e.currentTileNumber]
	tccp := &tcp.TCCPs[0]
	img := e.privateImage
	nbComp := img.Numcomps
	nbPoc := 1 + tcp.Numpocs
	pocRoom := uint32(1)
	if nbComp > 256 {
		pocRoom = 2
	}
	pocSize := 4 + (5+2*pocRoom)*nbPoc
	p := data
	cio.WriteBytes(p, msPOC, 2)
	p = p[2:]
	cio.WriteBytes(p, pocSize-2, 2)
	p = p[2:]
	for i := uint32(0); i < nbPoc; i++ {
		poc := &tcp.Pocs[i]
		cio.WriteBytes(p, poc.Resno0, 1)
		p = p[1:]
		cio.WriteBytes(p, poc.Compno0, pocRoom)
		p = p[pocRoom:]
		cio.WriteBytes(p, poc.Layno1, 2)
		p = p[2:]
		cio.WriteBytes(p, poc.Resno1, 1)
		p = p[1:]
		cio.WriteBytes(p, poc.Compno1, pocRoom)
		p = p[pocRoom:]
		cio.WriteBytes(p, uint32(poc.Prg), 1)
		p = p[1:]
		poc.Layno1 = uint32(opjmath.IntMin(int32(poc.Layno1), int32(tcp.Numlayers)))
		poc.Resno1 = uint32(opjmath.IntMin(int32(poc.Resno1), int32(tccp.Numresolutions)))
		poc.Compno1 = uint32(opjmath.IntMin(int32(poc.Compno1), int32(nbComp)))
	}
	*written = pocSize
}

// writeTLM ports opj_j2k_write_tlm.
func (e *Encoder) writeTLM(stream *cio.Stream, mgr *event.Manager) error {
	if e.enc.totalTileParts > 10921 {
		mgr.Errorf("A maximum of 10921 tile-parts are supported currently when writing TLM marker\n")
		return ErrEncodeWrite
	}
	var sizePerTilePart uint32
	if e.enc.totalTileParts <= 255 {
		sizePerTilePart = 5
		e.enc.ttlmiIsByte = true
	} else {
		sizePerTilePart = 6
		e.enc.ttlmiIsByte = false
	}
	tlmSize := 2 + 4 + sizePerTilePart*e.enc.totalTileParts
	buf := make([]byte, tlmSize)
	e.enc.tlmStart = stream.Tell()
	p := buf
	cio.WriteBytes(p, msTLM, 2)
	p = p[2:]
	cio.WriteBytes(p, tlmSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, 0, 1) // Ztlm
	p = p[1:]
	if sizePerTilePart == 5 {
		cio.WriteBytes(p, 0x50, 1)
	} else {
		cio.WriteBytes(p, 0x60, 1)
	}
	return writeToStream(stream, buf, mgr)
}

// --- tile-part iteration counts ---

// getNumTP ports opj_j2k_get_num_tp.
func (e *Encoder) getNumTP(pino, tileno uint32) uint32 {
	cp := &e.CP
	tcp := &cp.Tcps[tileno]
	poc := &tcp.Pocs[pino]
	prog := cparams.ConvertProgressionOrder(tcp.Prg)
	tpnum := uint32(1)
	if cp.MEnc.MTpOn == 1 {
		for i := 0; i < 4 && i < len(prog); i++ {
			switch prog[i] {
			case 'C':
				tpnum *= poc.CompE
			case 'R':
				tpnum *= poc.ResE
			case 'P':
				tpnum *= poc.PrcE
			case 'L':
				tpnum *= poc.LayE
			}
			if cp.MEnc.MTpFlag == prog[i] {
				cp.MEnc.MTpPos = int32(i)
				break
			}
		}
	} else {
		tpnum = 1
	}
	return tpnum
}

// calculateTP ports opj_j2k_calculate_tp (and opj_j2k_init_info).
func (e *Encoder) calculateTP(mgr *event.Manager) error {
	cp := &e.CP
	nbTiles := cp.Tw * cp.Th
	total := uint32(0)
	for tileno := uint32(0); tileno < nbTiles; tileno++ {
		pi.UpdateEncodingParameters(e.privateImage, cp, tileno)
		curTotnumTp := uint32(0)
		tcp := &cp.Tcps[tileno]
		for pino := uint32(0); pino <= tcp.Numpocs; pino++ {
			tpNum := e.getNumTP(pino, tileno)
			total += tpNum
			curTotnumTp += tpNum
		}
		tcp.MNbTileParts = curTotnumTp
	}
	e.enc.totalTileParts = total
	return nil
}

// --- header size estimation (update_rates) ---

func (e *Encoder) getMaxTOCSize() uint32 {
	var max uint32
	nbTiles := e.CP.Tw * e.CP.Th
	for i := uint32(0); i < nbTiles; i++ {
		max = opjmath.UintMax(max, e.CP.Tcps[i].MNbTileParts)
	}
	return 12 * max
}

func (e *Encoder) getMaxCOCSize() uint32 {
	var max uint32
	nbTiles := e.CP.Tw * e.CP.Th
	nbComp := e.privateImage.Numcomps
	for i := uint32(0); i < nbTiles; i++ {
		for j := uint32(0); j < nbComp; j++ {
			max = opjmath.UintMax(max, e.getSPCodSPCocSize(i, j))
		}
	}
	return 6 + max
}

func (e *Encoder) getMaxQCCSize() uint32 { return e.getMaxCOCSize() }

func (e *Encoder) getMaxPOCSize() uint32 {
	var maxPoc uint32
	nbTiles := e.CP.Th * e.CP.Tw
	for i := uint32(0); i < nbTiles; i++ {
		maxPoc = opjmath.UintMax(maxPoc, e.CP.Tcps[i].Numpocs)
	}
	maxPoc++
	return 4 + 9*maxPoc
}

func (e *Encoder) getSpecificHeaderSizes() uint32 {
	nbBytes := uint32(0)
	nbComps := e.privateImage.Numcomps - 1
	nbBytes += e.getMaxTOCSize()
	if !cparams.IsCinema(e.CP.Rsiz) {
		nbBytes += nbComps * e.getMaxCOCSize()
		nbBytes += nbComps * e.getMaxQCCSize()
	}
	nbBytes += e.getMaxPOCSize()
	if e.enc.plt {
		var maxPacketCount uint32
		nbTiles := e.CP.Th * e.CP.Tw
		for i := uint32(0); i < nbTiles; i++ {
			maxPacketCount = opjmath.UintMax(maxPacketCount,
				pi.GetEncodingPacketCount(e.privateImage, &e.CP, i))
		}
		e.enc.reservedBytesPLT = 6 * opjmath.UintCeildiv(maxPacketCount, 16382)
		nbBytes += 5 * maxPacketCount
		e.enc.reservedBytesPLT += 5 * maxPacketCount
		e.enc.reservedBytesPLT++
		nbBytes += e.enc.reservedBytesPLT
	}
	return nbBytes
}

// updateRates ports opj_j2k_update_rates.
func (e *Encoder) updateRates(stream *cio.Stream, mgr *event.Manager) error {
	cp := &e.CP
	img := e.privateImage

	bitsEmpty := 8 * img.Comps[0].Dx * img.Comps[0].Dy
	sizePixel := img.Numcomps * img.Comps[0].Prec
	sotRemove := float32(stream.Tell()) / float32(cp.Th*cp.Tw)

	tpStride := func(tcp *cparams.TCP) float32 {
		if cp.MEnc.MTpOn != 0 {
			return float32((tcp.MNbTileParts - 1) * 14)
		}
		return 0
	}

	tIdx := 0
	for i := uint32(0); i < cp.Th; i++ {
		for j := uint32(0); j < cp.Tw; j++ {
			tcp := &cp.Tcps[tIdx]
			offset := tpStride(tcp) / float32(tcp.Numlayers)
			x0 := opjmath.IntMax(int32(cp.Tx0+j*cp.Tdx), int32(img.X0))
			y0 := opjmath.IntMax(int32(cp.Ty0+i*cp.Tdy), int32(img.Y0))
			x1 := opjmath.IntMin(int32(cp.Tx0+(j+1)*cp.Tdx), int32(img.X1))
			y1 := opjmath.IntMin(int32(cp.Ty0+(i+1)*cp.Tdy), int32(img.Y1))
			for k := uint32(0); k < tcp.Numlayers; k++ {
				if tcp.Rates[k] > 0.0 {
					tcp.Rates[k] = float32((float64(sizePixel)*float64(uint32(x1-x0))*
						float64(uint32(y1-y0)))/
						(float64(tcp.Rates[k])*float64(bitsEmpty))) - offset
				}
			}
			tIdx++
		}
	}

	tIdx = 0
	for i := uint32(0); i < cp.Th; i++ {
		for j := uint32(0); j < cp.Tw; j++ {
			tcp := &cp.Tcps[tIdx]
			if tcp.Rates[0] > 0.0 {
				tcp.Rates[0] -= sotRemove
				if tcp.Rates[0] < 30.0 {
					tcp.Rates[0] = 30.0
				}
			}
			lastRes := tcp.Numlayers - 1
			for k := uint32(1); k < lastRes; k++ {
				if tcp.Rates[k] > 0.0 {
					tcp.Rates[k] -= sotRemove
					if tcp.Rates[k] < tcp.Rates[k-1]+10.0 {
						tcp.Rates[k] = tcp.Rates[k-1] + 20.0
					}
				}
			}
			// Faithful to the C pointer arithmetic: the final block operates on
			// rates[lastRes], but for numlayers==1 the pointer has advanced to
			// rates[1] (which is 0 and a no-op).
			finalIdx := lastRes
			if lastRes == 0 {
				finalIdx = 1
			}
			if tcp.Rates[finalIdx] > 0.0 {
				tcp.Rates[finalIdx] -= sotRemove + 2.0
				if tcp.Rates[finalIdx] < tcp.Rates[finalIdx-1]+10.0 {
					tcp.Rates[finalIdx] = tcp.Rates[finalIdx-1] + 20.0
				}
			}
			tIdx++
		}
	}

	var tileSize uint64
	for i := uint32(0); i < img.Numcomps; i++ {
		c := &img.Comps[i]
		tileSize += uint64(opjmath.UintCeildiv(cp.Tdx, c.Dx)) *
			uint64(opjmath.UintCeildiv(cp.Tdy, c.Dy)) * uint64(c.Prec)
	}
	tileSize = uint64(float64(tileSize) * 1.4 / 8)
	tileSize += 500
	tileSize += uint64(e.getSpecificHeaderSizes())
	if tileSize > 0xFFFFFFFF {
		tileSize = 0xFFFFFFFF
	}
	e.enc.encodedTileSize = uint32(tileSize)
	e.enc.encodedTileData = make([]byte, e.enc.encodedTileSize)

	if e.enc.tlm {
		e.enc.tlmBuffer = make([]byte, 6*e.enc.totalTileParts)
		e.enc.tlmCurrent = 0
	}
	return nil
}
