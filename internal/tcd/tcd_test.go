package tcd

import (
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// buildCP builds a minimal single-tile, single-component CP/image for geometry
// tests: a 64x64 image, one 5/3 component with 3 resolutions.
func buildCP() (*image.Image, *cparams.CP) {
	img := &image.Image{X0: 0, Y0: 0, X1: 64, Y1: 64, Numcomps: 1}
	img.Comps = []image.Comp{{Dx: 1, Dy: 1, Prec: 8, Sgnd: 0, W: 64, H: 64}}
	cp := &cparams.CP{Tx0: 0, Ty0: 0, Tdx: 64, Tdy: 64, Tw: 1, Th: 1}
	tccp := cparams.TCCP{Numresolutions: 3, Cblkw: 6, Cblkh: 6, Qmfbid: 1, Qntsty: cparams.CCPQntStyNoQnt, Numgbits: 2}
	for i := range tccp.Prcw {
		tccp.Prcw[i] = 15
		tccp.Prch[i] = 15
	}
	tcp := cparams.TCP{Numlayers: 1, NumLayersToDecode: 1, TCCPs: []cparams.TCCP{tccp}}
	cp.Tcps = []cparams.TCP{tcp}
	return img, cp
}

func TestInitDecodeTileGeometry(t *testing.T) {
	img, cp := buildCP()
	tc := Create(true)
	if !tc.Init(img, cp) {
		t.Fatal("Init failed")
	}
	if err := tc.InitDecodeTile(0, nil); err != nil {
		t.Fatalf("InitDecodeTile: %v", err)
	}
	tile := tc.tile()
	if tile.X0 != 0 || tile.X1 != 64 || tile.Y1 != 64 {
		t.Fatalf("tile geometry: %+v", tile)
	}
	comp := &tile.Comps[0]
	if comp.Numresolutions != 3 {
		t.Fatalf("numresolutions = %d, want 3", comp.Numresolutions)
	}
	// Highest resolution must span the full tile-component.
	res := &comp.Resolutions[2]
	if res.X1-res.X0 != 64 || res.Y1-res.Y0 != 64 {
		t.Fatalf("res[2] = %dx%d, want 64x64", res.X1-res.X0, res.Y1-res.Y0)
	}
	// Lowest resolution level must be a quarter size (2 decompositions).
	res0 := &comp.Resolutions[0]
	if res0.X1-res0.X0 != 16 {
		t.Fatalf("res[0] width = %d, want 16", res0.X1-res0.X0)
	}
	// Tag trees must be allocated (never nil) for tier-2.
	for ri := range comp.Resolutions {
		r := &comp.Resolutions[ri]
		for bi := uint32(0); bi < r.Numbands; bi++ {
			for pi := range r.Bands[bi].Precincts {
				p := &r.Bands[bi].Precincts[pi]
				if p.Incltree == nil || p.Imsbtree == nil {
					t.Fatalf("res %d band %d precinct %d has nil tag tree", ri, bi, pi)
				}
			}
		}
	}
}

func TestInitEncodeTile(t *testing.T) {
	img, cp := buildCP()
	tc := Create(false)
	tc.Init(img, cp)
	if err := tc.InitEncodeTile(0, nil); err != nil {
		t.Fatalf("InitEncodeTile: %v", err)
	}
}
