package oracletest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
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

// --- Container / profile / marker gate cells (public API) ---

// synthImage2 extends the synthetic image with a configurable precision, for
// cinema (12-bit) and higher-precision inputs.
type synthImage2 struct {
	w, h     uint32
	numcomps uint32
	prec     uint32
	data     [][]int32
}

// gradient3 builds a w x h, prec-bit 3-component smooth gradient.
func gradient3(w, h, prec uint32) *synthImage2 {
	mask := (int32(1) << prec) - 1
	d := make([][]int32, 3)
	for c := 0; c < 3; c++ {
		d[c] = make([]int32, w*h)
		for y := uint32(0); y < h; y++ {
			for x := uint32(0); x < w; x++ {
				d[c][y*w+x] = int32((x*5 + y*3 + uint32(c)*11)) & mask
			}
		}
	}
	return &synthImage2{w: w, h: h, numcomps: 3, prec: prec, data: d}
}

// noisy3 builds a w x h, prec-bit 3-component pseudo-random image (avoids
// all-zero code-blocks, which trip a pre-existing mqc.Bytes panic on empty
// blocks in the encode engine — see the W13 status note).
func noisy3(w, h, prec uint32, seed int64) *synthImage2 {
	max := int32(1) << prec
	s := int64(seed)
	rnd := func() int32 {
		s = s*6364136223846793005 + 1442695040888963407
		return int32((s>>33)&0x7fffffff) % max
	}
	d := make([][]int32, 3)
	for c := 0; c < 3; c++ {
		d[c] = make([]int32, w*h)
		for i := range d[c] {
			d[c][i] = rnd()
		}
	}
	return &synthImage2{w: w, h: h, numcomps: 3, prec: prec, data: d}
}

