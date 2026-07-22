package j2k

// Part-2 custom (array-based) MCT encode support: ports of the mct_data branch
// of opj_j2k_setup_encoder, opj_j2k_setup_mct_encoding, and the marker writers
// opj_j2k_write_cbd / opj_j2k_write_mct_record / opj_j2k_write_mcc_record /
// opj_j2k_write_mco (assembled by opj_j2k_write_mct_data_group).

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/mct"
)

// setupCustomMCT ports the parameters->mct_data branch of opj_j2k_setup_encoder:
// build the coding/decoding matrices, norms, per-component DC shifts, and the
// MCT/MCC records, for the given tile's TCP.
func (e *Encoder) setupCustomMCT(tcp *cparams.TCP, p *CParameters, img *image.Image, mgr *event.Manager) error {
	nc := img.Numcomps
	if uint32(len(p.MctData)) != nc*nc || uint32(len(p.MctDcShift)) != nc {
		mgr.Errorf("Invalid custom MCT matrix dimensions\n")
		return ErrEncodeSetup
	}

	tcp.MCT = 2
	tcp.MMctCodingMatrix = append([]float32(nil), p.MctData...)

	tmp := append([]float32(nil), p.MctData...)
	tcp.MMctDecodingMatrix = make([]float32, nc*nc)
	if !mct.MatrixInversionF(tmp, tcp.MMctDecodingMatrix, nc) {
		mgr.Errorf("Failed to inverse encoder MCT decoding matrix \n")
		return ErrEncodeSetup
	}

	tcp.MctNorms = make([]float64, nc)
	mct.CalculateNorms(tcp.MctNorms, nc, tcp.MMctDecodingMatrix)

	for i := uint32(0); i < nc; i++ {
		tcp.TCCPs[i].MDcLevelShift = p.MctDcShift[i]
	}

	if !setupMctEncoding(tcp, img) {
		mgr.Errorf("Failed to setup j2k mct encoding\n")
		return ErrEncodeSetup
	}
	return nil
}

// mctElementSizeEnc mirrors MCT_ELEMENT_SIZE for the encode-side writers.
var mctElementSizeEnc = [4]uint32{2, 4, 4, 8}

// setupMctEncoding ports opj_j2k_setup_mct_encoding.
func setupMctEncoding(tcp *cparams.TCP, img *image.Image) bool {
	if tcp.MCT != 2 {
		return true
	}
	indix := uint32(1)
	nc := img.Numcomps

	var decoIndex int = -1
	if tcp.MMctDecodingMatrix != nil {
		if tcp.MNbMctRecords == tcp.MNbMaxMctRecords {
			tcp.MNbMaxMctRecords += mctDefaultNbRecords
			grown := make([]cparams.MctData, tcp.MNbMaxMctRecords)
			copy(grown, tcp.MMctRecords)
			tcp.MMctRecords = grown
		}
		rec := &tcp.MMctRecords[tcp.MNbMctRecords]
		rec.Index = indix
		indix++
		rec.ArrayType = cparams.MctTypeDecorrelation
		rec.ElementType = cparams.MctTypeFloat
		nbElem := nc * nc
		rec.Data = make([]byte, nbElem*mctElementSizeEnc[rec.ElementType])
		mctWriteFromFloat(rec.ElementType, tcp.MMctDecodingMatrix, rec.Data, nbElem)
		decoIndex = int(tcp.MNbMctRecords)
		tcp.MNbMctRecords++
	}

	if tcp.MNbMctRecords == tcp.MNbMaxMctRecords {
		tcp.MNbMaxMctRecords += mctDefaultNbRecords
		grown := make([]cparams.MctData, tcp.MNbMaxMctRecords)
		copy(grown, tcp.MMctRecords)
		tcp.MMctRecords = grown
	}
	offsetIndex := int(tcp.MNbMctRecords)
	offRec := &tcp.MMctRecords[offsetIndex]
	offRec.Index = indix
	indix++
	offRec.ArrayType = cparams.MctTypeOffset
	offRec.ElementType = cparams.MctTypeFloat
	nbElem := nc
	data := make([]float32, nbElem)
	for i := uint32(0); i < nbElem; i++ {
		data[i] = float32(tcp.TCCPs[i].MDcLevelShift)
	}
	offRec.Data = make([]byte, nbElem*mctElementSizeEnc[offRec.ElementType])
	mctWriteFromFloat(offRec.ElementType, data, offRec.Data, nbElem)
	tcp.MNbMctRecords++

	if tcp.MNbMccRecords == tcp.MNbMaxMccRecords {
		tcp.MNbMaxMccRecords += mccDefaultNbRecords
		grown := make([]cparams.MccData, tcp.MNbMaxMccRecords)
		copy(grown, tcp.MccRecords)
		tcp.MccRecords = grown
	}
	mcc := &tcp.MccRecords[tcp.MNbMccRecords]
	mcc.DecorrelationArray = int32(decoIndex)
	mcc.OffsetArray = int32(offsetIndex)
	mcc.IsIrreversible = true
	mcc.NbComps = nc
	mcc.Index = indix
	tcp.MNbMccRecords++
	return true
}

// mctWriteFromFloat ports j2k_mct_write_functions_from_float[elementType].
func mctWriteFromFloat(et cparams.MctElementType, src []float32, dst []byte, nbElem uint32) {
	switch et {
	case cparams.MctTypeInt16:
		for i := uint32(0); i < nbElem; i++ {
			cio.WriteBytes(dst[i*2:], uint32(int32(src[i])), 2)
		}
	case cparams.MctTypeInt32:
		for i := uint32(0); i < nbElem; i++ {
			cio.WriteBytes(dst[i*4:], uint32(int32(src[i])), 4)
		}
	case cparams.MctTypeFloat:
		for i := uint32(0); i < nbElem; i++ {
			cio.WriteFloat(dst[i*4:], src[i])
		}
	case cparams.MctTypeDouble:
		for i := uint32(0); i < nbElem; i++ {
			cio.WriteDouble(dst[i*8:], float64(src[i]))
		}
	}
}

