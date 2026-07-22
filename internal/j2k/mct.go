package j2k

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
)

// mctElementSize ports MCT_ELEMENT_SIZE.
var mctElementSize = [4]uint32{2, 4, 4, 8}

// readMCT ports opj_j2k_read_mct.
func (d *Decoder) readMCT(data []byte) error {
	tcp := d.tcpAt()
	if len(data) < 2 {
		d.mgr.Errorf("Error reading MCT marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	if r.u(2) != 0 { // Zmct
		d.mgr.Warnf("Cannot take in charge mct data within multiple MCT records\n")
		return nil
	}
	if len(data) <= 6 {
		d.mgr.Errorf("Error reading MCT marker\n")
		return ErrMarkerHandler
	}
	imct := r.u(2) // Imct
	indix := imct & 0xff

	// find existing record by index
	found := -1
	for i := uint32(0); i < tcp.MNbMctRecords; i++ {
		if tcp.MMctRecords[i].Index == indix {
			found = int(i)
			break
		}
	}
	var rec *cparams.MctData
	if found < 0 {
		if tcp.MNbMctRecords == tcp.MNbMaxMctRecords {
			tcp.MNbMaxMctRecords += mctDefaultNbRecords
			grown := make([]cparams.MctData, tcp.MNbMaxMctRecords)
			copy(grown, tcp.MMctRecords)
			tcp.MMctRecords = grown
		}
		rec = &tcp.MMctRecords[tcp.MNbMctRecords]
		tcp.MNbMctRecords++
	} else {
		rec = &tcp.MMctRecords[found]
	}

	rec.Data = nil
	rec.Index = indix
	rec.ArrayType = cparams.MctArrayType((imct >> 8) & 3)
	rec.ElementType = cparams.MctElementType((imct >> 10) & 3)

	if r.u(2) != 0 { // Ymct
		d.mgr.Warnf("Cannot take in charge multiple MCT markers\n")
		return nil
	}
	payload := data[r.pos:]
	rec.Data = append([]byte(nil), payload...)
	return nil
}

// readMCC ports opj_j2k_read_mcc.
func (d *Decoder) readMCC(data []byte) error {
	tcp := d.tcpAt()
	if len(data) < 2 {
		d.mgr.Errorf("Error reading MCC marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	if r.u(2) != 0 { // Zmcc
		d.mgr.Warnf("Cannot take in charge multiple data spanning\n")
		return nil
	}
	if len(data) < 7 {
		d.mgr.Errorf("Error reading MCC marker\n")
		return ErrMarkerHandler
	}
	indix := r.u(1) // Imcc

	found := -1
	for i := uint32(0); i < tcp.MNbMccRecords; i++ {
		if tcp.MccRecords[i].Index == indix {
			found = int(i)
			break
		}
	}
	newMcc := false
	var recIdx int
	if found < 0 {
		if tcp.MNbMccRecords == tcp.MNbMaxMccRecords {
			tcp.MNbMaxMccRecords += mccDefaultNbRecords
			grown := make([]cparams.MccData, tcp.MNbMaxMccRecords)
			copy(grown, tcp.MccRecords)
			tcp.MccRecords = grown
		}
		recIdx = int(tcp.MNbMccRecords)
		newMcc = true
	} else {
		recIdx = found
	}
	rec := &tcp.MccRecords[recIdx]
	rec.Index = indix

	if r.u(2) != 0 { // Ymcc
		d.mgr.Warnf("Cannot take in charge multiple data spanning\n")
		return nil
	}
	nbCollections := r.u(2) // Qmcc
	if nbCollections > 1 {
		d.mgr.Warnf("Cannot take in charge multiple collections\n")
		return nil
	}
	hs := len(data) - 7

	for i := uint32(0); i < nbCollections; i++ {
		if hs < 3 {
			d.mgr.Errorf("Error reading MCC marker\n")
			return ErrMarkerHandler
		}
		if r.u(1) != 1 { // Xmcci
			d.mgr.Warnf("Cannot take in charge collections other than array decorrelation\n")
			return nil
		}
		nbComps := r.u(2)
		hs -= 3
		nbBytesByComp := 1 + int(nbComps>>15)
		rec.NbComps = nbComps & 0x7fff
		if hs < nbBytesByComp*int(rec.NbComps)+2 {
			d.mgr.Errorf("Error reading MCC marker\n")
			return ErrMarkerHandler
		}
		hs -= nbBytesByComp*int(rec.NbComps) + 2
		for j := uint32(0); j < rec.NbComps; j++ {
			if r.u(nbBytesByComp) != j { // Cmccij
				d.mgr.Warnf("Cannot take in charge collections with indix shuffle\n")
				return nil
			}
		}
		nbComps2 := r.u(2)
		nbBytesByComp = 1 + int(nbComps2>>15)
		nbComps2 &= 0x7fff
		if nbComps2 != rec.NbComps {
			d.mgr.Warnf("Cannot take in charge collections without same number of indixes\n")
			return nil
		}
		if hs < nbBytesByComp*int(rec.NbComps)+3 {
			d.mgr.Errorf("Error reading MCC marker\n")
			return ErrMarkerHandler
		}
		hs -= nbBytesByComp*int(rec.NbComps) + 3
		for j := uint32(0); j < rec.NbComps; j++ {
			if r.u(nbBytesByComp) != j { // Wmccij
				d.mgr.Warnf("Cannot take in charge collections with indix shuffle\n")
				return nil
			}
		}
		tmcc := r.u(3)
		rec.IsIrreversible = ((tmcc >> 16) & 1) == 0
		rec.DecorrelationArray = -1
		rec.OffsetArray = -1

		if idx := tmcc & 0xff; idx != 0 {
			ref := -1
			for j := uint32(0); j < tcp.MNbMctRecords; j++ {
				if tcp.MMctRecords[j].Index == idx {
					ref = int(j)
					break
				}
			}
			if ref < 0 {
				d.mgr.Errorf("Error reading MCC marker\n")
				return ErrMarkerHandler
			}
			rec.DecorrelationArray = int32(ref)
		}
		if idx := (tmcc >> 8) & 0xff; idx != 0 {
			ref := -1
			for j := uint32(0); j < tcp.MNbMctRecords; j++ {
				if tcp.MMctRecords[j].Index == idx {
					ref = int(j)
					break
				}
			}
			if ref < 0 {
				d.mgr.Errorf("Error reading MCC marker\n")
				return ErrMarkerHandler
			}
			rec.OffsetArray = int32(ref)
		}
	}
	if hs != 0 {
		d.mgr.Errorf("Error reading MCC marker\n")
		return ErrMarkerHandler
	}
	if newMcc {
		tcp.MNbMccRecords++
	}
	return nil
}

// readMCO ports opj_j2k_read_mco.
func (d *Decoder) readMCO(data []byte) error {
	img := d.privateImage
	tcp := d.tcpAt()
	if len(data) < 1 {
		d.mgr.Errorf("Error reading MCO marker\n")
		return ErrMarkerHandler
	}
	r := &mreader{data: data}
	nbStages := r.u(1) // Nmco
	if nbStages > 1 {
		d.mgr.Warnf("Cannot take in charge multiple transformation stages.\n")
		return nil
	}
	if len(data) != int(nbStages)+1 {
		d.mgr.Warnf("Error reading MCO marker\n")
		return ErrMarkerHandler
	}
	for i := uint32(0); i < img.Numcomps; i++ {
		tcp.TCCPs[i].MDcLevelShift = 0
	}
	tcp.MMctDecodingMatrix = nil
	for i := uint32(0); i < nbStages; i++ {
		idx := r.u(1)
		if err := d.addMCT(tcp, idx); err != nil {
			return err
		}
	}
	return nil
}

// addMCT ports opj_j2k_add_mct.
func (d *Decoder) addMCT(tcp *cparams.TCP, index uint32) error {
	img := d.privateImage
	mccIdx := -1
	for i := uint32(0); i < tcp.MNbMccRecords; i++ {
		if tcp.MccRecords[i].Index == index {
			mccIdx = int(i)
			break
		}
	}
	if mccIdx < 0 {
		return nil // element discarded
	}
	mcc := &tcp.MccRecords[mccIdx]
	if mcc.NbComps != img.Numcomps {
		return nil
	}
	if mcc.DecorrelationArray >= 0 {
		deco := &tcp.MMctRecords[mcc.DecorrelationArray]
		dataSize := mctElementSize[deco.ElementType] * img.Numcomps * img.Numcomps
		if uint32(len(deco.Data)) != dataSize {
			return ErrMarkerHandler
		}
		nbElem := img.Numcomps * img.Numcomps
		tcp.MMctDecodingMatrix = make([]float32, nbElem)
		mctReadToFloat(deco.ElementType, deco.Data, tcp.MMctDecodingMatrix, nbElem)
	}
	if mcc.OffsetArray >= 0 {
		off := &tcp.MMctRecords[mcc.OffsetArray]
		dataSize := mctElementSize[off.ElementType] * img.Numcomps
		if uint32(len(off.Data)) != dataSize {
			return ErrMarkerHandler
		}
		nbElem := img.Numcomps
		offData := make([]int32, nbElem)
		mctReadToInt32(off.ElementType, off.Data, offData, nbElem)
		for i := uint32(0); i < img.Numcomps; i++ {
			tcp.TCCPs[i].MDcLevelShift = offData[i]
		}
	}
	return nil
}

// mctReadToFloat ports j2k_mct_read_functions_to_float[elementType].
func mctReadToFloat(et cparams.MctElementType, src []byte, dst []float32, nbElem uint32) {
	switch et {
	case cparams.MctTypeInt16:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = float32(cio.ReadBytes(src[i*2:], 2))
		}
	case cparams.MctTypeInt32:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = float32(cio.ReadBytes(src[i*4:], 4))
		}
	case cparams.MctTypeFloat:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = cio.ReadFloat(src[i*4:])
		}
	case cparams.MctTypeDouble:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = float32(cio.ReadDouble(src[i*8:]))
		}
	}
}

// mctReadToInt32 ports j2k_mct_read_functions_to_int32[elementType].
func mctReadToInt32(et cparams.MctElementType, src []byte, dst []int32, nbElem uint32) {
	switch et {
	case cparams.MctTypeInt16:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = int32(cio.ReadBytes(src[i*2:], 2))
		}
	case cparams.MctTypeInt32:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = int32(cio.ReadBytes(src[i*4:], 4))
		}
	case cparams.MctTypeFloat:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = int32(cio.ReadFloat(src[i*4:]))
		}
	case cparams.MctTypeDouble:
		for i := uint32(0); i < nbElem; i++ {
			dst[i] = int32(cio.ReadDouble(src[i*8:]))
		}
	}
}
