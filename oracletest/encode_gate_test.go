package oracletest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
)

// synthImage is a raw sample image used to drive both opj_compress and the Go
// encoder from identical pixels.
type synthImage struct {
	w, h     uint32
	numcomps uint32
	prec     uint32
	// data[c] holds w*h samples for component c.
	data [][]int32
}

// grayGradient builds a w x h 8-bit grayscale image.
func grayGradient(w, h uint32) *synthImage {
	d := make([]int32, w*h)
	for y := uint32(0); y < h; y++ {
		for x := uint32(0); x < w; x++ {
			d[y*w+x] = int32((x*7 + y*13 + (x^y)*3) & 0xff)
		}
	}
	return &synthImage{w: w, h: h, numcomps: 1, prec: 8, data: [][]int32{d}}
}

// rgb builds a w x h 8-bit RGB image.
func rgb(w, h uint32) *synthImage {
	r := make([]int32, w*h)
	g := make([]int32, w*h)
	b := make([]int32, w*h)
	for y := uint32(0); y < h; y++ {
		for x := uint32(0); x < w; x++ {
			r[y*w+x] = int32((x * 5) & 0xff)
			g[y*w+x] = int32((y * 3) & 0xff)
			b[y*w+x] = int32((x*2 + y*2) & 0xff)
		}
	}
	return &synthImage{w: w, h: h, numcomps: 3, prec: 8, data: [][]int32{r, g, b}}
}

