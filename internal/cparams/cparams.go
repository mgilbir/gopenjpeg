// Package cparams holds the data-only coding-parameter structures shared by the
// packet iterator (pi) and tier-2 (t2) code, ported from j2k.h and openjpeg.h.
//
// These are the C structs opj_poc_t, opj_stepsize_t, opj_tccp_t, opj_tcp_t and
// opj_cp_t, reduced (for now) to the fields that pi.c and t2.c actually read.
// j2k-only fields (marker parsing state, mct records, rate-allocation working
// storage, etc.) are intentionally omitted and will be added when the j2k
// package lands. Every retained field carries its C name in a doc comment.
//
// Where the C code uses a union (opj_cp_t.m_specific_param), this port uses
// separate embedded fields (MEnc / MDec); only one is meaningful at a time,
// exactly as in C.
package cparams

// Sizing constants ported from openjpeg.h / j2k.h.
const (
	// MaxRLvls ports OPJ_J2K_MAXRLVLS.
	MaxRLvls = 33
	// MaxBands ports OPJ_J2K_MAXBANDS (3*OPJ_J2K_MAXRLVLS-2).
	MaxBands = 3*MaxRLvls - 2
	// MaxPocs ports J2K_MAX_POCS.
	MaxPocs = 32
	// DefaultNbSegs ports OPJ_J2K_DEFAULT_NB_SEGS.
	DefaultNbSegs = 10
)

// Coding-style flags ported from j2k.h.
const (
	// CPCstyPRT ports J2K_CP_CSTY_PRT.
	CPCstyPRT = 0x01
	// CPCstySOP ports J2K_CP_CSTY_SOP.
	CPCstySOP = 0x02
	// CPCstyEPH ports J2K_CP_CSTY_EPH.
	CPCstyEPH = 0x04

	// CCPCstyPRT ports J2K_CCP_CSTY_PRT.
	CCPCstyPRT = 0x01
)

// Code-block style flags ported from j2k.h (J2K_CCP_CBLKSTY_*).
const (
	// CCPCblkStyLazy ports J2K_CCP_CBLKSTY_LAZY (selective arithmetic coding bypass).
	CCPCblkStyLazy = 0x01
	// CCPCblkStyReset ports J2K_CCP_CBLKSTY_RESET (reset context probabilities on pass boundaries).
	CCPCblkStyReset = 0x02
	// CCPCblkStyTermall ports J2K_CCP_CBLKSTY_TERMALL (termination on each coding pass).
	CCPCblkStyTermall = 0x04
	// CCPCblkStyVSC ports J2K_CCP_CBLKSTY_VSC (vertically stripe causal context).
	CCPCblkStyVSC = 0x08
	// CCPCblkStyPterm ports J2K_CCP_CBLKSTY_PTERM (predictable termination).
	CCPCblkStyPterm = 0x10
	// CCPCblkStySegsym ports J2K_CCP_CBLKSTY_SEGSYM (segmentation symbols are used).
	CCPCblkStySegsym = 0x20
	// CCPCblkStyHT ports J2K_CCP_CBLKSTY_HT (high throughput HT code-blocks).
	CCPCblkStyHT = 0x40
	// CCPCblkStyHTMixed ports J2K_CCP_CBLKSTY_HTMIXED (MIXED mode HT code-blocks).
	CCPCblkStyHTMixed = 0x80
)

// Quantisation-style constants ported from j2k.h (J2K_CCP_QNTSTY_*).
const (
	// CCPQntStyNoQnt ports J2K_CCP_QNTSTY_NOQNT.
	CCPQntStyNoQnt = 0
	// CCPQntStySiQnt ports J2K_CCP_QNTSTY_SIQNT.
	CCPQntStySiQnt = 1
	// CCPQntStySeQnt ports J2K_CCP_QNTSTY_SEQNT.
	CCPQntStySeQnt = 2
)

