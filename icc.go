package gopenjpeg

import (
	"encoding/binary"
	"errors"

	lcms2 "github.com/mgilbir/golittlecms"

	"github.com/mgilbir/gopenjpeg/internal/image"
)

// ErrICCApply is returned by ApplyICCProfile when an embedded ICC profile could
// not be applied (the profile failed to open, the transform could not be built,
// the profile's output colour space is unsupported, or the component layout does
// not match the profile). The reference color_apply_icc_profile is a void
// best-effort routine: on any of these conditions it silently leaves the image
// untouched. We surface the reason as an error so the CLI can warn, but — like
// the C code — the image components are left unmodified when this is returned.
var ErrICCApply = errors.New("gopenjpeg: could not apply embedded ICC profile")

// ErrNoICCProfile is returned by ApplyICCProfile when the image carries no
// embedded ICC profile to apply (ICCProfileLen == 0). It is distinct from
// ErrICCApply, which signals that a profile is present but could not be applied
// (malformed profile, unbuildable transform, unsupported output colour space, or
// a mismatched component layout). Callers can distinguish "nothing to do" from
// "tried and failed".
var ErrNoICCProfile = errors.New("gopenjpeg: image has no embedded ICC profile")

// ApplyICCProfile ports color_apply_icc_profile from OpenJPEG's
// src/bin/common/color.c. It renders the decoded components to sRGB using the
// embedded ICC profile (Image.ICCProfileBuf / ICCProfileLen), driving the
// pure-Go Little CMS port (github.com/mgilbir/golittlecms) exactly as
// opj_decompress drives the C liblcms2:
//
//   - open the embedded profile from memory (cmsOpenProfileFromMem);
//   - read its output colour space (cmsGetColorSpace) and header rendering
//     intent (cmsGetHeaderRenderingIntent);
//   - select the transform pixel types by that colour space and the component
//     precision (RGB_8/RGB_16 for RGB, GRAY_8->RGB_8 for grey with the
//     grey->RGB component expansion, YCbCr_16->RGB_16 for YCbCr); any other
//     output colour space is unsupported and the image is left untouched;
//   - build the transform to a freshly created sRGB profile
//     (cmsCreate_sRGBProfile) at the profile's rendering intent;
//   - transform the samples in place and set the colour space to sRGB.
//
// Like the C function it is best-effort and never panics: any failure leaves the
// image untouched and returns ErrICCApply (the C code returns void in the same
// cases). It handles only real embedded ICC profiles (ICCProfileLen > 0); the
// CIELab enumerated colour space (colr meth 2 with icc_profile_len == 0) is
// handled by ConvertToRGB via cielabToRGB, matching opj_decompress's split
// between color_apply_icc_profile and color_cielab_to_rgb.
func (im *Image) ApplyICCProfile() error {
	return applyICCProfile(im.img)
}

