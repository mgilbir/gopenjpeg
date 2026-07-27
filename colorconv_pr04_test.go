package gopenjpeg

import (
	"errors"
	"os"
	"testing"
)

// makeSYCC420OneRowShort builds a 4:2:0 sYCC image whose chroma components are
// exactly one row short of ceil(luma_h/2). This is the degenerate geometry the
// double-ceil dimension math can produce for a 4:2:0 sYCC image decoded with a
// resolution reduction (WithReduce r>=1): reduce does not change dx/dy, so the
// sYCC dispatch still fires, but the chroma plane has fewer rows than the walker
// expects. Before the C8 fix, sycc420ToRGB's odd-trailing-row walk indexed past
// the chroma slice and panicked.
func makeSYCC420OneRowShort(t *testing.T) *Image {
	t.Helper()
	const w, h = 4, 4
	y := make([]int32, w*h)
	for i := range y {
		y[i] = int32(16 + (i % 200)) // arbitrary luma
	}
	// Correct chroma height for 4:2:0 would be ceil(4/2)=2 rows of width 2 (4
	// samples). Provide only ONE row (2 samples) => one row short.
	const cw, ch = 2, 1
	cb := []int32{90, 160}
	cr := []int32{200, 40}
	comps := []Component{
		{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: y},
		{Dx: 2, Dy: 2, W: cw, H: ch, Prec: 8, Data: cb},
		{Dx: 2, Dy: 2, W: cw, H: ch, Prec: 8, Data: cr},
	}
	return NewImage(ColorSpaceSYCC, 0, 0, w, h, comps)
}

// TestConvertToRGBSYCC420OneRowShortNoPanic covers C8: a 4:2:0 sYCC image with a
// chroma plane one row short must not panic and must produce a sane sRGB image.
func TestConvertToRGBSYCC420OneRowShortNoPanic(t *testing.T) {
	im := makeSYCC420OneRowShort(t)
	if err := im.ConvertToRGB(); err != nil {
		t.Fatalf("ConvertToRGB returned error: %v", err)
	}
	if im.ColorSpace() != ColorSpaceSRGB {
		t.Fatalf("colour space = %v, want sRGB", im.ColorSpace())
	}
	if im.NumComponents() != 3 {
		t.Fatalf("numcomps = %d, want 3", im.NumComponents())
	}
	// After conversion the chroma geometry is synced to luma (4x4) and every
	// sample must be a valid 8-bit sRGB value.
	for c := 0; c < 3; c++ {
		comp := im.Component(c)
		if comp.W != 4 || comp.H != 4 {
			t.Fatalf("comp %d dims = %dx%d, want 4x4", c, comp.W, comp.H)
		}
		if len(comp.Data) < 16 {
			t.Fatalf("comp %d data len = %d, want >= 16", c, len(comp.Data))
		}
		for k, v := range comp.Data[:16] {
			if v < 0 || v > 255 {
				t.Fatalf("comp %d sample %d = %d out of 8-bit range", c, k, v)
			}
		}
	}
}

// TestConvertToRGBReducedSYCCNoPanic covers C8 via the public Decode+WithReduce
// path on the real 4:2:0/4:2:2 sYCC vectors, sweeping reduce levels. The double-
// ceil geometry that leaves chroma short only arises at r>=1; the assertion is
// simply that no reduce level panics and each conversion yields a valid result.
func TestConvertToRGBReducedSYCCNoPanic(t *testing.T) {
	files := []string{
		"testdata/vectors/jp2/files/issue411-ycc420.jp2",
		"testdata/vectors/jp2/files/issue411-ycc422.jp2",
	}
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("missing vector %s: %v", path, err)
		}
		for r := uint32(0); r <= 4; r++ {
			r := r
			t.Run(path+"_r"+string(rune('0'+r)), func(t *testing.T) {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				im, err := Decode(f, WithReduce(r))
				if err != nil {
					// A reduce beyond the available resolution levels is a decode
					// error, not a colour-path panic; that is fine to skip.
					t.Skipf("decode r=%d: %v", r, err)
				}
				// Must not panic; ErrColorConvert for an unhandled layout is fine.
				if err := im.ConvertToRGB(); err != nil && !errors.Is(err, ErrColorConvert) {
					t.Fatalf("ConvertToRGB r=%d: %v", r, err)
				}
			})
		}
	}
}

