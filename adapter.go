package gopenjpeg

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
	"github.com/mgilbir/gopenjpeg/internal/jp2"
)

// j2kAdapter satisfies jp2.CodestreamCodec by delegating to a *j2k.Decoder. It
// is the decode-side wiring between the JP2 container port (internal/jp2) and
// the codestream decoder port (internal/j2k), which were developed against the
// abstract interface and are joined here.
//
// The reduce/layer decode parameters are not part of jp2.DecoderParams (which
// models only the JP2-specific flags jp2.c reads directly), so they are held on
// the adapter and applied to the codec in SetupDecoder, reproducing how the C
// public API forwards the full opj_dparameters_t to opj_j2k_setup_decoder.
//
// Only the decode methods are implemented. The tile-streaming methods
// (ReadTileHeader/DecodeTile) and every encode method return ErrNotImplemented:
// the decode paths this port exercises use whole-image decode or the
// get-decoded-tile entry point (GetTile), not the streaming tile API, and
// encoding is out of scope.
type j2kAdapter struct {
	d      *j2k.Decoder
	reduce uint32
	layers uint32
}

// newJ2KAdapter builds an adapter over a fresh decompressor.
func newJ2KAdapter(reduce, layers uint32) *j2kAdapter {
	return &j2kAdapter{d: j2k.CreateDecompress(), reduce: reduce, layers: layers}
}

func (a *j2kAdapter) SetupDecoder(params *jp2.DecoderParams) {
	a.d.SetupDecoder(a.reduce, a.layers)
}

func (a *j2kAdapter) SetDecoderStrictMode(strict bool) { a.d.SetStrictMode(strict) }

// SetThreads is a no-op: this port decodes sequentially. It returns nil, the
// success value the C opj_j2k_set_threads returns.
func (a *j2kAdapter) SetThreads(numThreads uint32) error { return nil }

func (a *j2kAdapter) ReadHeader(stream *cio.Stream, mgr *event.Manager) (*image.Image, error) {
	return a.d.ReadHeader(stream, mgr)
}

func (a *j2kAdapter) Decode(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	return a.d.Decode(stream, img, mgr)
}

func (a *j2kAdapter) EndDecompress(stream *cio.Stream, mgr *event.Manager) error {
	return a.d.EndDecompress()
}

func (a *j2kAdapter) SetDecodeArea(img *image.Image, startX, startY, endX, endY int32, mgr *event.Manager) error {
	return a.d.SetDecodeArea(img, startX, startY, endX, endY)
}

func (a *j2kAdapter) SetDecodedComponents(comps []uint32, mgr *event.Manager) error {
	return a.d.SetDecodedComponents(comps)
}

func (a *j2kAdapter) SetDecodedResolutionFactor(resFactor uint32, mgr *event.Manager) error {
	return a.d.SetDecodedResolutionFactor(resFactor)
}

func (a *j2kAdapter) GetTile(stream *cio.Stream, img *image.Image, tileIndex uint32, mgr *event.Manager) error {
	return a.d.GetTile(stream, img, tileIndex, mgr)
}

func (a *j2kAdapter) ReadTileHeader(stream *cio.Stream, mgr *event.Manager) (jp2.TileHeader, error) {
	return jp2.TileHeader{}, ErrNotImplemented
}

func (a *j2kAdapter) DecodeTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kAdapter) SetupEncoder(params *jp2.EncoderParams, img *image.Image, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kAdapter) StartCompress(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kAdapter) Encode(stream *cio.Stream, mgr *event.Manager) error { return ErrNotImplemented }

func (a *j2kAdapter) EndCompress(stream *cio.Stream, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kAdapter) WriteTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kAdapter) EncoderSetExtraOptions(options []string, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kAdapter) SetAllowDifferentBitDepthSign(allow bool) {
	a.d.SetAllowDifferentBitDepthSign(allow)
}

func (a *j2kAdapter) SetIHDRDimensions(w, h uint32) { a.d.SetIHDRDimensions(w, h) }

func (a *j2kAdapter) NumCompsToDecode() uint32 { return a.d.NumCompsToDecode() }

// Destroy is a no-op: the wrapped decoder holds no resources that Go's garbage
// collector will not reclaim.
func (a *j2kAdapter) Destroy() {}

// verify the adapter satisfies the interface at compile time.
var _ jp2.CodestreamCodec = (*j2kAdapter)(nil)
