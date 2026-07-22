package t1

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"math"
	"os"
	"testing"
)

// cursor is a little-endian reader over an in-memory vector file.
type cursor struct {
	b   []byte
	pos int
}

func (c *cursor) u32() uint32 {
	v := binary.LittleEndian.Uint32(c.b[c.pos:])
	c.pos += 4
	return v
}
func (c *cursor) i32() int32 { return int32(c.u32()) }
func (c *cursor) f64() float64 {
	v := binary.LittleEndian.Uint64(c.b[c.pos:])
	c.pos += 8
	return math.Float64frombits(v)
}
func (c *cursor) bytes(n uint32) []byte {
	s := c.b[c.pos : c.pos+int(n)]
	c.pos += int(n)
	return s
}
func (c *cursor) magic() string {
	s := string(c.b[c.pos : c.pos+8])
	c.pos += 8
	return s
}

func loadVectors(t testing.TB, name string) *cursor {
	t.Helper()
	f, err := os.Open("../../testdata/vectors/t1/" + name)
	if err != nil {
		t.Skipf("vector file %s not present: %v", name, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return &cursor{b: raw}
}

// TestEncodeVectors checks that EncodeCblk reproduces the C encoder's byte
// stream and per-pass rate / term / distortion for every oracle vector.
func TestEncodeVectors(t *testing.T) {
	c := loadVectors(t, "t1_encode.bin.gz")
	if m := c.magic(); m != "T1EN0001" {
		t.Fatalf("bad magic %q", m)
	}
	count := c.u32()

	enc := New(true)
	var cblk CodeBlockEnc

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
		wantNumbps := c.u32()
		wantTotal := c.u32()

		type passWant struct {
			rate uint32
			term uint32
			dist float64
		}
		wantPasses := make([]passWant, wantTotal)
		for p := range wantPasses {
			wantPasses[p] = passWant{c.u32(), c.u32(), c.f64()}
		}
		wantLen := c.u32()
		wantStream := append([]byte(nil), c.bytes(wantLen)...)

		enc.SetData(input, w, h)
		cblk = CodeBlockEnc{X0: 0, Y0: 0, X1: int32(w), Y1: int32(h)}
		enc.EncodeCblk(&cblk, orient, compno, level, qmfbid, stepsize, cblksty, numcomps, nil, 0)

		if cblk.Numbps != wantNumbps {
			t.Fatalf("rec %d (%dx%d orient=%d sty=%#x qmfbid=%d): numbps=%d want %d",
				rec, w, h, orient, cblksty, qmfbid, cblk.Numbps, wantNumbps)
		}
		if cblk.Totalpasses != wantTotal {
			t.Fatalf("rec %d: totalpasses=%d want %d", rec, cblk.Totalpasses, wantTotal)
		}
		for p := uint32(0); p < wantTotal; p++ {
			gp := cblk.Passes[p]
			wp := wantPasses[p]
			if gp.Rate != wp.rate {
				t.Fatalf("rec %d pass %d: rate=%d want %d (sty=%#x qmfbid=%d)",
					rec, p, gp.Rate, wp.rate, cblksty, qmfbid)
			}
			if uint32(gp.Term) != wp.term {
				t.Fatalf("rec %d pass %d: term=%d want %d", rec, p, gp.Term, wp.term)
			}
			if !floatClose(gp.DistortionDec, wp.dist) {
				t.Fatalf("rec %d pass %d: distortiondec=%g want %g", rec, p, gp.DistortionDec, wp.dist)
			}
		}
		// The published stream is the full mqc output; compare to fullLen bytes.
		got := cblk.Data
		if len(got) < int(wantLen) || !bytes.Equal(got[:wantLen], wantStream) {
			t.Fatalf("rec %d: stream mismatch (sty=%#x qmfbid=%d)\n got=% x\nwant=% x",
				rec, cblksty, qmfbid, got, wantStream)
		}
	}
	t.Logf("verified %d encode vectors", count)
}

// TestDecodeVectors checks that DecodeCblk reproduces the C decoder's t1->data
// for every oracle vector (across truncation points and roishift values).
func TestDecodeVectors(t *testing.T) {
	c := loadVectors(t, "t1_decode.bin.gz")
	if m := c.magic(); m != "T1DE0001" {
		t.Fatalf("bad magic %q", m)
	}
	count := c.u32()

	dec := New(false)

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

		cblk := &CodeBlockDec{
			X0: 0, Y0: 0, X1: int32(w), Y1: int32(h),
			Numbps:      numbps,
			Chunks:      []Chunk{{Data: chunk, Len: chunkLen}},
			NumChunks:   1,
			Segs:        segs,
			RealNumSegs: nsegs,
		}
		ok, err := dec.DecodeCblk(cblk, orient, roishift, cblksty, false)
		if err != nil || !ok {
			t.Fatalf("rec %d: decode failed: ok=%v err=%v", rec, ok, err)
		}
		got := dec.Data()[:w*h]
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("rec %d (%dx%d orient=%d roi=%d sty=%#x): data[%d]=%d want %d",
					rec, w, h, orient, roishift, cblksty, i, got[i], want[i])
			}
		}
	}
	t.Logf("verified %d decode vectors", count)
}

