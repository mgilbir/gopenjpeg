package oracletest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/gopenjpeg"
)

// This is the JP2/JPH container decode-parity gate. Unlike decode_gate_test.go
// (which drives internal/j2k directly on raw codestreams), this gate exercises
// the whole public wiring: it decodes through the root gopenjpeg package, which
// detects the container, runs the internal/jp2 box layer over the internal/j2k
// codestream decoder via the CodestreamCodec adapter, and then applies the same
// post-decode colour handling opj_decompress performs (Image.ConvertToRGB). The
// decoded component samples must be bit-exact with the PGX that opj_decompress
// writes.
//
// EXCLUSIONS (documented; the goal is zero unexplained ones):
//
//   - ICC-managed files (colr method 2, non-zero ICC profile length): the
//     reference opj_decompress renders these to sRGB through Little CMS
//     (OPJ_HAVE_LIBLCMS2 is enabled in the oracle build). This pure-Go port
//     embeds no colour-management engine, so ConvertToRGB reports
//     ErrICCUnsupported and cannot reproduce LCMS output bit-exactly. Affected
//     conformance files: file5, file7, file8. Affected nonregression file:
//     issue171.jp2.
//
// CMYK files (issue205.jp2, issue208.jp2) are NO LONGER excluded: color.c is
// compiled into opj_decompress -ffast-math, and gcc reassociates 255.0F*X*K into
// X*(255.0F*K). colorconv.go's cmykToRGB now mirrors that grouping and is
// bit-identical with opj_decompress on both files (~1.5M pixels, every channel).
//
// CIELab files (issue559-eci-090/091-CIELab.jp2) are gated with a TOLERANCE of 1
// (out of 65535), not bit-exact — see TestJP2CIELab. opj_decompress converts
// these through LittleCMS (cmsCreateLab4Profile(D50) -> cmsCreate_sRGBProfile,
// INTENT_PERCEPTUAL, TYPE_Lab_DBL -> TYPE_RGB_16). colorconv.go's cielabToRGB
// reproduces the colorimetric pipeline in pure Go (Lab D50 -> XYZ -> Bradford-
// adapted sRGB matrix -> sRGB tone curve -> 16-bit). Bit-exactness is impractical
// because LittleCMS evaluates the transform through interpolated 16-bit lookup
// tables with its own rounding, not the reference float pipeline: after matching
// LCMS's exact D50-adapted matrix (verified against liblcms2 on synthetic Lab
// probes), the residual is at most 1/65535 on ~0.15% of samples (383/269664 and
// 425/269664, all off-by-one; every other sample is exact). Embedded ICC
// profiles (colr meth 2, len>0: file5/7/8, issue171.jp2) remain excluded — those
// need a full ICC CMS engine, which is out of scope.

// jp2ConformancePass are conformance JP2 files that decode bit-exact end to end.
// file5/file7/file8 are excluded (ICC/LCMS); see the exclusion notes above.
var jp2ConformancePass = []string{
	"file1.jp2", // sRGB, 3 comp
	"file2.jp2", // sRGB
	"file3.jp2", // sYCC 4:2:0 (chroma dx=dy=2) -> RGB
	"file4.jp2", // grayscale
	"file6.jp2", // grayscale 12-bit
	"file9.jp2", // palette (pclr) -> 3 comp sRGB
}

// jp2NonregPass is a curated, diverse nonregression set: palette, channel
// definitions (alpha), sYCC at all three sub-samplings, eYCC, and plain sRGB.
var jp2NonregPass = []string{
	"basn6a08.jp2",            // RGBA, cdef alpha channel
	"basn4a08.jp2",            // grayscale+alpha, cdef
	"issue236-ESYCC-CDEF.jp2", // eYCC + cdef -> RGB
	"issue411-ycc420.jp2",     // sYCC 4:2:0
	"issue411-ycc422.jp2",     // sYCC 4:2:2
	"issue411-ycc444.jp2",     // sYCC 4:4:4
	"Marrin.jp2",              // palette
	"text_GBR.jp2",            // sRGB
	"issue412.jp2",
	"issue414.jp2",
	"issue458.jp2",
	"relax.jp2",
	"merged.jp2",
	"file409752.jp2",
	"issue206_image-000.jp2",
	"issue205.jp2", // CMYK -> RGB (color_cmyk_to_rgb, -ffast-math reassociated)
	"issue208.jp2", // CMYK -> RGB
	// Additional sYCC coverage (found via a broad corpus sweep, W14): the
	// double-precision sycc_to_rgb path is bit-exact on every sub-sampled-chroma
	// JP2 in the corpus that opj_decompress can render to PGX, not only the
	// curated issue411 trio. (subsampling_1.jp2 / zoo1.jp2 are also sYCC and match
	// under a direct decode, but opj_decompress declines to write them as PGX, so
	// they are not usable as gate fixtures.)
	"issue134.jp2",
}

// htContainerPass are the HTJ2K container files: a JPH-boxed file and a raw HT
// codestream.
var htContainerPass = []string{
	"htj2k/byte.jph",
	"htj2k/byte_causal.jhc",
}

// decodeGoPublic decodes path through the public API and applies the same
// post-decode colour handling opj_decompress performs before writing.
func decodeGoPublic(t *testing.T, path string) (*gopenjpeg.Image, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := gopenjpeg.Decode(f)
	if err != nil {
		return nil, err
	}
	// Mirror the CLI: attempt the opj_decompress-style colour conversion. An
	// ICC/CMYK/CIELab file that we cannot colour-manage is left with its raw
	// components (ErrICCUnsupported / ErrColorConvert are tolerated here); the
	// subsequent byte comparison decides parity. Files whose oracle output is
	// actually altered by Little CMS are excluded from the curated lists.
	if cerr := img.ConvertToRGB(); cerr != nil &&
		!errors.Is(cerr, gopenjpeg.ErrICCUnsupported) &&
		!errors.Is(cerr, gopenjpeg.ErrColorConvert) {
		return nil, cerr
	}
	return img, nil
}

