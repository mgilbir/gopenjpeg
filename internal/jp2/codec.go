package jp2

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// IgnorePclrCmapCdefFlag ports OPJ_DPARAMETERS_IGNORE_PCLR_CMAP_CDEF_FLAG
// (openjpeg.h): when set in DecoderParams.Flags, the JP2 layer bypasses the
// pclr/cmap/cdef colour transforms in opj_jp2_apply_color_postprocessing.
const IgnorePclrCmapCdefFlag uint32 = 0x0001

// DecoderParams carries the subset of opj_dparameters_t that jp2.c reads
// directly. Every other decompression parameter is consumed by the codec's
// SetupDecoder (opj_j2k_setup_decoder), so it is not modelled here. This is a
// minimal local stand-in to be reconciled when the public API layer lands.
type DecoderParams struct {
	// Flags ports opj_dparameters_t.flags. Only IgnorePclrCmapCdefFlag is read
	// by jp2.c (opj_jp2_setup_decoder).
	Flags uint32
}

// EncoderParams carries the subset of opj_cparameters_t that jp2.c reads
// directly. Every other compression parameter is consumed by the codec's
// SetupEncoder (opj_j2k_setup_encoder). Minimal local stand-in, to be
// reconciled with the public API layer.
type EncoderParams struct {
	// JpipOn ports opj_cparameters_t.jpip_on, read by opj_jp2_setup_encoder to
	// decide whether the JPIP iptr box is reserved during header writing.
	JpipOn bool
}

// CodestreamCodec is the narrow interface the JP2 container invokes on its
// embedded codestream codec. It abstracts opj_j2k_t: every method documents the
// exact opj_j2k_* function (or embedded-struct field access) in jp2.c that it
// stands for. The future internal/j2k package implements this interface.
//
// Methods return an error where the C function returns OPJ_BOOL (nil == OPJ_TRUE);
// the event.Manager still carries the human-readable diagnostic, matching C.
type CodestreamCodec interface {
	// --- decode path ---

	// SetupDecoder ports opj_j2k_setup_decoder: apply decompression parameters
	// to the codec. The JP2-only fields of DecoderParams are handled by the
	// caller; the codec receives the full parameter set in the real API.
	SetupDecoder(params *DecoderParams)

	// SetDecoderStrictMode ports opj_j2k_decoder_set_strict_mode.
	SetDecoderStrictMode(strict bool)

	// SetThreads ports opj_j2k_set_threads. Returns an error where C returns
	// OPJ_FALSE.
	SetThreads(numThreads uint32) error

	// ReadHeader ports opj_j2k_read_header: read the codestream main header and
	// return the decoded image structure (the C out-parameter opj_image_t**).
	// A nil image with nil error is possible only if C would leave *p_image
	// NULL on success; callers guard for that.
	ReadHeader(stream *cio.Stream, mgr *event.Manager) (*image.Image, error)

	// Decode ports opj_j2k_decode: decode the whole image into img.
	Decode(stream *cio.Stream, img *image.Image, mgr *event.Manager) error

	// EndDecompress ports opj_j2k_end_decompress.
	EndDecompress(stream *cio.Stream, mgr *event.Manager) error

	// SetDecodeArea ports opj_j2k_set_decode_area.
	SetDecodeArea(img *image.Image, startX, startY, endX, endY int32, mgr *event.Manager) error

	// SetDecodedComponents ports opj_j2k_set_decoded_components.
	SetDecodedComponents(comps []uint32, mgr *event.Manager) error

	// SetDecodedResolutionFactor ports opj_j2k_set_decoded_resolution_factor.
	SetDecodedResolutionFactor(resFactor uint32, mgr *event.Manager) error

	// GetTile ports opj_j2k_get_tile.
	GetTile(stream *cio.Stream, img *image.Image, tileIndex uint32, mgr *event.Manager) error

	// ReadTileHeader ports opj_j2k_read_tile_header. It fills the tile geometry
	// out-parameters and reports (via goOn) whether decoding should continue.
	ReadTileHeader(stream *cio.Stream, mgr *event.Manager) (res TileHeader, err error)

	// DecodeTile ports opj_j2k_decode_tile.
	DecodeTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error

	// --- encode path ---

	// SetupEncoder ports opj_j2k_setup_encoder. Returns an error where C returns
	// OPJ_FALSE.
	SetupEncoder(params *EncoderParams, img *image.Image, mgr *event.Manager) error

	// StartCompress ports opj_j2k_start_compress.
	StartCompress(stream *cio.Stream, img *image.Image, mgr *event.Manager) error

	// Encode ports opj_j2k_encode.
	Encode(stream *cio.Stream, mgr *event.Manager) error

	// EndCompress ports opj_j2k_end_compress.
	EndCompress(stream *cio.Stream, mgr *event.Manager) error

	// WriteTile ports opj_j2k_write_tile.
	WriteTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error

	// EncoderSetExtraOptions ports opj_j2k_encoder_set_extra_options.
	EncoderSetExtraOptions(options []string, mgr *event.Manager) error

	// --- embedded-struct field accesses jp2.c performs directly on jp2->j2k ---

	// SetAllowDifferentBitDepthSign ports the assignment
	// jp2->j2k->m_cp.allow_different_bit_depth_sign = (jp2->bpc == 255) in
	// opj_jp2_read_ihdr.
	SetAllowDifferentBitDepthSign(allow bool)

	// SetIHDRDimensions ports the assignments jp2->j2k->ihdr_w = jp2->w and
	// jp2->j2k->ihdr_h = jp2->h in opj_jp2_read_ihdr.
	SetIHDRDimensions(w, h uint32)

	// NumCompsToDecode ports the read of
	// jp2->j2k->m_specific_param.m_decoder.m_numcomps_to_decode in
	// opj_jp2_apply_color_postprocessing (non-zero bypasses colour transforms).
	NumCompsToDecode() uint32

	// Destroy ports opj_j2k_destroy.
	Destroy()
}

// TileHeader carries the out-parameters of opj_j2k_read_tile_header
// (opj_jp2_read_tile_header forwards them unchanged).
type TileHeader struct {
	TileIndex uint32 // p_tile_index
	DataSize  uint32 // p_data_size
	TileX0    int32  // p_tile_x0
	TileY0    int32  // p_tile_y0
	TileX1    int32  // p_tile_x1
	TileY1    int32  // p_tile_y1
	NbComps   uint32 // p_nb_comps
	GoOn      bool   // p_go_on
}