// writePNM writes the synthetic image as a binary PGM (P5) or PPM (P6) file.
func (s *synthImage) writePNM(path string) error {
	var buf bytes.Buffer
	if s.numcomps == 1 {
		fmt.Fprintf(&buf, "P5\n%d %d\n255\n", s.w, s.h)
		for i := uint32(0); i < s.w*s.h; i++ {
			buf.WriteByte(byte(s.data[0][i]))
		}
	} else {
		fmt.Fprintf(&buf, "P6\n%d %d\n255\n", s.w, s.h)
		for i := uint32(0); i < s.w*s.h; i++ {
			buf.WriteByte(byte(s.data[0][i]))
			buf.WriteByte(byte(s.data[1][i]))
			buf.WriteByte(byte(s.data[2][i]))
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// toImage builds an image.Image from the synthetic samples.
func (s *synthImage) toImage() *image.Image {
	img := &image.Image{
		X0: 0, Y0: 0, X1: s.w, Y1: s.h, Numcomps: s.numcomps,
		ColorSpace: image.ClrspcGray,
	}
	if s.numcomps >= 3 {
		img.ColorSpace = image.ClrspcSRGB
	}
	img.Comps = make([]image.Comp, s.numcomps)
	for c := uint32(0); c < s.numcomps; c++ {
		data := make([]int32, s.w*s.h)
		copy(data, s.data[c])
		img.Comps[c] = image.Comp{
			Dx: 1, Dy: 1, W: s.w, H: s.h, X0: 0, Y0: 0,
			Prec: s.prec, Sgnd: 0, Data: data,
		}
	}
	return img
}

// defaultCParams mirrors opj_set_default_encoder_parameters plus the opj_compress
// CLI defaults that affect the codestream.
func defaultCParams(numcomps uint32) j2k.CParameters {
	var p j2k.CParameters
	p.Rsiz = cparams.ProfileNone
	p.NumResolution = 6
	p.CblockWInit = 64
	p.CblockHInit = 64
	p.ProgOrder = cparams.LRCP
	p.RoiCompno = -1
	// opj_compress sets tcp_mct = (numcomps>=3)?1:0 when -mct is not given.
	if numcomps >= 3 {
		p.TcpMct = 1
	}
	return p
}

// goEncode encodes img with the Go encoder and returns the codestream bytes.
func goEncode(params *j2k.CParameters, img *image.Image) ([]byte, error) {
	var mgr *event.Manager // nil: nil-safe throughout (see decode_test note)
	enc := j2k.CreateCompress()
	if err := enc.SetupEncoder(params, img, mgr); err != nil {
		return nil, fmt.Errorf("SetupEncoder: %w", err)
	}
	stream := cio.NewMemoryOutputStream()
	if err := enc.StartCompress(stream, img, mgr); err != nil {
		return nil, fmt.Errorf("StartCompress: %w", err)
	}
	if err := enc.Encode(stream, mgr); err != nil {
		return nil, fmt.Errorf("Encode: %w", err)
	}
	if err := enc.EndCompress(stream, mgr); err != nil {
		return nil, fmt.Errorf("EndCompress: %w", err)
	}
	return stream.Bytes(), nil
}

// runCompress runs opj_compress and returns the produced .j2k bytes.
func runCompress(t *testing.T, input string, flags []string) ([]byte, error) {
	dir := filepath.Dir(input)
	out := filepath.Join(dir, "c_out.j2k")
	args := append([]string{"-i", input, "-o", out}, flags...)
	cmd := exec.Command(Bin("opj_compress"), args...)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("opj_compress %v: %v\n%s", args, err, combined)
	}
	return os.ReadFile(out)
}

// gateCase is one row of the settings matrix.
type gateCase struct {
	name  string
	img   *synthImage
	flags []string
	// mutate adjusts the Go CParameters to mirror the opj_compress flags.
	mutate func(p *j2k.CParameters)
}

func TestEncodeGate(t *testing.T) {
	Require(t)

	gray := grayGradient(160, 96)
	color := rgb(96, 64)

	cases := []gateCase{
		{name: "lossless_5x3_default_gray", img: gray},
		{name: "lossless_5x3_default_rgb", img: color},
		{
			name: "irrev_9x7_rates_gray", img: gray,
			flags:  []string{"-I", "-r", "20,10,5"},
			mutate: func(p *j2k.CParameters) { p.Irreversible = 1; setRates(p, 20, 10, 5) },
		},
		{
			name: "irrev_9x7_quality_gray", img: gray,
			flags:  []string{"-I", "-q", "30,40"},
			mutate: func(p *j2k.CParameters) { p.Irreversible = 1; setQuality(p, 30, 40) },
		},
		{
			name: "layers_rates_rgb", img: color,
			flags:  []string{"-r", "40,20,10,1"},
			mutate: func(p *j2k.CParameters) { setRates(p, 40, 20, 10, 1) },
		},
		{
			name: "tiling_128_gray", img: gray,
			flags:  []string{"-t", "128,128"},
			mutate: func(p *j2k.CParameters) { p.TileSizeOn = true; p.CpTdx = 128; p.CpTdy = 128 },
		},
		{
			name: "numres_3_gray", img: gray,
			flags:  []string{"-n", "3"},
			mutate: func(p *j2k.CParameters) { p.NumResolution = 3 },
		},
		{
			name: "precincts_gray", img: gray,
			flags: []string{"-c", "[128,128],[128,128]"},
			mutate: func(p *j2k.CParameters) {
				p.Csty |= 0x01
				p.ResSpec = 2
				p.PrcwInit[0], p.PrchInit[0] = 128, 128
				p.PrcwInit[1], p.PrchInit[1] = 128, 128
			},
		},
		{
			name: "prog_RLCP_rgb", img: color,
			flags:  []string{"-p", "RLCP"},
			mutate: func(p *j2k.CParameters) { p.ProgOrder = cparams.RLCP },
		},
		{
			name: "prog_RPCL_rgb", img: color,
			flags:  []string{"-p", "RPCL"},
			mutate: func(p *j2k.CParameters) { p.ProgOrder = cparams.RPCL },
		},
		{
			name: "sop_eph_gray", img: gray,
			flags:  []string{"-SOP", "-EPH"},
			mutate: func(p *j2k.CParameters) { p.Csty |= 0x02 | 0x04 },
		},
		{
			name: "mode_lazy_gray", img: gray,
			flags:  []string{"-M", "1"},
			mutate: func(p *j2k.CParameters) { p.Mode = 1 },
		},
		{
			name: "mode_termall_gray", img: gray,
			flags:  []string{"-M", "4"},
			mutate: func(p *j2k.CParameters) { p.Mode = 4 },
		},
		{
			name: "mode_reset_vsc_gray", img: gray,
			flags:  []string{"-M", "10"},
			mutate: func(p *j2k.CParameters) { p.Mode = 10 },
		},
		{
			name: "mode_segsym_gray", img: gray,
			flags:  []string{"-M", "32"},
			mutate: func(p *j2k.CParameters) { p.Mode = 32 },
		},
		{
			name: "mode_pterm_gray", img: gray,
			flags:  []string{"-M", "16"},
			mutate: func(p *j2k.CParameters) { p.Mode = 16 },
		},
		{
			name: "tp_R_rgb", img: color,
			flags:  []string{"-TP", "R"},
			mutate: func(p *j2k.CParameters) { p.TpOn = 1; p.TpFlag = 'R' },
		},
		{
			name: "roi_gray", img: gray,
			flags:  []string{"-ROI", "c=0,U=3"},
			mutate: func(p *j2k.CParameters) { p.RoiCompno = 0; p.RoiShift = 3 },
		},
		{
			name: "mct_off_rgb", img: color,
			flags:  []string{"-mct", "0"},
			mutate: func(p *j2k.CParameters) { p.TcpMct = 0 },
		},
		{
			name: "mct_on_irrev_rgb", img: color,
			flags:  []string{"-I", "-mct", "1"},
			mutate: func(p *j2k.CParameters) { p.Irreversible = 1; p.TcpMct = 1 },
		},
	}

	dir := t.TempDir()
	var identical, total int
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			total++
			ext := ".pgm"
			if tc.img.numcomps >= 3 {
				ext = ".ppm"
			}
			input := filepath.Join(dir, tc.name+ext)
			if err := tc.img.writePNM(input); err != nil {
				t.Fatalf("write input: %v", err)
			}

			cbytes, err := runCompress(t, input, tc.flags)
			if err != nil {
				t.Fatalf("opj_compress: %v", err)
			}

			params := defaultCParams(tc.img.numcomps)
			if tc.mutate != nil {
				tc.mutate(&params)
			}
			gbytes, err := goEncode(&params, tc.img.toImage())
			if err != nil {
				t.Fatalf("goEncode: %v", err)
			}

			if bytes.Equal(cbytes, gbytes) {
				identical++
				return
			}
			t.Errorf("codestream mismatch: go=%d bytes, c=%d bytes; first diff at %d",
				len(gbytes), len(cbytes), firstDiff(gbytes, cbytes))
		})
	}
	t.Logf("byte-identical: %d/%d", identical, total)
}

