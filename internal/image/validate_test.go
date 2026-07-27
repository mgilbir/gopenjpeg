package image

import "testing"

func validComp() Comp {
	return Comp{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)}
}

func TestValidateForEncode(t *testing.T) {
	cases := []struct {
		name    string
		img     *Image
		wantErr bool
	}{
		{
			name:    "valid",
			img:     &Image{Numcomps: 1, Comps: []Comp{validComp()}},
			wantErr: false,
		},
		{
			name:    "zero_components",
			img:     &Image{Numcomps: 0},
			wantErr: true,
		},
		{
			name:    "comps_slice_too_short",
			img:     &Image{Numcomps: 2, Comps: []Comp{validComp()}},
			wantErr: true,
		},
		{
			name: "zero_width",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 1, Dy: 1, W: 0, H: 4, Prec: 8, Data: make([]int32, 16)},
			}},
			wantErr: true,
		},
		{
			name: "zero_dx",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 0, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)},
			}},
			wantErr: true,
		},
		{
			name: "dx_over_255",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 256, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)},
			}},
			wantErr: true,
		},
		{
			name: "dx_255_ok",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 255, Dy: 255, W: 4, H: 4, Prec: 8, Data: make([]int32, 16)},
			}},
			wantErr: false,
		},
		{
			name: "zero_prec",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 0, Data: make([]int32, 16)},
			}},
			wantErr: true,
		},
		{
			name: "prec_over_31",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 32, Data: make([]int32, 16)},
			}},
			wantErr: true,
		},
		{
			name: "data_too_short",
			img: &Image{Numcomps: 1, Comps: []Comp{
				{Dx: 1, Dy: 1, W: 4, H: 4, Prec: 8, Data: make([]int32, 15)},
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
