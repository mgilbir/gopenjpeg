package gopenjpeg

import (
	"bytes"
	"strings"
	"testing"
)

// rgbImage builds a valid w x h, 8-bit three-component image for encode tests.
func rgbImage(w, h uint32) *Image {
	comps := make([]Component, 3)
	for c := range comps {
		data := make([]int32, int(w)*int(h))
		for i := range data {
			data[i] = int32((i*(c+1) + c*37) % 256)
		}
		comps[c] = Component{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: data}
	}
	return NewImage(ColorSpaceSRGB, 0, 0, w, h, comps)
}

// snapshotSamples copies every component's samples so they can be compared after
// an Encode call.
func snapshotSamples(im *Image) [][]int32 {
	out := make([][]int32, im.NumComponents())
	for c := range out {
		out[c] = append([]int32(nil), im.Component(c).Data...)
	}
	return out
}

// C12: Encode must not steal or mutate the caller's pixel data, and must be
// repeatable on the same *Image.
func TestEncodeDoesNotConsumeInputImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  *Image
		opts []EncodeOption
	}{
		{"j2k_gray", grayImage(64, 48), nil},
		{"j2k_rgb", rgbImage(48, 32), nil},
		{"jp2_rgb", rgbImage(48, 32), []EncodeOption{WithEncodeFormat(FormatJP2)}},
		{"j2k_rgb_irrev_rates", rgbImage(48, 32),
			[]EncodeOption{WithIrreversible(), WithRates(20, 10)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshotSamples(tc.img)

			var first bytes.Buffer
			if err := runEncode(t, tc.img, append([]EncodeOption(nil), tc.opts...)...); err != nil {
				t.Fatalf("first Encode: %v", err)
			}
			// Re-run capturing the bytes (runEncode discards them) so both the
			// panic guard and the byte comparison are covered.
			first.Reset()
			if err := Encode(tc.img, &first, tc.opts...); err != nil {
				t.Fatalf("second Encode: %v", err)
			}

			// The caller's samples must be untouched.
			for c, want := range before {
				got := tc.img.Component(c).Data
				if got == nil {
					t.Fatalf("component %d: Data was stolen (nil) by Encode", c)
				}
				if len(got) != len(want) {
					t.Fatalf("component %d: length %d, want %d", c, len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("component %d sample %d: got %d want %d", c, i, got[i], want[i])
					}
				}
			}

			// Encoding again must produce the identical codestream.
			var third bytes.Buffer
			if err := Encode(tc.img, &third, tc.opts...); err != nil {
				t.Fatalf("third Encode: %v", err)
			}
			if !bytes.Equal(first.Bytes(), third.Bytes()) {
				t.Fatalf("Encode is not idempotent: %d bytes then %d bytes",
					first.Len(), third.Len())
			}
			if third.Len() == 0 {
				t.Fatalf("Encode produced no output")
			}
		})
	}
}

// markerSegmentIn returns the payload of the first occurrence of marker (the two
// marker bytes plus the length-prefixed segment) in a raw codestream, or nil.
func markerSegmentIn(b []byte, marker uint16) []byte {
	hi, lo := byte(marker>>8), byte(marker)
	for i := 0; i+3 < len(b); i++ {
		if b[i] != hi || b[i+1] != lo {
			continue
		}
		length := int(b[i+2])<<8 | int(b[i+3])
		if length < 2 || i+2+length > len(b) {
			continue
		}
		return b[i : i+2+length]
	}
	return nil
}

// C26: profiles OpenJPEG cannot encode (long-term storage, broadcast) must be
// coerced to PROFILE_NONE, so SIZ carries Rsiz=0 and a warning is emitted.
func TestEncodeCoercesUnsupportedProfiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		rsiz uint16
		want string
	}{
		{"storage_LTS", 0x0007, "Long Term Storage"},
		{"broadcast_single", 0x0100, "Broadcast"},
		{"broadcast_single_level", 0x0103, "Broadcast"},
		{"broadcast_multi_r", 0x0300, "Broadcast"},
		{"broadcast_multi_r_top", 0x030b, "Broadcast"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warnings strings.Builder
			var out bytes.Buffer
			err := Encode(rgbImage(64, 48), &out,
				WithProfile(tc.rsiz),
				WithEncodeWarningHandler(func(s string) { warnings.WriteString(s) }))
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			siz := markerSegmentIn(out.Bytes(), 0xFF51)
			if len(siz) < 6 {
				t.Fatalf("no SIZ marker in output")
			}
			if siz[4] != 0 || siz[5] != 0 {
				t.Errorf("SIZ Rsiz = 0x%02x%02x, want 0x0000", siz[4], siz[5])
			}
			if !strings.Contains(warnings.String(), tc.want) {
				t.Errorf("missing %q warning, got %q", tc.want, warnings.String())
			}
			if !strings.Contains(warnings.String(), "not yet supported") {
				t.Errorf("missing 'not yet supported' warning, got %q", warnings.String())
			}
		})
	}
}