// writeMctDataGroup ports opj_j2k_write_mct_data_group: CBD, then each MCT and
// MCC record, then MCO.
func (e *Encoder) writeMctDataGroup(stream *cio.Stream, mgr *event.Manager) error {
	if err := e.writeCBD(stream, mgr); err != nil {
		return err
	}
	tcp := &e.CP.Tcps[e.currentTileNumber]
	for i := uint32(0); i < tcp.MNbMctRecords; i++ {
		if err := e.writeMctRecord(&tcp.MMctRecords[i], stream, mgr); err != nil {
			return err
		}
	}
	for i := uint32(0); i < tcp.MNbMccRecords; i++ {
		if err := e.writeMccRecord(&tcp.MccRecords[i], stream, mgr); err != nil {
			return err
		}
	}
	return e.writeMCO(stream, mgr)
}

// writeCBD ports opj_j2k_write_cbd.
func (e *Encoder) writeCBD(stream *cio.Stream, mgr *event.Manager) error {
	img := e.privateImage
	cbdSize := 6 + img.Numcomps
	buf := make([]byte, cbdSize)
	p := buf
	cio.WriteBytes(p, msCBD, 2)
	p = p[2:]
	cio.WriteBytes(p, cbdSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, img.Numcomps, 2)
	p = p[2:]
	for i := uint32(0); i < img.Numcomps; i++ {
		c := &img.Comps[i]
		cio.WriteBytes(p, (c.Sgnd<<7)|(c.Prec-1), 1)
		p = p[1:]
	}
	return writeToStream(stream, buf, mgr)
}

// writeMctRecord ports opj_j2k_write_mct_record.
func (e *Encoder) writeMctRecord(rec *cparams.MctData, stream *cio.Stream, mgr *event.Manager) error {
	mctSize := 10 + uint32(len(rec.Data))
	buf := make([]byte, mctSize)
	p := buf
	cio.WriteBytes(p, msMCT, 2)
	p = p[2:]
	cio.WriteBytes(p, mctSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, 0, 2) // Zmct
	p = p[2:]
	tmp := (rec.Index & 0xff) | (uint32(rec.ArrayType) << 8) | (uint32(rec.ElementType) << 10)
	cio.WriteBytes(p, tmp, 2)
	p = p[2:]
	cio.WriteBytes(p, 0, 2) // Ymct
	p = p[2:]
	copy(p, rec.Data)
	return writeToStream(stream, buf, mgr)
}

// writeMccRecord ports opj_j2k_write_mcc_record.
func (e *Encoder) writeMccRecord(rec *cparams.MccData, stream *cio.Stream, mgr *event.Manager) error {
	var nbBytesForComp, mask uint32
	if rec.NbComps > 255 {
		nbBytesForComp = 2
		mask = 0x8000
	} else {
		nbBytesForComp = 1
		mask = 0
	}
	mccSize := rec.NbComps*2*nbBytesForComp + 19
	buf := make([]byte, mccSize)
	p := buf
	cio.WriteBytes(p, msMCC, 2)
	p = p[2:]
	cio.WriteBytes(p, mccSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, 0, 2) // Zmcc
	p = p[2:]
	cio.WriteBytes(p, rec.Index, 1) // Imcc
	p = p[1:]
	cio.WriteBytes(p, 0, 2) // Ymcc
	p = p[2:]
	cio.WriteBytes(p, 1, 2) // Qmcc
	p = p[2:]
	cio.WriteBytes(p, 0x1, 1) // Xmcci
	p = p[1:]
	cio.WriteBytes(p, rec.NbComps|mask, 2) // Nmcci
	p = p[2:]
	for i := uint32(0); i < rec.NbComps; i++ {
		cio.WriteBytes(p, i, nbBytesForComp)
		p = p[nbBytesForComp:]
	}
	cio.WriteBytes(p, rec.NbComps|mask, 2) // Mmcci
	p = p[2:]
	for i := uint32(0); i < rec.NbComps; i++ {
		cio.WriteBytes(p, i, nbBytesForComp)
		p = p[nbBytesForComp:]
	}
	var tmcc uint32
	if !rec.IsIrreversible {
		tmcc = 1 << 16
	}
	tcp := &e.CP.Tcps[e.currentTileNumber]
	if rec.DecorrelationArray >= 0 {
		tmcc |= tcp.MMctRecords[rec.DecorrelationArray].Index
	}
	if rec.OffsetArray >= 0 {
		tmcc |= tcp.MMctRecords[rec.OffsetArray].Index << 8
	}
	cio.WriteBytes(p, tmcc, 3) // Tmcci
	return writeToStream(stream, buf, mgr)
}

// writeMCO ports opj_j2k_write_mco.
func (e *Encoder) writeMCO(stream *cio.Stream, mgr *event.Manager) error {
	tcp := &e.CP.Tcps[e.currentTileNumber]
	mcoSize := 5 + tcp.MNbMccRecords
	buf := make([]byte, mcoSize)
	p := buf
	cio.WriteBytes(p, msMCO, 2)
	p = p[2:]
	cio.WriteBytes(p, mcoSize-2, 2)
	p = p[2:]
	cio.WriteBytes(p, tcp.MNbMccRecords, 1) // Nmco
	p = p[1:]
	for i := uint32(0); i < tcp.MNbMccRecords; i++ {
		cio.WriteBytes(p, tcp.MccRecords[i].Index, 1)
		p = p[1:]
	}
	return writeToStream(stream, buf, mgr)
}
