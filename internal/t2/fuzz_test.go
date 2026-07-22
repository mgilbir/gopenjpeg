package t2

import (
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/tgt"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// buildFuzzDecodeTile constructs a small mirror decode tile (1 component, 2
// resolutions, 1x1 code-block per precinct) for the packet-header fuzzer.
func buildFuzzDecodeTile() *tile.Tile {
	const numcomps, numres = 1, 2
	tl := &tile.Tile{
		X0: 0, Y0: 0, X1: 64, Y1: 64,
		Numcomps: numcomps,
		Comps:    make([]tile.TileComp, numcomps),
	}
	tc := &tl.Comps[0]
	tc.Numresolutions = numres
	tc.MinimumNumResolutions = numres
	tc.X1, tc.Y1 = 64, 64
	tc.Resolutions = make([]tile.Resolution, numres)
	for r := uint32(0); r < numres; r++ {
		res := &tc.Resolutions[r]
		res.Pw, res.Ph = 1, 1
		if r == 0 {
			res.Numbands = 1
		} else {
			res.Numbands = 3
		}
		res.X1, res.Y1 = 8, 8
		for b := uint32(0); b < res.Numbands; b++ {
			band := &res.Bands[b]
			if r == 0 {
				band.Bandno = 0
			} else {
				band.Bandno = b + 1
			}
			band.X1, band.Y1 = 8, 8
			band.Numbps = 8
			band.Precincts = make([]tile.Precinct, 1)
			prc := &band.Precincts[0]
			prc.X1, prc.Y1 = 8, 8
			prc.Cw, prc.Ch = 1, 1
			prc.Incltree, _ = tgt.Create(1, 1, nil)
			prc.Imsbtree, _ = tgt.Create(1, 1, nil)
			prc.CblksDec = make([]tile.CblkDec, 1)
			prc.CblksDec[0].X1, prc.CblksDec[0].Y1 = 8, 8
		}
	}
	return tl
}

func buildFuzzCP(csty, cblksty uint32) *cparams.CP {
	cp := &cparams.CP{
		Rsiz: cparams.ProfileNone,
		Tx0:  0, Ty0: 0, Tdx: 64, Tdy: 64,
		Tw: 1, Th: 1,
		Strict: false,
		Tcps:   make([]cparams.TCP, 1),
	}
	tcp := &cp.Tcps[0]
	tcp.Csty = csty
	tcp.Prg = cparams.LRCP
	tcp.Numlayers = 3
	tcp.NumLayersToDecode = 3
	tcp.TCCPs = make([]cparams.TCCP, 1)
	tccp := &tcp.TCCPs[0]
	tccp.Numresolutions = 2
	tccp.Cblksty = cblksty
	tccp.Qmfbid = 1
	for r := uint32(0); r < cparams.MaxRLvls; r++ {
		tccp.Prcw[r] = 15
		tccp.Prch[r] = 15
	}
	return cp
}

// FuzzDecodePackets feeds arbitrary bytes to the tier-2 packet decoder (which
// exercises opj_t2_read_packet_header / read_packet_data over untrusted input),
// asserting it never panics or indexes out of bounds. The first two bytes pick
// the coding-style / code-block-style so SOP/EPH/HT header paths are all reached.
func FuzzDecodePackets(f *testing.F) {
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{0x06, 0x00, 0xff, 0x91, 0x00, 0x04, 0x00, 0x00, 0x80})
	f.Add([]byte{0x00, 0x40, 0xc0, 0x00, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		var csty, cblksty uint32
		src := data
		if len(src) > 0 {
			csty = uint32(src[0]) & (cparams.CPCstySOP | cparams.CPCstyEPH)
			src = src[1:]
		}
		if len(src) > 0 {
			cblksty = uint32(src[0])
			src = src[1:]
		}

		img := &image.Image{
			X0: 0, Y0: 0, X1: 64, Y1: 64,
			Numcomps: 1,
			Comps:    []image.Comp{{Dx: 1, Dy: 1}},
		}
		cp := buildFuzzCP(csty, cblksty)
		t2 := Create(img, cp)
		decTile := buildFuzzDecodeTile()

		// Must not panic regardless of input. Return values are ignored.
		_, _ = t2.DecodePackets(WholeTileAOI{}, 0, decTile, src, uint32(len(src)), nil)
	})
}
