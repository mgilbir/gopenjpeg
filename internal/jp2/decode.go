package jp2

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// SetupDecoder ports opj_jp2_setup_decoder: forward the parameters to the codec
// and initialise the JP2-specific decode state (reset the colr flag and record
// the ignore-pclr/cmap/cdef request).
func (jp2 *JP2) SetupDecoder(params *DecoderParams) {
	jp2.codec.SetupDecoder(params)
	jp2.color.JP2HasColr = 0
	jp2.ignorePclrCmapCdef = params.Flags&IgnorePclrCmapCdefFlag != 0
}

// SetDecoderStrictMode ports opj_jp2_decoder_set_strict_mode.
func (jp2 *JP2) SetDecoderStrictMode(strict bool) {
	jp2.codec.SetDecoderStrictMode(strict)
}

// SetThreads ports opj_jp2_set_threads.
func (jp2 *JP2) SetThreads(numThreads uint32) error {
	return jp2.codec.SetThreads(numThreads)
}

// Decode ports opj_jp2_decode: run the codestream decode and then apply the JP2
// colour post-processing. A nil image is rejected, matching the C guard.
func (jp2 *JP2) Decode(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	if img == nil {
		return ErrRead
	}

	if err := jp2.codec.Decode(stream, img, mgr); err != nil {
		mgr.Errorf("Failed to decode the codestream in the JP2 file\n")
		return err
	}

	if !jp2.applyColorPostprocessing(img, mgr) {
		return ErrRead
	}
	return nil
}

// EndDecompress ports opj_jp2_end_decompress: read any boxes after the
// codestream (opj_jp2_setup_end_header_reading installs the same
// readHeaderProcedure) and then end the codestream decode.
func (jp2 *JP2) EndDecompress(stream *cio.Stream, mgr *event.Manager) error {
	// setup_end_header_reading -> [readHeaderProcedure]
	if !jp2.readHeaderProcedure(stream, mgr) {
		return ErrRead
	}
	return jp2.codec.EndDecompress(stream, mgr)
}

// SetDecodeArea ports opj_jp2_set_decode_area.
func (jp2 *JP2) SetDecodeArea(img *image.Image, startX, startY, endX, endY int32, mgr *event.Manager) error {
	return jp2.codec.SetDecodeArea(img, startX, startY, endX, endY, mgr)
}

// SetDecodedComponents ports opj_jp2_set_decoded_components.
func (jp2 *JP2) SetDecodedComponents(comps []uint32, mgr *event.Manager) error {
	return jp2.codec.SetDecodedComponents(comps, mgr)
}

// SetDecodedResolutionFactor ports opj_jp2_set_decoded_resolution_factor.
func (jp2 *JP2) SetDecodedResolutionFactor(resFactor uint32, mgr *event.Manager) error {
	return jp2.codec.SetDecodedResolutionFactor(resFactor, mgr)
}

// ReadTileHeader ports opj_jp2_read_tile_header: forwarded unchanged to the
// codec.
func (jp2 *JP2) ReadTileHeader(stream *cio.Stream, mgr *event.Manager) (TileHeader, error) {
	return jp2.codec.ReadTileHeader(stream, mgr)
}

// DecodeTile ports opj_jp2_decode_tile: forwarded unchanged to the codec.
func (jp2 *JP2) DecodeTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error {
	return jp2.codec.DecodeTile(tileIndex, data, stream, mgr)
}

// GetTile ports opj_jp2_get_tile: decode a single tile into img then apply the
// JP2 colour post-processing. As in C, it warns that trailing JP2 boxes are not
// read by this path.
func (jp2 *JP2) GetTile(stream *cio.Stream, img *image.Image, tileIndex uint32, mgr *event.Manager) error {
	if img == nil {
		return ErrRead
	}

	mgr.Warnf("JP2 box which are after the codestream will not be read by this function.\n")

	if err := jp2.codec.GetTile(stream, img, tileIndex, mgr); err != nil {
		mgr.Errorf("Failed to decode the codestream in the JP2 file\n")
		return err
	}

	if !jp2.applyColorPostprocessing(img, mgr) {
		return ErrRead
	}
	return nil
}