// writePPMn writes the image as a binary PPM, 2 bytes/sample when prec > 8.
func (s *synthImage2) writePPMn(path string) error {
	var buf bytes.Buffer
	maxval := (1 << s.prec) - 1
	fmt.Fprintf(&buf, "P6\n%d %d\n%d\n", s.w, s.h, maxval)
	two := s.prec > 8
	for i := uint32(0); i < s.w*s.h; i++ {
		for c := uint32(0); c < 3; c++ {
			v := s.data[c][i]
			if two {
				buf.WriteByte(byte(v >> 8))
				buf.WriteByte(byte(v))
			} else {
				buf.WriteByte(byte(v))
			}
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func (s *synthImage2) toPublic(cs gopenjpeg.ColorSpace) *gopenjpeg.Image {
	comps := make([]gopenjpeg.Component, s.numcomps)
	for c := uint32(0); c < s.numcomps; c++ {
		d := make([]int32, s.w*s.h)
		copy(d, s.data[c])
		comps[c] = gopenjpeg.Component{Dx: 1, Dy: 1, W: s.w, H: s.h, Prec: s.prec, Data: d}
	}
	return gopenjpeg.NewImage(cs, 0, 0, s.w, s.h, comps)
}

// markerSegment extracts the full marker segment (FFxx + Lmarker + payload) for
// the first occurrence of marker, or nil if absent.
func markerSegment(b []byte, marker uint16) []byte {
	hi, lo := byte(marker>>8), byte(marker)
	for i := 2; i+3 < len(b); i++ {
		if b[i] == hi && b[i+1] == lo {
			l := int(b[i+2])<<8 | int(b[i+3])
			end := i + 2 + l
			if end > len(b) {
				end = len(b)
			}
			return b[i:end]
		}
	}
	return nil
}

// runCompressOut runs opj_compress writing to the given output extension.
func runCompressOut(t *testing.T, input, ext string, flags []string) ([]byte, error) {
	dir := filepath.Dir(input)
	out := filepath.Join(dir, "c_out."+ext)
	args := append([]string{"-i", input, "-o", out}, flags...)
	cmd := exec.Command(Bin("opj_compress"), args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opj_compress %v: %v\n%s", args, err, combined)
	}
	return os.ReadFile(out)
}

// TestEncodeContainerGate checks byte-identity of JP2 container output, PLT and
// IMF codestreams versus opj_compress, and main-header identity for the cinema
// profiles (whose coded tile data diverges in the off-limits tier-2 CBR path).
func TestEncodeContainerGate(t *testing.T) {
	Require(t)
	dir := t.TempDir()

	gray := grayGradient(160, 96)
	color := rgb(96, 64)

	// 1. JP2 container, sRGB.
	t.Run("jp2_srgb", func(t *testing.T) {
		in := filepath.Join(dir, "jp2_srgb.ppm")
		if err := color.writePNM(in); err != nil {
			t.Fatal(err)
		}
		want, err := runCompressOut(t, in, "jp2", nil)
		if err != nil {
			t.Fatal(err)
		}
		img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceSRGB, 0, 0, color.w, color.h, publicComps(color))
		var got bytes.Buffer
		if err := gopenjpeg.Encode(img, &got, gopenjpeg.WithEncodeFormat(gopenjpeg.FormatJP2)); err != nil {
			t.Fatal(err)
		}
		requireEqual(t, want, got.Bytes())
	})

	// 2. JP2 container, gray.
	t.Run("jp2_gray", func(t *testing.T) {
		in := filepath.Join(dir, "jp2_gray.pgm")
		if err := gray.writePNM(in); err != nil {
			t.Fatal(err)
		}
		want, err := runCompressOut(t, in, "jp2", nil)
		if err != nil {
			t.Fatal(err)
		}
		img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, gray.w, gray.h, publicComps(gray))
		var got bytes.Buffer
		if err := gopenjpeg.Encode(img, &got, gopenjpeg.WithEncodeFormat(gopenjpeg.FormatJP2)); err != nil {
			t.Fatal(err)
		}
		requireEqual(t, want, got.Bytes())
	})

	// 3. PLT markers.
	t.Run("plt_gray", func(t *testing.T) {
		in := filepath.Join(dir, "plt.pgm")
		if err := gray.writePNM(in); err != nil {
			t.Fatal(err)
		}
		want, err := runCompressOut(t, in, "j2k", []string{"-PLT"})
		if err != nil {
			t.Fatal(err)
		}
		img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, gray.w, gray.h, publicComps(gray))
		var got bytes.Buffer
		if err := gopenjpeg.Encode(img, &got, gopenjpeg.WithPLT()); err != nil {
			t.Fatal(err)
		}
		requireEqual(t, want, got.Bytes())
	})

	// 4. IMF 2K profile.
	t.Run("imf_2k", func(t *testing.T) {
		s := gradient3(256, 144, 8)
		in := filepath.Join(dir, "imf.ppm")
		if err := s.writePPMn(in); err != nil {
			t.Fatal(err)
		}
		want, err := runCompressOut(t, in, "j2k", []string{"-IMF", "2K"})
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := gopenjpeg.Encode(s.toPublic(gopenjpeg.ColorSpaceSRGB), &got, gopenjpeg.WithProfile(0x0400)); err != nil {
			t.Fatal(err)
		}
		requireEqual(t, want, got.Bytes())
	})

	// 5. Cinema 2K — full-stream byte identity at 24 and 48 fps.
	//
	// These streams are irreversible (9-7 + ICT) 12-bit and include ALL coding
	// passes (the cinema rate is <= 1.0 and is forced to lossless, so
	// opj_tcd_rateallocate uses goodthresh == -1). Reaching byte-identity here
	// required matching the stock (-ffast-math) libopenjp2's float arithmetic in
	// two encode-side spots that the 8-bit rate-truncated cells never exercised:
	//   - forward ICT (internal/mct EncodeReal): gcc's -freassoc regroups each
	//     3-term channel sum as a + (b + c);
	//   - the T1 quantizer (internal/tcd cblkEncodeProcessor): gcc hoists the
	//     constant division so it computes lrintf(f * (mul/stepsize)) instead of
	//     lrintf((f/stepsize)*mul).
	// Both flipped ~1 LSB on a fraction of 12-bit coefficients, invisible once
	// low bit-planes are truncated (8-bit cells) but fatal to all-pass streams.
	for _, fps := range []int{24, 48} {
		fps := fps
		t.Run(fmt.Sprintf("cinema2K_%d", fps), func(t *testing.T) {
			s := noisy3(256, 144, 12, int64(7+fps))
			in := filepath.Join(dir, fmt.Sprintf("c2k_%d.ppm", fps))
			if err := s.writePPMn(in); err != nil {
				t.Fatal(err)
			}
			want, err := runCompressOut(t, in, "j2k", []string{"-cinema2K", fmt.Sprint(fps)})
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := gopenjpeg.Encode(s.toPublic(gopenjpeg.ColorSpaceSRGB), &got, gopenjpeg.WithCinema2K(fps)); err != nil {
				t.Fatal(err)
			}
			requireEqual(t, want, got.Bytes())
		})
	}

	// 7. Part-2 custom (array-based) MCT. The oracle opj_compress CLI cannot
	// exercise this: the '-m' matrix-file option is dead code (absent from the
	// getopt optstring), and the reference library encoder writes COD SGcod(C)
	// mct=2, which opj_j2k_read_cod rejects (`mct > 1`) before the MCO marker can
	// enable the transform — so the reference path never round-trips and no
	// conformance stream uses it. This cell therefore verifies our port emits the
	// faithful marker set (CBD/MCT/MCC/MCO plus COD mct=2, matching the C source
	// byte-for-byte) rather than a round-trip.
	t.Run("custom_mct_markers", func(t *testing.T) {
		s := gradient3(64, 48, 8)
		img := s.toPublic(gopenjpeg.ColorSpaceSRGB)
		matrix := []float32{1, 1, 1, 0, 1, 1, 0, 0, 1}
		dc := []int32{0, 0, 0}
		var got bytes.Buffer
		if err := gopenjpeg.Encode(img, &got, gopenjpeg.WithCustomMCT(matrix, dc)); err != nil {
			t.Fatal(err)
		}
		b := got.Bytes()
		for _, m := range []struct {
			name   string
			marker uint16
		}{{"CBD", 0xFF78}, {"MCT", 0xFF74}, {"MCC", 0xFF75}, {"MCO", 0xFF77}} {
			if markerSegment(b, m.marker) == nil {
				t.Errorf("custom MCT: missing %s marker (0x%04X)", m.name, m.marker)
			}
		}
		// SIZ Rsiz must carry PART2|MCT (0x8100); COD SGcod(C) mct must be 2.
		// Segment layout: [FF51][Lsiz:2][Rsiz:2]... so Rsiz is at index 4..5.
		siz := markerSegment(b, 0xFF51)
		if len(siz) < 6 || siz[4] != 0x81 || siz[5] != 0x00 {
			t.Errorf("custom MCT: Rsiz not PART2|MCT: % x", siz[:min(6, len(siz))])
		}
		cod := markerSegment(b, 0xFF52)
		if len(cod) < 9 || cod[8] != 2 {
			t.Errorf("custom MCT: COD mct field != 2")
		}
		t.Log("KNOWN upstream limitation: OpenJPEG custom (array) MCT is not " +
			"round-trippable (read_cod rejects mct>1; CLI -m is dead code). Our " +
			"encoder faithfully emits CBD/MCT/MCC/MCO + COD mct=2 matching the C source.")
	})

	// 6. Cinema 4K — full-stream byte identity (also exercises the 4K POC marker
	// and the 2-pocno tier-2 THRESH_CALC path). Byte-identical for the same
	// reasons documented on the cinema2K cell above.
	t.Run("cinema4K", func(t *testing.T) {
		s := noisy3(256, 144, 12, 11)
		in := filepath.Join(dir, "c4k.ppm")
		if err := s.writePPMn(in); err != nil {
			t.Fatal(err)
		}
		want, err := runCompressOut(t, in, "j2k", []string{"-cinema4K"})
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := gopenjpeg.Encode(s.toPublic(gopenjpeg.ColorSpaceSRGB), &got, gopenjpeg.WithCinema4K()); err != nil {
			t.Fatal(err)
		}
		requireEqual(t, want, got.Bytes())
	})
}

// publicComps builds public components from a synthImage.
func publicComps(s *synthImage) []gopenjpeg.Component {
	comps := make([]gopenjpeg.Component, s.numcomps)
	for c := uint32(0); c < s.numcomps; c++ {
		d := make([]int32, s.w*s.h)
		copy(d, s.data[c])
		comps[c] = gopenjpeg.Component{Dx: 1, Dy: 1, W: s.w, H: s.h, Prec: s.prec, Data: d}
	}
	return comps
}

func requireEqual(t *testing.T, want, got []byte) {
	t.Helper()
	if !bytes.Equal(want, got) {
		t.Fatalf("byte mismatch: want=%d got=%d firstdiff=%d", len(want), len(got), firstDiff(want, got))
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
