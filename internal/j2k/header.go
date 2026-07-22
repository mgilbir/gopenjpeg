package j2k

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/tcd"
)

// markerHandler ports one entry of j2k_memory_marker_handler_tab.
type markerHandler struct {
	id      uint32
	states  uint32
	handler func(*Decoder, []byte) error
}

// markerTab ports j2k_memory_marker_handler_tab (decode entries only).
var markerTab = []markerHandler{
	{msSOT, stMH | stTPHSOT, (*Decoder).readSOT}, // main-header loop breaks on SOT before dispatch
	{msCOD, stMH | stTPH, (*Decoder).readCOD},
	{msCOC, stMH | stTPH, (*Decoder).readCOC},
	{msRGN, stMH | stTPH, (*Decoder).readRGN},
	{msQCD, stMH | stTPH, (*Decoder).readQCD},
	{msQCC, stMH | stTPH, (*Decoder).readQCC},
	{msPOC, stMH | stTPH, (*Decoder).readPOC},
	{msSIZ, stMHSIZ, (*Decoder).readSIZ},
	{msTLM, stMH, (*Decoder).readTLM},
	{msPLM, stMH, (*Decoder).readPLM},
	{msPLT, stTPH, (*Decoder).readPLT},
	{msPPM, stMH, (*Decoder).readPPM},
	{msPPT, stTPH, (*Decoder).readPPT},
	{msSOP, 0, nil},
	{msCRG, stMH, (*Decoder).readCRG},
	{msCOM, stMH | stTPH, (*Decoder).readCOM},
	{msMCT, stMH | stTPH, (*Decoder).readMCT},
	{msCBD, stMH, (*Decoder).readCBD},
	{msCAP, stMH, (*Decoder).readCAP},
	{msCPF, stMH, (*Decoder).readCPF},
	{msMCC, stMH | stTPH, (*Decoder).readMCC},
	{msMCO, stMH | stTPH, (*Decoder).readMCO},
}

// getMarkerHandler ports opj_j2k_get_marker_handler. Returns the entry for the
// marker, or a synthetic UNK entry (id 0) if unknown.
func getMarkerHandler(id uint32) markerHandler {
	for _, e := range markerTab {
		if e.id == id {
			return e
		}
	}
	return markerHandler{id: msUNK, states: stMH | stTPH, handler: nil}
}

// readExact reads exactly n bytes from the stream, returning an error otherwise.
func readExact(s *cio.Stream, n int, mgr *event.Manager) ([]byte, error) {
	buf := make([]byte, n)
	got := 0
	for got < n {
		m, err := s.Read(buf[got:], mgr)
		if m > 0 {
			got += m
		}
		if err != nil || m == 0 {
			break
		}
	}
	if got != n {
		return nil, ErrStreamTooShort
	}
	return buf, nil
}

// readSOC ports opj_j2k_read_soc.
func (d *Decoder) readSOC(s *cio.Stream) error {
	buf, err := readExact(s, 2, d.mgr)
	if err != nil {
		return err
	}
	if cio.ReadBytes(buf, 2) != msSOC {
		return ErrExpectedSOC
	}
	d.dec.state = stMHSIZ
	return nil
}

// readHeaderProcedure ports opj_j2k_read_header_procedure.
func (d *Decoder) readHeaderProcedure(s *cio.Stream) error {
	d.dec.state = stMHSOC
	if err := d.readSOC(s); err != nil {
		d.mgr.Errorf("Expected a SOC marker \n")
		return err
	}

	buf, err := readExact(s, 2, d.mgr)
	if err != nil {
		d.mgr.Errorf("Stream too short\n")
		return err
	}
	curMarker := cio.ReadBytes(buf, 2)

	hasSIZ, hasCOD, hasQCD := false, false, false

	for curMarker != msSOT {
		if curMarker < 0xff00 {
			d.mgr.Errorf("A marker ID was expected (0xff--) instead of %x\n", curMarker)
			return ErrMarkerID
		}
		mh := getMarkerHandler(curMarker)
		if mh.id == msUNK {
			nm, uerr := d.readUnk(s, curMarker)
			if uerr != nil {
				d.mgr.Errorf("Unknown marker has been detected and generated error.\n")
				return uerr
			}
			curMarker = nm
			if curMarker == msSOT {
				break
			}
			mh = getMarkerHandler(curMarker)
		}
		switch mh.id {
		case msSIZ:
			hasSIZ = true
		case msCOD:
			hasCOD = true
		case msQCD:
			hasQCD = true
		}
		if d.dec.state&mh.states == 0 {
			d.mgr.Errorf("Marker is not compliant with its position\n")
			return ErrMarkerPosition
		}

		sizeBuf, serr := readExact(s, 2, d.mgr)
		if serr != nil {
			d.mgr.Errorf("Stream too short\n")
			return serr
		}
		markerSize := cio.ReadBytes(sizeBuf, 2)
		if markerSize < 2 {
			d.mgr.Errorf("Invalid marker size\n")
			return ErrInvalidMarker
		}
		markerSize -= 2

		seg, rerr := readExact(s, int(markerSize), d.mgr)
		if rerr != nil {
			d.mgr.Errorf("Stream too short\n")
			return rerr
		}
		if mh.handler != nil {
			if herr := mh.handler(d, seg); herr != nil {
				d.mgr.Errorf("Marker handler function failed to read the marker segment\n")
				return herr
			}
		}

		nb, nerr := readExact(s, 2, d.mgr)
		if nerr != nil {
			d.mgr.Errorf("Stream too short\n")
			return nerr
		}
		curMarker = cio.ReadBytes(nb, 2)
	}

	if !hasSIZ {
		d.mgr.Errorf("required SIZ marker not found in main header\n")
		return ErrRequiredMarker
	}
	if !hasCOD {
		d.mgr.Errorf("required COD marker not found in main header\n")
		return ErrRequiredMarker
	}
	if !hasQCD {
		d.mgr.Errorf("required QCD marker not found in main header\n")
		return ErrRequiredMarker
	}

	if err := d.mergePPM(); err != nil {
		d.mgr.Errorf("Failed to merge PPM data\n")
		return err
	}

	d.mainHeadEnd = s.Tell() - 2
	d.dec.state = stTPHSOT
	return nil
}

