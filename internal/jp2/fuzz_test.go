package jp2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// FuzzReadHeader fuzzes the full JP2 box-parsing surface over arbitrary bytes.
// Per the project no-panic contract, box parsing must never panic, hang, read
// out of bounds, or over-allocate on untrusted input: any panic here is a bug
// (the go fuzzer reports it as a failure; the explicit recover turns it into a
// clear, attributable test failure with the offending input length).
//
// It drives both readHeaderProcedure (the box loop) and the full ReadHeader
// (which additionally exercises the colour-space/ICC-transfer tail) with a stub
// codec, so no codestream decode is involved.
func FuzzReadHeader(f *testing.F) {
	// Seed with every checked-in real JP2 file.
	if entries, err := os.ReadDir(filesDir); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".jp2" {
				if data, err := os.ReadFile(filepath.Join(filesDir, e.Name())); err == nil {
					f.Add(data)
				}
			}
		}
	}

	// Seed with hand-crafted structural corner cases.
	f.Add([]byte{})                                  // empty
	f.Add(make([]byte, 8))                           // zero-length/zero-type box
	f.Add(sigBox())                                  // signature only
	f.Add(concat(sigBox(), ftypBox()))               // sig + ftyp
	f.Add(concat(sigBox(), ftypBox(), mkbox(boxJP2H, // sig + ftyp + jp2h{ihdr}
		mkbox(boxIHDR, ihdrPayload(4, 4, 1, 7, 7, 0, 0)))))
	// XL box with undefined size.
	xl := make([]byte, 16)
	cio.WriteBytes(xl, 1, 4)
	f.Add(concat(sigBox(), xl))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()
		mgr := &event.Manager{} // silent

		// Path 1: raw box loop.
		jp2a, _ := newTestJP2()
		jp2a.readHeaderProcedure(cio.NewMemoryInputStream(data), mgr)

		// Path 2: full ReadHeader with a stub codec.
		jp2b, _ := newTestJP2()
		_, _ = jp2b.ReadHeader(cio.NewMemoryInputStream(data), mgr)
	})
}
