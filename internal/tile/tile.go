// Package tile holds the data-only tile data model ported from tcd.h. These are
// the opj_tcd_* structures that describe a tile as it is coded/decoded: tiles,
// tile-components, resolutions, sub-bands, precincts, code-blocks, segments,
// passes and layers. They are used by the tier-2 (t2) code now and by the tile
// coder/decoder (tcd) later, so every field of every struct is retained, each
// with its C name in a doc comment.
//
// Where tcd.h uses a union for the per-precinct code-block array
// (opj_tcd_precinct_t.cblks {enc, dec}), this port uses two separate slices
// (CblksEnc / CblksDec); only one is populated at a time, matching the union.
package tile

import "github.com/mgilbir/gopenjpeg/internal/tgt"

// CblkDataExtra ports OPJ_COMMON_CBLK_DATA_EXTRA: the margin (for a fake 0xFFFF
// marker) added to code-block data buffers.
const CblkDataExtra = 2

// Pass ports opj_tcd_pass_t: information about a single coding pass.
type Pass struct {
	Rate          uint32  // rate
	Distortiondec float64 // distortiondec
	Len           uint32  // len
	Term          bool    // term (OPJ_BITFIELD term:1)
}

// Layer ports opj_tcd_layer_t: information about a code-block's contribution to
// one quality layer.
type Layer struct {
	Numpasses uint32  // numpasses: number of passes in the layer
	Len       uint32  // len: length of information
	Disto     float64 // disto: distortion (for index)
	Data      []byte  // data
}

// CblkEnc ports opj_tcd_cblk_enc_t: a code-block for encoding.
type CblkEnc struct {
	Data              []byte  // data
	Layers            []Layer // layers: per-layer information
	Passes            []Pass  // passes: per-pass information
	X0                int32   // x0
	Y0                int32   // y0
	X1                int32   // x1
	Y1                int32   // y1
	Numbps            uint32  // numbps
	Numlenbits        uint32  // numlenbits
	DataSize          uint32  // data_size: size of allocated data buffer
	Numpasses         uint32  // numpasses: passes already done for the code-block
	Numpassesinlayers uint32  // numpassesinlayers: number of passes in the layer
	Totalpasses       uint32  // totalpasses
}

// SegDataChunk ports opj_tcd_seg_data_chunk_t: a chunk of codestream data that
// is part of a code block. The data slice points into the tile-part buffer (no
// copy is made), so that buffer must be kept alive while decoding.
type SegDataChunk struct {
	Data []byte // data: points into the tile-part buffer
	Len  uint32 // len: usable length of data
}

// Seg ports opj_tcd_seg_t: a segment of a code-block (a run of consecutive
// coding passes without MQC/RAW termination between them).
type Seg struct {
	Len           uint32 // len: size of data related to this segment
	Numpasses     uint32 // numpasses: number of passes decoded (including skipped)
	RealNumPasses uint32 // real_num_passes: passes actually to be decoded
	Maxpasses     uint32 // maxpasses: maximum number of passes for this segment
	Numnewpasses  uint32 // numnewpasses: new passes for current packet (transitory)
	Newlen        uint32 // newlen: codestream length for current packet (transitory)
}

// CblkDec ports opj_tcd_cblk_dec_t: a code-block for decoding.
type CblkDec struct {
	Segs   []Seg          // segs: segment information
	Chunks []SegDataChunk // chunks: array of chunks
	X0     int32          // x0
	Y0     int32          // y0
	X1     int32          // x1
	Y1     int32          // y1
	// Mb is the maximum number of bit-planes available for the representation of
	// coefficients in any sub-band (Equation E-2, Section B.10.5). Currently used
	// only to check if HT decoding is correct.
	Mb uint32 // Mb
	// Numbps is Mb - P (Section B.10.5).
	Numbps          uint32 // numbps
	Numlenbits      uint32 // numlenbits: number of bits for len (current packet, transitory)
	Numnewpasses    uint32 // numnewpasses: passes added for current packet (transitory)
	Numsegs         uint32 // numsegs: number of segments (including skipped)
	RealNumSegs     uint32 // real_num_segs: segments used for code-block decoding
	MCurrentMaxSegs uint32 // m_current_max_segs: allocated number of segs[] items
	Numchunks       uint32 // numchunks: number of valid chunks
	Numchunksalloc  uint32 // numchunksalloc: number of chunks items allocated
	// DecodedData is the decoded code-block; only used for subtile decoding.
	DecodedData []int32 // decoded_data
	Corrupted   bool    // corrupted: whether the code-block data is corrupted
}

