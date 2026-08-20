package oracletest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/gopenjpeg"
)

// This gate verifies the documented error bound between gopenjpeg and an
// OpenJPEG build of ANY architecture, using only the binaries — no corpus, no
// frozen vectors. It generates codestreams with both encoders, decodes each
// with both decoders, and requires per-sample agreement:
//
//   - reversible 5/3 (and RCT): exact — the path is integer arithmetic;
//   - irreversible 9/7 (and ICT): within one unit — the C library's float
//     rounding varies with the architecture it was built for (issue #14), and
//     one differently-rounded float intermediate can move the single final
//     integer rounding by at most a unit, matching ISO/IEC 15444-4's
//     maximum-error conformance model.
//
// On an amd64 oracle the observed difference is 0 everywhere; on an arm64
// oracle the 9/7 rows are where the tolerance becomes load-bearing. If any
// platform ever exceeds the bound, this gate fails and the README claim is
// falsified — that is its job. Unlike the byte-identity gates it needs no
// -ffast-math amd64 build, so CI runs it against a plain from-source OpenJPEG
// on amd64 and both arm64 runners (job oracle-bounds).

// boundsImage builds a deterministic single- or three-component image whose
// samples span the full precision range with noise, the same generator the
// short-block and issue-11 tests use.
func boundsImage(w, h, ncomp, prec int) *gopenjpeg.Image {
	comps := make([]gopenjpeg.Component, ncomp)
	var s uint64 = 0x5EED
	for c := 0; c < ncomp; c++ {
		data := make([]int32, w*h)
		for i := range data {
			s = s*6364136223846793005 + 1442695040888963407
			data[i] = int32((i*7+c*13)%(1<<prec)+int(s>>60)) % (1 << prec)
		}
		comps[c] = gopenjpeg.Component{Dx: 1, Dy: 1, W: uint32(w), H: uint32(h),
			Prec: uint32(prec), Data: data}
	}
	cs := gopenjpeg.ColorSpaceGray
	if ncomp == 3 {
		cs = gopenjpeg.ColorSpaceSRGB
	}
	return gopenjpeg.NewImage(cs, 0, 0, uint32(w), uint32(h), comps)
}

// goDecode decodes a codestream with the public API and returns the component
// sample planes.
func goDecode(t *testing.T, stream []byte, format gopenjpeg.Format) [][]int32 {
	t.Helper()
	img, err := gopenjpeg.Decode(bytes.NewReader(stream),
		gopenjpeg.WithFormat(format), gopenjpeg.WithStrictMode(true))
	if err != nil {
		t.Fatalf("gopenjpeg.Decode: %v", err)
	}
	out := make([][]int32, img.NumComponents())
	for i := range out {
		out[i] = img.Component(i).Data
	}
	return out
}

// assertWithinBound compares Go and oracle component planes per sample and
// fails past tol; it reports the observed maximum so CI logs show how much of
// the bound each platform actually uses.
func assertWithinBound(t *testing.T, goComps [][]int32, cComps []*pgx, tol int32) {
	t.Helper()
	if len(goComps) != len(cComps) {
		t.Fatalf("component count: go=%d oracle=%d", len(goComps), len(cComps))
	}
	var maxDiff int32
	var diffs int
	for ci := range goComps {
		g, c := goComps[ci], cComps[ci]
		if len(g) != len(c.data) {
			t.Fatalf("comp %d: sample count go=%d oracle=%d", ci, len(g), len(c.data))
		}
		for i := range g {
			d := g[i] - c.data[i]
			if d < 0 {
				d = -d
			}
			if d > 0 {
				diffs++
				if d > maxDiff {
					maxDiff = d
				}
				if d > tol {
					t.Fatalf("comp %d sample %d: go=%d oracle=%d (|diff|=%d exceeds bound %d)",
						ci, i, g[i], c.data[i], d, tol)
				}
			}
		}
	}
	t.Logf("max |diff| = %d (bound %d), %d differing samples", maxDiff, tol, diffs)
}