func applyICCProfile(img *image.Image) error {
	// C4 (colour side): a zero-component image must not reach the Comps[0]
	// dereferences below. opj_decompress only ever calls color_apply_icc_profile
	// on a decoded image with components; guard the public/API-reachable path.
	if img.Numcomps == 0 || len(img.Comps) == 0 {
		return ErrICCApply
	}
	// C46: distinguish "no profile present" from "profile failed to apply". A
	// zero ICCProfileLen means there is no embedded ICC profile to apply (it is
	// also the CIELab enumerated-space marker, handled by ConvertToRGB before it
	// reaches here); report it with a distinct sentinel.
	if img.ICCProfileLen == 0 {
		return ErrNoICCProfile
	}
	if len(img.ICCProfileBuf) == 0 {
		// Length claims a profile but the buffer is empty: malformed, treat as an
		// apply failure (leave the image untouched), matching the C void path.
		return ErrICCApply
	}
	n := int(img.ICCProfileLen)
	if n > len(img.ICCProfileBuf) {
		n = len(img.ICCProfileBuf)
	}

	inProf, err := lcms2.OpenProfileFromMem(img.ICCProfileBuf[:n])
	if err != nil || inProf == nil {
		// cmsOpenProfileFromMem == NULL: return, leaving the image untouched.
		return ErrICCApply
	}

	outSpace := inProf.GetColorSpace()
	intent := inProf.GetHeaderRenderingIntent()

	maxW := int(img.Comps[0].W)
	maxH := int(img.Comps[0].H)
	prec := int(img.Comps[0].Prec)

	var inType, outType uint32
	var outProf *lcms2.Profile

	switch outSpace {
	case lcms2.SigRgbData: // enumCS 16
		nrComp := int(img.Numcomps)
		if nrComp < 3 { // GRAY or GRAYA, not RGB or RGBA
			return ErrICCApply
		}
		if nrComp > 4 {
			nrComp = 4
		}
		// AFL test: all used components must share dx/dy/prec/sgnd.
		i := 1
		for ; i < nrComp; i++ {
			if img.Comps[0].Dx != img.Comps[i].Dx {
				break
			}
			if img.Comps[0].Dy != img.Comps[i].Dy {
				break
			}
			if img.Comps[0].Prec != img.Comps[i].Prec {
				break
			}
			if img.Comps[0].Sgnd != img.Comps[i].Sgnd {
				break
			}
		}
		if i != nrComp {
			return ErrICCApply
		}
		if prec <= 8 {
			inType = lcms2.TypeRGB8
			outType = lcms2.TypeRGB8
		} else {
			inType = lcms2.TypeRGB16
			outType = lcms2.TypeRGB16
		}
		outProf, err = lcms2.Create_sRGBProfile()

	case lcms2.SigGrayData: // enumCS 17
		inType = lcms2.TypeGray8
		outType = lcms2.TypeRGB8
		outProf, err = lcms2.Create_sRGBProfile()

	case lcms2.SigYCbCrData: // enumCS 18
		if img.Numcomps < 3 {
			return ErrICCApply
		}
		inType = lcms2.TypeYCbCr16
		outType = lcms2.TypeRGB16
		outProf, err = lcms2.Create_sRGBProfile()

	default:
		// ICC Profile has unknown output colorspace: ignore, leave untouched.
		return ErrICCApply
	}
	if err != nil || outProf == nil {
		return ErrICCApply
	}

	transform, err := lcms2.CreateTransform(inProf, inType, outProf, outType, intent, 0)
	if err != nil || transform == nil {
		// cmsCreateTransform failed: ICC Profile ignored.
		return ErrICCApply
	}

	if img.Numcomps > 2 { // RGB, RGBA
		c := img.Comps
		if !(c[0].W == c[1].W && c[0].W == c[2].W &&
			c[0].H == c[1].H && c[0].H == c[2].H) {
			// "[ERROR] Image components should have the same width and height"
			return ErrICCApply
		}
		max := maxW * maxH
		r := c[0].Data
		g := c[1].Data
		b := c[2].Data
		if prec <= 8 {
			inbuf := make([]byte, max*3)
			outbuf := make([]byte, max*3)
			for i := 0; i < max; i++ {
				inbuf[i*3+0] = byte(r[i])
				inbuf[i*3+1] = byte(g[i])
				inbuf[i*3+2] = byte(b[i])
			}
			transform.DoTransform(inbuf, outbuf, uint32(max))
			for i := 0; i < max; i++ {
				r[i] = int32(outbuf[i*3+0])
				g[i] = int32(outbuf[i*3+1])
				b[i] = int32(outbuf[i*3+2])
			}
		} else { // prec > 8
			inbuf := make([]byte, max*3*2)
			outbuf := make([]byte, max*3*2)
			for i := 0; i < max; i++ {
				binary.LittleEndian.PutUint16(inbuf[(i*3+0)*2:], uint16(r[i]))
				binary.LittleEndian.PutUint16(inbuf[(i*3+1)*2:], uint16(g[i]))
				binary.LittleEndian.PutUint16(inbuf[(i*3+2)*2:], uint16(b[i]))
			}
			transform.DoTransform(inbuf, outbuf, uint32(max))
			for i := 0; i < max; i++ {
				r[i] = int32(binary.LittleEndian.Uint16(outbuf[(i*3+0)*2:]))
				g[i] = int32(binary.LittleEndian.Uint16(outbuf[(i*3+1)*2:]))
				b[i] = int32(binary.LittleEndian.Uint16(outbuf[(i*3+2)*2:]))
			}
		}
	} else { // numcomps <= 2 : GRAY, GRAYA
		// Grey -> RGB expansion: color.c reallocs the component array to
		// numcomps+2, moving the alpha (comps[1]) to comps[3] for GRAYA, and
		// synthesises comps[1]/comps[2] as copies of comps[0] to hold G and B.
		// The transform in/out types are TYPE_GRAY_8 / TYPE_RGB_8 (8-bit).
		//
		// C38: color.c carries a separate prec>8 sub-branch that packs the grey
		// samples as unsigned short, but still feeds them to a TYPE_GRAY_8
		// transform (in_type is never overridden for GRAY) -- a degenerate path
		// with no defined correct output, and no >8-bit grey-ICC file exists in
		// the conformance corpus to pin it. This port keeps a single 8-bit
		// transform: the prec<=8 case (the only one the oracle exercises) is
		// byte-for-byte unchanged, and for prec>8 we downscale the sample to 8
		// bits (rather than truncating its low byte, which would be garbage) so
		// a genuine high-precision grey ICC image degrades sensibly. This is a
		// deliberate, documented deviation, not bit-exact with the C path.
		grayShift := uint(0)
		if p := img.Comps[0].Prec; p > 8 {
			grayShift = uint(p - 8)
		}
		max := maxW * maxH
		gData := make([]int32, max)
		bData := make([]int32, max)

		newComps := make([]image.Comp, img.Numcomps+2)
		newComps[0] = img.Comps[0]
		if img.Numcomps == 2 {
			newComps[3] = img.Comps[1]
		}
		newComps[1] = img.Comps[0]
		newComps[2] = img.Comps[0]
		newComps[1].Data = gData
		newComps[2].Data = bData
		img.Comps = newComps
		img.Numcomps += 2

		r := img.Comps[0].Data
		g := img.Comps[1].Data
		b := img.Comps[2].Data

		inbuf := make([]byte, max)
		outbuf := make([]byte, max*3)
		for i := 0; i < max; i++ {
			inbuf[i] = byte(r[i] >> grayShift)
		}
		transform.DoTransform(inbuf, outbuf, uint32(max))
		for i := 0; i < max; i++ {
			r[i] = int32(outbuf[i*3+0])
			g[i] = int32(outbuf[i*3+1])
			b[i] = int32(outbuf[i*3+2])
		}
	}

	img.ColorSpace = image.ClrspcSRGB
	// C9: clear the profile after a successful apply so ConvertToRGB is
	// idempotent. opj_decompress frees icc_profile_buf and zeroes the length
	// after color_apply_icc_profile; ConvertToRGB re-enters the ICC branch
	// whenever ICCProfileBuf != nil, so leaving it set would re-apply the
	// transform to already-transformed samples on a second call. Mirrors the
	// same clearing cielabToRGB already does (colorconv.go).
	img.ICCProfileBuf = nil
	img.ICCProfileLen = 0
	return nil
}
