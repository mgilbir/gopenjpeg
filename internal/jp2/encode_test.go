package jp2

import (
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// makeImage builds a test image with numcomps components of the given precision
// and sign, in the given colour space, sized w x h.
func makeImage(w, h, numcomps, prec, sgnd uint32, cs image.ColorSpace) *image.Image {
	im := &image.Image{
		X0: 0, Y0: 0, X1: w, Y1: h,
		Numcomps:   numcomps,
		ColorSpace: cs,
		Comps:      make([]image.Comp, numcomps),
	}
	for i := range im.Comps {
		im.Comps[i].Prec = prec
		im.Comps[i].Sgnd = sgnd
	}
	return im
}

// TestSetupEncoderBasics checks the JP2-box parameters derived from a uniform
// sRGB image. jp2.c opj_jp2_setup_encoder.
func TestSetupEncoderBasics(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	im := makeImage(64, 48, 3, 8, 0, image.ClrspcSRGB)
	if err := jp2.SetupEncoder(&EncoderParams{}, im, cap.mgr); err != nil {
		t.Fatalf("SetupEncoder: %v", err)
	}
	if jp2.brand != boxJP2 || jp2.numcl != 1 || jp2.cl[0] != boxJP2 {
		t.Fatalf("ftyp fields: brand=%x numcl=%d", jp2.brand, jp2.numcl)
	}
	if jp2.w != 64 || jp2.h != 48 || jp2.numcomps != 3 {
		t.Fatalf("dims: w=%d h=%d nc=%d", jp2.w, jp2.h, jp2.numcomps)
	}
	if jp2.bpc != 7 { // prec-1 = 7, sign 0
		t.Fatalf("bpc=%d", jp2.bpc)
	}
	if jp2.meth != 1 || jp2.enumcs != 16 {
		t.Fatalf("colr: meth=%d enumcs=%d", jp2.meth, jp2.enumcs)
	}
	for i := range jp2.comps {
		if jp2.comps[i].Bpcc != 7 {
			t.Fatalf("comp %d bpcc=%d", i, jp2.comps[i].Bpcc)
		}
	}
}

// TestSetupEncoderVariableDepthSetsBpc255 checks that differing component
// precisions force bpc=255 (and a bpcc box on write). jp2.c line ~1973.
func TestSetupEncoderVariableDepthSetsBpc255(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	im := makeImage(8, 8, 3, 8, 0, image.ClrspcSRGB)
	im.Comps[1].Prec = 10 // differs from comp 0
	if err := jp2.SetupEncoder(&EncoderParams{}, im, cap.mgr); err != nil {
		t.Fatalf("SetupEncoder: %v", err)
	}
	if jp2.bpc != 255 {
		t.Fatalf("bpc=%d want 255", jp2.bpc)
	}
}

// TestSetupEncoderBadNumcomps rejects an out-of-range component count. jp2.c
// line ~1925.
func TestSetupEncoderBadNumcomps(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	im := &image.Image{Numcomps: 0}
	if err := jp2.SetupEncoder(&EncoderParams{}, im, cap.mgr); err == nil {
		t.Fatal("expected error on 0 components")
	}
	if !cap.hasErr("Invalid number of components") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestSetupEncoderICC selects meth=2 and copies the ICC profile. jp2.c line
// ~1987.
func TestSetupEncoderICC(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	im := makeImage(8, 8, 3, 8, 0, image.ClrspcUnknown)
	im.ICCProfileLen = 4
	im.ICCProfileBuf = []byte{1, 2, 3, 4}
	if err := jp2.SetupEncoder(&EncoderParams{}, im, cap.mgr); err != nil {
		t.Fatalf("SetupEncoder: %v", err)
	}
	if jp2.meth != 2 || jp2.enumcs != 0 || jp2.color.ICCProfileLen != 4 {
		t.Fatalf("meth=%d enumcs=%d iccLen=%d", jp2.meth, jp2.enumcs, jp2.color.ICCProfileLen)
	}
}

// TestSetupEncoderAlphaCdef creates a cdef box for a single alpha channel. jp2.c
// line ~2050.
func TestSetupEncoderAlphaCdef(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	im := makeImage(8, 8, 4, 8, 0, image.ClrspcSRGB) // 3 colour + 1 alpha
	im.Comps[3].Alpha = 1
	if err := jp2.SetupEncoder(&EncoderParams{}, im, cap.mgr); err != nil {
		t.Fatalf("SetupEncoder: %v", err)
	}
	if jp2.color.Cdef == nil || jp2.color.Cdef.N != 4 {
		t.Fatalf("cdef not created: %+v", jp2.color.Cdef)
	}
	if jp2.color.Cdef.Info[3].Typ != 1 || jp2.color.Cdef.Info[3].Asoc != 0 {
		t.Fatalf("alpha channel not tagged: %+v", jp2.color.Cdef.Info[3])
	}
}

// TestEncodeRoundTrip writes a JP2 header (with an empty stub codestream) and
// re-parses it, verifying the box structure survives the write path including
// the jp2c length back-patch. jp2.c opj_jp2_start_compress / end_compress /
// write_jp2c.
func TestEncodeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name       string
		numcomps   uint32
		prec       uint32
		cs         image.ColorSpace
		wantEnumcs uint32
		wantBpc    uint32
	}{
		{"srgb8", 3, 8, image.ClrspcSRGB, 16, 7},
		{"gray12", 1, 12, image.ClrspcGray, 17, 11},
		{"cmyk8", 4, 8, image.ClrspcCMYK, 12, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, _ := newTestJP2()
			cap := newCapture()
			im := makeImage(16, 8, tc.numcomps, tc.prec, 0, tc.cs)
			if err := enc.SetupEncoder(&EncoderParams{}, im, cap.mgr); err != nil {
				t.Fatalf("SetupEncoder: %v", err)
			}

			out := cio.NewMemoryOutputStream()
			if err := enc.StartCompress(out, im, cap.mgr); err != nil {
				t.Fatalf("StartCompress: %v", err)
			}
			if err := enc.Encode(out, cap.mgr); err != nil { // stub: writes nothing
				t.Fatalf("Encode: %v", err)
			}
			if err := enc.EndCompress(out, cap.mgr); err != nil {
				t.Fatalf("EndCompress: %v", err)
			}
			if err := out.Flush(cap.mgr); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			// Re-parse.
			dec, _ := newTestJP2()
			f := factsAfterParse(t, dec, out.Bytes())
			if f.IHDR[1] != 16 || f.IHDR[0] != 8 || f.IHDR[2] != tc.numcomps {
				t.Fatalf("ihdr round-trip: %v", f.IHDR)
			}
			if f.IHDR[3] != tc.wantBpc {
				t.Fatalf("bpc round-trip: %d want %d", f.IHDR[3], tc.wantBpc)
			}
			if f.Colr[3] != tc.wantEnumcs {
				t.Fatalf("enumcs round-trip: %d want %d", f.Colr[3], tc.wantEnumcs)
			}
		})
	}
}

// factsAfterParse parses data through readHeaderProcedure and returns the facts.
func factsAfterParse(t *testing.T, jp2 *JP2, data []byte) headerFacts {
	t.Helper()
	cap := newCapture()
	stream := cio.NewMemoryInputStream(data)
	if !jp2.readHeaderProcedure(stream, cap.mgr) {
		t.Fatalf("re-parse failed: errs=%v", cap.errs)
	}
	return factsFromJP2(jp2)
}
