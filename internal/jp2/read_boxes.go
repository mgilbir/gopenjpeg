package jp2

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// readIhdr ports opj_jp2_read_ihdr: parse the Image Header box.
func (jp2 *JP2) readIhdr(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.comps != nil {
		mgr.Warnf("Ignoring ihdr box. First ihdr box already read\n")
		return true
	}

	if size != 14 {
		mgr.Errorf("Bad image header box (bad size)\n")
		return false
	}

	jp2.h = cio.ReadBytes(data[0:], 4)        // HEIGHT
	jp2.w = cio.ReadBytes(data[4:], 4)        // WIDTH
	jp2.numcomps = cio.ReadBytes(data[8:], 2) // NC
	p := data[10:]

	if jp2.h < 1 || jp2.w < 1 || jp2.numcomps < 1 {
		mgr.Errorf("Wrong values for: w(%d) h(%d) numcomps(%d) (ihdr)\n",
			jp2.w, jp2.h, jp2.numcomps)
		return false
	}
	// unsigned underflow is well defined: 1 <= numcomps <= 16384 is the valid range.
	if (jp2.numcomps - 1) >= 16384 {
		mgr.Errorf("Invalid number of components (ihdr)\n")
		return false
	}

	// allocate memory for components
	jp2.comps = make([]Comps, jp2.numcomps)

	jp2.bpc = cio.ReadBytes(p[0:], 1) // BPC
	jp2.c = cio.ReadBytes(p[1:], 1)   // C

	// Should be equal to 7 cf. the image header box chapter of the norm.
	if jp2.c != 7 {
		mgr.Infof("JP2 IHDR box: compression type indicate that the file is not a conforming JP2 file (%d) \n",
			jp2.c)
	}

	jp2.unkC = cio.ReadBytes(p[2:], 1) // UnkC
	jp2.ipr = cio.ReadBytes(p[3:], 1)  // IPR

	jp2.codec.SetAllowDifferentBitDepthSign(jp2.bpc == 255)
	jp2.codec.SetIHDRDimensions(jp2.w, jp2.h)
	jp2.hasIhdr = true

	return true
}

// readBpcc ports opj_jp2_read_bpcc: parse the Bits Per Component box.
func (jp2 *JP2) readBpcc(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.bpc != 255 {
		mgr.Warnf("A BPCC header box is available although BPC given by the IHDR box (%d) indicate components bit depth is constant\n",
			jp2.bpc)
	}

	if size != jp2.numcomps {
		mgr.Errorf("Bad BPCC header box (bad size)\n")
		return false
	}

	for i := uint32(0); i < jp2.numcomps; i++ {
		jp2.comps[i].Bpcc = cio.ReadBytes(data[i:], 1) // read each BPCC component
	}

	return true
}