// setRates mirrors opj_compress -r: rates + cp_disto_alloc, numlayers.
func setRates(p *j2k.CParameters, rates ...float32) {
	p.CpDistoAlloc = 1
	p.TcpNumlayers = int32(len(rates))
	for i, r := range rates {
		p.TcpRates[i] = r
	}
}

// setQuality mirrors opj_compress -q: distoratio + cp_fixed_quality, numlayers.
func setQuality(p *j2k.CParameters, q ...float32) {
	p.CpFixedQuality = 1
	p.TcpNumlayers = int32(len(q))
	for i, v := range q {
		p.TcpDistoratio[i] = v
	}
}

// TestEncodeRoundTrip encodes lossless with the Go encoder, decodes with the
// Go decoder, and checks the pixels are recovered exactly.
func TestEncodeRoundTrip(t *testing.T) {
	Require(t)
	for _, s := range []*synthImage{grayGradient(80, 48), rgb(64, 40)} {
		params := defaultCParams(s.numcomps)
		gbytes, err := goEncode(&params, s.toImage())
		if err != nil {
			t.Fatalf("goEncode: %v", err)
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "rt.j2k")
		if err := os.WriteFile(path, gbytes, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		img, err := decodeGo(t, path, 0, 0, nil)
		if err != nil {
			t.Fatalf("decodeGo: %v", err)
		}
		if img.Numcomps != s.numcomps {
			t.Fatalf("numcomps: got %d want %d", img.Numcomps, s.numcomps)
		}
		for c := uint32(0); c < s.numcomps; c++ {
			for i := uint32(0); i < s.w*s.h; i++ {
				if img.Comps[c].Data[i] != s.data[c][i] {
					t.Fatalf("comp %d sample %d: got %d want %d",
						c, i, img.Comps[c].Data[i], s.data[c][i])
				}
			}
		}
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}