// Profile constants ported from openjpeg.h, needed by the cinema/IMF tests in pi
// and t2.
const (
	// ProfileNone ports OPJ_PROFILE_NONE.
	ProfileNone = 0x0000
	// ProfilePart2 ports OPJ_PROFILE_PART2.
	ProfilePart2 = 0x8000
	// ProfileCinema2K ports OPJ_PROFILE_CINEMA_2K.
	ProfileCinema2K = 0x0003
	// ProfileCinema4K ports OPJ_PROFILE_CINEMA_4K.
	ProfileCinema4K = 0x0004
	// ProfileCinemaS2K ports OPJ_PROFILE_CINEMA_S2K.
	ProfileCinemaS2K = 0x0005
	// ProfileCinemaS4K ports OPJ_PROFILE_CINEMA_S4K.
	ProfileCinemaS4K = 0x0006
	// ProfileCinemaLTS ports OPJ_PROFILE_CINEMA_LTS.
	ProfileCinemaLTS = 0x0007
	// ProfileIMF2K ports OPJ_PROFILE_IMF_2K.
	ProfileIMF2K = 0x0400
	// ProfileIMF8KR ports OPJ_PROFILE_IMF_8K_R.
	ProfileIMF8KR = 0x0900
)

// IsCinema ports the OPJ_IS_CINEMA(v) macro.
func IsCinema(rsiz uint16) bool {
	return rsiz >= ProfileCinema2K && rsiz <= ProfileCinemaS4K
}

// IsIMF ports the OPJ_IS_IMF(v) macro.
func IsIMF(rsiz uint16) bool {
	return rsiz >= ProfileIMF2K && rsiz <= (ProfileIMF8KR|0x009b)
}

// ProgOrder ports OPJ_PROG_ORDER (enum PROG_ORDER).
type ProgOrder int32

// Progression orders ported from openjpeg.h.
const (
	// ProgUnknown ports OPJ_PROG_UNKNOWN.
	ProgUnknown ProgOrder = -1
	// LRCP ports OPJ_LRCP (layer-resolution-component-precinct).
	LRCP ProgOrder = 0
	// RLCP ports OPJ_RLCP (resolution-layer-component-precinct).
	RLCP ProgOrder = 1
	// RPCL ports OPJ_RPCL (resolution-precinct-component-layer).
	RPCL ProgOrder = 2
	// PCRL ports OPJ_PCRL (precinct-component-resolution-layer).
	PCRL ProgOrder = 3
	// CPRL ports OPJ_CPRL (component-precinct-resolution-layer).
	CPRL ProgOrder = 4
)

// ConvertProgressionOrder ports opj_j2k_convert_progression_order: it returns
// the 4-letter progression string used by the tile-part / POC iteration logic.
// For an unrecognised order it returns "" (the C code returns the empty
// sentinel string at the end of j2k_prog_order_list).
func ConvertProgressionOrder(prg ProgOrder) string {
	switch prg {
	case CPRL:
		return "CPRL"
	case LRCP:
		return "LRCP"
	case PCRL:
		return "PCRL"
	case RLCP:
		return "RLCP"
	case RPCL:
		return "RPCL"
	default:
		return ""
	}
}

// T2Mode ports J2K_T2_MODE (enum T2_MODE).
type T2Mode int32

const (
	// ThreshCalc ports THRESH_CALC (rate-allocation threshold calculation pass).
	ThreshCalc T2Mode = 0
	// FinalPass ports FINAL_PASS (tier-2 final pass).
	FinalPass T2Mode = 1
)

// POC ports opj_poc_t: a progression-order-change record. All fields are
// retained because pi.c reads and writes the full set (including the temporary
// tile-part fields set up in opj_pi_create_encode).
type POC struct {
	Resno0  uint32 // resno0: resolution number start (from POC)
	Compno0 uint32 // compno0: component number start (from POC)

	Layno1  uint32 // layno1: layer number end (from POC)
	Resno1  uint32 // resno1: resolution number end (from POC)
	Compno1 uint32 // compno1: component number end (from POC)

	Layno0  uint32 // layno0: layer number start
	Precno0 uint32 // precno0: precinct number start
	Precno1 uint32 // precno1: precinct number end

	Prg1 ProgOrder // prg1: progression order (as declared)
	Prg  ProgOrder // prg: progression order (current)

	ProgOrder string // progorder: progression order string (OPJ_CHAR[5])
	Tile      uint32 // tile: tile number (starting at 1)

	// tx0,tx1,ty0,ty1 are OPJ_UINT32_SEMANTICALLY_BUT_INT32 (stored unsigned,
	// used with int32 semantics in some arithmetic).
	Tx0 uint32 // tx0
	Tx1 uint32 // tx1
	Ty0 uint32 // ty0
	Ty1 uint32 // ty1

	// Start values, initialised in opj_pi_initialise_encode.
	LayS  uint32 // layS
	ResS  uint32 // resS
	CompS uint32 // compS
	PrcS  uint32 // prcS

	// End values, initialised in opj_pi_initialise_encode.
	LayE  uint32 // layE
	ResE  uint32 // resE
	CompE uint32 // compE
	PrcE  uint32 // prcE

	// Start/end values of tile width and height, initialised in
	// opj_pi_initialise_encode.
	TxS uint32 // txS
	TxE uint32 // txE
	TyS uint32 // tyS
	TyE uint32 // tyE
	Dx  uint32 // dx
	Dy  uint32 // dy

	// Temporary values for tile parts, initialised in opj_pi_create_encode.
	LayT  uint32 // lay_t
	ResT  uint32 // res_t
	CompT uint32 // comp_t
	PrcT  uint32 // prc_t
	Tx0T  uint32 // tx0_t
	Ty0T  uint32 // ty0_t
}