// C27: opj_j2k_mct_validation equivalents. Both configurations previously
// "encoded successfully" into codestreams no decoder accepts.
func TestEncodeRejectsInvalidCustomMCT(t *testing.T) {
	matrix := []float32{1, 1, 1, 0, 1, 1, 0, 0, 1}
	dc := []int32{0, 0, 0}

	t.Run("custom_mct_without_matrix", func(t *testing.T) {
		// Part-2/MCT profile plus mct=2, but no matrix was ever supplied.
		err := runEncode(t, rgbImage(48, 32), WithProfile(0x8100), WithIrreversible(), WithMCT(2))
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid encoder parameters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("custom_mct_with_reversible", func(t *testing.T) {
		// WithCustomMCT forces irreversible; a later WithLossless takes it back,
		// which the array-based MCT cannot express.
		err := runEncode(t, rgbImage(48, 32), WithCustomMCT(matrix, dc), WithLossless())
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid encoder parameters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid_custom_mct_still_encodes", func(t *testing.T) {
		var out bytes.Buffer
		if err := Encode(rgbImage(48, 32), &out, WithCustomMCT(matrix, dc)); err != nil {
			t.Fatalf("valid custom MCT rejected: %v", err)
		}
		if out.Len() == 0 {
			t.Fatalf("no output")
		}
	})
}

// C53: an explicitly empty comment must still emit a COM marker (FF64 0004
// 0001), matching opj_compress -C "". The oracle gate proves byte identity; this
// keeps the behaviour pinned without the oracle binaries.
func TestEncodeCommentMarker(t *testing.T) {
	t.Run("empty_comment_emits_empty_COM", func(t *testing.T) {
		var out bytes.Buffer
		if err := Encode(grayImage(32, 32), &out, WithComment("")); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		com := markerSegmentIn(out.Bytes(), 0xFF64)
		if com == nil {
			t.Fatalf("no COM marker emitted for WithComment(\"\")")
		}
		if want := []byte{0xFF, 0x64, 0x00, 0x04, 0x00, 0x01}; !bytes.Equal(com, want) {
			t.Errorf("COM segment = % x, want % x", com, want)
		}
	})

	t.Run("default_comment_is_openjpeg_version", func(t *testing.T) {
		var out bytes.Buffer
		if err := Encode(grayImage(32, 32), &out); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		com := markerSegmentIn(out.Bytes(), 0xFF64)
		if com == nil {
			t.Fatalf("no COM marker emitted by default")
		}
		if !bytes.Contains(com, []byte("Created by OpenJPEG version")) {
			t.Errorf("default COM payload = %q", com)
		}
	})

	t.Run("explicit_comment_round_trips", func(t *testing.T) {
		var out bytes.Buffer
		if err := Encode(grayImage(32, 32), &out, WithComment("hello")); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		com := markerSegmentIn(out.Bytes(), 0xFF64)
		want := []byte{0xFF, 0x64, 0x00, 0x09, 0x00, 0x01, 'h', 'e', 'l', 'l', 'o'}
		if !bytes.Equal(com, want) {
			t.Errorf("COM segment = % x, want % x", com, want)
		}
	})
}

// C29: the rate/quality ordering diagnostics and the max-codestream-size cap
// warning are message-only; they must fire without changing the output.
func TestEncodeLayerOrderingDiagnostics(t *testing.T) {
	encodeWithWarnings := func(t *testing.T, opts ...EncodeOption) (string, []byte) {
		t.Helper()
		var warnings strings.Builder
		var out bytes.Buffer
		opts = append(opts, WithEncodeWarningHandler(func(s string) { warnings.WriteString(s) }))
		if err := Encode(grayImage(64, 48), &out, opts...); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		return warnings.String(), out.Bytes()
	}

	t.Run("non_decreasing_rates_warn", func(t *testing.T) {
		w, withWarn := encodeWithWarnings(t, WithRates(10, 20))
		if !strings.Contains(w, "tcp_rates[1]") || !strings.Contains(w, "strictly lesser") {
			t.Errorf("missing rate-ordering warning, got %q", w)
		}
		// Message-only: the same parameters without a warning handler must
		// produce the identical codestream.
		var quiet bytes.Buffer
		if err := Encode(grayImage(64, 48), &quiet, WithRates(10, 20)); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if !bytes.Equal(withWarn, quiet.Bytes()) {
			t.Errorf("diagnostics changed the codestream")
		}
	})

	t.Run("non_increasing_distoratio_warns", func(t *testing.T) {
		w, _ := encodeWithWarnings(t, WithIrreversible(), WithQualityLayers(40, 30))
		if !strings.Contains(w, "tcp_distoratio[1]") || !strings.Contains(w, "strictly greater") {
			t.Errorf("missing distoratio-ordering warning, got %q", w)
		}
	})

	t.Run("trailing_zero_distoratio_is_exempt", func(t *testing.T) {
		w, _ := encodeWithWarnings(t, WithIrreversible(), WithQualityLayers(30, 40, 0))
		if strings.Contains(w, "tcp_distoratio") {
			t.Errorf("unexpected distoratio warning for a lossless final layer: %q", w)
		}
	})

	t.Run("max_codestream_size_cap_warns", func(t *testing.T) {
		w, _ := encodeWithWarnings(t, WithRates(2), WithMaxCodestreamSize(400))
		if !strings.Contains(w, "maximum codestream size has limited") {
			t.Errorf("missing max-codestream-size cap warning, got %q", w)
		}
	})
}
