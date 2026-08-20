package gopenjpeg

import (
	"bytes"
	"testing"
)

// TestDecodeAllocsBounded pins the decode allocation budget: the tier-1/tier-2
// per-code-block state (tag-tree stacks, the CodeBlockDec mapping, Segs/Chunks
// backing) must not allocate per code-block. The stream below holds 128
// code-blocks; the ceiling is far above the fixed per-decode cost (~150
// allocations) but far below one allocation per block, so any reintroduced
// per-block allocation fails here while harmless drift does not.
func TestDecodeAllocsBounded(t *testing.T) {
	const w, h = 8192, 1 // 128 64-wide code-blocks, the GRIB2 shape
	data := make([]int32, w*h)
	var s uint64 = 0x5EED
	for i := range data {
		s = s*6364136223846793005 + 1442695040888963407
		data[i] = int32((i*7)%4096 + int(s>>60))
	}
	img := NewImage(ColorSpaceGray, 0, 0, w, h,
		[]Component{{Dx: 1, Dy: 1, W: w, H: h, Prec: 16, Data: data}})
	var buf bytes.Buffer
	if err := Encode(img, &buf,
		WithEncodeFormat(FormatJ2K), WithLossless(),
		WithResolutions(1), WithCodeBlockSize(64, 64)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	stream := buf.Bytes()

	allocs := testing.AllocsPerRun(5, func() {
		if _, err := Decode(bytes.NewReader(stream),
			WithFormat(FormatJ2K), WithStrictMode(true)); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})
	const maxAllocs = 220
	if allocs > maxAllocs {
		t.Fatalf("Decode allocated %.0f times for a 128-code-block stream (budget %d): "+
			"a per-code-block allocation has crept back in", allocs, maxAllocs)
	}
}