// Stepsize ports opj_stepsize_t: a quantisation step size.
type Stepsize struct {
	Expn int32 // expn: exponent
	Mant int32 // mant: mantissa
}

// TCCP ports opj_tccp_t: tile-component coding parameters.
type TCCP struct {
	Csty           uint32             // csty: coding style
	Numresolutions uint32             // numresolutions
	Cblkw          uint32             // cblkw: code-block width (log2)
	Cblkh          uint32             // cblkh: code-block height (log2)
	Cblksty        uint32             // cblksty: code-block coding style
	Qmfbid         uint32             // qmfbid: DWT identifier
	Qntsty         uint32             // qntsty: quantisation style
	Stepsizes      [MaxBands]Stepsize // stepsizes[OPJ_J2K_MAXBANDS]
	Numgbits       uint32             // numgbits: number of guard bits
	Roishift       int32              // roishift: region-of-interest shift
	Prcw           [MaxRLvls]uint32   // prcw[OPJ_J2K_MAXRLVLS]: precinct width (log2)
	Prch           [MaxRLvls]uint32   // prch[OPJ_J2K_MAXRLVLS]: precinct height (log2)
	MDcLevelShift  int32              // m_dc_level_shift
}

// TCP ports opj_tcp_t (subset read by pi.c and t2.c). j2k-only fields (marker
// state, mct records, rate-allocation working storage, tile-part bookkeeping)
// are deferred until j2k lands.
type TCP struct {
	Csty              uint32       // csty: coding style
	Prg               ProgOrder    // prg: progression order
	Numlayers         uint32       // numlayers
	NumLayersToDecode uint32       // num_layers_to_decode
	MCT               uint32       // mct: multi-component transform identifier
	Rates             [100]float32 // rates[100]: rates of layers
	Numpocs           uint32       // numpocs: number of progression order changes
	Pocs              [MaxPocs]POC // pocs[J2K_MAX_POCS]
	Distoratio        [100]float32 // distoratio[100]: PSNR values
	TCCPs             []TCCP       // tccps: per-component coding parameters

	// PPT (packed packet headers, per-tile) state consumed by
	// opj_t2_read_packet_header. PptData is advanced (and PptLen decremented)
	// as packet headers are consumed, mirroring the C pointer/length pair.
	Ppt     uint32 // ppt flag: there was a PPT marker for this tile
	PptData []byte // ppt_data
	PptLen  uint32 // ppt_len

	// POC flag: a POC marker was used (O:NO, 1:YES).
	POC uint32

	// MMctDecodingMatrix ports opj_tcp_t.m_mct_decoding_matrix: the custom MCT
	// decoding matrix (only used when MCT==2). nil means no matrix.
	MMctDecodingMatrix []float32
	// MMctCodingMatrix ports opj_tcp_t.m_mct_coding_matrix (encode side).
	MMctCodingMatrix []float32

	// ---- j2k decode-side marker/tile-part bookkeeping ----

	// MCurrentTilePartNumber ports m_current_tile_part_number (-1 before the
	// first tile-part of this tile is seen).
	MCurrentTilePartNumber int32
	// MNbTileParts ports m_nb_tile_parts (TNsot).
	MNbTileParts uint32
	// MData / MDataSize accumulate the tile's coded data across tile-parts
	// (opj_j2k_read_sod), with a trailing OPJ_COMMON_CBLK_DATA_EXTRA margin.
	MData     []byte
	MDataSize uint32
	// Cod ports the cod:1 flag (a COD marker was read for this tile).
	Cod bool

	// PPT marker assembly (opj_j2k_read_ppt / merge_ppt).
	PptMarkers      []Ppx
	PptMarkersCount uint32
	PptBuffer       []byte

	// MCT/MCC records (Part-2 custom transforms).
	MMctRecords      []MctData
	MNbMctRecords    uint32
	MNbMaxMctRecords uint32
	MccRecords       []MccData
	MNbMccRecords    uint32
	MNbMaxMccRecords uint32
	// MctNorms ports mct_norms (encode side).
	MctNorms []float64
}

