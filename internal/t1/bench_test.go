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

// benchEncCblk is a preloaded encode vector ready to be replayed.
type benchEncCblk struct {
	input                                            []int32
	w, h                                             uint32
	orient, compno, level, qmfbid, cblksty, numcomps uint32
	stepsize                                         float64
}

// loadEncodeCblks preloads every t1 encode vector so BenchmarkEncodeCblkVectors
// measures only EncodeCblk (tier-1 + MQC encoder), no I/O.
func loadEncodeCblks(b *testing.B) []benchEncCblk {
	c := loadVectors(b, "t1_encode.bin.gz")
	if m := c.magic(); m != "T1EN0001" {
		b.Fatalf("bad magic %q", m)
	}
	count := c.u32()
	out := make([]benchEncCblk, 0, count)
	for rec := uint32(0); rec < count; rec++ {
		w := c.u32()
		h := c.u32()
		orient := c.u32()
		compno := c.u32()
		level := c.u32()
		qmfbid := c.u32()
		cblksty := c.u32()
		numcomps := c.u32()
		stepsize := c.f64()
		input := make([]int32, w*h)
		for i := range input {
			input[i] = c.i32()
		}
		// Skip the expected-output portion of the record (numbps, passes, stream).
		_ = c.u32() // numbps
		wantTotal := c.u32()
		for p := uint32(0); p < wantTotal; p++ {
			c.u32() // rate
			c.u32() // term
			c.f64() // dist
		}
		wantLen := c.u32()
		c.bytes(wantLen)
		out = append(out, benchEncCblk{
			input: input, w: w, h: h,
			orient: orient, compno: compno, level: level, qmfbid: qmfbid,
			cblksty: cblksty, numcomps: numcomps, stepsize: stepsize,
		})
	}
	return out
}

// BenchmarkEncodeCblkVectors replays every encode vector; low-noise measurement
// of the tier-1 + MQC encoder hot path in isolation from DWT/MCT/rate control.
func BenchmarkEncodeCblkVectors(b *testing.B) {
	vecs := loadEncodeCblks(b)
	enc := New(true)
	var cblk CodeBlockEnc
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range vecs {
			v := &vecs[j]
			enc.SetData(v.input, v.w, v.h)
			cblk = CodeBlockEnc{X0: 0, Y0: 0, X1: int32(v.w), Y1: int32(v.h),
				Passes: cblk.Passes[:0], Data: cblk.Data[:0]}
			enc.EncodeCblk(&cblk, v.orient, v.compno, v.level, v.qmfbid,
				v.stepsize, v.cblksty, v.numcomps, nil, 0)
		}
	}
}
