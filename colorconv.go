package gopenjpeg

import (
	"errors"

	"github.com/mgilbir/gopenjpeg/internal/image"
)

// ErrICCUnsupported is returned by ConvertToRGB when the image carries an ICC
// profile. The reference opj_decompress applies such profiles through Little
// CMS (an external colour-management library); this pure-Go port does not embed
// a CMS engine, so ICC-managed images cannot be rendered to sRGB identically.
// Callers that only need the raw decoded components can ignore this error.
var ErrICCUnsupported = errors.New("gopenjpeg: ICC profile colour management is not supported")

// ErrColorConvert is returned when a colour transform cannot be applied because
// the component layout does not match the transform's requirements (mirrors the
// "CAN NOT CONVERT" diagnostics in OpenJPEG's color.c).
var ErrColorConvert = errors.New("gopenjpeg: cannot convert colour space")

// ConvertToRGB reproduces the post-decode colour handling that opj_decompress
// performs before writing an output file: it first normalises the colour-space
// label with the same heuristic the CLI uses, then converts sYCC, eYCC or CMYK
// images to sRGB in place. It is a no-op for images that are already sRGB or
// greyscale.
//
// It returns ErrICCUnsupported when the image carries an ICC profile (the C CLI
// would defer to Little CMS), and ErrColorConvert when the component layout is
// not one the built-in transforms handle.
func (im *Image) ConvertToRGB() error {
	img := im.img

	// Colour-space normalisation heuristic (opj_decompress.c). A 3-component
	// image whose chroma planes are sub-sampled is treated as sYCC; a 1- or
	// 2-component image is treated as greyscale.
	if img.ColorSpace != image.ClrspcSYCC && img.Numcomps == 3 &&
		img.Comps[0].Dx == img.Comps[0].Dy && img.Comps[1].Dx != 1 {
		img.ColorSpace = image.ClrspcSYCC
	} else if img.Numcomps <= 2 {
		img.ColorSpace = image.ClrspcGray
	}

	switch img.ColorSpace {
	case image.ClrspcSYCC:
		if err := syccToRGB(img); err != nil {
			return err
		}
	case image.ClrspcCMYK:
		if err := cmykToRGB(img); err != nil {
			return err
		}
	case image.ClrspcEYCC:
		if err := esyccToRGB(img); err != nil {
			return err
		}
	}

	if img.ICCProfileBuf != nil {
		// opj_decompress would apply the profile (or the CIELab special case)
		// via Little CMS here. We cannot reproduce that bit-exactly.
		return ErrICCUnsupported
	}
	return nil
}

// syccToRGBsample ports the static sycc_to_rgb helper in color.c. The
// multiplications use double precision to match the C code (the constants are
// C double literals).
func syccToRGBsample(offset, upb, y, cb, cr int) (r, g, b int) {
	cb -= offset
	cr -= offset
	r = y + int(1.402*float64(cr))
	if r < 0 {
		r = 0
	} else if r > upb {
		r = upb
	}
	g = y - int(0.344*float64(cb)+0.714*float64(cr))
	if g < 0 {
		g = 0
	} else if g > upb {
		g = upb
	}
	b = y + int(1.772*float64(cb))
	if b < 0 {
		b = 0
	} else if b > upb {
		b = upb
	}
	return r, g, b
}

// syccToRGB ports color_sycc_to_rgb, dispatching on the chroma sub-sampling.
func syccToRGB(img *image.Image) error {
	if img.Numcomps < 3 {
		img.ColorSpace = image.ClrspcGray
		return nil
	}
	c := img.Comps
	switch {
	case c[0].Dx == 1 && c[1].Dx == 2 && c[2].Dx == 2 &&
		c[0].Dy == 1 && c[1].Dy == 2 && c[2].Dy == 2:
		sycc420ToRGB(img)
	case c[0].Dx == 1 && c[1].Dx == 2 && c[2].Dx == 2 &&
		c[0].Dy == 1 && c[1].Dy == 1 && c[2].Dy == 1:
		sycc422ToRGB(img)
	case c[0].Dx == 1 && c[1].Dx == 1 && c[2].Dx == 1 &&
		c[0].Dy == 1 && c[1].Dy == 1 && c[2].Dy == 1:
		sycc444ToRGB(img)
	default:
		return ErrColorConvert
	}
	return nil
}

func sycc444ToRGB(img *image.Image) {
	upb := int(img.Comps[0].Prec)
	offset := 1 << (upb - 1)
	upb = (1 << upb) - 1

	maxw := int(img.Comps[0].W)
	maxh := int(img.Comps[0].H)
	max := maxw * maxh

	y := img.Comps[0].Data
	cb := img.Comps[1].Data
	cr := img.Comps[2].Data
	r := make([]int32, max)
	g := make([]int32, max)
	b := make([]int32, max)
	for i := 0; i < max; i++ {
		rr, gg, bb := syccToRGBsample(offset, upb, int(y[i]), int(cb[i]), int(cr[i]))
		r[i], g[i], b[i] = int32(rr), int32(gg), int32(bb)
	}
	img.Comps[0].Data = r
	img.Comps[1].Data = g
	img.Comps[2].Data = b
	img.ColorSpace = image.ClrspcSRGB
}