// readColr ports opj_jp2_read_colr: parse the Colour Specification box,
// capturing an enumerated colour space, an embedded ICC profile, or the CIELab
// special case, exactly as C does (including the subtle use of the previous
// jp2->enumcs value in the meth==1 over-size warning).
func (jp2 *JP2) readColr(data []byte, size uint32, mgr *event.Manager) bool {
	if size < 3 {
		mgr.Errorf("Bad COLR header box (bad size)\n")
		return false
	}

	// Part 1, I.5.3.3: ignore all Colour Specification boxes after the first.
	if jp2.color.JP2HasColr != 0 {
		mgr.Infof("A conforming JP2 reader shall ignore all Colour Specification boxes after the first, so we ignore this one.\n")
		return true
	}

	jp2.meth = cio.ReadBytes(data[0:], 1)       // METH
	jp2.precedence = cio.ReadBytes(data[1:], 1) // PRECEDENCE
	jp2.approx = cio.ReadBytes(data[2:], 1)     // APPROX
	p := data[3:]

	switch {
	case jp2.meth == 1:
		if size < 7 {
			mgr.Errorf("Bad COLR header box (bad size: %d)\n", size)
			return false
		}
		// NOTE: this compares the PREVIOUS jp2.enumcs value (still 0 for the
		// first colr box) — a faithful quirk of the C code, which reads EnumCS
		// only after this check.
		if size > 7 && jp2.enumcs != 14 { // 14 (CIELab) handled below
			mgr.Warnf("Bad COLR header box (bad size: %d)\n", size)
		}

		jp2.enumcs = cio.ReadBytes(p[0:], 4) // EnumCS
		p = p[4:]

		if jp2.enumcs == 14 { // CIELab
			cielab := make([]uint32, 9)
			cielab[0] = 14 // enumcs

			// default values
			var rl, ol, ra, oa, rb, ob uint32
			il := uint32(0x00443530) // D50
			cielab[1] = 0x44454600   // DEF

			if size == 35 {
				rl = cio.ReadBytes(p[0:], 4)
				ol = cio.ReadBytes(p[4:], 4)
				ra = cio.ReadBytes(p[8:], 4)
				oa = cio.ReadBytes(p[12:], 4)
				rb = cio.ReadBytes(p[16:], 4)
				ob = cio.ReadBytes(p[20:], 4)
				il = cio.ReadBytes(p[24:], 4)
				cielab[1] = 0
			} else if size != 7 {
				mgr.Warnf("Bad COLR header box (CIELab, bad size: %d)\n", size)
			}
			cielab[2] = rl
			cielab[4] = ra
			cielab[6] = rb
			cielab[3] = ol
			cielab[5] = oa
			cielab[7] = ob
			cielab[8] = il

			// The C code stores an OPJ_UINT32[9] array reinterpreted as bytes,
			// with icc_profile_len == 0 signalling the CIELab case. We pack the
			// nine words big-endian into ICCProfileBuf; the future CIELab-aware
			// colour converter must decode them the same way. (Deviation from
			// C's native-endian in-memory layout — documented and internal.)
			jp2.color.ICCProfileBuf = packUint32sBE(cielab)
			jp2.color.ICCProfileLen = 0
		}
		jp2.color.JP2HasColr = 1

	case jp2.meth == 2:
		// ICC profile. C computes icc_len as a signed OPJ_INT32; we use unsigned
		// arithmetic (size >= 3 is guaranteed above, and size == len(payload)
		// bounds so the reads stay in range) so no negative length can reach
		// make() — honouring the never-panic contract on untrusted input.
		iccLen := size - 3
		jp2.color.ICCProfileLen = iccLen
		buf := make([]byte, iccLen)
		for i := uint32(0); i < iccLen; i++ {
			buf[i] = byte(cio.ReadBytes(p[i:], 1))
		}
		jp2.color.ICCProfileBuf = buf
		jp2.color.JP2HasColr = 1

	case jp2.meth > 2:
		// ISO/IEC 15444-1:2004 (E) Table I.9: a conforming JP2 reader shall
		// ignore the entire Colour Specification box for other METH values.
		mgr.Infof("COLR BOX meth value is not a regular value (%d), so we will ignore the entire Colour Specification box. \n",
			jp2.meth)
	}

	return true
}

// packUint32sBE serialises a slice of uint32 into a big-endian byte buffer. It
// backs the CIELab parameter capture in readColr.
func packUint32sBE(vals []uint32) []byte {
	buf := make([]byte, 4*len(vals))
	for i, v := range vals {
		cio.WriteBytes(buf[4*i:], v, 4)
	}
	return buf
}

