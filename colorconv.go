package gopenjpeg

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/mgilbir/gopenjpeg/internal/image"
)

// ErrICCUnsupported is retained for backward compatibility. Embedded ICC
// profiles are now colour-managed by ApplyICCProfile (via the pure-Go Little CMS
// port), so ConvertToRGB no longer returns this error; a profile that cannot be
// applied yields ErrICCApply instead (see icc.go).
//
// Deprecated: ConvertToRGB no longer returns ErrICCUnsupported.
var ErrICCUnsupported = errors.New("gopenjpeg: ICC profile colour management is not supported")

// ErrColorConvert is returned when a colour transform cannot be applied because
// the component layout does not match the transform's requirements (mirrors the
// "CAN NOT CONVERT" diagnostics in OpenJPEG's color.c).
var ErrColorConvert = errors.New("gopenjpeg: cannot convert colour space")

// ConvertToRGB reproduces the post-decode colour handling that opj_decompress
// performs before writing an output file, in the exact order of opj_decompress.c:
// it first normalises the colour-space label with the same heuristic the CLI
// uses, then converts sYCC, eYCC or CMYK images to sRGB in place, and finally
// applies any colr-box colour management — CIELab (cielabToRGB) for the
// enumerated CIELab space, or an embedded ICC profile (ApplyICCProfile, via the
// pure-Go Little CMS port) for a real profile. It is a no-op for images that are
// already sRGB or greyscale with no profile.
//
// It returns ErrColorConvert when the component layout is not one the built-in
// transforms handle (including an image with zero components), or ErrICCApply
// when an embedded ICC profile cannot be applied (a malformed profile or one
// whose transform cannot be built); like opj_decompress those are best-effort.
// "Leave the components untouched" holds for a layout the built-in transforms
// reject outright, but note the ordering: sYCC/eYCC/CMYK conversion runs first
// and mutates the components in place, so if a subsequent ICC step then fails
// with ErrICCApply the image already reflects that earlier conversion (this
// matches opj_decompress, which likewise applies the colour transform before
// color_apply_icc_profile and does not roll it back).
func (im *Image) ConvertToRGB() error {
	img := im.img

	// C4 (colour side): a zero-component image has nothing to convert and must
	// not reach the Comps[0]/Comps[1] dereferences in the heuristic below or in
	// the transforms. Return a clean error rather than panicking.
	if img.Numcomps == 0 || len(img.Comps) == 0 {
		return ErrColorConvert
	}

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
		// Mirror opj_decompress.c: with an ICC profile buffer present, a non-zero
		// icc_profile_len is a real embedded ICC profile handled through Little CMS
		// (color_apply_icc_profile); a zero length is the CIELab enumerated colour
		// space (colr meth 2), whose box parameters are packed big-endian into
		// ICCProfileBuf (see internal/jp2 read_boxes.go) and handled by
		// color_cielab_to_rgb.
		if img.ICCProfileLen == 0 && isCIELabBuf(img.ICCProfileBuf) {
			return cielabToRGB(img)
		}
		return applyICCProfile(img)
	}
	return nil
}

// componentsSane reports whether the first n components of img are safe to walk
// with the C-faithful transforms below: a usable precision (1..31, the range
// readSIZ already enforces for decoded images) and at least W*H samples.
//
// A decoded image always satisfies this. An Image built through the public
// NewImage does not have to (it validates nothing — C21), and the C transforms
// index Data[i] for i < W*H unconditionally, so without this check a caller-
// supplied short slice or a zero precision turns into an index-out-of-range or
// negative-shift panic instead of the documented ErrColorConvert.
func componentsSane(img *image.Image, n int) bool {
	if int(img.Numcomps) < n || len(img.Comps) < n {
		return false
	}
	for i := 0; i < n; i++ {
		c := &img.Comps[i]
		if c.Prec == 0 || c.Prec > 31 {
			return false
		}
		if uint64(len(c.Data)) < uint64(c.W)*uint64(c.H) {
			return false
		}
	}
	return true
}

// chromaAt reads chroma sample i, clamping to the last available sample (and
// substituting neutral chroma for an empty plane). The sYCC walkers advance the
// chroma index with the luma walk; when the chroma plane is short of
// ceil(luma/2) — which a resolution reduction can produce, since reduce does not
// touch dx/dy — the C code over-reads the heap and Go would panic. Clamping is a
// no-op for every layout the oracle also converts.
func chromaAt(data []int32, i, offset int) int {
	if i >= len(data) {
		if len(data) == 0 {
			return offset
		}
		i = len(data) - 1
	}
	return int(data[i])
}

