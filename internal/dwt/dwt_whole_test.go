package dwt

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const (
	tEnc53 = 0
	tDec53 = 1
	tEnc97 = 2
	tDec97 = 3
)

type binReader struct {
	b   []byte
	pos int
}

func (r *binReader) u32() uint32 {
	v := binary.LittleEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v
}
func (r *binReader) i32() int32 { return int32(r.u32()) }
func (r *binReader) i32s(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(r.u32())
	}
	return out
}

func vectorPath(name string) string {
	return filepath.Join("..", "..", "testdata", "vectors", "dwt", name)
}

// TestWholeTileVectors replays the C-generated whole-tile DWT vectors and
// requires bit-exact equality (for 9/7, float32 words are compared by bits).
func TestWholeTileVectors(t *testing.T) {
	data, err := os.ReadFile(vectorPath("whole.bin"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	r := &binReader{b: data}
	ncases := r.u32()

	counts := map[uint32]int{}
	for ci := uint32(0); ci < ncases; ci++ {
		typ := r.u32()
		numres := r.u32()
		w := r.u32()
		h := r.u32()
		x0 := r.i32()
		y0 := r.i32()

		tc := &TileComponent{
			X0: x0, Y0: y0, X1: x0 + int32(w), Y1: y0 + int32(h),
			Numresolutions:        numres,
			MinimumNumResolutions: numres,
			Resolutions:           make([]Resolution, numres),
		}
		for ri := uint32(0); ri < numres; ri++ {
			res := &tc.Resolutions[ri]
			res.X0 = r.i32()
			res.Y0 = r.i32()
			res.X1 = r.i32()
			res.Y1 = r.i32()
			if ri == 0 {
				res.Numbands = 1
			} else {
				res.Numbands = 3
			}
		}
		n := int(w * h)
		input := r.i32s(n)
		want := r.i32s(n)

		tc.Data = make([]int32, n)
		copy(tc.Data, input)

		switch typ {
		case tEnc53:
			Encode(tc)
		case tDec53:
			DecodeTile(tc, numres)
		case tEnc97:
			EncodeReal(tc)
		case tDec97:
			DecodeTile97(tc, numres)
		default:
			t.Fatalf("case %d: unknown type %d", ci, typ)
		}
		counts[typ]++

		for i := 0; i < n; i++ {
			if tc.Data[i] != want[i] {
				t.Fatalf("case %d type=%d numres=%d %dx%d origin=(%d,%d) data[%d]: got %d want %d",
					ci, typ, numres, w, h, x0, y0, i, tc.Data[i], want[i])
			}
		}
	}
	if r.pos != len(data) {
		t.Fatalf("trailing bytes: pos=%d len=%d", r.pos, len(data))
	}
	t.Logf("enc53=%d dec53=%d enc97=%d dec97=%d",
		counts[tEnc53], counts[tDec53], counts[tEnc97], counts[tDec97])
}

// TestRoundTrip53 exercises the lossless property of the reversible transform:
// forward then inverse must reproduce the original tile exactly.
func TestRoundTrip53(t *testing.T) {
	sizes := []struct{ w, h uint32 }{
		{1, 1}, {5, 3}, {8, 8}, {9, 9}, {13, 7}, {16, 15}, {32, 33}, {31, 17},
	}
	origins := []struct{ x, y int32 }{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	seed := uint32(0x1234)
	next := func() int32 {
		seed = seed*1103515245 + 12345
		return int32(seed&0xFFFF) - 32768
	}
	for _, s := range sizes {
		for _, o := range origins {
			for numres := uint32(1); numres <= 5; numres++ {
				// 1x1 with >1 resolution level is degenerate (C reads OOB).
				if s.w == 1 && s.h == 1 && numres > 1 {
					continue
				}
				tc := makeTile(s.w, s.h, o.x, o.y, numres)
				// The 5/3 transform is only exactly invertible when no
				// processed resolution (res[1..numres-1]) collapses to a
				// zero-width or zero-height band; over-decomposed tiny tiles
				// lose the lossless property in the reference too. Skip those.
				if resolutionCollapses(tc, numres) {
					continue
				}
				orig := make([]int32, len(tc.Data))
				for i := range orig {
					orig[i] = next()
				}
				copy(tc.Data, orig)
				Encode(tc)
				DecodeTile(tc, numres)
				for i := range orig {
					if tc.Data[i] != orig[i] {
						t.Fatalf("roundtrip %dx%d origin=(%d,%d) numres=%d data[%d]: got %d want %d",
							s.w, s.h, o.x, o.y, numres, i, tc.Data[i], orig[i])
					}
				}
			}
		}
	}
}

// resolutionCollapses reports whether any processed resolution (res[1..numres-1])
// has a zero-width or zero-height extent.
func resolutionCollapses(tc *TileComponent, numres uint32) bool {
	for r := uint32(1); r < numres; r++ {
		res := tc.Resolutions[r]
		if res.X1 == res.X0 || res.Y1 == res.Y0 {
			return true
		}
	}
	return false
}

// makeTile builds a TileComponent with resolutions computed by the standard
// reduction (matching the C harness build_res).
func makeTile(w, h uint32, x0, y0 int32, numres uint32) *TileComponent {
	tc := &TileComponent{
		X0: x0, Y0: y0, X1: x0 + int32(w), Y1: y0 + int32(h),
		Numresolutions:        numres,
		MinimumNumResolutions: numres,
		Resolutions:           make([]Resolution, numres),
		Data:                  make([]int32, int(w*h)),
	}
	for r := uint32(0); r < numres; r++ {
		level := numres - 1 - r
		res := &tc.Resolutions[r]
		res.X0 = int32(uintCeildivpow2(uint32(x0), level))
		res.Y0 = int32(uintCeildivpow2(uint32(y0), level))
		res.X1 = int32(uintCeildivpow2(uint32(x0)+w, level))
		res.Y1 = int32(uintCeildivpow2(uint32(y0)+h, level))
		if r == 0 {
			res.Numbands = 1
		} else {
			res.Numbands = 3
		}
	}
	return tc
}
