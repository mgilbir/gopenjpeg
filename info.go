package gopenjpeg

import (
	"fmt"
	"io"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
	"github.com/mgilbir/gopenjpeg/internal/jp2"
)

// ReadInfo reads only the header of a JPEG 2000 image and returns its structural
// information without decoding sample data. It is used by the gopj-dump command
// and by callers that need geometry, precision and colour metadata cheaply.
//
// If rd is an io.ReadSeeker it is read on demand and never fully buffered. Any
// other io.Reader is read entirely into memory before format detection; use
// WithMaxInputSize to bound that buffer for untrusted, non-seekable inputs.
//
// # Which options apply
//
// ReadInfo accepts the same Option values as Decode, but only those that affect
// header parsing change anything (C54):
//
//   - WithFormat, WithMaxInputSize, WithStrictMode, WithWarningHandler,
//     WithErrorHandler and WithInfoHandler are honoured exactly as in Decode.
//   - WithReduce is forwarded to the decoder and is validated against the
//     codestream: a reduce level at or above a component's resolution count
//     makes ReadInfo fail, just as it makes Decode fail. It does not change the
//     reported geometry — Info always describes the full reference grid.
//   - WithLayers is forwarded but has no observable effect: layer truncation
//     happens at tile-decode time.
//   - WithDecodeArea, WithComponents, WithTile and WithConcurrency are accepted
//     and ignored. They govern sample decoding, which ReadInfo never performs.
func ReadInfo(rd io.Reader, opts ...Option) (*Info, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	stream, magic, cleanup, err := openStream(rd, o.maxInputSize)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	format := o.format
	if format == FormatAuto {
		format = detectFormat(magic)
		if format == FormatAuto {
			return nil, ErrUnknownFormat
		}
	}
	mgr := o.manager()

	info := &Info{Format: format}
	switch format {
	case FormatJP2:
		adapter := newJ2KAdapter(o.reduce, o.layers)
		container := jp2.Create(adapter, true)
		container.SetupDecoder(&jp2.DecoderParams{})
		container.SetDecoderStrictMode(o.strict)
		img, err := container.ReadHeader(stream, mgr)
		if err != nil {
			return nil, fmt.Errorf("gopenjpeg: read header: %w", err)
		}
		fillImageInfo(info, img, &adapter.d.CP)
		info.IsJP2 = true
		info.Brand = container.Brand()
		info.Meth = container.Meth()
		info.EnumCS = container.EnumCS()
		info.ICCLen = container.ICCProfileLen()
		if col := container.Color(); col != nil {
			if col.Pclr != nil {
				info.HasPalette = true
				info.PaletteChans = int(col.Pclr.NrChannels)
			}
			if col.Cdef != nil {
				info.CdefChannels = int(col.Cdef.N)
			}
		}
	case FormatJ2K:
		d := j2k.CreateDecompress()
		d.SetupDecoder(o.reduce, o.layers)
		d.SetStrictMode(o.strict)
		img, err := d.ReadHeader(stream, mgr)
		if err != nil {
			return nil, fmt.Errorf("gopenjpeg: read header: %w", err)
		}
		fillImageInfo(info, img, &d.CP)
	default:
		return nil, ErrUnknownFormat
	}
	return info, nil
}

// fillImageInfo copies the image and tile-grid geometry into an Info.
//
// It is always called on a freshly built Info, before the JP2 branch overwrites
// ICCLen with the authoritative colr-box length, so the ICC length is assigned
// unconditionally (C54: the former "if info.ICCLen == 0" guard could never be
// false and merely suggested an ordering constraint that does not exist).
func fillImageInfo(info *Info, img *image.Image, cp *cparams.CP) {
	info.X0, info.Y0, info.X1, info.Y1 = img.X0, img.Y0, img.X1, img.Y1
	info.ColorSpace = ColorSpace(img.ColorSpace)
	info.ICCLen = img.ICCProfileLen
	info.Components = make([]ComponentInfo, img.Numcomps)
	for i := range info.Components {
		c := &img.Comps[i]
		info.Components[i] = ComponentInfo{Dx: c.Dx, Dy: c.Dy, Prec: c.Prec, Sgnd: c.Sgnd != 0}
	}
	info.TileX0, info.TileY0 = cp.Tx0, cp.Ty0
	info.TileWidth, info.TileHeight = cp.Tdx, cp.Tdy
	info.NumTilesX, info.NumTilesY = cp.Tw, cp.Th
}
