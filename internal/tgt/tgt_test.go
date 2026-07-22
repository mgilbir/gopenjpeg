package tgt

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/bio"
)

type tgtCase struct {
	h, v      uint32
	threshold int32
	vals      []int32
	enc       []byte
	decR      []uint32
	decVal    []int32
}

func loadTgtVectors(t *testing.T) []tgtCase {
	t.Helper()
	f, err := os.Open("../../testdata/vectors/tgt/tgt.txt")
	if err != nil {
		t.Fatalf("open vectors: %v", err)
	}
	defer f.Close()

	var cases []tgtCase
	var cur tgtCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fld := strings.Fields(sc.Text())
		if len(fld) == 0 {
			continue
		}
		switch fld[0] {
		case "TREE":
			cur = tgtCase{}
			h, _ := strconv.ParseUint(fld[1], 10, 32)
			v, _ := strconv.ParseUint(fld[2], 10, 32)
			th, _ := strconv.ParseInt(fld[3], 10, 32)
			cur.h, cur.v, cur.threshold = uint32(h), uint32(v), int32(th)
		case "VALS":
			for _, tok := range fld[1:] {
				x, _ := strconv.ParseInt(tok, 10, 32)
				cur.vals = append(cur.vals, int32(x))
			}
		case "ENC":
			h := ""
			if len(fld) > 1 {
				h = fld[1]
			}
			b, err := hex.DecodeString(h)
			if err != nil {
				t.Fatalf("hex %q: %v", h, err)
			}
			cur.enc = b
		case "DEC":
			for _, tok := range fld[1:] {
				parts := strings.SplitN(tok, ":", 2)
				r, _ := strconv.ParseUint(parts[0], 10, 32)
				val, _ := strconv.ParseInt(parts[1], 10, 32)
				cur.decR = append(cur.decR, uint32(r))
				cur.decVal = append(cur.decVal, int32(val))
			}
			cases = append(cases, cur)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return cases
}

// TestEncodeVectors checks tag-tree encoding matches the C oracle bytes.
func TestEncodeVectors(t *testing.T) {
	cases := loadTgtVectors(t)
	if len(cases) == 0 {
		t.Fatal("no vectors")
	}
	for ci, c := range cases {
		tree, err := Create(c.h, c.v, nil)
		if err != nil {
			t.Fatalf("case %d: create: %v", ci, err)
		}
		for i, val := range c.vals {
			tree.SetValue(uint32(i), val)
		}
		buf := make([]byte, len(c.vals)*4+16)
		b := bio.NewEncoder(buf)
		for i := range c.vals {
			tree.Encode(b, uint32(i), c.threshold)
		}
		if !b.Flush() {
			t.Fatalf("case %d: flush", ci)
		}
		got := buf[:b.NumBytes()]
		if !bytes.Equal(got, c.enc) {
			t.Errorf("case %d (H=%d V=%d T=%d): encode got %x want %x",
				ci, c.h, c.v, c.threshold, got, c.enc)
		}
	}
}

// TestDecodeVectors checks tag-tree decoding matches the C oracle return values
// and recovered node values.
func TestDecodeVectors(t *testing.T) {
	cases := loadTgtVectors(t)
	for ci, c := range cases {
		tree, err := Create(c.h, c.v, nil)
		if err != nil {
			t.Fatalf("case %d: create: %v", ci, err)
		}
		b := bio.NewDecoder(c.enc)
		for i := range c.vals {
			r := tree.Decode(b, uint32(i), c.threshold)
			if r != c.decR[i] {
				t.Errorf("case %d leaf %d: decode return %d want %d", ci, i, r, c.decR[i])
			}
			if got := tree.nodes[i].value; got != c.decVal[i] {
				t.Errorf("case %d leaf %d: node value %d want %d", ci, i, got, c.decVal[i])
			}
		}
	}
}

// TestCreateTooSmall verifies the numnodes==0 error path. A 0-leaf tree yields
// no nodes.
func TestCreateTooSmall(t *testing.T) {
	if _, err := Create(0, 0, nil); err != ErrTooSmall {
		t.Errorf("Create(0,0) err = %v, want ErrTooSmall", err)
	}
}

// TestInitReuse checks that Init reinitialises an existing tree for new
// dimensions and that encode/decode still round-trips.
func TestInitReuse(t *testing.T) {
	tree, err := Create(3, 3, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Re-init to a larger tree.
	if err := tree.Init(5, 4, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	vals := make([]int32, 20)
	for i := range vals {
		vals[i] = int32((i * 3) % 11)
	}
	for i, v := range vals {
		tree.SetValue(uint32(i), v)
	}
	buf := make([]byte, len(vals)*4+16)
	b := bio.NewEncoder(buf)
	th := int32(12)
	for i := range vals {
		tree.Encode(b, uint32(i), th)
	}
	if !b.Flush() {
		t.Fatal("flush")
	}

	dtree, _ := Create(5, 4, nil)
	db := bio.NewDecoder(buf[:b.NumBytes()])
	for i, v := range vals {
		r := dtree.Decode(db, uint32(i), th)
		want := uint32(0)
		if v < th {
			want = 1
		}
		if r != want {
			t.Errorf("leaf %d val %d thr %d: decode=%d want %d", i, v, th, r, want)
		}
		if v < th && dtree.nodes[i].value != v {
			t.Errorf("leaf %d: recovered value %d want %d", i, dtree.nodes[i].value, v)
		}
	}
}

// FuzzDecode ensures decoding arbitrary bytes against a fixed tree never
// panics (bounds safety on untrusted packet-header bits).
func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x00}, int32(5))
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF}, int32(20))
	f.Add([]byte{}, int32(1))
	f.Fuzz(func(t *testing.T, data []byte, threshold int32) {
		tree, err := Create(4, 4, nil)
		if err != nil {
			return
		}
		b := bio.NewDecoder(data)
		for i := uint32(0); i < 16; i++ {
			_ = tree.Decode(b, i, threshold)
		}
	})
}
