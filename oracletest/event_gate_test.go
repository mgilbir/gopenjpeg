package oracletest

import (
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
)

// infoWholeImageArea is the C-ported literal opj_j2k_set_decode_area emits when
// called with an all-zero area (the gates' normal configuration). Message text
// ported verbatim from OpenJPEG is stable by construction — it is part of the
// parity surface — so asserting on it is not flaky.
const infoWholeImageArea = "No decoded area parameters, set the decoded area to the whole image"

// warnTilePartLength is the C-ported warning opj_j2k_read_sot emits when the
// declared tile-part length exceeds what the stream can hold, i.e. for any
// truncated codestream.
const warnTilePartLength = "Tile part length size inconsistent with stream length"

// TestEventEmissionPath covers C61 directly: before this, every gate passed a
// nil *event.Manager, so nothing in the harness ever executed the warn/info
// emission path. This exercises it end to end on both a well-formed and a
// truncated codestream, and asserts the two stable C-ported messages actually
// reach the installed handlers.
func TestEventEmissionPath(t *testing.T) {
	Require(t)

	path := DataDir("input", "conformance", "p0_01.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("corpus file unavailable: %v", err)
	}

	decode := func(t *testing.T, in []byte, strict bool) (*EventCollector, error) {
		t.Helper()
		var ec EventCollector
		mgr := ec.Manager()
		s := cio.NewMemoryInputStream(in)
		d := j2k.CreateDecompress()
		d.SetupDecoder(0, 0)
		d.SetStrictMode(strict)
		img, err := d.ReadHeader(s, mgr)
		if err != nil {
			return &ec, err
		}
		if err := d.SetDecodeArea(img, 0, 0, 0, 0); err != nil {
			return &ec, err
		}
		return &ec, d.Decode(s, img, mgr)
	}

	t.Run("info_on_valid_stream", func(t *testing.T) {
		ec, err := decode(t, data, false)
		if err != nil {
			t.Fatalf("decode of a conformance file failed: %v", err)
		}
		ec.Check(t, "valid stream")
		if !ec.Contains(infoWholeImageArea) {
			t.Fatalf("expected %q on the info channel, got %q", infoWholeImageArea, ec.All())
		}
		if len(ec.Errors()) != 0 {
			t.Fatalf("a conformance file must not emit errors, got %q", ec.Errors())
		}
	})

	t.Run("warning_on_truncated_stream", func(t *testing.T) {
		ec, _ := decode(t, data[:len(data)/2], false)
		ec.Check(t, "truncated stream")
		if !ec.Contains(warnTilePartLength) {
			t.Fatalf("expected %q on the warning channel, got %q", warnTilePartLength, ec.All())
		}
		if len(ec.Warnings()) == 0 {
			t.Fatalf("a truncated codestream must produce warnings, got none")
		}
	})

	t.Run("no_message_carries_a_bad_verb", func(t *testing.T) {
		// A wrong argument count in a ported format string surfaces as "%!"
		// (see C50). Sweep a few truncation points to hit varied code paths.
		for _, frac := range []int{2, 3, 4, 8, 16, 32} {
			ec, _ := decode(t, data[:len(data)/frac], true)
			for _, m := range ec.All() {
				if strings.Contains(m, "%!") || strings.TrimSpace(m) == "" {
					t.Errorf("frac=%d: malformed diagnostic %q", frac, m)
				}
			}
		}
	})
}
