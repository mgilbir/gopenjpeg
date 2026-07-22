package sparse

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const sentinel = int32(0x5A5A5A5A)

const (
	opWrite  = 0
	opRead   = 1
	opCreate = 2
)

type reader struct {
	b   []byte
	pos int
}

func (r *reader) u32() uint32 {
	v := binary.LittleEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v
}

func (r *reader) i32s(n uint32) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(r.u32())
	}
	return out
}

// TestSparseVectors replays the C-generated op stream against the Go port and
// requires bit-exact equality of every returned buffer and boolean result.
func TestSparseVectors(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "vectors", "sparse", "vectors.bin")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	r := &reader{b: data}

	ncfg := r.u32()
	var sa *Array
	totalOps := 0
	for ci := uint32(0); ci < ncfg; ci++ {
		// cfg header (create params, informational — actual creates come as ops)
		_ = r.u32() // w
		_ = r.u32() // h
		_ = r.u32() // bw
		_ = r.u32() // bh
		nops := r.u32()

		for oi := uint32(0); oi < nops; oi++ {
			typ := r.u32()
			switch typ {
			case opCreate:
				w := r.u32()
				h := r.u32()
				bw := r.u32()
				bh := r.u32()
				sa = New(w, h, bw, bh)
				if sa == nil {
					t.Fatalf("cfg %d op %d: New(%d,%d,%d,%d) returned nil", ci, oi, w, h, bw, bh)
				}
				totalOps++
			case opWrite:
				x0 := r.u32()
				y0 := r.u32()
				x1 := r.u32()
				y1 := r.u32()
				col := r.u32()
				line := r.u32()
				forgiving := r.u32() != 0
				wantRet := r.u32() != 0
				buflen := r.u32()
				src := r.i32s(buflen)
				got := sa.Write(x0, y0, x1, y1, src, col, line, forgiving)
				if got != wantRet {
					t.Fatalf("cfg %d op %d write ret: got %v want %v", ci, oi, got, wantRet)
				}
				totalOps++
			case opRead:
				x0 := r.u32()
				y0 := r.u32()
				x1 := r.u32()
				y1 := r.u32()
				col := r.u32()
				line := r.u32()
				forgiving := r.u32() != 0
				wantRet := r.u32() != 0
				buflen := r.u32()
				want := r.i32s(buflen)
				dest := make([]int32, buflen)
				for i := range dest {
					dest[i] = sentinel
				}
				got := sa.Read(x0, y0, x1, y1, dest, col, line, forgiving)
				if got != wantRet {
					t.Fatalf("cfg %d op %d read ret: got %v want %v", ci, oi, got, wantRet)
				}
				for i := range want {
					if dest[i] != want[i] {
						t.Fatalf("cfg %d op %d read buf[%d]: got %d want %d (region %d,%d,%d,%d col=%d line=%d)",
							ci, oi, i, dest[i], want[i], x0, y0, x1, y1, col, line)
					}
				}
				totalOps++
			default:
				t.Fatalf("cfg %d op %d: unknown op type %d", ci, oi, typ)
			}
		}
	}
	if r.pos != len(data) {
		t.Fatalf("trailing bytes: pos=%d len=%d", r.pos, len(data))
	}
	t.Logf("replayed %d ops across %d configs", totalOps, ncfg)
}

// TestCreateEdgeCases covers the create-time bounds/overflow guards.
func TestCreateEdgeCases(t *testing.T) {
	if New(0, 1, 1, 1) != nil {
		t.Error("width 0 should return nil")
	}
	if New(1, 0, 1, 1) != nil {
		t.Error("height 0 should return nil")
	}
	if New(1, 1, 0, 1) != nil {
		t.Error("block_width 0 should return nil")
	}
	if New(1, 1, 1, 0) != nil {
		t.Error("block_height 0 should return nil")
	}
	// block_width > (~0U)/block_height/4 overflow guard
	if New(1, 1, 0xFFFFFFFF, 0xFFFFFFFF) != nil {
		t.Error("overflow guard should return nil")
	}
	if sa := New(10, 10, 4, 4); sa == nil {
		t.Error("valid create returned nil")
	}
}

// TestIsRegionValid checks the region-validity predicate directly.
func TestIsRegionValid(t *testing.T) {
	sa := New(10, 8, 4, 4)
	cases := []struct {
		x0, y0, x1, y1 uint32
		want           bool
	}{
		{0, 0, 10, 8, true},
		{0, 0, 11, 8, false},  // x1 > width
		{0, 0, 10, 9, false},  // y1 > height
		{5, 5, 5, 6, false},   // x1 <= x0
		{5, 5, 6, 5, false},   // y1 <= y0
		{10, 0, 11, 1, false}, // x0 >= width
		{0, 8, 1, 9, false},   // y0 >= height
		{9, 7, 10, 8, true},
	}
	for _, c := range cases {
		if got := sa.IsRegionValid(c.x0, c.y0, c.x1, c.y1); got != c.want {
			t.Errorf("IsRegionValid(%d,%d,%d,%d)=%v want %v", c.x0, c.y0, c.x1, c.y1, got, c.want)
		}
	}
}