// syccToRGBsample ports the static sycc_to_rgb helper in color.c. The
// multiplications use double precision to match the C code (the constants are
// C double literals).
//
// The green term wraps each product in an explicit float64(...) conversion: Go's
// spec lets the compiler contract `x*y + z` into a single-rounding FMA (gc does
// so on arm64/ppc64/s390x/riscv64, never on amd64), which would round the sum
// differently from the C reference and can flip the truncation to int. The
// conversion forces the product to be rounded first; it changes no amd64 code
// and preserves the operand order exactly.
func syccToRGBsample(offset, upb, y, cb, cr int) (r, g, b int) {
	cb -= offset
	cr -= offset
	r = y + int(1.402*float64(cr))
	if r < 0 {
		r = 0
	} else if r > upb {
		r = upb
	}
	g = y - int(float64(0.344*float64(cb))+float64(0.714*float64(cr)))
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
	// The luma plane is walked unbounded by all three sYCC variants (the C code
	// does the same); a short or zero-precision comp 0 must be an error, not a
	// panic. Short CHROMA stays supported (see chromaAt / C8).
	if !componentsSane(img, 1) {
		return ErrColorConvert
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
		// chromaAt bounds the 1:1 chroma reads the same way the 4:2:0/4:2:2
		// walkers do: a caller-built image may carry chroma planes shorter than
		// the luma plane, which the C code would read past.
		rr, gg, bb := syccToRGBsample(offset, upb, int(y[i]), chromaAt(cb, i, offset), chromaAt(cr, i, offset))
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

	// Bound the chroma index before every read. The C walker (color.c) advances
	// cb/cr with the luma walk and dereferences them unconditionally; when the
	// chroma component is short of ceil(luma/2) samples/rows (possible under a
	// resolution reduction, since reduce does not touch dx/dy) the C read runs
	// off the end of the heap buffer (undefined behaviour). Go would panic. We
	// clamp to the last valid chroma sample, which is a no-op for every layout
	// the oracle also converts (chroma is not short there, so the index never
	// exceeds len) and merely keeps the degenerate reduced case panic-free with
	// a sane value. len==0 cannot happen for a dispatched 3-component sYCC image,
	// but we still return the neutral chroma (offset) rather than index [-1].
	cbAt := func(i int) int { return chromaAt(cb, i, offset) }
	crAt := func(i int) int { return chromaAt(cr, i, offset) }

	yi, cbi, cri, oi := 0, 0, 0, 0
	set := func(yy, ccb, ccr int) {
		rr, gg, bb := syccToRGBsample(offset, upb, yy, ccb, ccr)
		r[oi], g[oi], b[oi] = int32(rr), int32(gg), int32(bb)
		oi++
	}

	offx := int(img.X0) & 1
	// A zero-extent luma plane (a deep reduction can shrink a component to
	// nothing) has no samples to walk, but the offx/trailing branches below
	// still emit one sample per row and would index the empty planes; C reads
	// past its buffer there.
	if maxw == 0 || maxh == 0 {
		img.Comps[0].Data = r
		img.Comps[1].Data = g
		img.Comps[2].Data = b
		syncChroma(img)
		img.ColorSpace = image.ClrspcSRGB
		return
	}
	loopmaxw := maxw - offx
	for i := 0; i < maxh; i++ {
		if offx > 0 {
			set(int(y[yi]), 0, 0)
			yi++
		}
		var j int
		for j = 0; j < (loopmaxw &^ 1); j += 2 {
			set(int(y[yi]), cbAt(cbi), crAt(cri))
			yi++
			set(int(y[yi]), cbAt(cbi), crAt(cri))
			yi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				set(int(y[yi]), 0, 0)
			} else {
				set(int(y[yi]), cbAt(cbi), crAt(cri))
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

	// Bound the chroma index before every read. See sycc422ToRGB for the full
	// rationale: under a resolution reduction the double-ceil dimension math can
	// leave the chroma component one ROW short of ceil(luma_h/2); the odd-
	// trailing-row branch then reads a chroma row that does not exist (the
	// j/2==comp12w guard only substitutes for the last COLUMN). The C code
	// over-reads the heap; Go would panic. Clamping to the last valid chroma
	// sample is a no-op wherever the oracle also converts (chroma is not short,
	// so the index stays in range) and keeps the degenerate reduced case sane.
	cbAt := func(i int) int { return chromaAt(cb, i, offset) }
	crAt := func(i int) int { return chromaAt(cr, i, offset) }

	// Absolute-index helpers into r/g/b and y (the C code walks two rows at a
	// time with "next" pointers nr/ng/nb/ny offset by maxw).
	setAt := func(o, yy, ccb, ccr int) {
		rr, gg, bb := syccToRGBsample(offset, upb, yy, ccb, ccr)
		r[o], g[o], b[o] = int32(rr), int32(gg), int32(bb)
	}

	offx := int(img.X0) & 1
	// See sycc422ToRGB: a zero-extent luma plane has nothing to walk, but the
	// offx/offy branches still emit samples and would index the empty planes.
	if maxw == 0 || maxh == 0 {
		img.Comps[0].Data = r
		img.Comps[1].Data = g
		img.Comps[2].Data = b
		syncChroma(img)
		img.ColorSpace = image.ClrspcSRGB
		return
	}
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
			setAt(noi, int(y[nyi]), cbAt(cbi), crAt(cri))
			nyi++
			noi++
		}
		var j int
		for j = 0; j < (loopmaxw &^ 1); j += 2 {
			setAt(oi, int(y[yi]), cbAt(cbi), crAt(cri))
			yi++
			oi++
			setAt(oi, int(y[yi]), cbAt(cbi), crAt(cri))
			yi++
			oi++
			setAt(noi, int(y[nyi]), cbAt(cbi), crAt(cri))
			nyi++
			noi++
			setAt(noi, int(y[nyi]), cbAt(cbi), crAt(cri))
			nyi++
			noi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				setAt(oi, int(y[yi]), 0, 0)
			} else {
				setAt(oi, int(y[yi]), cbAt(cbi), crAt(cri))
			}
			yi++
			oi++
			if j/2 == comp12w {
				setAt(noi, int(y[nyi]), 0, 0)
			} else {
				setAt(noi, int(y[nyi]), cbAt(cbi), crAt(cri))
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
			setAt(oi, int(y[yi]), cbAt(cbi), crAt(cri))
			yi++
			oi++
			setAt(oi, int(y[yi]), cbAt(cbi), crAt(cri))
			yi++
			oi++
			cbi++
			cri++
		}
		if j < loopmaxw {
			if j/2 == comp12w {
				setAt(oi, int(y[yi]), 0, 0)
			} else {
				setAt(oi, int(y[yi]), cbAt(cbi), crAt(cri))
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
	// All four planes are walked to W*H unbounded (as in C).
	if !componentsSane(img, 4) {
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
		// The explicit float32(...) around each scaling product is an FMA
		// barrier: the products feed the `1.0 - x` inversions below, a shape gc
		// contracts into a single-rounding FNMSUB on arm64/ppc64/s390x/riscv64.
		// C rounds the product first; the conversion makes Go do the same on
		// every GOARCH (and is a no-op on amd64, which never fuses).
		cc := float32(float32(c[0].Data[i]) * sC)
		mm := float32(float32(c[1].Data[i]) * sM)
		yy := float32(float32(c[2].Data[i]) * sY)
		kk := float32(float32(c[3].Data[i]) * sK)
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
	// All three planes are walked to W*H unbounded, and Prec-1 is a shift count.
	if !componentsSane(img, 3) {
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
		// Each product is wrapped in an explicit float32(...) conversion: an FMA
		// barrier (Go spec) that stops gc contracting these multiply-accumulate
		// chains into single-rounding FMADDS/FMSUBS on arm64/ppc64/s390x/riscv64.
		// The C reference rounds every product separately. The barriers forbid
		// fusion only — the left-associated source order above is preserved, and
		// on amd64 (which never fuses) the emitted code is unchanged.
		r := int32(float32(y) - float32(0.0000368*float32(cb)) + float32(1.40199*float32(cr)) + 0.5)
		c[0].Data[i] = clamp(r)
		g := int32(float32(1.0003*float32(y)) - float32(0.344125*float32(cb)) - float32(0.7141128*float32(cr)) + 0.5)
		c[1].Data[i] = clamp(g)
		b := int32(float32(0.999823*float32(y)) + float32(1.77204*float32(cb)) - float32(0.000008*float32(cr)) + 0.5)
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
	// float64(...) is an FMA barrier (see the eYCC/sYCC notes above): without it
	// gc contracts this into FMSUBD on arm64 and the CIELab output becomes
	// GOARCH-dependent. No-op on amd64.
	return float64(1.055*math.Pow(v, 1.0/2.4)) - 0.055
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
	// L*, a* and b* are walked to W*H unbounded and Prec feeds math.Pow/denoms.
	if !componentsSane(img, 3) {
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
			// float64(...) around each product is an FMA barrier, keeping the
			// matrix multiply and the 16-bit quantisation GOARCH-independent.
			lin := float64(cielabXYZ2RGBD50[j][0]*X) + float64(cielabXYZ2RGBD50[j][1]*Y) + float64(cielabXYZ2RGBD50[j][2]*Z)
			v := int32(math.Floor(float64(cielabSRGBGamma(lin)*65535.0) + 0.5))
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
