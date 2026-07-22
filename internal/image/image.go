// Package image implements the opj_image_t model of OpenJPEG: the image and
// per-component geometry (offsets, sub-sampling, precision, data) and the
// operations on them (creation, header copy, and the decode-time component
// geometry update). It is a faithful port of image.c / image.h and the
// opj_image_t / opj_image_comp_t types from openjpeg.h.
package image

import (
	"errors"
	"math"

	"github.com/mgilbir/gopenjpeg/internal/opjmath"
)

// ColorSpace is a port of OPJ_COLOR_SPACE.
type ColorSpace int32

// Color space values, ports of the COLOR_SPACE enum.
const (
	ClrspcUnknown     ColorSpace = -1 // OPJ_CLRSPC_UNKNOWN: not supported by the library
	ClrspcUnspecified ColorSpace = 0  // OPJ_CLRSPC_UNSPECIFIED: not specified in the codestream
	ClrspcSRGB        ColorSpace = 1  // OPJ_CLRSPC_SRGB
	ClrspcGray        ColorSpace = 2  // OPJ_CLRSPC_GRAY
	ClrspcSYCC        ColorSpace = 3  // OPJ_CLRSPC_SYCC: YUV
	ClrspcEYCC        ColorSpace = 4  // OPJ_CLRSPC_EYCC: e-YCC
	ClrspcCMYK        ColorSpace = 5  // OPJ_CLRSPC_CMYK
)

// ErrImageAlloc mirrors the NULL-return failure path of opj_image_create /
// opj_image_tile_create (allocation-size overflow or out-of-memory in C).
var ErrImageAlloc = errors.New("image: allocation size overflow")

// Comp is a port of opj_image_comp_t: the per-component geometry and data of an
// image.
type Comp struct {
	Dx           uint32  // XRsiz: horizontal sub-sampling of the component w.r.t. the reference grid
	Dy           uint32  // YRsiz: vertical sub-sampling of the component w.r.t. the reference grid
	W            uint32  // data width
	H            uint32  // data height
	X0           uint32  // x component offset compared to the whole image
	Y0           uint32  // y component offset compared to the whole image
	Prec         uint32  // precision: number of bits per component per pixel
	Sgnd         uint32  // signed (1) / unsigned (0)
	ResnoDecoded uint32  // number of decoded resolutions
	Factor       uint32  // number of division by 2 of the out image compared to the original size
	Data         []int32 // image component data
	Alpha        uint16  // alpha channel
}

// Image is a port of opj_image_t: the image data and characteristics.
type Image struct {
	X0            uint32     // XOsiz: horizontal offset from the reference grid origin to the image area
	Y0            uint32     // YOsiz: vertical offset from the reference grid origin to the image area
	X1            uint32     // Xsiz: width of the reference grid
	Y1            uint32     // Ysiz: height of the reference grid
	Numcomps      uint32     // number of components in the image
	ColorSpace    ColorSpace // color space: sRGB, greyscale or YUV
	Comps         []Comp     // image components
	ICCProfileBuf []byte     // 'restricted' ICC profile
	ICCProfileLen uint32     // size of ICC profile
}

// CompParm is a port of opj_image_cmptparm_t: the per-component parameters used
// to create an image.
type CompParm struct {
	Dx   uint32 // XRsiz: horizontal sub-sampling
	Dy   uint32 // YRsiz: vertical sub-sampling
	W    uint32 // data width
	H    uint32 // data height
	X0   uint32 // x component offset compared to the whole image
	Y0   uint32 // y component offset compared to the whole image
	Prec uint32 // precision
	Sgnd uint32 // signed (1) / unsigned (0)
}

// Create0 is a port of opj_image_create0: an empty image.
func Create0() *Image {
	return &Image{}
}

// Create is a port of opj_image_create. It builds an image with numcmpts
// components from cmptparms in color space clrspc, allocating and zeroing each
// component's data. It returns ErrImageAlloc on the allocation-size overflow
// guard (the C code returns NULL there).
func Create(numcmpts uint32, cmptparms []CompParm, clrspc ColorSpace) (*Image, error) {
	image := &Image{}
	image.ColorSpace = clrspc
	image.Numcomps = numcmpts
	image.Comps = make([]Comp, numcmpts)

	for compno := uint32(0); compno < numcmpts; compno++ {
		comp := &image.Comps[compno]
		comp.Dx = cmptparms[compno].Dx
		comp.Dy = cmptparms[compno].Dy
		comp.W = cmptparms[compno].W
		comp.H = cmptparms[compno].H
		comp.X0 = cmptparms[compno].X0
		comp.Y0 = cmptparms[compno].Y0
		comp.Prec = cmptparms[compno].Prec
		comp.Sgnd = cmptparms[compno].Sgnd
		// Faithful port of the integer-overflow guard in opj_image_create:
		//   if (comp->h != 0 &&
		//       (OPJ_SIZE_T)comp->w > SIZE_MAX / comp->h / sizeof(OPJ_INT32))
		if comp.H != 0 &&
			uint64(comp.W) > math.MaxUint64/uint64(comp.H)/4 {
			return nil, ErrImageAlloc
		}
		comp.Data = make([]int32, uint64(comp.W)*uint64(comp.H))
	}

	return image, nil
}