// TestOracleDecodeBounds is the bounds gate over gopenjpeg-encoded streams.
func TestOracleDecodeBounds(t *testing.T) {
	RequireBins(t)

	cases := []struct {
		name  string
		w, h  int
		ncomp int
		prec  int
		fmt   gopenjpeg.Format
		tol   int32 // 0 = reversible/integer, 1 = irreversible/float
		opts  []gopenjpeg.EncodeOption
	}{
		{"rev53_gray8", 129, 67, 1, 8, gopenjpeg.FormatJ2K, 0,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithLossless(), gopenjpeg.WithResolutions(3)}},
		{"rev53_gray16", 257, 33, 1, 16, gopenjpeg.FormatJ2K, 0,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithLossless(), gopenjpeg.WithResolutions(2)}},
		{"rev53_grib_n_x_1", 4096, 1, 1, 16, gopenjpeg.FormatJ2K, 0,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithLossless(), gopenjpeg.WithResolutions(1)}},
		{"rev53_rate_limited", 128, 128, 1, 8, gopenjpeg.FormatJ2K, 0,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithLossless(), gopenjpeg.WithRates(4)}},
		{"rev53_rct_rgb", 96, 64, 3, 8, gopenjpeg.FormatJP2, 0,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithLossless()}},
		{"irr97_gray16_r2", 128, 128, 1, 16, gopenjpeg.FormatJ2K, 1,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithRates(2)}},
		{"irr97_gray16_r40", 128, 128, 1, 16, gopenjpeg.FormatJ2K, 1,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithRates(40)}},
		{"irr97_layers", 128, 128, 1, 16, gopenjpeg.FormatJ2K, 1,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithRates(16, 8, 2)}},
		{"irr97_quality", 129, 67, 1, 8, gopenjpeg.FormatJ2K, 1,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithQualityLayers(35, 40)}},
		{"irr97_ict_rgb", 96, 64, 3, 8, gopenjpeg.FormatJP2, 1,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithRates(10)}},
		{"irr97_tiled", 200, 100, 1, 8, gopenjpeg.FormatJ2K, 1,
			[]gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithRates(8), gopenjpeg.WithTileSize(64, 64)}},
	}

	dir := t.TempDir()
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			img := boundsImage(c.w, c.h, c.ncomp, c.prec)
			var buf bytes.Buffer
			opts := append([]gopenjpeg.EncodeOption{gopenjpeg.WithEncodeFormat(c.fmt)}, c.opts...)
			if err := gopenjpeg.Encode(img, &buf, opts...); err != nil {
				t.Fatalf("encode: %v", err)
			}
			ext := ".j2k"
			if c.fmt == gopenjpeg.FormatJP2 {
				ext = ".jp2"
			}
			path := filepath.Join(dir, c.name+ext)
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}

			goComps := goDecode(t, buf.Bytes(), c.fmt)
			cComps, err := oracleDecodePGX(t, path)
			if err != nil {
				t.Fatalf("oracle decode: %v", err)
			}
			assertWithinBound(t, goComps, cComps, c.tol)
		})
	}
}

// TestOracleDecodeBoundsCEncoded is the same bound over opj_compress-encoded
// streams, so the C encoder's own (possibly non-amd64) float rounding is in
// the loop as well.
func TestOracleDecodeBoundsCEncoded(t *testing.T) {
	RequireBins(t)

	dir := t.TempDir()
	pgm := filepath.Join(dir, "in.pgm")
	if err := writeGrayPGM(pgm, 150, 90); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		tol   int32
		flags []string
	}{
		{"rev53_lossless", 0, nil},
		{"rev53_rate_limited", 0, []string{"-r", "4"}},
		{"irr97_r8", 1, []string{"-I", "-r", "8"}},
		{"irr97_quality", 1, []string{"-I", "-q", "35"}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stream := filepath.Join(dir, c.name+".j2k")
			args := append([]string{"-i", pgm, "-o", stream}, c.flags...)
			if out, err := execOracle("opj_compress", args...); err != nil {
				t.Fatalf("opj_compress %v: %v\n%s", args, err, out)
			}
			raw, err := os.ReadFile(stream)
			if err != nil {
				t.Fatal(err)
			}

			goComps := goDecode(t, raw, gopenjpeg.FormatJ2K)
			cComps, err := oracleDecodePGX(t, stream)
			if err != nil {
				t.Fatalf("oracle decode: %v", err)
			}
			assertWithinBound(t, goComps, cComps, c.tol)
		})
	}
}
