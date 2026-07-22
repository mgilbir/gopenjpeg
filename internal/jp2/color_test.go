package jp2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// --- checkColor security paths ---------------------------------------------

func img1(numcomps uint32) *image.Image {
	im := &image.Image{Numcomps: numcomps, Comps: make([]image.Comp, numcomps)}
	for i := range im.Comps {
		im.Comps[i].W = 1
		im.Comps[i].H = 1
		im.Comps[i].Data = []int32{0}
	}
	return im
}

// TestCheckColorCdefOutOfRange: a cdef whose cn >= nr_channels is rejected.
// jp2.c line ~912.
func TestCheckColorCdefOutOfRange(t *testing.T) {
	im := img1(3)
	color := &Color{Cdef: &Cdef{N: 1, Info: []CdefInfo{{Cn: 5, Typ: 0, Asoc: 1}}}}
	cap := newCapture()
	if checkColor(im, color, cap.mgr) {
		t.Fatal("expected checkColor failure")
	}
	if !cap.hasErr("Invalid component index") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCheckColorCdefIncomplete: cdef must list every channel (issue 397).
// jp2.c line ~937.
func TestCheckColorCdefIncomplete(t *testing.T) {
	im := img1(3)
	// Only defines channels 0 and 1; channel 2 missing -> incomplete.
	color := &Color{Cdef: &Cdef{N: 2, Info: []CdefInfo{
		{Cn: 0, Typ: 0, Asoc: 1},
		{Cn: 1, Typ: 0, Asoc: 2},
	}}}
	cap := newCapture()
	if checkColor(im, color, cap.mgr) {
		t.Fatal("expected checkColor failure")
	}
	if !cap.hasErr("Incomplete channel definitions") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCheckColorCmapCompOutOfRange: cmap.cmp must reference an existing
// component. jp2.c line ~953.
func TestCheckColorCmapCompOutOfRange(t *testing.T) {
	im := img1(1)
	color := &Color{Pclr: &Pclr{
		NrChannels: 1,
		Cmap:       []CmapComp{{Cmp: 9, Mtyp: 1, Pcol: 0}},
	}}
	cap := newCapture()
	if checkColor(im, color, cap.mgr) {
		t.Fatal("expected failure")
	}
	if !cap.hasErr("Invalid component index") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCheckColorCmapInvalidMtyp: MTYP must be 0 or 1. jp2.c line ~970.
func TestCheckColorCmapInvalidMtyp(t *testing.T) {
	im := img1(3)
	color := &Color{Pclr: &Pclr{
		NrChannels: 1,
		Cmap:       []CmapComp{{Cmp: 0, Mtyp: 7, Pcol: 0}},
	}}
	cap := newCapture()
	if checkColor(im, color, cap.mgr) {
		t.Fatal("expected failure")
	}
	if !cap.hasErr("Invalid value for cmap") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCheckColorCmapMappedTwice: a palette column targeted twice is rejected.
// jp2.c line ~980.
func TestCheckColorCmapMappedTwice(t *testing.T) {
	im := img1(3)
	// Two palette-mapping channels both using pcol=0 -> duplicate. The second
	// also violates pcol==i, but the "mapped twice" branch fires for pcol reuse.
	color := &Color{Pclr: &Pclr{
		NrChannels: 2,
		Cmap: []CmapComp{
			{Cmp: 0, Mtyp: 1, Pcol: 0},
			{Cmp: 1, Mtyp: 1, Pcol: 0},
		},
	}}
	cap := newCapture()
	if checkColor(im, color, cap.mgr) {
		t.Fatal("expected failure")
	}
	if !cap.hasErr("mapped twice") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCheckColorPcolImplLimitation: palette mapping requires pcol==i. jp2.c
// line ~991.
func TestCheckColorPcolImplLimitation(t *testing.T) {
	im := img1(3)
	color := &Color{Pclr: &Pclr{
		NrChannels: 2,
		Cmap: []CmapComp{
			{Cmp: 0, Mtyp: 1, Pcol: 1}, // pcol != i (0)
			{Cmp: 1, Mtyp: 1, Pcol: 0},
		},
	}}
	cap := newCapture()
	if checkColor(im, color, cap.mgr) {
		t.Fatal("expected failure")
	}
	if !cap.hasErr("Implementation limitation") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCheckColorWeirdCmapCorrected: for a single-component image with an
// all-unused mapping, checkColor warns and self-corrects (issue 235/447).
// jp2.c line ~1009.
func TestCheckColorWeirdCmapCorrected(t *testing.T) {
	im := img1(1)
	color := &Color{Pclr: &Pclr{
		NrChannels: 3,
		// All direct-use with pcol 0 -> pcolUsage[1],[2] never set for numcomps==1.
		Cmap: []CmapComp{
			{Cmp: 0, Mtyp: 0, Pcol: 0},
			{Cmp: 0, Mtyp: 0, Pcol: 0},
			{Cmp: 0, Mtyp: 0, Pcol: 0},
		},
	}}
	cap := newCapture()
	if !checkColor(im, color, cap.mgr) {
		t.Fatalf("expected success after correction, errs=%v", cap.errs)
	}
	if !cap.hasWarn("Trying to correct") {
		t.Fatalf("expected correction warning, warns=%v", cap.warns)
	}
	// After correction every channel is palette-mapped to its own column.
	for i, c := range color.Pclr.Cmap {
		if c.Mtyp != 1 || int(c.Pcol) != i {
			t.Fatalf("channel %d not corrected: %+v", i, c)
		}
	}
}

// --- applyPclr -------------------------------------------------------------

// TestApplyPclrExpansionAndClamp exercises palette expansion including the
// signed-index clamp into [0, top_k]. jp2.c line ~1105.
func TestApplyPclrExpansionAndClamp(t *testing.T) {
	im := &image.Image{Numcomps: 1, Comps: []image.Comp{{
		W: 3, H: 1, Prec: 8, Data: []int32{-4, 0, 99}, // indices: negative and >top_k
	}}}
	color := &Color{Pclr: &Pclr{
		NrEntries:   2,
		NrChannels:  3,
		ChannelSize: []byte{8, 8, 8},
		ChannelSign: []byte{0, 0, 0},
		Entries:     []uint32{10, 11, 12, 20, 21, 22}, // [entry0: 10,11,12][entry1: 20,21,22]
		Cmap: []CmapComp{
			{Cmp: 0, Mtyp: 1, Pcol: 0},
			{Cmp: 0, Mtyp: 1, Pcol: 1},
			{Cmp: 0, Mtyp: 1, Pcol: 2},
		},
	}}
	cap := newCapture()
	if !applyPclr(im, color, cap.mgr) {
		t.Fatalf("applyPclr failed: %v", cap.errs)
	}
	if im.Numcomps != 3 {
		t.Fatalf("numcomps=%d", im.Numcomps)
	}
	// index sequence clamps to [0,0,1]; column c yields entries[k*3+c].
	want := [][]int32{
		{10, 10, 20}, // column 0
		{11, 11, 21}, // column 1
		{12, 12, 22}, // column 2
	}
	for c := 0; c < 3; c++ {
		for j := 0; j < 3; j++ {
			if im.Comps[c].Data[j] != want[c][j] {
				t.Errorf("comp %d [%d] = %d, want %d", c, j, im.Comps[c].Data[j], want[c][j])
			}
		}
		if im.Comps[c].Prec != 8 {
			t.Errorf("comp %d prec=%d", c, im.Comps[c].Prec)
		}
	}
}

// --- applyCdef -------------------------------------------------------------

// TestApplyCdefSwapAndAlpha checks channel reordering and alpha tagging.
// jp2.c line ~1329.
func TestApplyCdefSwapAndAlpha(t *testing.T) {
	// Mark components by their W so swaps are observable.
	im := &image.Image{Numcomps: 3, Comps: []image.Comp{
		{W: 100}, {W: 200}, {W: 300},
	}}
	color := &Color{Cdef: &Cdef{N: 3, Info: []CdefInfo{
		{Cn: 0, Typ: 0, Asoc: 3}, // swap comp0 <-> comp2 (acn=2)
		{Cn: 1, Typ: 1, Asoc: 0}, // comp1 = opacity, whole image
		{Cn: 2, Typ: 0, Asoc: 1}, // after the first swap this refers to old comp0
	}}}
	cap := newCapture()
	applyCdef(im, color, cap.mgr)

	if im.Comps[0].W != 300 || im.Comps[2].W != 100 {
		t.Fatalf("swap failed: W0=%d W2=%d", im.Comps[0].W, im.Comps[2].W)
	}
	if im.Comps[1].Alpha != 1 {
		t.Fatalf("alpha not tagged: %d", im.Comps[1].Alpha)
	}
	if color.Cdef != nil {
		t.Fatal("cdef should be cleared after apply")
	}
}

// --- end-to-end ReadHeader colour-space and ICC transfer -------------------

// readWithStubImage runs the full ReadHeader over a real file, with a stub codec
// that returns a minimal image so the colour-space mapping and ICC transfer run.
func readWithStubImage(t *testing.T, name string) (*JP2, *image.Image) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	sc := &stubCodec{readHeaderImage: &image.Image{Numcomps: 1, Comps: make([]image.Comp, 1)}}
	jp2 := Create(sc, true)
	stream := cio.NewMemoryInputStream(data)
	img, err := jp2.ReadHeader(stream, newCapture().mgr)
	if err != nil {
		t.Fatalf("ReadHeader(%s): %v", name, err)
	}
	return jp2, img
}

// TestReadHeaderColorSpaceSRGB: enumcs 16 maps to sRGB. jp2.c line ~2884.
func TestReadHeaderColorSpaceSRGB(t *testing.T) {
	_, img := readWithStubImage(t, "issue458.jp2")
	if img.ColorSpace != image.ClrspcSRGB {
		t.Fatalf("colorspace=%d want sRGB", img.ColorSpace)
	}
}

// TestReadHeaderColorSpaceEYCC: enumcs 24 maps to e-YCC. jp2.c line ~2890.
func TestReadHeaderColorSpaceEYCC(t *testing.T) {
	_, img := readWithStubImage(t, "issue725.jp2")
	if img.ColorSpace != image.ClrspcEYCC {
		t.Fatalf("colorspace=%d want EYCC", img.ColorSpace)
	}
}

// TestReadHeaderColorSpaceUnknown: an ignored colr (meth 4) leaves the image
// colour space UNKNOWN. jp2.c line ~2895.
func TestReadHeaderColorSpaceUnknown(t *testing.T) {
	_, img := readWithStubImage(t, "issue211.jp2")
	if img.ColorSpace != image.ClrspcUnknown {
		t.Fatalf("colorspace=%d want UNKNOWN", img.ColorSpace)
	}
}

// TestReadHeaderICCTransfer: an ICC profile is transferred to the image and the
// JP2 collector releases its copy. jp2.c line ~2898.
func TestReadHeaderICCTransfer(t *testing.T) {
	jp2, img := readWithStubImage(t, "relax.jp2")
	if img.ICCProfileLen != 278 || len(img.ICCProfileBuf) != 278 {
		t.Fatalf("icc len=%d buf=%d", img.ICCProfileLen, len(img.ICCProfileBuf))
	}
	if jp2.color.ICCProfileBuf != nil {
		t.Fatal("jp2 should have released its ICC buffer after transfer")
	}
}
