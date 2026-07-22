package gopenjpeg

import (
	"encoding/binary"
	"errors"
	"math"

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
		// A JP2 colr box with meth==2 and icc_profile_len==0 signals the CIELab
		// enumerated colour space; the box parameters are packed big-endian into
		// ICCProfileBuf (see internal/jp2 read_boxes.go). Handle that here; a real
		// embedded ICC profile (len>0) still requires a CMS engine we do not ship.
		if img.ICCProfileLen == 0 && isCIELabBuf(img.ICCProfileBuf) {
			return cielabToRGB(img)
		}
		// opj_decompress would apply the profile via Little CMS here. We cannot
		// reproduce that bit-exactly.
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
//
// The C source computes each channel as 255.0F * X * K, i.e. left-associated
// (255.0F * X) * K. But opj_decompress (which contains color.c) is built
// -ffast-math, and gcc's -freassoc pass regroups the three-factor product,
// hoisting 255.0F * K (shared across the R/G/B of a pixel) and computing each
// channel as X * (255.0F * K). Verified against the shipped opj_decompress:
// over the two CMYK conformance files (issue205, issue208, ~1.5M pixels) the
// left-associated source order mismatches ~1 LSB on a handful of samples, while
// X * (255.0F * K) is bit-identical on every channel of every pixel. The final
// (int) cast truncates toward zero (Go int32() does likewise), so no rounding
// adjustment is needed. This is the same -ffast-math reassociation class as the
// ICT/quantizer fixes on the encode side.
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
		k255 := 255.0 * kk
		c[0].Data[i] = int32(cc * k255)
		c[1].Data[i] = int32(mm * k255)
		c[2].Data[i] = int32(yy * k255)
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
//
// color.c is compiled into opj_decompress -ffast-math, and disassembly of the
// shipped color_esycc_to_rgb shows gcc's -freassoc regroups each channel's
// four-term float32 sum: e.g. green is computed as
// (1.0003*y + 0.5) - (0.344125*cb + 0.7141128*cr) rather than the left-
// associated source order 1.0003*y - 0.344125*cb - 0.7141128*cr + 0.5 used
// below. The only eYCC file in the conformance corpus (issue236-ESYCC-CDEF.jp2)
// does NOT distinguish the two groupings: replaying both associations over all
// 307200 pixels of all three channels yields bit-identical results (verified,
// W14), so the source-order port passes bit-exact and there is no corpus test
// that would reveal a divergence. The grouping is left in source order because no
// distinguishing vector exists to validate a change; a maintainer who obtains an
// eYCC image whose samples straddle a rounding boundary should switch to the
// -ffast-math grouping above (the shipped binary's actual arithmetic).
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

// isCIELabBuf reports whether buf is the packed CIELab parameter block that
// internal/jp2 stores in ICCProfileBuf for a colr box with meth==2,
// icc_profile_len==0 and EnumCS==14 (nine big-endian uint32 words, the first
// being the enumerated colour-space value 14).
func isCIELabBuf(buf []byte) bool {
	return len(buf) >= 36 && binary.BigEndian.Uint32(buf) == 14
}

// D50-adapted sRGB XYZ->RGB matrix (Bradford chromatic adaptation), matching
// the matrix Little CMS's cmsCreate_sRGBProfile bakes into the profile: sRGB
// primaries (R 0.64/0.33, G 0.30/0.60, B 0.15/0.06) with the D65 white point,
// Bradford-adapted to the D50 PCS white {0.9642, 1.0, 0.8249} and inverted.
// Computed from first principles (not the rounded published tables) so it maps
// D50 white exactly to (1,1,1); this drops the worst-case error against LCMS on
// synthetic Lab probes from ~15/65535 to <=1/65535.
var cielabXYZ2RGBD50 = [3][3]float64{
	{3.1341863642, -1.6172089590, -0.4906940640},
	{-0.9787485042, 1.9161300968, 0.0334333992},
	{0.0719639278, -0.2289938735, 1.4057537329},
}

// cielabLabToXYZ converts CIE L*a*b* (D50) to XYZ (D50), the standard inverse
// Lab transform LittleCMS applies (cmsLab2XYZ) with the D50 white point
// {0.9642, 1.0, 0.8249}.
func cielabLabToXYZ(L, a, b float64) (X, Y, Z float64) {
	const xn, yn, zn = 0.9642, 1.0, 0.8249
	fy := (L + 16.0) / 116.0
	fx := fy + a/500.0
	fz := fy - b/200.0
	finv := func(t float64) float64 {
		if t > 6.0/29.0 {
			return t * t * t
		}
		return (t - 16.0/116.0) * 3.0 * (6.0 / 29.0) * (6.0 / 29.0)
	}
	return xn * finv(fx), yn * finv(fy), zn * finv(fz)
}

// cielabSRGBGamma applies the sRGB opto-electronic transfer function
// (linear -> gamma-encoded), the tone curve LittleCMS's built-in sRGB profile
// uses.
func cielabSRGBGamma(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1.0/2.4) - 0.055
}

