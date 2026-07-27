package t1

import "testing"

// TestDecodeCblkRejectsOutOfRangeOrient covers C39: the zero-coding context LUT
// holds 2048 entries and is indexed as lutCtxnoZC[orient<<9:], so any orient
// above 3 used to slice past the end and panic. DecodeCblk must return an error
// instead, for every out-of-range value, and must still accept 0..3.
func TestDecodeCblkRejectsOutOfRangeOrient(t *testing.T) {
	payload := []byte{0x80, 0x40, 0x20, 0x10}
	newCblk := func() *CodeBlockDec {
		return &CodeBlockDec{
			X0: 0, Y0: 0, X1: 8, Y1: 8,
			Numbps:      4,
			Chunks:      []Chunk{{Data: payload, Len: uint32(len(payload))}},
			NumChunks:   1,
			Segs:        []Seg{{Len: uint32(len(payload)), RealNumPasses: 3}},
			RealNumSegs: 1,
		}
	}

	dec := New(false)
	for orient := uint32(0); orient <= 3; orient++ {
		if ok, err := dec.DecodeCblk(newCblk(), orient, 0, 0, false); err != nil || !ok {
			t.Fatalf("orient %d: ok=%v err=%v, want ok with no error", orient, ok, err)
		}
	}
	for _, orient := range []uint32{4, 5, 7, 63, 1 << 20, ^uint32(0)} {
		ok, err := dec.DecodeCblk(newCblk(), orient, 0, 0, false)
		if err == nil {
			t.Fatalf("orient %d: expected an error, got ok=%v nil error", orient, ok)
		}
		if ok {
			t.Fatalf("orient %d: expected ok=false alongside the error", orient)
		}
	}
}

// TestEncodeCblkRejectsOutOfRangeOrient is the encode-side half of C39
// (encode.go used the same unchecked lutCtxnoZC[orient<<9:] slice).
func TestEncodeCblkRejectsOutOfRangeOrient(t *testing.T) {
	input := make([]int32, 16*16)
	for i := range input {
		input[i] = int32((i*37)&0x3ff) - 512
	}

	enc := New(true)
	for orient := uint32(0); orient <= 3; orient++ {
		enc.SetData(append([]int32(nil), input...), 16, 16)
		cblk := CodeBlockEnc{X0: 0, Y0: 0, X1: 16, Y1: 16}
		if _, err := enc.EncodeCblk(&cblk, orient, 0, 3, 1, 1.0, 0, 1, nil, 0); err != nil {
			t.Fatalf("orient %d: unexpected error: %v", orient, err)
		}
	}
	for _, orient := range []uint32{4, 5, 7, 63, 1 << 20, ^uint32(0)} {
		enc.SetData(append([]int32(nil), input...), 16, 16)
		cblk := CodeBlockEnc{X0: 0, Y0: 0, X1: 16, Y1: 16}
		if _, err := enc.EncodeCblk(&cblk, orient, 0, 3, 1, 1.0, 0, 1, nil, 0); err == nil {
			t.Fatalf("orient %d: expected an error, got nil", orient)
		}
	}
}