// Precinct ports opj_tcd_precinct_t: a precinct.
type Precinct struct {
	X0 int32  // x0
	Y0 int32  // y0
	X1 int32  // x1
	Y1 int32  // y1
	Cw uint32 // cw: number of code-blocks in width
	Ch uint32 // ch: number of code-blocks in height

	// cblks union: only one of CblksEnc / CblksDec is populated at a time.
	CblksEnc []CblkEnc // cblks.enc
	CblksDec []CblkDec // cblks.dec

	BlockSize uint32 // block_size: size taken by cblks (bytes)

	Incltree *tgt.Tree // incltree: inclusion tag tree
	Imsbtree *tgt.Tree // imsbtree: IMSB tag tree
}

// Band ports opj_tcd_band_t: a sub-band.
type Band struct {
	X0 int32 // x0
	Y0 int32 // y0
	X1 int32 // x1
	Y1 int32 // y1
	// Bandno is 0=LL for the lowest resolution level, otherwise 1=HL, 2=LH, 3=HH.
	Bandno            uint32     // bandno
	Precincts         []Precinct // precincts
	PrecinctsDataSize uint32     // precincts_data_size: size of data taken by precincts
	Numbps            int32      // numbps
	Stepsize          float32    // stepsize
}

// Resolution ports opj_tcd_resolution_t: a tile-component resolution level.
type Resolution struct {
	X0 int32  // x0
	Y0 int32  // y0
	X1 int32  // x1
	Y1 int32  // y1
	Pw uint32 // pw: number of precincts in width
	Ph uint32 // ph: number of precincts in height
	// Numbands is 1 for the lowest resolution level, 3 otherwise.
	Numbands uint32  // numbands
	Bands    [3]Band // bands[3]

	// Window-of-interest dimensions (valid only if tcd->whole_tile_decoding).
	WinX0 uint32 // win_x0
	WinY0 uint32 // win_y0
	WinX1 uint32 // win_x1
	WinY1 uint32 // win_y1
}

// TileComp ports opj_tcd_tilecomp_t: a tile-component.
type TileComp struct {
	X0 int32 // x0
	Y0 int32 // y0
	X1 int32 // x1
	Y1 int32 // y1

	Compno                uint32       // compno
	Numresolutions        uint32       // numresolutions
	MinimumNumResolutions uint32       // minimum_num_resolutions: resolutions to decode (at max)
	Resolutions           []Resolution // resolutions
	ResolutionsSize       uint32       // resolutions_size (bytes)

	// Data is the component data. For decoding, only valid if
	// tcd->whole_tile_decoding is set (exclusive of DataWin).
	Data           []int32 // data
	OwnsData       bool    // ownsData: whether to free after usage
	DataSizeNeeded uint64  // data_size_needed
	DataSize       uint64  // data_size

	// DataWin is the component data limited to the window of interest. Only valid
	// for decoding when whole_tile_decoding is NOT set (exclusive of Data).
	DataWin []int32 // data_win
	WinX0   uint32  // win_x0
	WinY0   uint32  // win_y0
	WinX1   uint32  // win_x1
	WinY1   uint32  // win_y1

	Numpix uint64 // numpix: number of pixels (OPJ_SIZE_T)
}

// Tile ports opj_tcd_tile_t: a tile.
type Tile struct {
	X0 int32 // x0
	Y0 int32 // y0
	X1 int32 // x1
	Y1 int32 // y1

	Numcomps   uint32       // numcomps: number of components in tile
	Comps      []TileComp   // comps: component information
	Numpix     uint64       // numpix: number of pixels (OPJ_SIZE_T)
	Distotile  float64      // distotile: distortion of the tile
	Distolayer [100]float64 // distolayer[100]: distortion per layer
	Packno     uint32       // packno: packet number
}

// Image ports opj_tcd_image_t.
type Image struct {
	Tiles []Tile // tiles
}

// MarkerInfo ports opj_tcd_marker_info_t: information needed to generate PLT
// markers, produced by the encoder tier-2 pass.
type MarkerInfo struct {
	NeedPLT     bool     // need_PLT (IN)
	PacketCount uint32   // packet_count (OUT)
	PacketSize  []uint32 // p_packet_size (OUT), size packet_count
}

// IsBandEmpty ports opj_tcd_is_band_empty: a sub-band is empty when it has a
// null area.
func IsBandEmpty(band *Band) bool {
	return (band.X1-band.X0 == 0) || (band.Y1-band.Y0 == 0)
}

// ReinitSegment ports opj_tcd_reinit_segment: zero a segment.
func ReinitSegment(seg *Seg) {
	*seg = Seg{}
}
