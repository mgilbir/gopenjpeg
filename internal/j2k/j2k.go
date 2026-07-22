// Package j2k is a pure-Go port of the DECODE path of OpenJPEG's j2k.c/j2k.h:
// the JPEG 2000 codestream marker machinery and the tile-part decode state
// machine. It parses the main header (SIZ, COD, COC, QCD, QCC, RGN, POC, PPM,
// PPT, TLM/PLM/PLT, COM, CRG, CAP, CPF, MCT/MCC/MCO, ...), then reads and
// decodes the tile-parts, driving package tcd for the per-tile pipeline.
//
// Only the decoder is ported (W7). Encode entry points are left as stubs that
// return errors (W9 owns them).
//
// The library never panics: all malformed input yields errors. Every bounds and
// overflow guard of the C reference is preserved, since the marker parsers are a
// primary CVE surface. The strict-vs-non-strict tolerance behaviour of the C
// decoder (truncated streams decoding partially in non-strict mode) is kept.
package j2k

import (
	"errors"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/tcd"
)

// Marker values (J2K_MS_*).
const (
	msSOC = 0xff4f
	msSOT = 0xff90
	msSOD = 0xff93
	msEOC = 0xffd9
	msCAP = 0xff50
	msSIZ = 0xff51
	msCOD = 0xff52
	msCOC = 0xff53
	msCPF = 0xff59
	msRGN = 0xff5e
	msQCD = 0xff5c
	msQCC = 0xff5d
	msPOC = 0xff5f
	msTLM = 0xff55
	msPLM = 0xff57
	msPLT = 0xff58
	msPPM = 0xff60
	msPPT = 0xff61
	msSOP = 0xff91
	msEPH = 0xff92
	msCRG = 0xff63
	msCOM = 0xff64
	msCBD = 0xff78
	msMCC = 0xff75
	msMCT = 0xff74
	msMCO = 0xff77
	msUNK = 0
)

// Decoder state flags (J2K_STATUS).
const (
	stNone   = 0x0000
	stMHSOC  = 0x0001
	stMHSIZ  = 0x0002
	stMH     = 0x0004
	stTPHSOT = 0x0008
	stTPH    = 0x0010
	stMT     = 0x0020
	stNEOC   = 0x0040
	stData   = 0x0080
	stEOC    = 0x0100
	stErr    = 0x8000
)

// Coding-style, quantisation and code-block style flags (mirrors cparams).
const (
	cpCstyPRT  = 0x01
	cpCstySOP  = 0x02
	cpCstyEPH  = 0x04
	ccpCstyPRT = 0x01

	ccpQntStyNoQnt = 0
	ccpQntStySiQnt = 1

	ccpCblkStyHTMixed = 0x80

	maxRLvls  = cparams.MaxRLvls
	maxBands  = cparams.MaxBands
	maxPocs   = cparams.MaxPocs
	cblkExtra = 2 // OPJ_COMMON_CBLK_DATA_EXTRA

	mctDefaultNbRecords = 10
	mccDefaultNbRecords = 10
)

// Errors returned by the decoder.
var (
	ErrExpectedSOC    = errors.New("j2k: expected SOC marker")
	ErrStreamTooShort = errors.New("j2k: stream too short")
	ErrMarkerPosition = errors.New("j2k: marker not compliant with its position")
	ErrMarkerID       = errors.New("j2k: a marker ID (0xff--) was expected")
	ErrInvalidMarker  = errors.New("j2k: invalid marker size")
	ErrMarkerHandler  = errors.New("j2k: marker handler failed")
	ErrRequiredMarker = errors.New("j2k: required marker missing in main header")
	ErrBadSIZ         = errors.New("j2k: error with SIZ marker")
	ErrBadParams      = errors.New("j2k: invalid coding parameters")
	ErrDecodeFailed   = errors.New("j2k: failed to decode")
	ErrNotImplemented = errors.New("j2k: encode path not implemented in W7 (owned by W9)")
	ErrHeaderNotRead  = errors.New("j2k: main header must be read first")
	ErrInvalidTile    = errors.New("j2k: invalid tile index")
	ErrValidation     = errors.New("j2k: decoding validation failed")
)

