package jp2

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// stubCodec is a test double for CodestreamCodec. It records the calls the JP2
// layer makes on its embedded codec and returns canned results, so the JP2
// container can be exercised without the (not-yet-ported) j2k codec. It is the
// test seam described in the worker brief: ReadHeader returns a minimal image so
// the colour-space/ICC transfer path in JP2.ReadHeader runs, and DecodeReturn
// lets a test drive the post-decode colour application.
type stubCodec struct {
	ihdrW, ihdrH          uint32
	allowDiffBitDepthSign bool
	numCompsToDecode      uint32

	// readHeaderImage is returned by ReadHeader (nil => mimic *p_image left nil).
	readHeaderImage *image.Image
	readHeaderErr   error

	// decodeImage, if non-nil, is copied into the caller's image by Decode.
	decodeErr error

	destroyed bool
}

func (s *stubCodec) SetupDecoder(*DecoderParams)          {}
func (s *stubCodec) SetDecoderStrictMode(bool)            {}
func (s *stubCodec) SetThreads(uint32) error              { return nil }
func (s *stubCodec) NumCompsToDecode() uint32             { return s.numCompsToDecode }
func (s *stubCodec) SetAllowDifferentBitDepthSign(a bool) { s.allowDiffBitDepthSign = a }
func (s *stubCodec) SetIHDRDimensions(w, h uint32)        { s.ihdrW, s.ihdrH = w, h }
func (s *stubCodec) Destroy()                             { s.destroyed = true }

func (s *stubCodec) ReadHeader(*cio.Stream, *event.Manager) (*image.Image, error) {
	return s.readHeaderImage, s.readHeaderErr
}

func (s *stubCodec) Decode(_ *cio.Stream, _ *image.Image, _ *event.Manager) error {
	return s.decodeErr
}

func (s *stubCodec) EndDecompress(*cio.Stream, *event.Manager) error { return nil }

func (s *stubCodec) SetDecodeArea(*image.Image, int32, int32, int32, int32, *event.Manager) error {
	return nil
}

func (s *stubCodec) SetDecodedComponents([]uint32, *event.Manager) error             { return nil }
func (s *stubCodec) SetDecodedResolutionFactor(uint32, *event.Manager) error         { return nil }
func (s *stubCodec) GetTile(*cio.Stream, *image.Image, uint32, *event.Manager) error { return nil }

func (s *stubCodec) ReadTileHeader(*cio.Stream, *event.Manager) (TileHeader, error) {
	return TileHeader{}, nil
}

func (s *stubCodec) DecodeTile(uint32, []byte, *cio.Stream, *event.Manager) error { return nil }

func (s *stubCodec) SetupEncoder(*EncoderParams, *image.Image, *event.Manager) error { return nil }
func (s *stubCodec) StartCompress(*cio.Stream, *image.Image, *event.Manager) error   { return nil }
func (s *stubCodec) Encode(*cio.Stream, *event.Manager) error                        { return nil }
func (s *stubCodec) EndCompress(*cio.Stream, *event.Manager) error                   { return nil }
func (s *stubCodec) WriteTile(uint32, []byte, *cio.Stream, *event.Manager) error     { return nil }
func (s *stubCodec) EncoderSetExtraOptions([]string, *event.Manager) error           { return nil }

// newTestJP2 builds a decoder JP2 wired to a fresh stubCodec.
func newTestJP2() (*JP2, *stubCodec) {
	sc := &stubCodec{}
	return Create(sc, true), sc
}
