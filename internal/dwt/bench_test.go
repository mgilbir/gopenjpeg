package dwt

import "testing"

// benchTile builds a 1024x1024, 6-level tile with deterministic coefficients.
func benchTile() ([]int32, *TileComponent) {
	const w, h = 1024, 1024
	const numres = 6
	tc := makeTile(w, h, 0, 0, numres)
	seed := uint32(0x2468ACE0)
	orig := make([]int32, len(tc.Data))
	for i := range orig {
		seed = seed*1103515245 + 12345
		orig[i] = int32(seed) >> 8
	}
	return orig, tc
}

// BenchmarkDecodeTile53 benchmarks the inverse 5/3 transform on a 1024x1024 tile.
func BenchmarkDecodeTile53(b *testing.B) {
	orig, tc := benchTile()
	b.SetBytes(int64(len(orig)) * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		copy(tc.Data, orig)
		b.StartTimer()
		DecodeTile(tc, tc.Numresolutions, 1)
	}
}

// BenchmarkDecodeTile97 benchmarks the inverse 9/7 transform on a 1024x1024 tile.
func BenchmarkDecodeTile97(b *testing.B) {
	orig, tc := benchTile()
	b.SetBytes(int64(len(orig)) * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		copy(tc.Data, orig)
		b.StartTimer()
		DecodeTile97(tc, tc.Numresolutions, 1)
	}
}

// BenchmarkDecodePartial97 benchmarks the region-constrained inverse 9/7
// transform: a 256x256 window of a 1024x1024, 6-level tile. It is the shape that
// exposes the cost of the int32<->float32 round trip between the sparse array
// and the lifting scratch, which is per-group work independent of the window.
func BenchmarkDecodePartial97(b *testing.B) {
	const w, h = 1024, 1024
	const numres = 6
	tc := buildPartialTile(w, h, 0, 0, numres, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setWindow(tc, 384, 384, 640, 640)
		if !DecodePartial97(tc, numres) {
			b.Fatal("DecodePartial97 failed")
		}
	}
}
