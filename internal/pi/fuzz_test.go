package pi

import (
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// cursor is a tiny bounded reader over the fuzz input.
type cursor struct {
	b []byte
	i int
}

func (c *cursor) u8() uint32 {
	if c.i >= len(c.b) {
		return 0
	}
	v := c.b[c.i]
	c.i++
	return uint32(v)
}

// FuzzPiNext feeds fuzzed but bounded coding parameters through the decode and
// encode packet iterators, asserting the iterators never panic or index out of
// bounds and always terminate. Every geometry is kept small so the include
// array and packet counts stay bounded.
func FuzzPiNext(f *testing.F) {
	f.Add([]byte{0, 1, 1, 0, 1, 1, 1, 0})
	f.Add([]byte{2, 3, 4, 2, 5, 6, 3, 3})
	f.Add([]byte{4, 2, 2, 1, 3, 8, 2, 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		c := &cursor{b: data}

		prg := cparams.ProgOrder(c.u8() % 5) // 0..4
		numcomps := 1 + c.u8()%4             // 1..4
		numres := 1 + c.u8()%6               // 1..6
		numlayers := 1 + c.u8()%5            // 1..5
		tw := 1 + c.u8()%3                   // 1..3
		th := 1 + c.u8()%3                   // 1..3
		prcExp := 3 + c.u8()%13              // 3..15 (keeps precinct count small)
		sub := c.u8()%2 == 1                 // subsample chroma

		// Image dimensions, bounded and non-degenerate.
		w := 8 + c.u8()%56 // 8..63
		h := 8 + c.u8()%56
		x0 := c.u8() % 4
		y0 := c.u8() % 4
		x1 := x0 + w
		y1 := y0 + h

		tileno := c.u8() % (tw * th)

		img := &image.Image{
			X0: x0, Y0: y0, X1: x1, Y1: y1,
			Numcomps: numcomps,
			Comps:    make([]image.Comp, numcomps),
		}
		for i := uint32(0); i < numcomps; i++ {
			img.Comps[i].Dx = 1
			img.Comps[i].Dy = 1
			if sub && i > 0 {
				img.Comps[i].Dx = 2
				img.Comps[i].Dy = 2
			}
		}

		// Tile grid must cover the image extent.
		tdx := (x1 - x0 + tw - 1) / tw
		tdy := (y1 - y0 + th - 1) / th
		if tdx == 0 {
			tdx = 1
		}
		if tdy == 0 {
			tdy = 1
		}

		cp := &cparams.CP{
			Rsiz: cparams.ProfileNone,
			Tx0:  x0, Ty0: y0, Tdx: tdx, Tdy: tdy,
			Tw: tw, Th: th,
			Tcps: make([]cparams.TCP, tw*th),
		}
		tcp := &cp.Tcps[tileno]
		tcp.Prg = prg
		tcp.Numlayers = numlayers
		tcp.NumLayersToDecode = numlayers
		tcp.TCCPs = make([]cparams.TCCP, numcomps)
		for comp := uint32(0); comp < numcomps; comp++ {
			tccp := &tcp.TCCPs[comp]
			tccp.Numresolutions = numres
			for r := uint32(0); r < cparams.MaxRLvls; r++ {
				tccp.Prcw[r] = prcExp
				tccp.Prch[r] = prcExp
			}
		}

		const capIters = 1 << 20

		if pis := CreateDecode(img, cp, tileno, nil); pis != nil {
			for pino := uint32(0); pino <= tcp.Numpocs; pino++ {
				cur := &pis[pino]
				n := 0
				for cur.Next() {
					n++
					if n > capIters {
						t.Fatal("decode iterator did not terminate")
					}
				}
			}
		}

		if pis := InitialiseEncode(img, cp, tileno, cparams.FinalPass, nil); pis != nil {
			for pino := uint32(0); pino <= tcp.Numpocs; pino++ {
				CreateEncode(pis, cp, tileno, pino, 0, 0, cparams.FinalPass)
				cur := &pis[pino]
				n := 0
				for cur.Next() {
					n++
					if n > capIters {
						t.Fatal("encode iterator did not terminate")
					}
				}
			}
		}
	})
}
