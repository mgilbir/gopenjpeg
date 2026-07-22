package gopenjpeg

import (
	"fmt"
	stdimage "image"

	"github.com/mgilbir/gopenjpeg/internal/image"
)

// ColorSpace identifies the colour space of a decoded Image. Its values mirror
// OpenJPEG's OPJ_COLOR_SPACE enumeration.
type ColorSpace int32

// Colour space values.
const (
	ColorSpaceUnknown     ColorSpace = ColorSpace(image.ClrspcUnknown)     // not supported / not recognised
	ColorSpaceUnspecified ColorSpace = ColorSpace(image.ClrspcUnspecified) // not specified in the codestream
	ColorSpaceSRGB        ColorSpace = ColorSpace(image.ClrspcSRGB)        // sRGB
	ColorSpaceGray        ColorSpace = ColorSpace(image.ClrspcGray)        // greyscale
	ColorSpaceSYCC        ColorSpace = ColorSpace(image.ClrspcSYCC)        // YCbCr
	ColorSpaceEYCC        ColorSpace = ColorSpace(image.ClrspcEYCC)        // e-YCC
	ColorSpaceCMYK        ColorSpace = ColorSpace(image.ClrspcCMYK)        // CMYK
)

// String renders a ColorSpace as its short name.
func (c ColorSpace) String() string {
	switch c {
	case ColorSpaceUnspecified:
		return "unspecified"
	case ColorSpaceSRGB:
		return "sRGB"
	case ColorSpaceGray:
		return "grayscale"
	case ColorSpaceSYCC:
		return "sYCC"
	case ColorSpaceEYCC:
		return "eYCC"
	case ColorSpaceCMYK:
		return "CMYK"
	default:
		return "unknown"
	}
}

// Component is a read-only view of one decoded image component. The Data slice
// is the component's raw samples in raster order (row-major, W*H entries), not
// copied: callers must not retain it beyond the parent Image's lifetime and
// must not mutate it.
type Component struct {
	Dx    uint32 // horizontal sub-sampling factor w.r.t. the reference grid
	Dy    uint32 // vertical sub-sampling factor
	W     uint32 // component width in samples
	H     uint32 // component height in samples
	X0    uint32 // horizontal offset of the component
	Y0    uint32 // vertical offset of the component
	Prec  uint32 // bit precision per sample
	Sgnd  bool   // samples are signed
	Alpha uint16 // non-zero if this is an alpha (opacity) channel
	Data  []int32
}

// Image is a decoded JPEG 2000 image. It preserves full fidelity: per-component
// geometry, sub-sampling, precision, signedness, colour space and any ICC
// profile. Convert it to a Go standard-library image with ToStandard.
type Image struct {
	img *image.Image
}

// newImage wraps an internal image.Image.
func newImage(img *image.Image) *Image { return &Image{img: img} }

// internal returns the wrapped internal image, for the encode path.
func (im *Image) internal() *image.Image { return im.img }

// NewImage builds an Image for encoding from the reference-grid rectangle
// [x0,y0)-(x1,y1), colour space cs and per-component sample data. Each
// Component's Data slice must hold W*H samples in raster order. The returned
// Image can be passed to Encode. It is the encode-side counterpart of the
// decode Component accessors.
func NewImage(cs ColorSpace, x0, y0, x1, y1 uint32, comps []Component) *Image {
	img := &image.Image{
		X0: x0, Y0: y0, X1: x1, Y1: y1,
		Numcomps:   uint32(len(comps)),
		ColorSpace: image.ColorSpace(cs),
		Comps:      make([]image.Comp, len(comps)),
	}
	for i, c := range comps {
		var sgnd uint32
		if c.Sgnd {
			sgnd = 1
		}
		img.Comps[i] = image.Comp{
			Dx: c.Dx, Dy: c.Dy, W: c.W, H: c.H, X0: c.X0, Y0: c.Y0,
			Prec: c.Prec, Sgnd: sgnd, Alpha: c.Alpha, Data: c.Data,
		}
	}
	return newImage(img)
}

// SetICCProfile attaches an ICC profile to the image (used for JP2 colr meth=2
// encoding). A nil or empty slice clears any existing profile.
func (im *Image) SetICCProfile(profile []byte) {
	if len(profile) == 0 {
		im.img.ICCProfileBuf = nil
		im.img.ICCProfileLen = 0
		return
	}
	im.img.ICCProfileBuf = append([]byte(nil), profile...)
	im.img.ICCProfileLen = uint32(len(profile))
}

// NumComponents returns the number of image components.
func (im *Image) NumComponents() int { return int(im.img.Numcomps) }

// Component returns a read-only view of component i. It panics only on an
// out-of-range index, which is a programming error; callers should bound i by
// NumComponents.
func (im *Image) Component(i int) Component {
	c := &im.img.Comps[i]
	return Component{
		Dx:    c.Dx,
		Dy:    c.Dy,
		W:     c.W,
		H:     c.H,
		X0:    c.X0,
		Y0:    c.Y0,
		Prec:  c.Prec,
		Sgnd:  c.Sgnd != 0,
		Alpha: c.Alpha,
		Data:  c.Data,
	}
}

// ColorSpace returns the image colour space.
func (im *Image) ColorSpace() ColorSpace { return ColorSpace(im.img.ColorSpace) }