// TestConvertToRGBICCIdempotent covers C9: ConvertToRGB must be idempotent for an
// embedded ICC profile. The profile is cleared after a successful apply, so a
// second call is a no-op that leaves the samples identical to after the first.
func TestConvertToRGBICCIdempotent(t *testing.T) {
	const path = "oracle/data/input/conformance/file5.jp2"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("missing ICC vector %s: %v", path, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(im.ICCProfile()) == 0 {
		t.Fatalf("expected an embedded ICC profile before conversion")
	}

	if err := im.ConvertToRGB(); err != nil {
		t.Fatalf("first ConvertToRGB: %v", err)
	}
	// C9: the profile must be cleared after a successful apply.
	if got := im.ICCProfile(); got != nil {
		t.Fatalf("ICC profile not cleared after apply (len=%d)", len(got))
	}

	// Snapshot the samples after the first conversion.
	nc := im.NumComponents()
	first := make([][]int32, nc)
	for c := 0; c < nc; c++ {
		d := im.Component(c).Data
		first[c] = append([]int32(nil), d...)
	}

	// A second conversion must be a no-op (no re-application of the transform).
	if err := im.ConvertToRGB(); err != nil {
		t.Fatalf("second ConvertToRGB: %v", err)
	}
	for c := 0; c < nc; c++ {
		d := im.Component(c).Data
		if len(d) != len(first[c]) {
			t.Fatalf("comp %d length changed on second convert: %d != %d", c, len(d), len(first[c]))
		}
		for k := range d {
			if d[k] != first[c][k] {
				t.Fatalf("comp %d sample %d changed on second convert: %d != %d",
					c, k, d[k], first[c][k])
			}
		}
	}
}

// TestConvertToRGBZeroComponentsNoPanic covers C4 (colour side): a 0-component
// image must return a clean error from ConvertToRGB and ApplyICCProfile rather
// than panicking on Comps[0].
func TestConvertToRGBZeroComponentsNoPanic(t *testing.T) {
	im := NewImage(ColorSpaceSRGB, 0, 0, 1, 1, nil)
	if im.NumComponents() != 0 {
		t.Fatalf("expected 0 components, got %d", im.NumComponents())
	}
	if err := im.ConvertToRGB(); err == nil {
		t.Fatalf("ConvertToRGB on 0-component image = nil, want error")
	} else if !errors.Is(err, ErrColorConvert) {
		t.Fatalf("ConvertToRGB on 0-component image = %v, want ErrColorConvert", err)
	}
	if err := im.ApplyICCProfile(); err == nil {
		t.Fatalf("ApplyICCProfile on 0-component image = nil, want error")
	}
}

// TestApplyICCProfileNoProfileSentinel covers C46: an image without an embedded
// ICC profile returns the distinct ErrNoICCProfile sentinel, not ErrICCApply.
func TestApplyICCProfileNoProfileSentinel(t *testing.T) {
	comps := []Component{{Dx: 1, Dy: 1, W: 2, H: 2, Prec: 8, Data: []int32{1, 2, 3, 4}}}
	im := NewImage(ColorSpaceGray, 0, 0, 2, 2, comps)
	err := im.ApplyICCProfile()
	if !errors.Is(err, ErrNoICCProfile) {
		t.Fatalf("ApplyICCProfile without profile = %v, want ErrNoICCProfile", err)
	}
	if errors.Is(err, ErrICCApply) {
		t.Fatalf("ErrNoICCProfile must be distinct from ErrICCApply")
	}
}

// TestToStandardCMYKErrors covers C45: ToStandard must refuse a 4-component CMYK
// or (s/e)YCC image (which is not an RGB(+alpha) image) and instruct the caller
// to run ConvertToRGB first, while the genuine 4-component RGBA case still works.
func TestToStandardCMYKErrors(t *testing.T) {
	mk := func(cs ColorSpace) *Image {
		comps := make([]Component, 4)
		for i := range comps {
			comps[i] = Component{Dx: 1, Dy: 1, W: 2, H: 2, Prec: 8, Data: []int32{10, 20, 30, 40}}
		}
		return NewImage(cs, 0, 0, 2, 2, comps)
	}

	for _, cs := range []ColorSpace{ColorSpaceCMYK, ColorSpaceSYCC, ColorSpaceEYCC} {
		if _, err := mk(cs).ToStandard(); err == nil {
			t.Fatalf("ToStandard on 4-component %v = nil error, want error", cs)
		}
	}

	// Genuine RGBA (sRGB with an alpha plane) must still render.
	if _, err := mk(ColorSpaceSRGB).ToStandard(); err != nil {
		t.Fatalf("ToStandard on genuine 4-component RGBA errored: %v", err)
	}
}
