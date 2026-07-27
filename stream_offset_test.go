package gopenjpeg

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestDecodeHonorsReaderOffset is the C20 regression: a valid codestream
// wrapped in an io.ReadSeeker positioned at a non-zero offset must decode from
// that offset, using the same origin for magic detection and for the decode
// (matching stdlib image.Decode). Before the fix, magic was read at the current
// position but decoding restarted at absolute offset 0, so a mid-stream reader
// either failed format detection or decoded the wrong bytes.
func TestDecodeHonorsReaderOffset(t *testing.T) {
	raw, err := os.ReadFile("testdata/fuzzseed/p0_12.j2k")
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}

	base, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("baseline decode: %v", err)
	}

	// Prepend arbitrary bytes so the real codestream begins at a non-zero
	// offset, then position the reader exactly at that offset.
	prefix := bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 13) // 39 bytes, non-multiple of 12
	buf := append(append([]byte(nil), prefix...), raw...)
	rs := bytes.NewReader(buf)
	if _, err := rs.Seek(int64(len(prefix)), io.SeekStart); err != nil {
		t.Fatalf("seek to offset: %v", err)
	}

	got, err := Decode(rs)
	if err != nil {
		t.Fatalf("offset decode: %v", err)
	}

	if got.NumComponents() != base.NumComponents() {
		t.Fatalf("component count mismatch: offset=%d baseline=%d",
			got.NumComponents(), base.NumComponents())
	}
	for i := 0; i < base.NumComponents(); i++ {
		b := base.Component(i)
		g := got.Component(i)
		if b.W != g.W || b.H != g.H {
			t.Fatalf("component %d dims mismatch: offset=%dx%d baseline=%dx%d",
				i, g.W, g.H, b.W, b.H)
		}
		if len(b.Data) != len(g.Data) {
			t.Fatalf("component %d sample count mismatch: offset=%d baseline=%d",
				i, len(g.Data), len(b.Data))
		}
		for k := range b.Data {
			if b.Data[k] != g.Data[k] {
				t.Fatalf("component %d sample %d mismatch: offset=%d baseline=%d",
					i, k, g.Data[k], b.Data[k])
			}
		}
	}
}
