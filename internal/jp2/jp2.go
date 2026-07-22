// Package jp2 is a faithful pure-Go port of jp2.c / jp2.h from OpenJPEG: the
// JP2 (JPEG 2000 Part 1) file-format container layer. It parses and writes the
// JP2 box structure (signature, ftyp, jp2h super-box with ihdr/bpcc/colr/pclr/
// cmap/cdef, and the jp2c codestream box) around an embedded codestream codec.
//
// In C, opj_jp2_t embeds a pointer to an opj_j2k_t and delegates all codestream
// work to it. Here the embedded codec is abstracted behind the CodestreamCodec
// interface (see codec.go) so the jp2 layer can be built and tested before the
// j2k package lands. Every exported and internal item documents the C symbol it
// ports.
//
// The C code models its read/write flows with opj_procedure_list (a list of
// function pointers executed in order). This port replaces those lists with
// direct, ordered method calls, preserving the exact sequence and short-circuit
// semantics; the comment on each flow names the procedures it stands in for.
package jp2

// Box type signatures, ports of the JP2_* defines in jp2.h.
const (
	boxJP   uint32 = 0x6a502020 // JP2_JP:   JPEG 2000 signature box ("jP  ")
	boxFTYP uint32 = 0x66747970 // JP2_FTYP: File type box ("ftyp")
	boxJP2H uint32 = 0x6a703268 // JP2_JP2H: JP2 header super-box ("jp2h")
	boxIHDR uint32 = 0x69686472 // JP2_IHDR: Image header box ("ihdr")
	boxCOLR uint32 = 0x636f6c72 // JP2_COLR: Colour specification box ("colr")
	boxJP2C uint32 = 0x6a703263 // JP2_JP2C: Contiguous codestream box ("jp2c")
	boxURL  uint32 = 0x75726c20 // JP2_URL:  Data entry URL box ("url ")
	boxPCLR uint32 = 0x70636c72 // JP2_PCLR: Palette box ("pclr")
	boxCMAP uint32 = 0x636d6170 // JP2_CMAP: Component mapping box ("cmap")
	boxCDEF uint32 = 0x63646566 // JP2_CDEF: Channel definition box ("cdef")
	boxDTBL uint32 = 0x6474626c // JP2_DTBL: Data reference box ("dtbl")
	boxBPCC uint32 = 0x62706363 // JP2_BPCC: Bits per component box ("bpcc")
	boxJP2  uint32 = 0x6a703220 // JP2_JP2:  File type brand/compat field ("jp2 ")
)

// jp2Magic is the 4-byte payload of the signature box (0x0d0a870a), the value
// checked in opj_jp2_read_jp and written by opj_jp2_write_jp.
const jp2Magic uint32 = 0x0d0a870a

// boxSize ports the OPJ_BOX_SIZE define: the initial scratch buffer size used
// by opj_jp2_read_header_procedure.
const boxSize = 1024

// State ports the JP2_STATE enum: the file-level parse state bitmask.
type State uint32

// JP2_STATE_* values.
const (
	stateNone          State = 0x0        // JP2_STATE_NONE
	stateSignature     State = 0x1        // JP2_STATE_SIGNATURE
	stateFileType      State = 0x2        // JP2_STATE_FILE_TYPE
	stateHeader        State = 0x4        // JP2_STATE_HEADER
	stateCodestream    State = 0x8        // JP2_STATE_CODESTREAM
	stateEndCodestream State = 0x10       // JP2_STATE_END_CODESTREAM
	stateUnknown       State = 0x7fffffff // JP2_STATE_UNKNOWN
)

// ImgState ports the JP2_IMG_STATE enum: the jp2h super-box parse state.
type ImgState uint32

// JP2_IMG_STATE_* values.
const (
	imgStateNone    ImgState = 0x0        // JP2_IMG_STATE_NONE
	imgStateUnknown ImgState = 0x7fffffff // JP2_IMG_STATE_UNKNOWN
)

// CdefInfo ports opj_jp2_cdef_info_t: a single channel description.
type CdefInfo struct {
	Cn   uint16 // channel index
	Typ  uint16 // channel type (0=color, 1=opacity, 2=premultiplied opacity, 65535=unknown)
	Asoc uint16 // association (color index; 0 or 65535 = whole image)
}

// Cdef ports opj_jp2_cdef_t: the channel definition table.
type Cdef struct {
	Info []CdefInfo // N entries
	N    uint16     // number of entries
}

// CmapComp ports opj_jp2_cmap_comp_t: a single component-mapping record.
type CmapComp struct {
	Cmp  uint16 // component index (CMP^i)
	Mtyp byte   // mapping type (MTYP^i): 0 = direct use, 1 = palette mapping
	Pcol byte   // palette column (PCOL^i)
}

// Pclr ports opj_jp2_pclr_t: palette data and its component mapping.
type Pclr struct {
	Entries     []uint32   // nr_entries*nr_channels flattened palette table
	ChannelSign []byte     // per-channel sign flag
	ChannelSize []byte     // per-channel bit depth (1..32)
	Cmap        []CmapComp // component mapping (nr_channels entries), nil until cmap box read
	NrEntries   uint16     // number of palette entries (NE)
	NrChannels  byte       // number of palette columns (NPC)
}

// Color ports opj_jp2_color_t: the collector for ICC profile, palette and
// channel-definition data gathered from the jp2h super-box.
type Color struct {
	ICCProfileBuf []byte // captured ICC profile (or packed CIELab parameters)
	ICCProfileLen uint32 // ICC profile length (0 for the CIELab special case)

	Cdef       *Cdef // channel definitions, nil if no cdef box
	Pclr       *Pclr // palette, nil if no pclr box
	JP2HasColr byte  // set once the first colr box is accepted
}