func sycc422ToRGB(img *image.Image) {
	upb := int(img.Comps[0].Prec)
	offset := 1 << (upb - 1)
	upb = (1 << upb) - 1

	maxw := int(img.Comps[0].W)
	comp12w := int(img.Comps[1].W)
	maxh := int(img.Comps[0].H)
	max := maxw * maxh

	y := img.Comps[0].Data
	cb := img.Comps[1].Data
	cr := img.Comps[2].Data
	r := make([]int32, max)
	g := make([]int32, max)
	b := make([]int32, max)

	yi, cbi, cri, oi := 0, 0, 0, 0
	set := func(yy, ccb, ccr int) {
		rr, gg, bb := syccToRGBsample(offset, upb, yy, ccb, ccr)
		r[oi], g[oi], b[oi] = int32(rr), int32(gg), int32(bb)
		oi++
	}

	offx := int(img.X0) & 1
	loopmaxw := maxw - offx
	for i := 0; i < maxh; i++ {
		if offx > 0 {
			set(int(y[yi]), 0, 0)
			yi++
		}
		var j int
		for j = 0; j < (loopmaxw &^ 1); j += 2 {
			set(int(y[yi]), int(cb[cbi]), int(cr[cri]))
			yi++
			set(int(y[yi]), int(cb[cbi]), int(cr[cri]))
			yi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				set(int(y[yi]), 0, 0)
			} else {
				set(int(y[yi]), int(cb[cbi]), int(cr[cri]))
			}
			yi++
			if j/2 < comp12w {
				cbi++
				cri++
			}
		}
	}

	img.Comps[0].Data = r
	img.Comps[1].Data = g
	img.Comps[2].Data = b
	syncChroma(img)
	img.ColorSpace = image.ClrspcSRGB
}

func sycc420ToRGB(img *image.Image) {
	upb := int(img.Comps[0].Prec)
	offset := 1 << (upb - 1)
	upb = (1 << upb) - 1

	maxw := int(img.Comps[0].W)
	comp12w := int(img.Comps[1].W)
	maxh := int(img.Comps[0].H)
	max := maxw * maxh

	y := img.Comps[0].Data
	cb := img.Comps[1].Data
	cr := img.Comps[2].Data
	r := make([]int32, max)
	g := make([]int32, max)
	b := make([]int32, max)

	// Absolute-index helpers into r/g/b and y (the C code walks two rows at a
	// time with "next" pointers nr/ng/nb/ny offset by maxw).
	setAt := func(o, yy, ccb, ccr int) {
		rr, gg, bb := syccToRGBsample(offset, upb, yy, ccb, ccr)
		r[o], g[o], b[o] = int32(rr), int32(gg), int32(bb)
	}

	offx := int(img.X0) & 1
	loopmaxw := maxw - offx
	offy := int(img.Y0) & 1
	loopmaxh := maxh - offy

	yi := 0 // index into y
	oi := 0 // index into r/g/b (current row)
	cbi, cri := 0, 0

	if offy > 0 {
		for j := 0; j < maxw; j++ {
			setAt(oi, int(y[yi]), 0, 0)
			yi++
			oi++
		}
	}

	var i int
	for i = 0; i < (loopmaxh &^ 1); i += 2 {
		nyi := yi + maxw
		noi := oi + maxw
		if offx > 0 {
			setAt(oi, int(y[yi]), 0, 0)
			yi++
			oi++
			setAt(noi, int(y[nyi]), int(cb[cbi]), int(cr[cri]))
			nyi++
			noi++
		}
		var j int
		for j = 0; j < (loopmaxw &^ 1); j += 2 {
			setAt(oi, int(y[yi]), int(cb[cbi]), int(cr[cri]))
			yi++
			oi++
			setAt(oi, int(y[yi]), int(cb[cbi]), int(cr[cri]))
			yi++
			oi++
			setAt(noi, int(y[nyi]), int(cb[cbi]), int(cr[cri]))
			nyi++
			noi++
			setAt(noi, int(y[nyi]), int(cb[cbi]), int(cr[cri]))
			nyi++
			noi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				setAt(oi, int(y[yi]), 0, 0)
			} else {
				setAt(oi, int(y[yi]), int(cb[cbi]), int(cr[cri]))
			}
			yi++
			oi++
			if j/2 == comp12w {
				setAt(noi, int(y[nyi]), 0, 0)
			} else {
				setAt(noi, int(y[nyi]), int(cb[cbi]), int(cr[cri]))
			}
			nyi++
			noi++
			if j/2 < comp12w {
				cbi++
				cri++
			}
		}
		// advance past the "next" row that was just filled.
		yi += maxw
		oi += maxw
	}
	if i < loopmaxh {
		if offx > 0 {
			setAt(oi, int(y[yi]), 0, 0)
			yi++
			oi++
		}
		var j int
		for j = 0; j < (loopmaxw &^ 1); j += 2 {
			setAt(oi, int(y[yi]), int(cb[cbi]), int(cr[cri]))
			yi++
			oi++
			setAt(oi, int(y[yi]), int(cb[cbi]), int(cr[cri]))
			yi++
			oi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				setAt(oi, int(y[yi]), 0, 0)
			} else {
				setAt(oi, int(y[yi]), int(cb[cbi]), int(cr[cri]))
			}
		}
	}

	img.Comps[0].Data = r
	img.Comps[1].Data = g
	img.Comps[2].Data = b
	syncChroma(img)
	img.ColorSpace = image.ClrspcSRGB
}

