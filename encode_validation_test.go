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
		// An unrecognised progression order makes ConvertProgressionOrder return
		// the empty sentinel string, which pi.CreateEncode then indexes at
		// [0..3]: C reads past a static NUL, Go panicked. Found by
		// FuzzEncodeOptions; it only bites once tile-parts put CreateEncode on
		// the path, so both the bare and the tile-part form are pinned here.
		{"prog_order_unknown", grayImage(16, 16), []EncodeOption{WithProgressionOrder(ProgressionOrder(-1))}},
		{"prog_order_unknown_tileparts", grayImage(16, 16), []EncodeOption{
			WithProgressionOrder(ProgressionOrder(-1)), WithTileParts('R')}},
		{"prog_order_too_large", grayImage(16, 16), []EncodeOption{WithProgressionOrder(ProgressionOrder(5))}},
		// A tiling origin outside the image made img.X1-Tx0 underflow in uint32.
		// Without an explicit tile size that underflow BECOMES the tile width, so
		// the tile-component buffers were sized at ~4 GiB — reachable from two
		// option calls, and the reason a fuzz worker was being OOM-killed.
		{"tile_origin_past_image", grayImage(16, 16), []EncodeOption{WithTileOrigin(64, 64)}},
		{"tile_origin_at_image_edge", grayImage(16, 16), []EncodeOption{WithTileOrigin(16, 0)}},
		// MCT applicability: opj_tcd_mct_encode walks every component with
		// component 0's sample count and, for mct==1, dereferences comps[1]/[2]
		// unconditionally. OpenJPEG rejected both at its CLI; this port has to do
		// it at Encode. Found by FuzzEncodeOptions.
		{"mct_with_two_components", NewImage(ColorSpaceGray, 0, 0, 16, 16, []Component{
			{Dx: 1, Dy: 1, W: 16, H: 16, Prec: 8, Data: make([]int32, 256)},
			{Dx: 1, Dy: 1, W: 16, H: 16, Prec: 8, Data: make([]int32, 256)},
		}), []EncodeOption{WithMCT(1)}},
		{"custom_mct_subsampled_components", NewImage(ColorSpaceSRGB, 0, 0, 16, 16, []Component{
			{Dx: 1, Dy: 1, W: 16, H: 16, Prec: 8, Data: make([]int32, 256)},
			{Dx: 2, Dy: 2, W: 8, H: 8, Prec: 8, Data: make([]int32, 64)},
			{Dx: 2, Dy: 2, W: 8, H: 8, Prec: 8, Data: make([]int32, 64)},
		}), []EncodeOption{WithCustomMCT(make([]float32, 9), make([]int32, 3))}},
		{"custom_mct_without_matrix", grayImage(16, 16), []EncodeOption{WithMCT(2)}},
		// The encoder derives a component's size from the reference grid twice,
		// with two formulas that only agree when the image offset is a multiple
		// of the sub-sampling factors; where they disagree it read past the
		// component data (GetTileData) or handed a zero-length row to the DWT.
		// Both shapes were found by FuzzEncodeOptions.
		{"unaligned_offset_subsampled", NewImage(ColorSpaceSRGB, 1, 0, 16, 15, []Component{
			{Dx: 1, Dy: 1, W: 15, H: 15, Prec: 8, Data: make([]int32, 225)},
			{Dx: 2, Dy: 2, W: 7, H: 8, Prec: 8, Data: make([]int32, 56)},
			{Dx: 2, Dy: 2, W: 7, H: 8, Prec: 8, Data: make([]int32, 56)},
		}), nil},
		{"empty_component_extent", NewImage(ColorSpaceGray, 1, 0, 2, 15, []Component{
			{Dx: 1, Dy: 1, W: 1, H: 15, Prec: 8, Data: make([]int32, 15)},
			{Dx: 2, Dy: 2, W: 1, H: 8, Prec: 8, Data: make([]int32, 8)},
		}), nil},
		{"poc_prog_order_unknown", grayImage(16, 16), []EncodeOption{
			WithTileParts('R'),
			WithPOC(POCChange{Tile: 1, ResEnd: 3, CompEnd: 1, LayEnd: 1, Order: ProgressionOrder(9)}),
		}},
		// An unrecognised tile-part grouping never matches the progression
		// string in get_num_tp, so the packet iterator re-visits packets until
		// cblk.Numpasses walks past the passes array. opj_compress only ever
		// passes R, L or C. Found by FuzzEncodeOptions.
		{"tile_parts_bad_flag", grayImage(16, 16), []EncodeOption{WithTileParts('X')}},
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
	// A properly formed 4:2:0 image, offset by a multiple of the sub-sampling
	// factors, must still encode: the geometry checks added for the unaligned
	// case must not reject legitimate chroma sub-sampling.
	sub := NewImage(ColorSpaceSYCC, 2, 2, 34, 34, []Component{
		{Dx: 1, Dy: 1, W: 32, H: 32, Prec: 8, Data: make([]int32, 32*32)},
		{Dx: 2, Dy: 2, W: 16, H: 16, Prec: 8, Data: make([]int32, 16*16)},
		{Dx: 2, Dy: 2, W: 16, H: 16, Prec: 8, Data: make([]int32, 16*16)},
	})
	if err := runEncode(t, sub, WithResolutions(3), WithMCT(0)); err != nil {
		t.Fatalf("sub-sampled encode returned error: %v", err)
	}
	// A 15x1 image at offset (2,2), tiled 16x16, produces a second tile whose
	// higher resolutions are 1x0: opj_dwt_encode_procedure still runs the
	// vertical pass over that zero-height resolution, and the odd-length branch
	// then indexed the scratch buffer past its end (C reads its over-allocated
	// buffer). Found by FuzzEncodeOptions; it must encode, not panic.
	thin := make([]int32, 15)
	for i := range thin {
		thin[i] = int32(i * 17)
	}
	strip := NewImage(ColorSpaceGray, 2, 2, 17, 3, []Component{
		{Dx: 1, Dy: 1, W: 15, H: 1, Prec: 14, Data: thin},
	})
	if err := runEncode(t, strip, WithResolutions(5), WithTileSize(16, 16)); err != nil {
		t.Fatalf("thin tiled encode returned error: %v", err)
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