// Comps ports opj_jp2_comps_t: per-component depth/sign as seen by the JP2
// layer (from ihdr's uniform bpc or the bpcc box).
type Comps struct {
	Depth uint32 // bit depth (unused directly by jp2.c beyond bpcc, kept for parity)
	Sgnd  uint32 // sign flag (parity field)
	Bpcc  uint32 // packed depth-1 | (sign<<7)
}

// JP2 ports opj_jp2_t: the JPEG-2000 file-format reader/writer state.
type JP2 struct {
	// codec is the embedded codestream codec (ports the opj_j2k_t* member).
	codec CodestreamCodec

	w          uint32 // image width  (from ihdr WIDTH)
	h          uint32 // image height (from ihdr HEIGHT)
	numcomps   uint32 // number of components (from ihdr NC)
	bpc        uint32 // ihdr BPC (255 = variable, see bpcc box)
	c          uint32 // ihdr C (compression type; should be 7)
	unkC       uint32 // ihdr UnkC (colourspace-unknown flag)
	ipr        uint32 // ihdr IPR (intellectual-property flag)
	meth       uint32 // colr METH
	approx     uint32 // colr APPROX
	enumcs     uint32 // colr EnumCS
	precedence uint32 // colr PRECEDENCE

	brand      uint32   // ftyp BR
	minversion uint32   // ftyp MinV
	numcl      uint32   // ftyp compatibility-list length
	cl         []uint32 // ftyp compatibility list (CLi)

	comps []Comps // per-component depth/sign

	j2kCodestreamOffset int64 // OPJ_OFF_T: offset where the jp2c box header is written
	jpipIptrOffset      int64 // OPJ_OFF_T: offset reserved for the JPIP iptr box
	jpipOn              bool  // JPIP indexing enabled (encode)

	jp2State    State    // file-level parse state
	jp2ImgState ImgState // jp2h super-box parse state

	color Color // colr/pclr/cmap/cdef collector

	ignorePclrCmapCdef bool // OPJ_DPARAMETERS_IGNORE_PCLR_CMAP_CDEF_FLAG
	hasJp2h            bool // jp2h box seen
	hasIhdr            bool // ihdr box seen
}

// Create ports opj_jp2_create. It builds a JP2 codec wrapping the supplied
// codestream codec. In C the codec is created internally via
// opj_j2k_create_compress / opj_j2k_create_decompress; here the caller (the
// future public API layer) supplies the codec so the container and codestream
// layers stay decoupled. isDecoder is retained for parity and documentation of
// intent; the codec must already be created in the matching mode.
func Create(codec CodestreamCodec, isDecoder bool) *JP2 {
	_ = isDecoder
	jp2 := &JP2{codec: codec}
	// Color structure defaults (opj_jp2_create initialises these to NULL/0).
	jp2.color.ICCProfileBuf = nil
	jp2.color.ICCProfileLen = 0
	jp2.color.Cdef = nil
	jp2.color.Pclr = nil
	jp2.color.JP2HasColr = 0
	return jp2
}

// Destroy ports opj_jp2_destroy. It releases the embedded codec; the remaining
// C frees are handled by Go's garbage collector, so this only forwards the
// codec teardown and drops references.
func (jp2 *JP2) Destroy() {
	if jp2 == nil {
		return
	}
	if jp2.codec != nil {
		jp2.codec.Destroy()
		jp2.codec = nil
	}
	jp2.comps = nil
	jp2.cl = nil
	jp2.color.ICCProfileBuf = nil
	jp2.color.Cdef = nil
	jp2.color.Pclr = nil
}

// freePclr ports opj_jp2_free_pclr: drop the collected palette. In Go this is a
// simple nil-out; it is called on the "pclr without cmap" cleanup path.
func (color *Color) freePclr() {
	color.Pclr = nil
}

// Width returns the ihdr WIDTH (test/inspection accessor; no direct C analogue).
func (jp2 *JP2) Width() uint32 { return jp2.w }

// Height returns the ihdr HEIGHT.
func (jp2 *JP2) Height() uint32 { return jp2.h }

// NumComps returns the ihdr NC.
func (jp2 *JP2) NumComps() uint32 { return jp2.numcomps }

// Bpc returns the ihdr BPC.
func (jp2 *JP2) Bpc() uint32 { return jp2.bpc }

// Meth returns the colr METH.
func (jp2 *JP2) Meth() uint32 { return jp2.meth }

// EnumCS returns the colr EnumCS.
func (jp2 *JP2) EnumCS() uint32 { return jp2.enumcs }

// Approx returns the colr APPROX.
func (jp2 *JP2) Approx() uint32 { return jp2.approx }

// Precedence returns the colr PRECEDENCE.
func (jp2 *JP2) Precedence() uint32 { return jp2.precedence }

// Brand returns the ftyp BR.
func (jp2 *JP2) Brand() uint32 { return jp2.brand }

// ICCProfileLen returns the captured ICC profile length.
func (jp2 *JP2) ICCProfileLen() uint32 { return jp2.color.ICCProfileLen }

// ICCProfile returns the captured ICC profile bytes (nil if none).
func (jp2 *JP2) ICCProfile() []byte { return jp2.color.ICCProfileBuf }

// Color returns the collected colr/pclr/cdef data (inspection accessor).
func (jp2 *JP2) Color() *Color { return &jp2.color }

// CodestreamOffset returns the recorded offset of the jp2c box header
// (jp2->j2k_codestream_offset), set on decode by opj_jp2_skip_jp2c parity paths
// and on encode by opj_jp2_skip_jp2c.
func (jp2 *JP2) CodestreamOffset() int64 { return jp2.j2kCodestreamOffset }