// TileCreate is a port of opj_image_tile_create. It is like Create but leaves
// each component's Data nil (no per-component allocation).
func TileCreate(numcmpts uint32, cmptparms []CompParm, clrspc ColorSpace) *Image {
	image := &Image{}
	image.ColorSpace = clrspc
	image.Numcomps = numcmpts
	image.Comps = make([]Comp, numcmpts)

	for compno := uint32(0); compno < numcmpts; compno++ {
		comp := &image.Comps[compno]
		comp.Dx = cmptparms[compno].Dx
		comp.Dy = cmptparms[compno].Dy
		comp.W = cmptparms[compno].W
		comp.H = cmptparms[compno].H
		comp.X0 = cmptparms[compno].X0
		comp.Y0 = cmptparms[compno].Y0
		comp.Prec = cmptparms[compno].Prec
		comp.Sgnd = cmptparms[compno].Sgnd
		comp.Data = nil
	}

	return image
}

// CopyHeader is a port of opj_copy_image_header. It copies the header (image
// bounds, per-component headers without data, color space and ICC profile) from
// src into dst. Any existing component data in dst is dropped.
func CopyHeader(src, dst *Image) {
	dst.X0 = src.X0
	dst.Y0 = src.Y0
	dst.X1 = src.X1
	dst.Y1 = src.Y1

	dst.Comps = nil
	dst.Numcomps = src.Numcomps
	dst.Comps = make([]Comp, dst.Numcomps)

	for compno := uint32(0); compno < dst.Numcomps; compno++ {
		dst.Comps[compno] = src.Comps[compno]
		dst.Comps[compno].Data = nil
	}

	dst.ColorSpace = src.ColorSpace
	dst.ICCProfileLen = src.ICCProfileLen

	if dst.ICCProfileLen != 0 {
		dst.ICCProfileBuf = make([]byte, dst.ICCProfileLen)
		copy(dst.ICCProfileBuf, src.ICCProfileBuf[:src.ICCProfileLen])
	} else {
		dst.ICCProfileBuf = nil
	}
}

// CompHeaderUpdateParams carries the subset of opj_cp fields read by
// opj_image_comp_header_update. It mirrors the tile-grid geometry members of
// opj_cp so this package need not depend on the (not-yet-ported) j2k coding
// parameters.
type CompHeaderUpdateParams struct {
	Tx0 uint32 // opj_cp.tx0: tile grid x origin
	Ty0 uint32 // opj_cp.ty0: tile grid y origin
	Tdx uint32 // opj_cp.tdx: nominal tile width
	Tdy uint32 // opj_cp.tdy: nominal tile height
	Tw  uint32 // opj_cp.tw: number of tiles across
	Th  uint32 // opj_cp.th: number of tiles down
}

// CompHeaderUpdate is a port of opj_image_comp_header_update. It recomputes each
// component's W/H/X0/Y0 from the image bounds, the tile grid in cp, and the
// per-component sub-sampling (Dx/Dy) and reduce Factor.
func (image *Image) CompHeaderUpdate(cp *CompHeaderUpdateParams) {
	lX0 := opjmath.UintMax(cp.Tx0, image.X0)
	lY0 := opjmath.UintMax(cp.Ty0, image.Y0)
	// validity of cp members used here checked in opj_j2k_read_siz; can't overflow.
	lX1 := cp.Tx0 + (cp.Tw-1)*cp.Tdx
	lY1 := cp.Ty0 + (cp.Th-1)*cp.Tdy
	// use add saturated to prevent overflow
	lX1 = opjmath.UintMin(opjmath.UintAdds(lX1, cp.Tdx), image.X1)
	lY1 = opjmath.UintMin(opjmath.UintAdds(lY1, cp.Tdy), image.Y1)

	for i := uint32(0); i < image.Numcomps; i++ {
		imgComp := &image.Comps[i]
		lCompX0 := opjmath.UintCeildiv(lX0, imgComp.Dx)
		lCompY0 := opjmath.UintCeildiv(lY0, imgComp.Dy)
		lCompX1 := opjmath.UintCeildiv(lX1, imgComp.Dx)
		lCompY1 := opjmath.UintCeildiv(lY1, imgComp.Dy)
		lWidth := opjmath.UintCeildivpow2(lCompX1-lCompX0, imgComp.Factor)
		lHeight := opjmath.UintCeildivpow2(lCompY1-lCompY0, imgComp.Factor)
		imgComp.W = lWidth
		imgComp.H = lHeight
		imgComp.X0 = lCompX0
		imgComp.Y0 = lCompY0
	}
}