// Bounds returns the image reference-grid rectangle (x0, y0, x1, y1).
func (im *Image) Bounds() (x0, y0, x1, y1 uint32) {
	return im.img.X0, im.img.Y0, im.img.X1, im.img.Y1
}

// ICCProfile returns the embedded ICC profile bytes, or nil if none. The
// returned slice is not copied.
func (im *Image) ICCProfile() []byte {
	if im.img.ICCProfileLen == 0 {
		return nil
	}
	return im.img.ICCProfileBuf[:im.img.ICCProfileLen]
}

// ToStandard converts the image to a Go standard-library image.Image. It
// supports the common shapes produced by JPEG 2000 decoding:
//
//   - 1 component  -> image.Gray (prec<=8) or image.Gray16 (prec<=16)
//   - 3 components -> image.NRGBA (prec<=8) or image.NRGBA64 (prec<=16)
//   - 4 components -> image.NRGBA / image.NRGBA64 (4th treated as alpha)
//
// Samples are sign-adjusted (signed components are shifted into unsigned range)
// and left-scaled from their native precision to 8 or 16 bits. Conversion is
// therefore lossy whenever the native precision is not exactly 8 or 16 bits.
//
// It returns an error for shapes it cannot faithfully render as a standard
// image: components with differing dimensions (e.g. chroma sub-sampling — call
// a colour/upsample conversion first), precision above 16 bits, or an
// unsupported component count. Use the Component accessors for full-fidelity
// access in those cases.
func (im *Image) ToStandard() (stdimage.Image, error) {
	nc := im.NumComponents()
	if nc == 0 {
		return nil, fmt.Errorf("gopenjpeg: image has no components")
	}
	c0 := im.Component(0)
	for i := 1; i < nc; i++ {
		ci := im.Component(i)
		if ci.W != c0.W || ci.H != c0.H {
			return nil, fmt.Errorf("gopenjpeg: ToStandard: components have differing dimensions "+
				"(comp0=%dx%d comp%d=%dx%d); apply colour/upsample conversion first",
				c0.W, c0.H, i, ci.W, ci.H)
		}
	}
	if c0.Prec > 16 {
		return nil, fmt.Errorf("gopenjpeg: ToStandard: precision %d bits exceeds 16", c0.Prec)
	}
	w, h := int(c0.W), int(c0.H)
	rect := stdimage.Rect(0, 0, w, h)
	wide := c0.Prec > 8

	// sample reads one adjusted, scaled sample from component c at index k.
	sample8 := func(c Component, k int) uint8 {
		v := c.Data[k]
		if c.Sgnd {
			v += 1 << (c.Prec - 1)
		}
		if c.Prec < 8 {
			v <<= (8 - c.Prec)
		} else if c.Prec > 8 {
			v >>= (c.Prec - 8)
		}
		if v < 0 {
			v = 0
		} else if v > 0xff {
			v = 0xff
		}
		return uint8(v)
	}
	sample16 := func(c Component, k int) uint16 {
		v := c.Data[k]
		if c.Sgnd {
			v += 1 << (c.Prec - 1)
		}
		if c.Prec < 16 {
			v <<= (16 - c.Prec)
		}
		if v < 0 {
			v = 0
		} else if v > 0xffff {
			v = 0xffff
		}
		return uint16(v)
	}

	switch nc {
	case 1:
		if wide {
			dst := stdimage.NewGray16(rect)
			for k := 0; k < w*h; k++ {
				v := sample16(c0, k)
				dst.Pix[2*k] = byte(v >> 8)
				dst.Pix[2*k+1] = byte(v)
			}
			return dst, nil
		}
		dst := stdimage.NewGray(rect)
		for k := 0; k < w*h; k++ {
			dst.Pix[k] = sample8(c0, k)
		}
		return dst, nil
	case 3, 4:
		c1, c2 := im.Component(1), im.Component(2)
		var ca Component
		hasAlpha := nc == 4
		if hasAlpha {
			ca = im.Component(3)
		}
		if wide {
			dst := stdimage.NewNRGBA64(rect)
			for k := 0; k < w*h; k++ {
				o := k * 8
				r, g, b := sample16(c0, k), sample16(c1, k), sample16(c2, k)
				a := uint16(0xffff)
				if hasAlpha {
					a = sample16(ca, k)
				}
				dst.Pix[o+0], dst.Pix[o+1] = byte(r>>8), byte(r)
				dst.Pix[o+2], dst.Pix[o+3] = byte(g>>8), byte(g)
				dst.Pix[o+4], dst.Pix[o+5] = byte(b>>8), byte(b)
				dst.Pix[o+6], dst.Pix[o+7] = byte(a>>8), byte(a)
			}
			return dst, nil
		}
		dst := stdimage.NewNRGBA(rect)
		for k := 0; k < w*h; k++ {
			o := k * 4
			dst.Pix[o+0] = sample8(c0, k)
			dst.Pix[o+1] = sample8(c1, k)
			dst.Pix[o+2] = sample8(c2, k)
			if hasAlpha {
				dst.Pix[o+3] = sample8(ca, k)
			} else {
				dst.Pix[o+3] = 0xff
			}
		}
		return dst, nil
	default:
		return nil, fmt.Errorf("gopenjpeg: ToStandard: unsupported component count %d", nc)
	}
}
