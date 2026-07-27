package gopenjpeg

import (
	"bytes"
	"strings"
	"testing"
)

// grayImage builds a valid w x h, 8-bit single-component image for encode tests.
func grayImage(w, h uint32) *Image {
	data := make([]int32, int(w)*int(h))
	for i := range data {
		data[i] = int32(i % 256)
	}
	return NewImage(ColorSpaceGray, 0, 0, w, h, []Component{
		{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: data},
	})
}

// runEncode runs Encode under a recover guard so a panic is reported as a test
// failure (the whole point of the validation is that these inputs no longer
// panic) rather than crashing the test binary. It returns the error Encode
// produced.
func runEncode(t *testing.T, img *Image, opts ...EncodeOption) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Encode panicked: %v", r)
		}
	}()
	return Encode(img, &bytes.Buffer{}, opts...)
}

func TestEncodeValidationRejectsWithoutPanic(t *testing.T) {
	// A slice of 101 entries exceeds the fixed [100] rate/quality arrays.
	rates101 := make([]float32, 101)
	for i := range rates101 {
		rates101[i] = float32(101 - i)
	}
	pocs33 := make([]POCChange, 33)

	cases := []struct {
		name string
		img  *Image
		opts []EncodeOption
	}{
		{"C1_rates_over_100", grayImage(16, 16), []EncodeOption{WithRates(rates101...)}},
		{"C1_quality_over_100", grayImage(16, 16), []EncodeOption{WithQualityLayers(rates101...)}},
		{"C2_poc_over_32", grayImage(16, 16), []EncodeOption{WithPOC(pocs33...)}},
		{"C3_precincts_empty", grayImage(16, 16), []EncodeOption{WithPrecincts()}},
		{"C5_degenerate_tile_grid", grayImage(16, 16), []EncodeOption{WithTileSize(16, 16), WithTileOrigin(0, 16)}},
		{"C44_negative_tile_size", grayImage(16, 16), []EncodeOption{WithTileSize(-1, -1)}},
		{"C44_negative_tile_origin", grayImage(16, 16), []EncodeOption{WithTileOrigin(-1, -1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runEncode(t, tc.img, tc.opts...); err == nil {
				t.Fatalf("expected an error, got nil")
			}
		})
	}
}

func TestEncodeValidationRejectsBadImages(t *testing.T) {
	shortData := NewImage(ColorSpaceGray, 0, 0, 16, 16, []Component{
		{Dx: 1, Dy: 1, W: 16, H: 16, Prec: 8, Data: make([]int32, 10)},
	})
	cases := []struct {
		name string
		img  *Image
	}{
		{"C4_zero_components", NewImage(ColorSpaceGray, 0, 0, 4, 4, nil)},
		{"C6_zero_dx", NewImage(ColorSpaceGray, 0, 0, 4, 4, []Component{{Dx: 0, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}})},
		{"C6_zero_dy", NewImage(ColorSpaceGray, 0, 0, 4, 4, []Component{{Dx: 1, Dy: 0, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}})},
		{"C6_dx_over_255", NewImage(ColorSpaceGray, 0, 0, 4, 4, []Component{{Dx: 256, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}})},
		{"C21_zero_width", NewImage(ColorSpaceGray, 0, 0, 4, 4, []Component{{Dx: 1, Dy: 1, W: 0, H: 4, Prec: 8, Data: make([]int32, 16)}})},
		{"C21_zero_prec", NewImage(ColorSpaceGray, 0, 0, 4, 4, []Component{{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 0, Data: make([]int32, 16)}})},
		{"C21_prec_over_31", NewImage(ColorSpaceGray, 0, 0, 4, 4, []Component{{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 32, Data: make([]int32, 16)}})},
		{"C21_short_data", shortData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runEncode(t, tc.img); err == nil {
				t.Fatalf("expected an error, got nil")
			}
		})
	}
}

// TestEncodeValidationAcceptsValidInput guards against the validation rejecting
// a legitimate image (the byte-identity gate covers output equality; this only
// confirms the happy path still returns nil).
func TestEncodeValidationAcceptsValidInput(t *testing.T) {
	// 64x64 comfortably admits the default 6 resolution levels.
	if err := runEncode(t, grayImage(64, 64)); err != nil {
		t.Fatalf("valid encode returned error: %v", err)
	}
	if err := runEncode(t, grayImage(64, 64), WithRates(20)); err != nil {
		t.Fatalf("valid rate encode returned error: %v", err)
	}
	// Exactly 100 layers is the boundary the fixed arrays support.
	rates100 := make([]float32, 100)
	for i := range rates100 {
		rates100[i] = float32(200 - i)
	}
	if err := runEncode(t, grayImage(64, 64), WithRates(rates100...)); err != nil {
		t.Fatalf("100-layer encode returned error: %v", err)
	}
}

func TestDecodeRejectsOversizedTileIndex(t *testing.T) {
	// 1<<33 does not survive the uint32 cast in the get-decoded-tile path.
	tileIdx := int(int64(1) << 33)
	if int64(tileIdx) <= 0xFFFFFFFF {
		t.Skip("int is not wide enough to trigger the truncation on this platform")
	}
	_, err := Decode(bytes.NewReader([]byte{0, 0, 0, 0}), WithTile(tileIdx))
	if err == nil {
		t.Fatalf("expected an error for an oversized tile index")
	}
	if !strings.Contains(err.Error(), "tile index") {
		t.Fatalf("expected a tile-index error, got: %v", err)
	}
}