// syncChroma copies the luma geometry onto the two chroma components, matching
// the trailing assignments in sycc422_to_rgb / sycc420_to_rgb.
func syncChroma(img *image.Image) {
	for _, i := range []int{1, 2} {
		img.Comps[i].W = img.Comps[0].W
		img.Comps[i].H = img.Comps[0].H
		img.Comps[i].Dx = img.Comps[0].Dx
		img.Comps[i].Dy = img.Comps[0].Dy
	}
}

// cmykToRGB ports color_cmyk_to_rgb (float32 arithmetic, matching the C floats).
func cmykToRGB(img *image.Image) error {
	c := img.Comps
	if img.Numcomps < 4 ||
		c[0].Dx != c[1].Dx || c[0].Dx != c[2].Dx || c[0].Dx != c[3].Dx ||
		c[0].Dy != c[1].Dy || c[0].Dy != c[2].Dy || c[0].Dy != c[3].Dy {
		return ErrColorConvert
	}
	w := int(c[0].W)
	h := int(c[0].H)
	max := w * h
	sC := float32(1.0) / float32((uint32(1)<<c[0].Prec)-1)
	sM := float32(1.0) / float32((uint32(1)<<c[1].Prec)-1)
	sY := float32(1.0) / float32((uint32(1)<<c[2].Prec)-1)
	sK := float32(1.0) / float32((uint32(1)<<c[3].Prec)-1)
	for i := 0; i < max; i++ {
		cc := float32(c[0].Data[i]) * sC
		mm := float32(c[1].Data[i]) * sM
		yy := float32(c[2].Data[i]) * sY
		kk := float32(c[3].Data[i]) * sK
		cc = 1.0 - cc
		mm = 1.0 - mm
		yy = 1.0 - yy
		kk = 1.0 - kk
		c[0].Data[i] = int32(255.0 * cc * kk)
		c[1].Data[i] = int32(255.0 * mm * kk)
		c[2].Data[i] = int32(255.0 * yy * kk)
	}
	c[3].Data = nil
	c[0].Prec = 8
	c[1].Prec = 8
	c[2].Prec = 8
	img.Numcomps--
	img.ColorSpace = image.ClrspcSRGB
	for i := uint32(3); i < img.Numcomps; i++ {
		img.Comps[i] = img.Comps[i+1]
	}
	img.Comps = img.Comps[:img.Numcomps]
	return nil
}

// esyccToRGB ports color_esycc_to_rgb (float32 arithmetic).
func esyccToRGB(img *image.Image) error {
	c := img.Comps
	if img.Numcomps < 3 ||
		c[0].Dx != c[1].Dx || c[0].Dx != c[2].Dx ||
		c[0].Dy != c[1].Dy || c[0].Dy != c[2].Dy {
		return ErrColorConvert
	}
	flip := int32(1) << (c[0].Prec - 1)
	maxValue := (int32(1) << c[0].Prec) - 1
	w := int(c[0].W)
	h := int(c[0].H)
	max := w * h
	sign1 := c[1].Sgnd != 0
	sign2 := c[2].Sgnd != 0
	clamp := func(v int32) int32 {
		if v > maxValue {
			return maxValue
		}
		if v < 0 {
			return 0
		}
		return v
	}
	for i := 0; i < max; i++ {
		y := c[0].Data[i]
		cb := c[1].Data[i]
		cr := c[2].Data[i]
		if !sign1 {
			cb -= flip
		}
		if !sign2 {
			cr -= flip
		}
		r := int32(float32(y) - 0.0000368*float32(cb) + 1.40199*float32(cr) + 0.5)
		c[0].Data[i] = clamp(r)
		g := int32(1.0003*float32(y) - 0.344125*float32(cb) - 0.7141128*float32(cr) + 0.5)
		c[1].Data[i] = clamp(g)
		b := int32(0.999823*float32(y) + 1.77204*float32(cb) - 0.000008*float32(cr) + 0.5)
		c[2].Data[i] = clamp(b)
	}
	img.ColorSpace = image.ClrspcSRGB
	return nil
}
