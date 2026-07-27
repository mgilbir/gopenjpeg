package image

import "testing"

func validComp() Comp {
	return Comp{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}
}

// grid4 is the reference grid a 4x4, dx=dy=1 component belongs to. The encoder
// re-derives every component's geometry from the grid, so the fixtures have to
// carry a grid that matches their components.
func grid4(numcomps uint32, comps ...Comp) *Image {
	return &Image{X1: 4, Y1: 4, Numcomps: numcomps, Comps: comps}
}

func TestValidateForEncode(t *testing.T) {
	cases := []struct {
		name    string
		img     *Image
		wantErr bool
	}{
		{
			name:    "valid",
			img:     grid4(1, validComp()),
			wantErr: false,
		},
		{
			name:    "zero_components",
			img:     &Image{Numcomps: 0},
			wantErr: true,
		},
		{
			name:    "comps_slice_too_short",
			img:     grid4(2, validComp()),
			wantErr: true,
		},
		{
			name:    "zero_width",
			img:     grid4(1, Comp{Dx: 1, Dy: 1, W: 0, H: 4, Prec: 8, Data: make([]int32, 16)}),
			wantErr: true,
		},
		{
			name:    "zero_dx",
			img:     grid4(1, Comp{Dx: 0, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}),
			wantErr: true,
		},
		{
			name:    "dx_over_255",
			img:     grid4(1, Comp{Dx: 256, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}),
			wantErr: true,
		},
		{
			name: "dx_255_ok",
			// 4 samples at dx=dy=255 span a 1020x1020 reference grid.
			img: &Image{X1: 1020, Y1: 1020, Numcomps: 1, Comps: []Comp{
				{Dx: 255, Dy: 255, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)},
			}},
			wantErr: false,
		},
		{
			name:    "zero_prec",
			img:     grid4(1, Comp{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 0, Data: make([]int32, 16)}),
			wantErr: true,
		},
		{
			name:    "prec_over_31",
			img:     grid4(1, Comp{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 32, Data: make([]int32, 16)}),
			wantErr: true,
		},
		{
			name:    "data_too_short",
			img:     grid4(1, Comp{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 15)}),
			wantErr: true,
		},
		{
			// The declared size disagrees with the reference grid: the encoder
			// walks the buffer with the grid-derived dimensions, so this either
			// over-reads or feeds a zero-length row to the DWT.
			name: "geometry_disagrees_with_grid",
			img: &Image{X1: 8, Y1: 4, Numcomps: 1, Comps: []Comp{
				{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)},
			}},
			wantErr: true,
		},
		{
			// The standard's ceildiv(x1,dx)-ceildiv(x0,dx) says 7 wide, but the
			// encoder strides the source buffer with ceildiv(x1-x0,dx) == 8 and
			// would read 8 samples per row out of a 7-wide plane.
			name: "offset_not_a_multiple_of_subsampling",
			img: &Image{X0: 1, X1: 16, Y0: 0, Y1: 16, Numcomps: 1, Comps: []Comp{
				{Dx: 2, Dy: 2, W: 7, H: 8, Prec: 8, Data: make([]int32, 56)},
			}},
			wantErr: true,
		},
		{
			// A 1-pixel-wide image with dx=2 has an empty component extent
			// (ceildiv(2,2)-ceildiv(1,2) == 0): dwt.Encode indexed row[0].
			name: "empty_extent_from_subsampling",
			img: &Image{X0: 1, X1: 2, Y0: 0, Y1: 4, Numcomps: 1, Comps: []Comp{
				{Dx: 2, Dy: 1, W: 1, H: 4, Prec: 8, Data: make([]int32, 4)},
			}},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.img.ValidateForEncode()
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateForEncode() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
