package t1

import "testing"

// benchCblk is a preloaded decode vector ready to be replayed.
type benchCblk struct {
	cblk     *CodeBlockDec
	orient   uint32
	roishift uint32
	cblksty  uint32
}

// loadDecodeCblks preloads every t1 decode vector into ready-to-run code-blocks
// so the benchmark measures only DecodeCblk (tier-1 + MQC), no I/O.
func loadDecodeCblks(b *testing.B) []benchCblk {
	c := loadVectors(b, "t1_decode.bin.gz")
	if m := c.magic(); m != "T1DE0001" {
		b.Fatalf("bad magic %q", m)
	}
	count := c.u32()
	out := make([]benchCblk, 0, count)
	for rec := uint32(0); rec < count; rec++ {
		w := c.u32()
		h := c.u32()
		orient := c.u32()
		roishift := c.u32()
		cblksty := c.u32()
		numbps := c.u32()
		nsegs := c.u32()
		segs := make([]Seg, nsegs)
		for s := range segs {
			segs[s].Len = c.u32()
			segs[s].RealNumPasses = c.u32()
		}
		chunkLen := c.u32()
		chunk := append([]byte(nil), c.bytes(chunkLen)...)
		want := make([]int32, w*h)
		for i := range want {
			want[i] = c.i32()
		}
		out = append(out, benchCblk{
			cblk: &CodeBlockDec{
				X0: 0, Y0: 0, X1: int32(w), Y1: int32(h),
				Numbps:      numbps,
				Chunks:      []Chunk{{Data: chunk, Len: chunkLen}},
				NumChunks:   1,
				Segs:        segs,
				RealNumSegs: nsegs,
			},
			orient:   orient,
			roishift: roishift,
			cblksty:  cblksty,
		})
	}
	return out
}

// BenchmarkDecodeCblkVectors replays every decode vector; low-noise measurement
// of the tier-1 + MQC hot path in isolation from DWT/MCT.
func BenchmarkDecodeCblkVectors(b *testing.B) {
	cblks := loadDecodeCblks(b)
	dec := New(false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range cblks {
			bc := &cblks[j]
			if _, err := dec.DecodeCblk(bc.cblk, bc.orient, bc.roishift, bc.cblksty, false); err != nil {
				b.Fatal(err)
			}
		}
	}
}
