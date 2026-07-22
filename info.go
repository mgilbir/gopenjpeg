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
func ReadInfo(rd io.Reader, opts ...Option) (*Info, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	stream, magic, cleanup, err := openStream(rd)
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
func fillImageInfo(info *Info, img *image.Image, cp *cparams.CP) {
	info.X0, info.Y0, info.X1, info.Y1 = img.X0, img.Y0, img.X1, img.Y1
	info.ColorSpace = ColorSpace(img.ColorSpace)
	if info.ICCLen == 0 {
		info.ICCLen = img.ICCProfileLen
	}
	info.Components = make([]ComponentInfo, img.Numcomps)
	for i := range info.Components {
		c := &img.Comps[i]
		info.Components[i] = ComponentInfo{Dx: c.Dx, Dy: c.Dy, Prec: c.Prec, Sgnd: c.Sgnd != 0}
	}
	info.TileX0, info.TileY0 = cp.Tx0, cp.Ty0
	info.TileWidth, info.TileHeight = cp.Tdx, cp.Tdy
	info.NumTilesX, info.NumTilesY = cp.Tw, cp.Th
}