// comparePublic compares a public Image against the oracle PGX components.
func comparePublic(img *gopenjpeg.Image, comps []*pgx) error {
	if img.NumComponents() != len(comps) {
		return fmt.Errorf("component count mismatch: go=%d oracle=%d", img.NumComponents(), len(comps))
	}
	for i := 0; i < len(comps); i++ {
		gc := img.Component(i)
		oc := comps[i]
		if int(gc.W) != oc.w || int(gc.H) != oc.h {
			return fmt.Errorf("comp %d dim mismatch: go=%dx%d oracle=%dx%d", i, gc.W, gc.H, oc.w, oc.h)
		}
		if gc.Data == nil {
			return fmt.Errorf("comp %d: go data is nil", i)
		}
		n := oc.w * oc.h
		if len(gc.Data) < n {
			return fmt.Errorf("comp %d: go data too short %d < %d", i, len(gc.Data), n)
		}
		for k := 0; k < n; k++ {
			if gc.Data[k] != oc.data[k] {
				return fmt.Errorf("comp %d sample %d (x=%d,y=%d) mismatch: go=%d oracle=%d",
					i, k, k%oc.w, k/oc.w, gc.Data[k], oc.data[k])
			}
		}
	}
	return nil
}

func runJP2Case(t *testing.T, in string) {
	comps, err := oracleDecodePGX(t, in)
	if err != nil {
		t.Skipf("oracle decode failed: %v", err)
	}
	img, err := decodeGoPublic(t, in)
	if err != nil {
		t.Fatalf("public API decode failed: %v", err)
	}
	if err := comparePublic(img, comps); err != nil {
		t.Fatalf("not bit-exact: %v", err)
	}
}

// TestJP2Conformance decodes the curated conformance JP2 set bit-exact.
func TestJP2Conformance(t *testing.T) {
	Require(t)
	for _, name := range jp2ConformancePass {
		name := name
		t.Run(name, func(t *testing.T) {
			runJP2Case(t, DataDir("input", "conformance", name))
		})
	}
}

// TestJP2Nonregression decodes the curated nonregression JP2 set bit-exact.
func TestJP2Nonregression(t *testing.T) {
	Require(t)
	for _, name := range jp2NonregPass {
		name := name
		t.Run(name, func(t *testing.T) {
			runJP2Case(t, DataDir("input", "nonregression", name))
		})
	}
}

// cielabPass are the CIELab JP2 files, gated with a 1-LSB tolerance (see the
// package-level exclusion notes and cielabToRGB in colorconv.go).
var cielabPass = []string{
	"issue559-eci-090-CIELab.jp2",
	"issue559-eci-091-CIELab.jp2",
}

// cielabTolerance is the maximum permitted per-sample deviation (out of 65535)
// between cielabToRGB and opj_decompress's LittleCMS output. A tolerance of 1 is
// tight: on both corpus files every sample is within 1 and >99.8% are exact; the
// residual comes from LittleCMS's interpolated 16-bit LUTs (see notes above).
const cielabTolerance = 1

// TestJP2CIELab decodes the CIELab JP2 files and checks the pure-Go colorimetric
// conversion is within cielabTolerance of opj_decompress on every sample.
func TestJP2CIELab(t *testing.T) {
	Require(t)
	for _, name := range cielabPass {
		name := name
		t.Run(name, func(t *testing.T) {
			path := DataDir("input", "nonregression", name)
			comps, err := oracleDecodePGX(t, path)
			if err != nil {
				t.Skipf("oracle decode failed: %v", err)
			}
			img, err := decodeGoPublic(t, path)
			if err != nil {
				t.Fatalf("public API decode failed: %v", err)
			}
			if img.NumComponents() != len(comps) {
				t.Fatalf("component count: go=%d oracle=%d", img.NumComponents(), len(comps))
			}
			var maxDiff, nonzero, total int
			for i := 0; i < len(comps); i++ {
				gc := img.Component(i)
				oc := comps[i]
				if int(gc.W) != oc.w || int(gc.H) != oc.h {
					t.Fatalf("comp %d dim: go=%dx%d oracle=%dx%d", i, gc.W, gc.H, oc.w, oc.h)
				}
				n := oc.w * oc.h
				for k := 0; k < n; k++ {
					total++
					d := int(gc.Data[k]) - int(oc.data[k])
					if d < 0 {
						d = -d
					}
					if d > maxDiff {
						maxDiff = d
					}
					if d != 0 {
						nonzero++
					}
				}
			}
			if maxDiff > cielabTolerance {
				t.Errorf("%s: max diff %d exceeds tolerance %d (%d/%d samples differ)",
					name, maxDiff, cielabTolerance, nonzero, total)
			}
			t.Logf("%s: max diff %d (<=%d ok); %d/%d samples off-by-one (%.3f%%)",
				name, maxDiff, cielabTolerance, nonzero, total, 100*float64(nonzero)/float64(total))
		})
	}
}

// TestHTContainer decodes the HTJ2K container files bit-exact.
func TestHTContainer(t *testing.T) {
	Require(t)
	for _, name := range htContainerPass {
		name := name
		t.Run(filepath.Base(name), func(t *testing.T) {
			runJP2Case(t, DataDir("input", "nonregression", filepath.FromSlash(name)))
		})
	}
}