// cielabToRGB reproduces opj_decompress's color_cielab_to_rgb (color.c) for the
// EnumCS==14 CIELab case. opj_decompress performs this conversion through
// LittleCMS (cmsCreateLab4Profile -> cmsCreate_sRGBProfile, INTENT_PERCEPTUAL,
// TYPE_Lab_DBL -> TYPE_RGB_16). We reproduce the colorimetric pipeline in pure
// Go: scale the integer L*a*b* samples to Lab doubles with the box's range/
// offset parameters, Lab(D50) -> XYZ(D50) -> linear sRGB via the D50-adapted
// matrix -> sRGB tone curve -> 16-bit. This is NOT bit-exact with LittleCMS
// (which evaluates the pipeline through interpolated 16-bit lookup tables with
// its own rounding); the gate therefore compares with a small documented
// tolerance (see oracletest/jp2_gate_test.go). Output components are 16-bit sRGB.
func cielabToRGB(img *image.Image) error {
	if img.Numcomps != 3 {
		return ErrColorConvert
	}
	c := img.Comps
	if c[0].Dx != c[1].Dx || c[0].Dx != c[2].Dx ||
		c[0].Dy != c[1].Dy || c[0].Dy != c[2].Dy ||
		c[0].W != c[1].W || c[0].W != c[2].W ||
		c[0].H != c[1].H || c[0].H != c[2].H {
		return ErrColorConvert
	}
	buf := img.ICCProfileBuf
	row := make([]int32, 9)
	for i := 0; i < 9; i++ {
		row[i] = int32(binary.BigEndian.Uint32(buf[i*4:]))
	}
	prec0 := float64(c[0].Prec)
	prec1 := float64(c[1].Prec)
	prec2 := float64(c[2].Prec)

	var rl, ol, ra, oa, rb, ob float64
	if uint32(row[1]) == 0x44454600 { // "DEF\0": default ranges/offsets
		rl, ra, rb = 100, 170, 200
		ol = 0
		oa = math.Pow(2, prec1-1)
		ob = math.Pow(2, prec2-2) + math.Pow(2, prec2-3)
	} else {
		rl = float64(row[2])
		ol = float64(row[3])
		ra = float64(row[4])
		oa = float64(row[5])
		rb = float64(row[6])
		ob = float64(row[7])
	}

	minL := -(rl * ol) / (math.Pow(2, prec0) - 1)
	maxL := minL + rl
	mina := -(ra * oa) / (math.Pow(2, prec1) - 1)
	maxa := mina + ra
	minb := -(rb * ob) / (math.Pow(2, prec2) - 1)
	maxb := minb + rb

	w := int(c[0].W)
	h := int(c[0].H)
	max := w * h
	L := c[0].Data
	a := c[1].Data
	b := c[2].Data
	red := make([]int32, max)
	green := make([]int32, max)
	blue := make([]int32, max)

	denom0 := math.Pow(2, prec0) - 1
	denom1 := math.Pow(2, prec1) - 1
	denom2 := math.Pow(2, prec2) - 1
	for i := 0; i < max; i++ {
		ll := minL + float64(L[i])*(maxL-minL)/denom0
		aa := mina + float64(a[i])*(maxa-mina)/denom1
		bb := minb + float64(b[i])*(maxb-minb)/denom2
		X, Y, Z := cielabLabToXYZ(ll, aa, bb)
		out := [3]*[]int32{&red, &green, &blue}
		for j := 0; j < 3; j++ {
			lin := cielabXYZ2RGBD50[j][0]*X + cielabXYZ2RGBD50[j][1]*Y + cielabXYZ2RGBD50[j][2]*Z
			v := int32(math.Floor(cielabSRGBGamma(lin)*65535.0 + 0.5))
			if v < 0 {
				v = 0
			} else if v > 65535 {
				v = 65535
			}
			(*out[j])[i] = v
		}
	}

	c[0].Data = red
	c[1].Data = green
	c[2].Data = blue
	c[0].Prec = 16
	c[1].Prec = 16
	c[2].Prec = 16
	img.ColorSpace = image.ClrspcSRGB
	img.ICCProfileBuf = nil
	img.ICCProfileLen = 0
	return nil
}
