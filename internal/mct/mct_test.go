package mct

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

const mctVectorPath = "../../testdata/vectors/mct/vectors.json"

type rctVec struct {
	N     int     `json:"n"`
	C0    []int32 `json:"c0"`
	C1    []int32 `json:"c1"`
	C2    []int32 `json:"c2"`
	EncC0 []int32 `json:"enc_c0"`
	EncC1 []int32 `json:"enc_c1"`
	EncC2 []int32 `json:"enc_c2"`
	DecC0 []int32 `json:"dec_c0"`
	DecC1 []int32 `json:"dec_c1"`
	DecC2 []int32 `json:"dec_c2"`
}

type ictVec struct {
	N     int      `json:"n"`
	C0    []uint32 `json:"c0"`
	C1    []uint32 `json:"c1"`
	C2    []uint32 `json:"c2"`
	EncC0 []uint32 `json:"enc_c0"`
	EncC1 []uint32 `json:"enc_c1"`
	EncC2 []uint32 `json:"enc_c2"`
	DecC0 []uint32 `json:"dec_c0"`
	DecC1 []uint32 `json:"dec_c1"`
	DecC2 []uint32 `json:"dec_c2"`
}

type customVec struct {
	Nbcomp uint32     `json:"nbcomp"`
	N      int        `json:"n"`
	Matrix []uint32   `json:"matrix"`
	EncIn  [][]int32  `json:"enc_in"`
	EncOut [][]int32  `json:"enc_out"`
	DecIn  [][]uint32 `json:"dec_in"`
	DecOut [][]uint32 `json:"dec_out"`
}

type inversionVec struct {
	Nbcomp uint32   `json:"nbcomp"`
	Ok     bool     `json:"ok"`
	In     []uint32 `json:"in"`
	Out    []uint32 `json:"out"`
}

type mctVectors struct {
	RCT       rctVec         `json:"rct"`
	ICT       ictVec         `json:"ict"`
	Custom    []customVec    `json:"custom"`
	Inversion []inversionVec `json:"inversion"`
}

func loadMctVectors(t *testing.T) *mctVectors {
	t.Helper()
	raw, err := os.ReadFile(mctVectorPath)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v mctVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return &v
}

func bitsToF32(b []uint32) []float32 {
	out := make([]float32, len(b))
	for i, u := range b {
		out[i] = math.Float32frombits(u)
	}
	return out
}

func eqI32(t *testing.T, name string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len %d != %d", name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s[%d]: got %d want %d", name, i, got[i], want[i])
		}
	}
}

// eqF32Bits compares by raw bit pattern (NaN-safe, exactness required).
func eqF32Bits(t *testing.T, name string, got []float32, wantBits []uint32) {
	t.Helper()
	if len(got) != len(wantBits) {
		t.Fatalf("%s: len %d != %d", name, len(got), len(wantBits))
	}
	for i := range got {
		gb := math.Float32bits(got[i])
		if gb != wantBits[i] {
			t.Fatalf("%s[%d]: got bits %#08x (%v) want %#08x (%v)",
				name, i, gb, got[i], wantBits[i], math.Float32frombits(wantBits[i]))
		}
	}
}

func TestRCT(t *testing.T) {
	v := loadMctVectors(t).RCT
	n := v.N

	e0 := append([]int32(nil), v.C0...)
	e1 := append([]int32(nil), v.C1...)
	e2 := append([]int32(nil), v.C2...)
	Encode(e0, e1, e2, n)
	eqI32(t, "enc_c0", e0, v.EncC0)
	eqI32(t, "enc_c1", e1, v.EncC1)
	eqI32(t, "enc_c2", e2, v.EncC2)

	d0 := append([]int32(nil), v.C0...)
	d1 := append([]int32(nil), v.C1...)
	d2 := append([]int32(nil), v.C2...)
	Decode(d0, d1, d2, n)
	eqI32(t, "dec_c0", d0, v.DecC0)
	eqI32(t, "dec_c1", d1, v.DecC1)
	eqI32(t, "dec_c2", d2, v.DecC2)
}

