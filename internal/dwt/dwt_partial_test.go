package dwt

import (
	"os"
	"testing"
)

const (
	pT53 = 0
	pT97 = 1
)

// TestPartialVectors replays the C-generated region/partial DWT vectors and
// requires bit-exact equality of the reconstructed window.
func TestPartialVectors(t *testing.T) {
	data, err := os.ReadFile(vectorPath("partial.bin"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	r := &binReader{b: data}
	ncases := r.u32()

	n53, n97 := 0, 0
	for ci := uint32(0); ci < ncases; ci++ {
		typ := r.u32()
		numres := r.u32()
		x0 := r.i32()
		y0 := r.i32()
		x1 := r.i32()
		y1 := r.i32()
		wx0 := r.u32()
		wy0 := r.u32()
		wx1 := r.u32()
		wy1 := r.u32()

		tc := &TileComponent{
			X0: x0, Y0: y0, X1: x1, Y1: y1,
			Numresolutions:        numres,
			MinimumNumResolutions: numres,
			Resolutions:           make([]Resolution, numres),
			WinX0:                 wx0, WinY0: wy0, WinX1: wx1, WinY1: wy1,
		}
		for ri := uint32(0); ri < numres; ri++ {
			res := &tc.Resolutions[ri]
			res.X0 = r.i32()
			res.Y0 = r.i32()
			res.X1 = r.i32()
			res.Y1 = r.i32()
			res.Numbands = r.u32()
			res.Pw = 1
			res.Ph = 1
			for bi := uint32(0); bi < res.Numbands; bi++ {
				band := &res.Bands[bi]
				band.Bandno = r.u32()
				band.X0 = r.i32()
				band.Y0 = r.i32()
				band.X1 = r.i32()
				band.Y1 = r.i32()
				dl := r.u32()
				var cblks []CblkDec
				var cw, ch uint32
				if dl > 0 {
					cblks = []CblkDec{{
						X0: band.X0, Y0: band.Y0, X1: band.X1, Y1: band.Y1,
						DecodedData: r.i32s(int(dl)),
					}}
					cw, ch = 1, 1
				}
				band.Precincts = []Precinct{{Cw: cw, Ch: ch, Cblks: cblks}}
			}
		}
		// tr_max window equals tilec window (level 0 coords == tile-comp coords).
		trMax := &tc.Resolutions[numres-1]
		trMax.WinX0, trMax.WinY0, trMax.WinX1, trMax.WinY1 = wx0, wy0, wx1, wy1

		winW := wx1 - wx0
		winH := wy1 - wy0
		want := r.i32s(int(winW * winH))
		tc.DataWin = make([]int32, winW*winH)

		var ok bool
		switch typ {
		case pT53:
			ok = DecodePartialTile(tc, numres)
			n53++
		case pT97:
			ok = DecodePartial97(tc, numres)
			n97++
		default:
			t.Fatalf("case %d: unknown type %d", ci, typ)
		}
		if !ok {
			t.Fatalf("case %d type=%d: decode returned false", ci, typ)
		}
		for i := range want {
			if tc.DataWin[i] != want[i] {
				t.Fatalf("case %d type=%d numres=%d tile(%d,%d,%d,%d) win(%d,%d,%d,%d) dataWin[%d]: got %d want %d",
					ci, typ, numres, x0, y0, x1, y1, wx0, wy0, wx1, wy1, i, tc.DataWin[i], want[i])
			}
		}
	}
	if r.pos != len(data) {
		t.Fatalf("trailing bytes: pos=%d len=%d", r.pos, len(data))
	}
	t.Logf("partial 53=%d 97=%d", n53, n97)
}