// readPclr ports opj_jp2_read_pclr: collect palette data.
func (jp2 *JP2) readPclr(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.color.Pclr != nil {
		return false
	}

	if size < 3 {
		return false
	}

	orig := data
	nrEntries := uint16(cio.ReadBytes(data[0:], 2)) // NE
	data = data[2:]
	if nrEntries == 0 || nrEntries > 1024 {
		mgr.Errorf("Invalid PCLR box. Reports %d entries\n", int(nrEntries))
		return false
	}

	nrChannels := uint16(cio.ReadBytes(data[0:], 1)) // NPC
	data = data[1:]
	if nrChannels == 0 {
		mgr.Errorf("Invalid PCLR box. Reports 0 palette columns\n")
		return false
	}

	if size < 3+uint32(nrChannels) {
		return false
	}

	pclr := &Pclr{
		ChannelSign: make([]byte, nrChannels),
		ChannelSize: make([]byte, nrChannels),
		Entries:     make([]uint32, uint32(nrChannels)*uint32(nrEntries)),
		NrEntries:   nrEntries,
		NrChannels:  byte(nrChannels), // faithful to C: truncates NPC's low byte
		Cmap:        nil,
	}
	jp2.color.Pclr = pclr

	for i := uint16(0); i < nrChannels; i++ {
		v := cio.ReadBytes(data[0:], 1) // Bi
		data = data[1:]
		pclr.ChannelSize[i] = byte((v & 0x7f) + 1)
		if v&0x80 != 0 {
			pclr.ChannelSign[i] = 1
		} else {
			pclr.ChannelSign[i] = 0
		}
	}

	// Track consumed bytes relative to the original buffer for the bounds guard.
	consumed := uint32(len(orig) - len(data))
	ei := 0
	for j := uint16(0); j < nrEntries; j++ {
		for i := uint16(0); i < nrChannels; i++ {
			bytesToRead := uint32((pclr.ChannelSize[i] + 7) >> 3)
			if bytesToRead > 4 { // sizeof(OPJ_UINT32)
				bytesToRead = 4
			}
			if size < consumed+bytesToRead {
				return false
			}
			pclr.Entries[ei] = cio.ReadBytes(data[0:], bytesToRead) // Cji
			ei++
			data = data[bytesToRead:]
			consumed += bytesToRead
		}
	}

	return true
}

// readCmap ports opj_jp2_read_cmap: collect the component-mapping table.
func (jp2 *JP2) readCmap(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.color.Pclr == nil {
		mgr.Errorf("Need to read a PCLR box before the CMAP box.\n")
		return false
	}

	// Part 1, I.5.3.5: at most one Component Mapping box inside a JP2 Header box.
	if jp2.color.Pclr.Cmap != nil {
		mgr.Errorf("Only one CMAP box is allowed.\n")
		return false
	}

	nrChannels := jp2.color.Pclr.NrChannels
	if size < uint32(nrChannels)*4 {
		mgr.Errorf("Insufficient data for CMAP box.\n")
		return false
	}

	cmap := make([]CmapComp, nrChannels)
	for i := 0; i < int(nrChannels); i++ {
		cmap[i].Cmp = uint16(cio.ReadBytes(data[0:], 2)) // CMP^i
		cmap[i].Mtyp = byte(cio.ReadBytes(data[2:], 1))  // MTYP^i
		cmap[i].Pcol = byte(cio.ReadBytes(data[3:], 1))  // PCOL^i
		data = data[4:]
	}

	jp2.color.Pclr.Cmap = cmap
	return true
}

// readCdef ports opj_jp2_read_cdef: collect channel definitions.
func (jp2 *JP2) readCdef(data []byte, size uint32, mgr *event.Manager) bool {
	// Part 1, I.5.3.6: at most one Channel Definition box inside a JP2 Header box.
	if jp2.color.Cdef != nil {
		return false
	}

	if size < 2 {
		mgr.Errorf("Insufficient data for CDEF box.\n")
		return false
	}

	n := uint16(cio.ReadBytes(data[0:], 2)) // N
	data = data[2:]

	if n == 0 {
		mgr.Errorf("Number of channel description is equal to zero in CDEF box.\n")
		return false
	}

	if size < 2+uint32(n)*6 {
		mgr.Errorf("Insufficient data for CDEF box.\n")
		return false
	}

	info := make([]CdefInfo, n)
	jp2.color.Cdef = &Cdef{Info: info, N: n}

	for i := uint16(0); i < n; i++ {
		info[i].Cn = uint16(cio.ReadBytes(data[0:], 2))   // Cn^i
		info[i].Typ = uint16(cio.ReadBytes(data[2:], 2))  // Typ^i
		info[i].Asoc = uint16(cio.ReadBytes(data[4:], 2)) // Asoc^i
		data = data[6:]
	}

	return true
}
