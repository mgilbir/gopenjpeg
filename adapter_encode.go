package gopenjpeg

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
	"github.com/mgilbir/gopenjpeg/internal/jp2"
)

// j2kEncodeAdapter satisfies jp2.CodestreamCodec by delegating to a
// *j2k.Encoder. It is the encode-side counterpart of j2kAdapter: the JP2
// container (internal/jp2) drives the codestream encoder through this interface.
//
// The j2k CParameters (the full opj_cparameters_t equivalent) are not part of
// jp2.EncoderParams (which models only the JP2-specific jpip_on flag), so they
// are held on the adapter and applied in SetupEncoder, mirroring how the C
// public API forwards the full parameter set to opj_j2k_setup_encoder.
//
// Only the encode methods are implemented; the decode methods return
// ErrNotImplemented and the decode-side field accessors are inert.
type j2kEncodeAdapter struct {
	e      *j2k.Encoder
	params *j2k.CParameters
	extra  []string
}

// newJ2KEncodeAdapter builds an adapter over a fresh compressor.
func newJ2KEncodeAdapter(params *j2k.CParameters, extra []string) *j2kEncodeAdapter {
	return &j2kEncodeAdapter{e: j2k.CreateCompress(), params: params, extra: extra}
}

func (a *j2kEncodeAdapter) SetupEncoder(params *jp2.EncoderParams, img *image.Image, mgr *event.Manager) error {
	if err := a.e.SetupEncoder(a.params, img, mgr); err != nil {
		return err
	}
	if len(a.extra) > 0 {
		if err := a.e.EncoderSetExtraOptions(a.extra, mgr); err != nil {
			return err
		}
	}
	return nil
}

func (a *j2kEncodeAdapter) StartCompress(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	return a.e.StartCompress(stream, img, mgr)
}

func (a *j2kEncodeAdapter) Encode(stream *cio.Stream, mgr *event.Manager) error {
	return a.e.Encode(stream, mgr)
}

func (a *j2kEncodeAdapter) EndCompress(stream *cio.Stream, mgr *event.Manager) error {
	return a.e.EndCompress(stream, mgr)
}

func (a *j2kEncodeAdapter) WriteTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error {
	return ErrNotImplemented
}

func (a *j2kEncodeAdapter) EncoderSetExtraOptions(options []string, mgr *event.Manager) error {
	return a.e.EncoderSetExtraOptions(options, mgr)
}

// --- decode methods: not used on the encode path ---

func (a *j2kEncodeAdapter) SetupDecoder(params *jp2.DecoderParams) {}
func (a *j2kEncodeAdapter) SetDecoderStrictMode(strict bool)       {}
func (a *j2kEncodeAdapter) SetThreads(numThreads uint32) error     { return nil }
func (a *j2kEncodeAdapter) ReadHeader(stream *cio.Stream, mgr *event.Manager) (*image.Image, error) {
	return nil, ErrNotImplemented
}
func (a *j2kEncodeAdapter) Decode(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) EndDecompress(stream *cio.Stream, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) SetDecodeArea(img *image.Image, startX, startY, endX, endY int32, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) SetDecodedComponents(comps []uint32, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) SetDecodedResolutionFactor(resFactor uint32, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) GetTile(stream *cio.Stream, img *image.Image, tileIndex uint32, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) ReadTileHeader(stream *cio.Stream, mgr *event.Manager) (jp2.TileHeader, error) {
	return jp2.TileHeader{}, ErrNotImplemented
}
func (a *j2kEncodeAdapter) DecodeTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error {
	return ErrNotImplemented
}
func (a *j2kEncodeAdapter) SetAllowDifferentBitDepthSign(allow bool) {}
func (a *j2kEncodeAdapter) SetIHDRDimensions(w, h uint32)            {}
func (a *j2kEncodeAdapter) NumCompsToDecode() uint32                 { return 0 }
func (a *j2kEncodeAdapter) Destroy()                                 {}

var _ jp2.CodestreamCodec = (*j2kEncodeAdapter)(nil)
