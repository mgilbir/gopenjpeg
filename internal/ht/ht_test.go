package ht

import (
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/t1"
)

// vector is one instrumented-oracle record: the inputs to opj_t1_ht_decode_cblk
// and the decoded t1->data it produced.
type vector struct {
	width, height       int32
	orient, roishift    uint32
	cblksty, mb, numbps uint32
	numsegs             uint32
	s0p, s0l, s1p, s1l  uint32
	coded               []byte
	expected            []int32
}

// readVectors parses the gzipped binary vector file produced by the W10
// instrumented oracle (see testdata/vectors/ht/README.md for the format).
func readVectors(t testing.TB, path string) []vector {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open vectors: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()

	buf, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var vecs []vector
	off := 0
	u32 := func() uint32 {
		v := binary.LittleEndian.Uint32(buf[off:])
		off += 4
		return v
	}
	for off < len(buf) {
		var v vector
		v.width = int32(u32())
		v.height = int32(u32())
		v.orient = u32()
		v.roishift = u32()
		v.cblksty = u32()
		v.mb = u32()
		v.numbps = u32()
		v.numsegs = u32()
		v.s0p = u32()
		v.s0l = u32()
		v.s1p = u32()
		v.s1l = u32()
		total := u32()
		v.coded = append([]byte(nil), buf[off:off+int(total)]...)
		off += int(total)
		oc := u32()
		v.expected = make([]int32, oc)
		for i := range v.expected {
			v.expected[i] = int32(u32())
		}
		vecs = append(vecs, v)
	}
	return vecs
}

func (v vector) cblk() *t1.CodeBlockDec {
	return &t1.CodeBlockDec{
		X0: 0, Y0: 0, X1: v.width, Y1: v.height,
		Numbps:    v.numbps,
		Chunks:    []t1.Chunk{{Data: v.coded, Len: uint32(len(v.coded))}},
		NumChunks: 1,
		Segs: []t1.Seg{
			{Len: v.s0l, RealNumPasses: v.s0p},
			{Len: v.s1l, RealNumPasses: v.s1p},
		},
		RealNumSegs: v.numsegs,
	}
}

// TestDecodeVectors replays every oracle vector through DecodeCblk and requires
// the decoded raster to be bit-exact against the C reference output.
func TestDecodeVectors(t *testing.T) {
	vecs := readVectors(t, "../../testdata/vectors/ht/cleanup_vectors.bin.gz")
	if len(vecs) == 0 {
		t.Fatal("no vectors loaded")
	}
	dec := New()
	for i, v := range vecs {
		ok, err := dec.DecodeCblk(v.cblk(), v.orient, v.roishift, v.cblksty, v.mb, nil)
		if err != nil || !ok {
			t.Fatalf("vec %d (%dx%d sty=%#x Mb=%d): decode failed ok=%v err=%v",
				i, v.width, v.height, v.cblksty, v.mb, ok, err)
		}
		got := dec.Data()
		n := int(v.width * v.height)
		if len(got) < n {
			t.Fatalf("vec %d: short output %d < %d", i, len(got), n)
		}
		for j := 0; j < n; j++ {
			if got[j] != v.expected[j] {
				t.Fatalf("vec %d (%dx%d sty=%#x Mb=%d numbps=%d): mismatch at "+
					"%d: got %d want %d", i, v.width, v.height, v.cblksty, v.mb,
					v.numbps, j, got[j], v.expected[j])
			}
		}
	}
	t.Logf("verified %d vectors bit-exact", len(vecs))
}

// TestDecodeVectorsDecodedData checks the sub-tile decode path (cblk.DecodedData
// set): the same output must land in the caller-provided buffer.
func TestDecodeVectorsDecodedData(t *testing.T) {
	vecs := readVectors(t, "../../testdata/vectors/ht/cleanup_vectors.bin.gz")
	dec := New()
	for i, v := range vecs {
		n := int(v.width * v.height)
		cb := v.cblk()
		cb.DecodedData = make([]int32, n)
		ok, err := dec.DecodeCblk(cb, v.orient, v.roishift, v.cblksty, v.mb, nil)
		if err != nil || !ok {
			t.Fatalf("vec %d: decode failed ok=%v err=%v", i, ok, err)
		}
		for j := 0; j < n; j++ {
			if cb.DecodedData[j] != v.expected[j] {
				t.Fatalf("vec %d: DecodedData mismatch at %d: got %d want %d",
					i, j, cb.DecodedData[j], v.expected[j])
			}
		}
	}
}

// FuzzDecodeCblk feeds arbitrary code-block bytes and bounded geometry to the
// decoder; per the project no-panic rule it must never panic, OOB, or hang.
func FuzzDecodeCblk(f *testing.F) {
	// Seed with a couple of real vectors.
	vecs := readVectors(f, "../../testdata/vectors/ht/cleanup_vectors.bin.gz")
	for i := 0; i < len(vecs) && i < 8; i++ {
		v := vecs[i]
		f.Add(v.coded, uint8(v.width), uint8(v.height), uint32(v.cblksty),
			uint32(v.mb), uint32(v.numbps), uint32(v.s0l), uint32(v.s0p))
	}
	f.Add([]byte{0, 1, 2, 3, 4, 5}, uint8(4), uint8(4), uint32(0x40),
		uint32(4), uint32(4), uint32(6), uint32(1))

	em := &event.Manager{} // nil handlers: silent, exercises the message paths

	dec := New()
	f.Fuzz(func(t *testing.T, coded []byte, w, h uint8, cblksty, mb, numbps, seglen, passes uint32) {
		width := int32(w%68) + 1
		height := int32(h%68) + 1
		if int(width)*int(height) > 4096 {
			return
		}
		clen := uint32(len(coded)) + 1
		// Split the coded data into a cleanup segment (seg0) and a refinement
		// segment (seg1) so the SigProp/MagRef paths are exercised too. numsegs
		// alternates 1 and 2 based on the passes parameter.
		l1 := seglen % clen
		l2 := (seglen / 7) % clen
		numsegs := uint32(1)
		if passes&1 != 0 {
			numsegs = 2
		}
		cblk := &t1.CodeBlockDec{
			X0: 0, Y0: 0, X1: width, Y1: height,
			Numbps:    numbps % 40,
			Chunks:    []t1.Chunk{{Data: coded, Len: uint32(len(coded))}},
			NumChunks: 1,
			Segs: []t1.Seg{
				{Len: l1, RealNumPasses: (passes % 3) + 1},
				{Len: l2, RealNumPasses: (passes / 3) % 3},
			},
			RealNumSegs: numsegs,
		}
		// Must not panic regardless of the (bounded) inputs.
		_, _ = dec.DecodeCblk(cblk, 0, 0, cblksty, mb%40, em)
	})
}
