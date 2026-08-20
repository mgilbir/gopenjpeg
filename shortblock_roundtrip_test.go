package gopenjpeg

import (
	"bytes"
	"fmt"
	"testing"
)

// TestShortBlockRoundTrip pins the tier-1 partial-stripe remainder paths
// (issue #11): code-blocks shorter than 4 rows never enter the stripe loop, so
// every coefficient decodes through the remainder bodies, including the
// dedicated single-row fast path used by ecCodes/GRIB2 N x 1 codestreams.
// Lossless round-trips are self-validating — the decode must reproduce the
// source samples exactly — so any drift in those bodies fails here without
// needing the oracle. Heights 5-7 exercise the stripe+remainder mix, 8 the
// stripe-only control.
func TestShortBlockRoundTrip(t *testing.T) {
	encode := func(w, h int, extra ...EncodeOption) ([]byte, []int32) {
		data := make([]int32, w*h)
		var s uint64 = 0x5EED
		for i := range data {
			s = s*6364136223846793005 + 1442695040888963407
			data[i] = int32((i*7)%4096 + int(s>>60))
		}
		img := NewImage(ColorSpaceGray, 0, 0, uint32(w), uint32(h),
			[]Component{{Dx: 1, Dy: 1, W: uint32(w), H: uint32(h), Prec: 16, Data: data}})
		var buf bytes.Buffer
		opts := append([]EncodeOption{
			WithEncodeFormat(FormatJ2K),
			WithLossless(),
			WithCodeBlockSize(64, 64),
			WithResolutions(1),
		}, extra...)
		if err := Encode(img, &buf, opts...); err != nil {
			t.Fatalf("encode %dx%d: %v", w, h, err)
		}
		return buf.Bytes(), data
	}

	for _, h := range []int{1, 2, 3, 5, 6, 7, 8} {
		for _, v := range []struct {
			name  string
			extra []EncodeOption
		}{
			{"plain", nil},
			{"vsc", []EncodeOption{WithModeSwitches(8)}},
			{"lazy_termall", []EncodeOption{WithModeSwitches(1 | 4)}},
		} {
			t.Run(fmt.Sprintf("h%d_%s", h, v.name), func(t *testing.T) {
				w := 16384 / h
				stream, want := encode(w, h, v.extra...)
				img, err := Decode(bytes.NewReader(stream),
					WithFormat(FormatJ2K), WithStrictMode(true))
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				got := img.Component(0).Data
				if len(got) != len(want) {
					t.Fatalf("length %d != %d", len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("sample %d: got %d want %d", i, got[i], want[i])
					}
				}
			})
		}
	}
}