func floatClose(a, b float64) bool {
	if a == b {
		return true
	}
	d := math.Abs(a - b)
	m := math.Max(math.Abs(a), math.Abs(b))
	return d <= 1e-9*math.Max(1, m)
}

// FuzzDecodeCblk feeds arbitrary bytes with bounded geometry to the decoder; it
// must never panic or index out of bounds (t1 has a history of such CVEs).
func FuzzDecodeCblk(f *testing.F) {
	f.Add([]byte{0x00})
	f.Add([]byte{0xff, 0xff, 0xac, 0x91, 0x00, 0x10, 0x20})
	f.Add(bytes.Repeat([]byte{0xa5}, 64))

	dec := New(false)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 6 {
			return
		}
		// Bounded geometry: w,h in [1,64], w*h <= 4096.
		w := uint32(data[0])%64 + 1
		h := uint32(data[1])%64 + 1
		if w*h > 4096 {
			return
		}
		orient := uint32(data[2]) & 3
		cblksty := uint32(data[3]) & 0x3f
		numbps := uint32(data[4])%20 + 1
		numpasses := uint32(data[5])%40 + 1

		payload := data[6:]
		cblk := &CodeBlockDec{
			X0: 0, Y0: 0, X1: int32(w), Y1: int32(h),
			Numbps:      numbps,
			Chunks:      []Chunk{{Data: payload, Len: uint32(len(payload))}},
			NumChunks:   1,
			Segs:        []Seg{{Len: uint32(len(payload)), RealNumPasses: numpasses}},
			RealNumSegs: 1,
		}
		// Must not panic.
		_, _ = dec.DecodeCblk(cblk, orient, 0, cblksty, false)
	})
}

func benchInputData(w, h uint32) []int32 {
	d := make([]int32, w*h)
	var s uint64 = 0x1234567
	for i := range d {
		s ^= s << 13
		s ^= s >> 7
		s ^= s << 17
		v := int32(s & 0x1ffff)
		if s&0x100000 != 0 {
			v = -v
		}
		if s&3 == 0 {
			v = 0
		}
		d[i] = v
	}
	return d
}

func BenchmarkEncode64x64(b *testing.B) {
	enc := New(true)
	input := benchInputData(64, 64)
	var cblk CodeBlockEnc
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.SetData(input, 64, 64)
		cblk = CodeBlockEnc{X0: 0, Y0: 0, X1: 64, Y1: 64, Passes: cblk.Passes[:0], Data: cblk.Data[:0]}
		enc.EncodeCblk(&cblk, 0, 0, 3, 1, 1.0, 0, 1, nil, 0)
	}
}

func BenchmarkDecode64x64(b *testing.B) {
	// Encode a block once to obtain a valid stream, then benchmark decoding it.
	enc := New(true)
	input := benchInputData(64, 64)
	var ecblk CodeBlockEnc
	enc.SetData(input, 64, 64)
	ecblk = CodeBlockEnc{X0: 0, Y0: 0, X1: 64, Y1: 64}
	enc.EncodeCblk(&ecblk, 0, 0, 3, 1, 1.0, 0, 1, nil, 0)
	fullLen := ecblk.Passes[ecblk.Totalpasses-1].Rate
	stream := append([]byte(nil), ecblk.Data[:fullLen]...)

	dec := New(false)
	cblk := &CodeBlockDec{
		X0: 0, Y0: 0, X1: 64, Y1: 64,
		Numbps:      ecblk.Numbps,
		Chunks:      []Chunk{{Data: stream, Len: fullLen}},
		NumChunks:   1,
		Segs:        []Seg{{Len: fullLen, RealNumPasses: ecblk.Totalpasses}},
		RealNumSegs: 1,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dec.DecodeCblk(cblk, 0, 0, 0, false)
	}
}

// TestEncodeCblkAllZero encodes an all-zero code-block. The C reference emits
// no passes and no data for such blocks (t2 then codes it as not included);
// this must not panic (regression: mqc.Bytes on a zero-pass block used to
// slice [start:start-1]).
func TestEncodeCblkAllZero(t *testing.T) {
	enc := New(true)
	input := make([]int32, 16*16)
	enc.SetData(input, 16, 16)
	var cblk CodeBlockEnc
	cblk = CodeBlockEnc{X0: 0, Y0: 0, X1: 16, Y1: 16}
	enc.EncodeCblk(&cblk, 0, 0, 3, 1, 1.0, 0, 1, nil, 0)
	if cblk.Totalpasses != 0 {
		t.Fatalf("all-zero block: totalpasses = %d, want 0", cblk.Totalpasses)
	}
}