// decoderState ports the opj_j2k_dec_t fields the decode path uses.
type decoderState struct {
	state uint32 // m_state

	defaultTCP *cparams.TCP // m_default_tcp

	sotLength uint32 // m_sot_length

	startTileX uint32 // m_start_tile_x
	startTileY uint32
	endTileX   uint32
	endTileY   uint32

	tileIndToDec int32 // m_tile_ind_to_dec (-1 = all)

	lastTilePart bool // m_last_tile_part

	numcompsToDecode  uint32
	compsIndicesToDec []uint32

	canDecode    bool // m_can_decode
	discardTiles bool
	skipData     bool

	nbTilePartsCorrectionChecked bool
	nbTilePartsCorrection        uint32
}

// Decoder is the exported J2K decompressor, mirroring the C opj_j2k_t decode
// API surface. The root public package and jp2 wrap it.
type Decoder struct {
	CP           cparams.CP // m_cp
	dec          decoderState
	privateImage *image.Image // m_private_image
	outputImage  *image.Image // m_output_image

	currentTileNumber uint32 // m_current_tile_number
	tcd               *tcd.TCD

	mgr *event.Manager

	// mainHeadEnd is the stream position just after the main header.
	mainHeadEnd int64

	ihdrW uint32 // ihdr_w (from JP2 IHDR; 0 for raw codestream)
	ihdrH uint32
}

// CreateDecompress ports opj_j2k_create_decompress.
func CreateDecompress() *Decoder {
	d := &Decoder{}
	d.dec.tileIndToDec = -1
	d.dec.defaultTCP = &cparams.TCP{}
	d.CP.MIsDecoder = true
	return d
}

// SetupDecoder ports opj_j2k_setup_decoder: apply user decode parameters
// (reduction factor and layer count).
func (d *Decoder) SetupDecoder(reduce, layer uint32) {
	d.CP.MDec.MReduce = reduce
	d.CP.MDec.MLayer = layer
}

// SetStrictMode ports opj_j2k_decoder_set_strict_mode.
func (d *Decoder) SetStrictMode(strict bool) {
	d.CP.Strict = strict
	if strict {
		d.dec.nbTilePartsCorrectionChecked = true
	}
}

// SetDecodedResolutionFactor ports opj_j2k_set_decoded_resolution_factor.
func (d *Decoder) SetDecodedResolutionFactor(resFactor uint32) error {
	d.CP.MDec.MReduce = resFactor
	if d.privateImage != nil && d.privateImage.Comps != nil && d.dec.defaultTCP != nil {
		for i := uint32(0); i < d.privateImage.Numcomps; i++ {
			if i < uint32(len(d.dec.defaultTCP.TCCPs)) {
				maxRes := d.dec.defaultTCP.TCCPs[i].Numresolutions
				if resFactor >= maxRes {
					d.mgr.Errorf("Resolution factor is greater than the maximum resolution in the component.\n")
					return ErrBadParams
				}
				d.privateImage.Comps[i].Factor = resFactor
			}
		}
	}
	return nil
}

// SetDecodedComponents ports opj_j2k_set_decoded_components.
func (d *Decoder) SetDecodedComponents(compsIndices []uint32) error {
	if d.privateImage == nil {
		d.mgr.Errorf("opj_read_header() should be called before opj_set_decoded_components().\n")
		return ErrHeaderNotRead
	}
	mapped := make([]bool, d.privateImage.Numcomps)
	for _, ci := range compsIndices {
		if ci >= d.privateImage.Numcomps {
			d.mgr.Errorf("Invalid component index: %u\n", ci)
			return ErrBadParams
		}
		if mapped[ci] {
			d.mgr.Errorf("Component index %u used several times\n", ci)
			return ErrBadParams
		}
		mapped[ci] = true
	}
	if len(compsIndices) > 0 {
		d.dec.compsIndicesToDec = append([]uint32(nil), compsIndices...)
	} else {
		d.dec.compsIndicesToDec = nil
	}
	d.dec.numcompsToDecode = uint32(len(compsIndices))
	return nil
}

// EndDecompress ports opj_j2k_end_decompress (a no-op that succeeds).
func (d *Decoder) EndDecompress() error { return nil }

// tcpAt returns the TCP that a marker read in the current state should modify:
// the current tile's TCP when in a tile-part header, else the default TCP.
func (d *Decoder) tcpAt() *cparams.TCP {
	if d.dec.state == stTPH {
		return &d.CP.Tcps[d.currentTileNumber]
	}
	return d.dec.defaultTCP
}
