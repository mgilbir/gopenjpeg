package dwt

import "testing"

// cdp2i64 mirrors opj_int64_ceildivpow2.
func cdp2i64(a int64, b int32) int32 {
	return int32((a + (int64(1) << b) - 1) >> b)
}

// buildPartialTile constructs a TileComponent with resolutions, bands, one
// precinct and one whole-band code-block per band (decoded_data filled
// deterministically), mirroring the partial_gen.c harness. is97 selects float32
// bit-pattern data.
func buildPartialTile(w, h uint32, x0, y0 int32, numres uint32, is97 bool) *TileComponent {
	tc := &TileComponent{
		X0: x0, Y0: y0, X1: x0 + int32(w), Y1: y0 + int32(h),
		Numresolutions:        numres,
		MinimumNumResolutions: numres,
		Resolutions:           make([]Resolution, numres),
	}
	seed := uint32(0xABCD1234)
	next := func() int32 {
		seed = seed*1103515245 + 12345
		if is97 {
			f := float32(int32(seed&0xFFFF)-32768) / 211.0
			return f32bits(f)
		}
		return int32(seed&0xFFFF) - 32768
	}
	x1 := x0 + int32(w)
	y1 := y0 + int32(h)
	for r := uint32(0); r < numres; r++ {
		level := numres - 1 - r
		res := &tc.Resolutions[r]
		res.X0 = int32(uintCeildivpow2(uint32(x0), level))
		res.Y0 = int32(uintCeildivpow2(uint32(y0), level))
		res.X1 = int32(uintCeildivpow2(uint32(x1), level))
		res.Y1 = int32(uintCeildivpow2(uint32(y1), level))
		res.Pw, res.Ph = 1, 1
		if r == 0 {
			res.Numbands = 1
		} else {
			res.Numbands = 3
		}
		for b := uint32(0); b < res.Numbands; b++ {
			band := &res.Bands[b]
			if r == 0 {
				band.Bandno = 0
				band.X0, band.Y0, band.X1, band.Y1 = res.X0, res.Y0, res.X1, res.Y1
			} else {
				band.Bandno = b + 1
				x0b := int64(band.Bandno & 1)
				y0b := int64(band.Bandno >> 1)
				band.X0 = cdp2i64(int64(x0)-(x0b<<level), int32(level+1))
				band.Y0 = cdp2i64(int64(y0)-(y0b<<level), int32(level+1))
				band.X1 = cdp2i64(int64(x1)-(x0b<<level), int32(level+1))
				band.Y1 = cdp2i64(int64(y1)-(y0b<<level), int32(level+1))
			}
			bw := int32(0)
			if band.X1 > band.X0 {
				bw = band.X1 - band.X0
			}
			bh := int32(0)
			if band.Y1 > band.Y0 {
				bh = band.Y1 - band.Y0
			}
			var cblks []CblkDec
			var cw, ch uint32
			if bw > 0 && bh > 0 {
				dd := make([]int32, int(bw)*int(bh))
				for i := range dd {
					dd[i] = next()
				}
				cblks = []CblkDec{{X0: band.X0, Y0: band.Y0, X1: band.X1, Y1: band.Y1, DecodedData: dd}}
				cw, ch = 1, 1
			}
			band.Precincts = []Precinct{{Cw: cw, Ch: ch, Cblks: cblks}}
		}
	}
	return tc
}

// setWindow sets the tile-component and tr_max window and (re)allocates DataWin.
func setWindow(tc *TileComponent, wx0, wy0, wx1, wy1 uint32) {
	tc.WinX0, tc.WinY0, tc.WinX1, tc.WinY1 = wx0, wy0, wx1, wy1
	trMax := &tc.Resolutions[tc.Numresolutions-1]
	trMax.WinX0, trMax.WinY0, trMax.WinX1, trMax.WinY1 = wx0, wy0, wx1, wy1
	tc.DataWin = make([]int32, (wx1-wx0)*(wy1-wy0))
}

// FuzzPartialWindow fuzzes the region-decode window parameters against a fixed
// tile, checking that neither the 5/3 nor 9/7 partial decode panics or reads
// out of bounds for any window within the tile.
func FuzzPartialWindow(f *testing.F) {
	const w, h = 32, 30
	const x0, y0 = 1, 1
	const numres = 3
	tc53 := buildPartialTile(w, h, x0, y0, numres, false)
	tc97 := buildPartialTile(w, h, x0, y0, numres, true)

	f.Add(uint32(0), uint32(0), uint32(w), uint32(h))
	f.Add(uint32(5), uint32(7), uint32(6), uint32(8))
	f.Add(uint32(w-1), uint32(0), uint32(w), uint32(h))

	f.Fuzz(func(t *testing.T, a, b, c, d uint32) {
		// Map fuzz inputs into a valid window within the tile extent
		// [x0,x0+w) x [y0,y0+h). Windows are in tile-component coordinates.
		lo, hi := uint32(x0), uint32(x0+w)
		wx0 := lo + a%(hi-lo)
		wx1 := lo + c%(hi-lo)
		if wx0 > wx1 {
			wx0, wx1 = wx1, wx0
		}
		if wx0 == wx1 {
			if wx1 < hi {
				wx1++
			} else {
				wx0--
			}
		}
		lo, hi = uint32(y0), uint32(y0+h)
		wy0 := lo + b%(hi-lo)
		wy1 := lo + d%(hi-lo)
		if wy0 > wy1 {
			wy0, wy1 = wy1, wy0
		}
		if wy0 == wy1 {
			if wy1 < hi {
				wy1++
			} else {
				wy0--
			}
		}

		setWindow(tc53, wx0, wy0, wx1, wy1)
		if !DecodePartialTile(tc53, numres) {
			t.Fatalf("DecodePartialTile failed for window %d,%d,%d,%d", wx0, wy0, wx1, wy1)
		}
		setWindow(tc97, wx0, wy0, wx1, wy1)
		if !DecodePartial97(tc97, numres) {
			t.Fatalf("DecodePartial97 failed for window %d,%d,%d,%d", wx0, wy0, wx1, wy1)
		}
	})
}
