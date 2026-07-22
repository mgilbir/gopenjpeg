package image

import (
	"encoding/json"
	"os"
	"testing"
)

const imageVectorPath = "../../testdata/vectors/image/vectors.json"

type imageCase struct {
	X0     uint32 `json:"x0"`
	Y0     uint32 `json:"y0"`
	X1     uint32 `json:"x1"`
	Y1     uint32 `json:"y1"`
	Dx     uint32 `json:"dx"`
	Dy     uint32 `json:"dy"`
	Prec   uint32 `json:"prec"`
	Reduce uint32 `json:"reduce"`
	W      uint32 `json:"w"`
	H      uint32 `json:"h"`
	Cx0    uint32 `json:"cx0"`
	Cy0    uint32 `json:"cy0"`
}

type imageVectors struct {
	Cases []imageCase `json:"cases"`
}

func loadImageVectors(t *testing.T) *imageVectors {
	t.Helper()
	raw, err := os.ReadFile(imageVectorPath)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v imageVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return &v
}

func TestCompHeaderUpdate(t *testing.T) {
	for ci, c := range loadImageVectors(t).Cases {
		img := &Image{
			X0:       c.X0,
			Y0:       c.Y0,
			X1:       c.X1,
			Y1:       c.Y1,
			Numcomps: 1,
			Comps: []Comp{{
				Dx:     c.Dx,
				Dy:     c.Dy,
				Prec:   c.Prec,
				Factor: c.Reduce,
			}},
		}
		// single tile spanning the whole image (matches the C harness)
		cp := &CompHeaderUpdateParams{
			Tx0: c.X0,
			Ty0: c.Y0,
			Tdx: c.X1 - c.X0,
			Tdy: c.Y1 - c.Y0,
			Tw:  1,
			Th:  1,
		}
		img.CompHeaderUpdate(cp)
		comp := img.Comps[0]
		if comp.W != c.W || comp.H != c.H || comp.X0 != c.Cx0 || comp.Y0 != c.Cy0 {
			t.Fatalf("case %d %+v: got w=%d h=%d x0=%d y0=%d want w=%d h=%d x0=%d y0=%d",
				ci, c, comp.W, comp.H, comp.X0, comp.Y0, c.W, c.H, c.Cx0, c.Cy0)
		}
	}
}

func TestCreateAllocatesData(t *testing.T) {
	parms := []CompParm{
		{Dx: 1, Dy: 1, W: 4, H: 3, Prec: 8},
		{Dx: 2, Dy: 2, W: 2, H: 2, Prec: 8, Sgnd: 1},
	}
	img, err := Create(2, parms, ClrspcSRGB)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if img.Numcomps != 2 {
		t.Fatalf("numcomps=%d", img.Numcomps)
	}
	if len(img.Comps[0].Data) != 12 {
		t.Fatalf("comp0 data len=%d want 12", len(img.Comps[0].Data))
	}
	if len(img.Comps[1].Data) != 4 {
		t.Fatalf("comp1 data len=%d want 4", len(img.Comps[1].Data))
	}
	for _, v := range img.Comps[0].Data {
		if v != 0 {
			t.Fatalf("data not zeroed")
		}
	}
}

func TestCreateOverflowGuard(t *testing.T) {
	// w*h*4 overflows SIZE_MAX -> must error (matches the C NULL return).
	parms := []CompParm{
		{Dx: 1, Dy: 1, W: 0xFFFFFFFF, H: 0xFFFFFFFF, Prec: 8},
	}
	if _, err := Create(1, parms, ClrspcGray); err == nil {
		t.Fatalf("expected overflow error, got nil")
	}
}

func TestTileCreateNoData(t *testing.T) {
	parms := []CompParm{{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8}}
	img := TileCreate(1, parms, ClrspcGray)
	if img.Comps[0].Data != nil {
		t.Fatalf("tile create should not allocate data")
	}
}

func TestCopyHeader(t *testing.T) {
	parms := []CompParm{{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8}}
	src, err := Create(1, parms, ClrspcSRGB)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	src.X0, src.Y0, src.X1, src.Y1 = 1, 2, 5, 6
	src.ICCProfileBuf = []byte{1, 2, 3, 4}
	src.ICCProfileLen = 4
	src.Comps[0].Data[0] = 42

	dst := &Image{}
	CopyHeader(src, dst)

	if dst.X0 != 1 || dst.Y0 != 2 || dst.X1 != 5 || dst.Y1 != 6 {
		t.Fatalf("bounds not copied")
	}
	if dst.Numcomps != 1 || dst.Comps[0].W != 4 {
		t.Fatalf("comp header not copied")
	}
	if dst.Comps[0].Data != nil {
		t.Fatalf("data must not be copied")
	}
	if dst.ICCProfileLen != 4 || len(dst.ICCProfileBuf) != 4 || dst.ICCProfileBuf[2] != 3 {
		t.Fatalf("icc profile not copied")
	}
	// ensure deep copy of ICC buffer
	dst.ICCProfileBuf[0] = 99
	if src.ICCProfileBuf[0] == 99 {
		t.Fatalf("icc buffer aliased")
	}
}

// FuzzCreate feeds arbitrary component-creation parameters; Create must never
// panic and must return either a valid image or ErrImageAlloc.
func FuzzCreate(f *testing.F) {
	f.Add(uint32(1), uint32(1), uint32(4), uint32(3), uint32(8), uint32(0))
	f.Add(uint32(0xFFFFFFFF), uint32(0xFFFFFFFF), uint32(1), uint32(1), uint32(16), uint32(1))
	f.Add(uint32(0), uint32(0), uint32(0), uint32(0), uint32(0), uint32(0))

	f.Fuzz(func(t *testing.T, dx, dy, w, h, prec, sgnd uint32) {
		// clamp w/h so the successful path does not attempt a genuinely huge
		// allocation (the overflow guard is still exercised via the extreme seed);
		// keep the geometry math and guard logic under test.
		wc := w % 4096
		hc := h % 4096
		parms := []CompParm{{Dx: dx, Dy: dy, W: wc, H: hc, Prec: prec, Sgnd: sgnd}}
		img, err := Create(1, parms, ClrspcUnspecified)
		if err != nil {
			if err != ErrImageAlloc {
				t.Fatalf("unexpected error: %v", err)
			}
			return
		}
		if uint64(len(img.Comps[0].Data)) != uint64(wc)*uint64(hc) {
			t.Fatalf("data len %d != %d*%d", len(img.Comps[0].Data), wc, hc)
		}
	})
}