func TestICT(t *testing.T) {
	v := loadMctVectors(t).ICT
	n := v.N

	e0 := bitsToF32(v.C0)
	e1 := bitsToF32(v.C1)
	e2 := bitsToF32(v.C2)
	EncodeReal(e0, e1, e2, n)
	eqF32Bits(t, "enc_c0", e0, v.EncC0)
	eqF32Bits(t, "enc_c1", e1, v.EncC1)
	eqF32Bits(t, "enc_c2", e2, v.EncC2)

	d0 := bitsToF32(v.C0)
	d1 := bitsToF32(v.C1)
	d2 := bitsToF32(v.C2)
	DecodeReal(d0, d1, d2, n)
	eqF32Bits(t, "dec_c0", d0, v.DecC0)
	eqF32Bits(t, "dec_c1", d1, v.DecC1)
	eqF32Bits(t, "dec_c2", d2, v.DecC2)
}

func TestCustomEncode(t *testing.T) {
	for ci, c := range loadMctVectors(t).Custom {
		matrix := bitsToF32(c.Matrix)
		data := make([][]int32, c.Nbcomp)
		for j := range data {
			data[j] = append([]int32(nil), c.EncIn[j]...)
		}
		EncodeCustom(matrix, c.N, data, c.Nbcomp)
		for j := uint32(0); j < c.Nbcomp; j++ {
			eqI32(t, "custom["+strconv.Itoa(ci)+"].enc_out["+strconv.Itoa(int(j))+"]", data[j], c.EncOut[j])
		}
	}
}

func TestCustomDecode(t *testing.T) {
	for ci, c := range loadMctVectors(t).Custom {
		matrix := bitsToF32(c.Matrix)
		data := make([][]float32, c.Nbcomp)
		for j := range data {
			data[j] = bitsToF32(c.DecIn[j])
		}
		DecodeCustom(matrix, c.N, data, c.Nbcomp)
		for j := uint32(0); j < c.Nbcomp; j++ {
			eqF32Bits(t, "custom["+strconv.Itoa(ci)+"].dec_out["+strconv.Itoa(int(j))+"]", data[j], c.DecOut[j])
		}
	}
}

func TestMatrixInversion(t *testing.T) {
	for ci, c := range loadMctVectors(t).Inversion {
		src := bitsToF32(c.In)
		dest := make([]float32, len(src))
		ok := MatrixInversionF(src, dest, c.Nbcomp)
		if ok != c.Ok {
			t.Fatalf("inversion[%d]: ok=%v want %v", ci, ok, c.Ok)
		}
		if !c.Ok {
			continue
		}
		eqF32Bits(t, "inversion["+strconv.Itoa(ci)+"].out", dest, c.Out)
	}
}

// TestRCTRoundTrip is a property test: decode(encode(x)) == x for the
// reversible transform over arbitrary integer data.
func TestRCTRoundTrip(t *testing.T) {
	const n = 257
	rng := uint64(0xC0FFEE123)
	next := func() int32 {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int32(rng>>32) % (1 << 22)
	}
	c0 := make([]int32, n)
	c1 := make([]int32, n)
	c2 := make([]int32, n)
	o0 := make([]int32, n)
	o1 := make([]int32, n)
	o2 := make([]int32, n)
	for i := 0; i < n; i++ {
		c0[i] = next()
		c1[i] = next()
		c2[i] = next()
	}
	copy(o0, c0)
	copy(o1, c1)
	copy(o2, c2)
	Encode(c0, c1, c2, n)
	Decode(c0, c1, c2, n)
	eqI32(t, "roundtrip_c0", c0, o0)
	eqI32(t, "roundtrip_c1", c1, o1)
	eqI32(t, "roundtrip_c2", c2, o2)
}