// Ppx ports opj_ppx: one PPM/PPT marker's raw payload (indexed by Zppm/Zppt).
type Ppx struct {
	Data []byte // m_data (nil => not read yet)
}

// MctElementType ports J2K_MCT_ELEMENT_TYPE.
type MctElementType uint32

// MCT element types.
const (
	MctTypeInt16  MctElementType = 0
	MctTypeInt32  MctElementType = 1
	MctTypeFloat  MctElementType = 2
	MctTypeDouble MctElementType = 3
)

// MctArrayType ports J2K_MCT_ARRAY_TYPE.
type MctArrayType uint32

// MCT array types.
const (
	MctTypeDependency    MctArrayType = 0
	MctTypeDecorrelation MctArrayType = 1
	MctTypeOffset        MctArrayType = 2
)

// MctData ports opj_mct_data_t: a raw MCT array record from an MCT marker.
type MctData struct {
	ElementType MctElementType // m_element_type
	ArrayType   MctArrayType   // m_array_type
	Index       uint32         // m_index
	Data        []byte         // m_data
}

// MccData ports opj_simple_mcc_decorrelation_data_t. The C code stores pointers
// into the m_mct_records array; this port stores indices into TCP.MMctRecords
// (-1 meaning "none") to stay slice-append safe.
type MccData struct {
	Index              uint32 // m_index
	NbComps            uint32 // m_nb_comps
	DecorrelationArray int32  // index into MMctRecords, or -1
	OffsetArray        int32  // index into MMctRecords, or -1
	IsIrreversible     bool   // m_is_irreversible
}

// EncodingParam ports opj_encoding_param_t (the m_enc arm of the
// opj_cp_t.m_specific_param union).
type EncodingParam struct {
	MMaxCompSize uint32 // m_max_comp_size: max rate per component (0 => unlimited)
	MTpPos       int32  // m_tp_pos: position of tile-part flag in progression order
	MTpFlag      byte   // m_tp_flag: flag determining tile-part generation
	MTpOn        uint32 // m_tp_on: enabling tile-part generation (bitfield in C)
}

// DecodingParam ports opj_decoding_param_t (the m_dec arm of the union).
type DecodingParam struct {
	MReduce uint32 // m_reduce
	MLayer  uint32 // m_layer
}

// CP ports opj_cp_t (subset read by pi.c and t2.c). The C m_specific_param union
// is represented by the separate MEnc/MDec fields; only one is meaningful at a
// time (MEnc while encoding, MDec while decoding), matching the C union.
type CP struct {
	Rsiz uint16 // rsiz
	Tx0  uint32 // tx0 (XTOsiz)
	Ty0  uint32 // ty0 (YTOsiz)
	Tdx  uint32 // tdx (XTsiz)
	Tdy  uint32 // tdy (YTsiz)
	Tw   uint32 // tw: number of tiles in width
	Th   uint32 // th: number of tiles in height

	// PPM (packed packet headers, main header) state consumed by
	// opj_t2_read_packet_header. PpmData is advanced (and PpmLen decremented)
	// as packet headers are consumed.
	Ppm     uint32 // ppm flag: there was a PPM marker
	PpmData []byte // ppm_data
	PpmLen  uint32 // ppm_len

	Tcps []TCP // tcps: per-tile coding parameters

	MEnc EncodingParam // m_specific_param.m_enc
	MDec DecodingParam // m_specific_param.m_dec

	// Strict ports cp->strict: OPJ_TRUE if the entire bit stream must be
	// decoded, OPJ_FALSE if partial bitstream decoding is allowed.
	Strict bool

	// ---- j2k decode-side marker bookkeeping ----

	// Comment ports cp->comment (COM marker payload).
	Comment string
	// PPM marker assembly (opj_j2k_read_ppm / merge_ppm).
	PpmMarkers      []Ppx
	PpmMarkersCount uint32
	PpmBuffer       []byte
	// MIsDecoder ports m_is_decoder.
	MIsDecoder bool
	// AllowDifferentBitDepthSign ports allow_different_bit_depth_sign.
	AllowDifferentBitDepthSign bool
}