// readUnk ports opj_j2k_read_unk: skip an unknown marker segment until a known
// marker at a valid position is found. Returns the next known marker id.
func (d *Decoder) readUnk(s *cio.Stream, current uint32) (uint32, error) {
	d.mgr.Warnf("Unknown marker\n")
	for {
		buf, err := readExact(s, 2, d.mgr)
		if err != nil {
			d.mgr.Errorf("Stream too short\n")
			return 0, err
		}
		m := cio.ReadBytes(buf, 2)
		if m < 0xff00 {
			continue
		}
		mh := getMarkerHandler(m)
		if d.dec.state&mh.states == 0 {
			d.mgr.Errorf("Marker is not compliant with its position\n")
			return 0, ErrMarkerPosition
		}
		if mh.id != msUNK {
			return mh.id, nil
		}
	}
}

// copyDefaultTCPAndCreateTCD ports opj_j2k_copy_default_tcp_and_create_tcd.
func (d *Decoder) copyDefaultTCPAndCreateTCD() error {
	img := d.privateImage
	nbTiles := d.CP.Th * d.CP.Tw
	def := d.dec.defaultTCP

	for i := uint32(0); i < nbTiles; i++ {
		tcp := &d.CP.Tcps[i]
		tccps := tcp.TCCPs // keep the per-tile tccp storage

		// Copy scalar/coding parameters from the default tile.
		*tcp = *def
		tcp.Cod = false
		tcp.Ppt = 0
		tcp.PptData = nil
		tcp.MCurrentTilePartNumber = -1
		tcp.MData = nil
		tcp.MDataSize = 0
		tcp.PptMarkers = nil
		tcp.PptMarkersCount = 0
		tcp.PptBuffer = nil

		// Deep-copy the MCT decoding matrix.
		if def.MMctDecodingMatrix != nil {
			tcp.MMctDecodingMatrix = append([]float32(nil), def.MMctDecodingMatrix...)
		}
		// Deep-copy MCT records (each with its own data).
		tcp.MMctRecords = make([]cparams.MctData, def.MNbMaxMctRecords)
		copy(tcp.MMctRecords, def.MMctRecords)
		for j := uint32(0); j < def.MNbMctRecords; j++ {
			if def.MMctRecords[j].Data != nil {
				tcp.MMctRecords[j].Data = append([]byte(nil), def.MMctRecords[j].Data...)
			}
		}
		tcp.MNbMaxMctRecords = def.MNbMaxMctRecords
		tcp.MNbMctRecords = def.MNbMctRecords

		// Copy MCC records (indices into MMctRecords stay valid).
		tcp.MccRecords = make([]cparams.MccData, def.MNbMaxMccRecords)
		copy(tcp.MccRecords, def.MccRecords)
		tcp.MNbMaxMccRecords = def.MNbMaxMccRecords
		tcp.MNbMccRecords = def.MNbMccRecords

		// Reconnect the per-tile tccp storage and copy the default tccps into it.
		tcp.TCCPs = tccps
		copy(tcp.TCCPs, def.TCCPs)
	}

	d.tcd = tcd.Create(true)
	d.tcd.SetNumThreads(d.numThreads)
	if !d.tcd.Init(img, &d.CP) {
		d.mgr.Errorf("Cannot decode tile, memory error\n")
		return ErrDecodeFailed
	}
	return nil
}

// ReadHeader ports opj_j2k_read_header: read the codestream main header and
// return the output image header. It must be called before SetDecodeArea /
// Decode / GetTile.
func (d *Decoder) ReadHeader(s *cio.Stream, mgr *event.Manager) (*image.Image, error) {
	d.mgr = mgr
	d.privateImage = image.Create0()

	// decoding validation (state must be 0).
	if d.dec.state != stNone {
		d.mgr.Errorf("Invalid decoder state\n")
		return nil, ErrValidation
	}

	if err := d.readHeaderProcedure(s); err != nil {
		d.privateImage = nil
		return nil, err
	}
	if err := d.copyDefaultTCPAndCreateTCD(); err != nil {
		d.privateImage = nil
		return nil, err
	}

	out := image.Create0()
	image.CopyHeader(d.privateImage, out)
	return out, nil
}
