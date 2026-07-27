package gopenjpeg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// buildHugeSIZCodestream hand-crafts a minimal raw J2K codestream whose SIZ
// marker declares a tile-component grid (nbTiles x numcomps) whose product is
// enormous relative to the input size. It reproduces the C11 attack: Xsiz=1000,
// Ysiz=1, XTsiz=YTsiz=1 => 1000x1 tiles, Csiz=16384 components => a ~16.4M
// tile-component product driving ~17 GB of TCCP allocation from a ~49 KB input.
func buildHugeSIZCodestream() []byte {
	const csiz = 16384
	var b bytes.Buffer
	// SOC marker.
	b.Write([]byte{0xff, 0x4f})
	// SIZ marker.
	b.Write([]byte{0xff, 0x51})
	// Lsiz = 38 + 3*Csiz (marker payload length, includes the Lsiz field).
	lsiz := uint16(38 + 3*csiz)
	writeU16 := func(v uint16) { _ = binary.Write(&b, binary.BigEndian, v) }
	writeU32 := func(v uint32) { _ = binary.Write(&b, binary.BigEndian, v) }
	writeU16(lsiz)
	writeU16(0)    // Rsiz
	writeU32(1000) // Xsiz
	writeU32(1)    // Ysiz
	writeU32(0)    // X0siz
	writeU32(0)    // Y0siz
	writeU32(1)    // XTsiz
	writeU32(1)    // YTsiz
	writeU32(0)    // XT0siz
	writeU32(0)    // YT0siz
	writeU16(csiz) // Csiz
	for i := 0; i < csiz; i++ {
		b.WriteByte(7) // Ssiz: prec=8, unsigned
		b.WriteByte(1) // XRsiz
		b.WriteByte(1) // YRsiz
	}
	return b.Bytes()
}

// TestDecodeHugeSIZProductRejected verifies C11: a tiny codestream whose SIZ
// declares a gigantic nbTiles*numcomps product is rejected with an error rather
// than crashing the process with a multi-gigabyte allocation.
func TestDecodeHugeSIZProductRejected(t *testing.T) {
	cs := buildHugeSIZCodestream()
	if len(cs) > 60_000 {
		t.Fatalf("crafted codestream unexpectedly large: %d bytes", len(cs))
	}

	// bytes.Reader is an io.ReadSeeker; the stream length is known and the
	// per-field SIZ guards all pass, so only the C11 product bound can reject.
	_, err := Decode(bytes.NewReader(cs), WithFormat(FormatJ2K))
	if err == nil {
		t.Fatal("Decode accepted a SIZ declaring ~16.4M tile-components; want an error")
	}
	if !strings.Contains(err.Error(), "SIZ") {
		t.Fatalf("error = %q; want it to mention the SIZ marker", err)
	}
}

// readerOnly hides any Seek/ReadSeeker methods of the wrapped reader so the
// decode paths take the non-seekable io.ReadAll branch.
type readerOnly struct{ r io.Reader }

func (ro readerOnly) Read(p []byte) (int, error) { return ro.r.Read(p) }

func TestWithMaxInputSize(t *testing.T) {
	// 100 bytes of junk (no valid magic). Large enough to exceed a small cap.
	junk := bytes.Repeat([]byte{0x00}, 100)

	t.Run("over limit rejected", func(t *testing.T) {
		_, err := Decode(readerOnly{bytes.NewReader(junk)}, WithMaxInputSize(10))
		if !errors.Is(err, ErrInputTooLarge) {
			t.Fatalf("err = %v; want ErrInputTooLarge", err)
		}
	})

	t.Run("within limit is not a size error", func(t *testing.T) {
		// Under the cap: the size check must not fire. Format detection then
		// fails (junk has no magic), which is the expected non-size error.
		_, err := Decode(readerOnly{bytes.NewReader(junk)}, WithMaxInputSize(1000))
		if errors.Is(err, ErrInputTooLarge) {
			t.Fatalf("input within limit reported ErrInputTooLarge")
		}
		if !errors.Is(err, ErrUnknownFormat) {
			t.Fatalf("err = %v; want ErrUnknownFormat", err)
		}
	})

	t.Run("exactly at limit accepted", func(t *testing.T) {
		_, err := Decode(readerOnly{bytes.NewReader(junk)}, WithMaxInputSize(int64(len(junk))))
		if errors.Is(err, ErrInputTooLarge) {
			t.Fatalf("input exactly at limit reported ErrInputTooLarge")
		}
	})

	t.Run("unset is a no-op", func(t *testing.T) {
		_, err := Decode(readerOnly{bytes.NewReader(junk)})
		if errors.Is(err, ErrInputTooLarge) {
			t.Fatalf("default (no WithMaxInputSize) reported ErrInputTooLarge")
		}
		if !errors.Is(err, ErrUnknownFormat) {
			t.Fatalf("err = %v; want ErrUnknownFormat", err)
		}
	})

	t.Run("applies to ReadInfo", func(t *testing.T) {
		_, err := ReadInfo(readerOnly{bytes.NewReader(junk)}, WithMaxInputSize(10))
		if !errors.Is(err, ErrInputTooLarge) {
			t.Fatalf("ReadInfo err = %v; want ErrInputTooLarge", err)
		}
	})
}
