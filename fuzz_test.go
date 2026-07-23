package gopenjpeg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// maxFuzzDecodedBytes bounds the estimated decoded sample memory a fuzz input
// is allowed to imply before we attempt a full decode, so the fuzzer does not
// OOM on legitimately-huge declared geometry (mirrors the size cap in the
// internal j2k FuzzDecode target). 64 MiB of int32 samples.
const maxFuzzDecodedBytes = 64 << 20

// oracleSeedFiles is a curated ~40-file subset of the OpenJPEG conformance /
// non-regression corpus, spanning valid Part-1 codestreams, JP2/JPH containers,
// HTJ2K, and a broad set of known crashers (SIGSEGV/SIGFPE/asan/GDAL-fuzzer
// regressions). Paths are relative to oracle/data/input; missing files (corpus
// absent) are skipped at seed time. Small representative copies live under
// testdata/fuzzseed so seeds exist even without the oracle.
var oracleSeedFiles = []string{
	// Valid Part-1 codestreams.
	"conformance/p0_01.j2k", "conformance/p0_02.j2k", "conformance/p0_03.j2k",
	"conformance/p0_09.j2k", "conformance/p0_11.j2k", "conformance/p0_12.j2k",
	"conformance/p0_13.j2k", "conformance/p0_14.j2k", "conformance/p1_01.j2k",
	"conformance/p1_06.j2k", "conformance/p1_07.j2k",
	// Valid conformance JP2 containers.
	"conformance/file1.jp2", "conformance/file2.jp2",
	// HTJ2K.
	"nonregression/htj2k/byte.jph", "nonregression/htj2k/byte_causal.jhc",
	"nonregression/htj2k/Bretagne1_ht.j2k", "nonregression/htj2k/Bretagne1_ht_lossy.j2k",
	// Valid non-regression JP2 with alpha / chroma subsampling.
	"nonregression/basn4a08.jp2", "nonregression/basn6a08.jp2",
	"nonregression/issue411-ycc420.jp2", "nonregression/issue411-ycc422.jp2",
	"nonregression/issue411-ycc444.jp2",
	// Known crashers / malformed (the security surface).
	"nonregression/issue726.j2k", "nonregression/issue979.j2k",
	"nonregression/issue1438.j2k", "nonregression/issue1472-bigloop.j2k",
	"nonregression/issue226.j2k",
	"nonregression/huge-tile-size.jp2",
	"nonregression/issue427-null-image-size.jp2",
	"nonregression/issue427-illegal-tile-offset.jp2",
	"nonregression/issue823.jp2",
	"nonregression/gdal_fuzzer_check_number_of_tiles.jp2",
	"nonregression/gdal_fuzzer_check_comp_dx_dy.jp2",
	"nonregression/gdal_fuzzer_unchecked_numresolutions.jp2",
	"nonregression/gdal_fuzzer_assert_in_opj_j2k_read_SQcd_SQcc.patch.jp2",
	"nonregression/1851.pdf.SIGSEGV.ce9.948.jp2",
	"nonregression/2236.pdf.SIGSEGV.398.1376.jp2",
	"nonregression/26ccf3651020967f7778238ef5af08af.SIGFPE.d25.527.jp2",
	"nonregression/2977.pdf.asan.67.2198.jp2",
	"nonregression/4149.pdf.SIGSEGV.cf7.3501.jp2",
	"nonregression/451.pdf.SIGSEGV.5b5.3723.jp2",
	"nonregression/4241ac039aba57e6a9c948d519d94216_asan_heap-oob_14650f2_7469_602.jp2",
	"nonregression/broken1.jp2",
}

// addRootFuzzSeeds seeds a fuzz target from the checked-in small corpus (always
// present) and, when the oracle is available, the curated corpus subset.
func addRootFuzzSeeds(f *testing.F) {
	f.Helper()
	// Checked-in small seeds (always present, keep seeds valid without oracle).
	if entries, err := os.ReadDir("testdata/fuzzseed"); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) == ".md" {
				continue
			}
			if data, err := os.ReadFile(filepath.Join("testdata/fuzzseed", e.Name())); err == nil {
				f.Add(data)
			}
		}
	}
	// Curated oracle subset (skipped when the corpus is absent).
	for _, rel := range oracleSeedFiles {
		p := filepath.Join("oracle", "data", "input", rel)
		if data, err := os.ReadFile(p); err == nil && len(data) < 5<<20 {
			f.Add(data)
		}
	}
	// A few structural corner cases.
	f.Add([]byte{})
	f.Add([]byte{0xff, 0x4f})                                                             // SOC only
	f.Add([]byte{0x00, 0x00, 0x00, 0x0c, 0x6a, 0x50, 0x20, 0x20, 0x0d, 0x0a, 0x87, 0x0a}) // JP2 sig box only
}

// decodeOptionMatrix derives a decode-option set from the input's first bytes,
// exercising the format / reduce / layer / decode-area / tile / component /
// strict controls without depending on any particular byte being present.
func decodeOptionMatrix(data []byte) []Option {
	var c [6]byte
	for i := range c {
		if i < len(data) {
			c[i] = data[i]
		}
	}
	opts := make([]Option, 0, 6)
	switch c[0] % 3 {
	case 1:
		opts = append(opts, WithFormat(FormatJ2K))
	case 2:
		opts = append(opts, WithFormat(FormatJP2))
	}
	opts = append(opts, WithReduce(uint32(c[1]%5)))
	opts = append(opts, WithLayers(uint32(c[2]%4)))
	opts = append(opts, WithStrictMode(c[3]&1 == 1))
	switch {
	case c[4]&3 == 1:
		// Small decode area anchored at the origin.
		s := int32(c[5]%64) + 1
		opts = append(opts, WithDecodeArea(0, 0, s, s))
	case c[4]&3 == 2:
		opts = append(opts, WithTile(int(c[5]%8)))
	case c[4]&3 == 3:
		opts = append(opts, WithComponents(uint32(c[5]%5)))
	}
	return opts
}

// infoWithinCap reports whether the header geometry implies a decoded size
// within maxFuzzDecodedBytes (worst case: no reduce applied).
func infoWithinCap(info *Info) bool {
	if info == nil {
		return true
	}
	if info.X1 <= info.X0 || info.Y1 <= info.Y0 {
		return true // empty/degenerate; let the decoder reject it cheaply
	}
	var total uint64
	for _, comp := range info.Components {
		dx := uint64(comp.Dx)
		dy := uint64(comp.Dy)
		if dx == 0 {
			dx = 1
		}
		if dy == 0 {
			dy = 1
		}
		w := (uint64(info.X1) - uint64(info.X0) + dx - 1) / dx
		h := (uint64(info.Y1) - uint64(info.Y0) + dy - 1) / dy
		total += w * h * 4
		if total > maxFuzzDecodedBytes {
			return false
		}
	}
	return true
}

// FuzzDecode drives the public Decode over arbitrary bytes with an
// option matrix derived from the input, in both autodetect and forced-format
// modes. The library must never panic, hang, read out of bounds, or over-
// allocate: any panic here is a bug (the fuzzer reports it; the recover turns
// it into an attributable failure).
func FuzzDecode(f *testing.F) {
	addRootFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()

		// Cheap header geometry check to bound decoded memory.
		info, _ := ReadInfo(bytes.NewReader(data))
		if !infoWithinCap(info) {
			return
		}

		opts := decodeOptionMatrix(data)
		if img, err := Decode(bytes.NewReader(data), opts...); err == nil && img != nil {
			// Touch the result so a mis-sized component slice would surface.
			for i := 0; i < img.NumComponents(); i++ {
				_ = img.Component(i)
			}
		}
	})
}

// FuzzDecodeConcurrent runs the same decode surface under WithConcurrency(4):
// the worker scheduling (per-code-block tier-1, DWT row/column passes) must
// not introduce panics or data races on any input.
func FuzzDecodeConcurrent(f *testing.F) {
	addRootFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()

		info, _ := ReadInfo(bytes.NewReader(data))
		if !infoWithinCap(info) {
			return
		}

		opts := append(decodeOptionMatrix(data), WithConcurrency(4))
		_, _ = Decode(bytes.NewReader(data), opts...)
	})
}

// FuzzReadInfo drives ReadInfo (header-only) over arbitrary bytes in both
// autodetect and forced-format modes; it must never panic or over-allocate.
func FuzzReadInfo(f *testing.F) {
	addRootFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()
		_, _ = ReadInfo(bytes.NewReader(data))
		_, _ = ReadInfo(bytes.NewReader(data), WithFormat(FormatJ2K))
		_, _ = ReadInfo(bytes.NewReader(data), WithFormat(FormatJP2))
	})
}

// buildFuzzImage constructs a small, always-valid image from the fuzz input:
// the leading bytes pick geometry (w,h in [1,24], 1..4 components, 8-bit
// unsigned), the remainder fills sample data. Returns nil when there is not
// enough input to form even a 1x1 image.
func buildFuzzImage(data []byte) (*Image, int) {
	if len(data) < 3 {
		return nil, 0
	}
	w := int(data[0]%24) + 1
	h := int(data[1]%24) + 1
	nc := int(data[2]%4) + 1
	payload := data[3:]

	cs := ColorSpaceGray
	if nc >= 3 {
		cs = ColorSpaceSRGB
	}
	comps := make([]Component, nc)
	pi := 0
	for i := 0; i < nc; i++ {
		samples := make([]int32, w*h)
		for k := range samples {
			var v int32
			if pi < len(payload) {
				v = int32(payload[pi])
				pi++
			}
			samples[k] = v
		}
		comps[i] = Component{
			Dx: 1, Dy: 1, W: uint32(w), H: uint32(h),
			X0: 0, Y0: 0, Prec: 8, Sgnd: false, Data: samples,
		}
	}
	return NewImage(cs, 0, 0, uint32(w), uint32(h), comps), nc
}

// FuzzEncodeDecodeRoundTrip encodes a fuzz-shaped small image losslessly, then
// decodes the produced codestream and verifies the samples survive the round
// trip exactly. It exercises the encode path (which must never panic) and the
// lossless-fidelity contract. Encode options are derived from a control byte.
func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	// Seeds: a handful of tiny geometries.
	f.Add([]byte{4, 4, 1, 10, 20, 30, 40, 50})
	f.Add([]byte{8, 8, 3, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte{1, 1, 4, 0, 0, 0, 0})
	f.Add([]byte{16, 3, 2, 255, 128, 64, 0, 1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte input: %v", len(data), r)
			}
		}()

		img, nc := buildFuzzImage(data)
		if img == nil {
			return
		}
		// Snapshot the expected samples before Encode: the encoder is allowed
		// to consume/transform the source image's component buffers in place
		// (as the C reference does), so we must not read them back afterwards.
		wantW := make([]uint32, nc)
		wantH := make([]uint32, nc)
		want := make([][]int32, nc)
		for i := 0; i < nc; i++ {
			c := img.Component(i)
			wantW[i], wantH[i] = c.W, c.H
			want[i] = append([]int32(nil), c.Data...)
		}

		// Encode options from a control byte (kept lossless: no rate/quality
		// allocation, reversible 5/3, reversible MCT when applicable).
		var ctrl byte
		if len(data) > 0 {
			ctrl = data[len(data)-1]
		}
		encOpts := []EncodeOption{WithLossless()}
		if res := int(ctrl%5) + 1; res >= 1 {
			encOpts = append(encOpts, WithResolutions(res))
		}
		switch (ctrl >> 3) % 5 {
		case 0:
			encOpts = append(encOpts, WithProgressionOrder(ProgLRCP))
		case 1:
			encOpts = append(encOpts, WithProgressionOrder(ProgRLCP))
		case 2:
			encOpts = append(encOpts, WithProgressionOrder(ProgRPCL))
		case 3:
			encOpts = append(encOpts, WithProgressionOrder(ProgPCRL))
		case 4:
			encOpts = append(encOpts, WithProgressionOrder(ProgCPRL))
		}
		format := FormatJ2K
		if ctrl&0x40 != 0 {
			format = FormatJP2
			encOpts = append(encOpts, WithEncodeFormat(FormatJP2))
		}

		var buf bytes.Buffer
		if err := Encode(img, &buf, encOpts...); err != nil {
			// Rejecting an input (e.g. impossible resolution count for a 1x1
			// image) is fine; only a panic or a lossy round trip is a bug.
			return
		}

		var decOpts []Option
		if format == FormatJP2 {
			decOpts = append(decOpts, WithFormat(FormatJP2))
		} else {
			decOpts = append(decOpts, WithFormat(FormatJ2K))
		}
		out, err := Decode(bytes.NewReader(buf.Bytes()), decOpts...)
		if err != nil {
			t.Fatalf("lossless encode then decode failed: %v (w=%d h=%d nc=%d fmt=%d)",
				err, wantW[0], wantH[0], nc, format)
		}
		if out.NumComponents() != nc {
			t.Fatalf("component count changed: got %d want %d", out.NumComponents(), nc)
		}
		for i := 0; i < nc; i++ {
			got := out.Component(i)
			if got.W != wantW[i] || got.H != wantH[i] {
				t.Fatalf("comp %d geometry changed: got %dx%d want %dx%d",
					i, got.W, got.H, wantW[i], wantH[i])
			}
			if len(got.Data) != len(want[i]) {
				t.Fatalf("comp %d sample count changed: got %d want %d",
					i, len(got.Data), len(want[i]))
			}
			for k := range want[i] {
				if got.Data[k] != want[i][k] {
					t.Fatalf("comp %d sample %d not lossless: got %d want %d",
						i, k, got.Data[k], want[i][k])
				}
			}
		}
	})
}

// FuzzApplyICCProfile drives ApplyICCProfile (icc.go), and through it the
// pure-Go Little CMS profile parser and transform builder, over arbitrary
// profile bytes attached to a small fixed decoded image. golittlecms is
// documented to never panic on malformed profiles; this target enforces that
// end to end through our wiring, and exercises both the RGB and the grey->RGB
// component-expansion branches (selected by a control byte) so a mutated
// profile that opens but produces an odd transform cannot slip a panic through.
// It must never panic regardless of the profile bytes; a successful apply is
// not required.
func FuzzApplyICCProfile(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 128))
	// A byte pattern that looks vaguely like an ICC header (size + 'acsp').
	hdr := make([]byte, 132)
	copy(hdr[36:], []byte("acsp"))
	f.Add(hdr)

	f.Fuzz(func(t *testing.T, profile []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %d-byte profile: %v", len(profile), r)
			}
		}()

		// Build a tiny fixed image whose component count depends on the first
		// profile byte, so both the RGB (>2 comps) and grey (<=2 comps) branches
		// of ApplyICCProfile are reached. 2x2 samples per component.
		nc := 3
		if len(profile) > 0 {
			nc = int(profile[0]%4) + 1 // 1..4
		}
		const w, h = 2, 2
		comps := make([]Component, nc)
		for i := range comps {
			data := make([]int32, w*h)
			for k := range data {
				data[k] = int32((i*7 + k*3) & 0xff)
			}
			comps[i] = Component{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: data}
		}
		img := NewImage(ColorSpaceSRGB, 0, 0, w, h, comps)
		img.SetICCProfile(profile)
		// Best-effort: an inapplicable profile returns ErrICCApply, an applicable
		// one returns nil. Either way it must not panic and must not corrupt the
		// component slices' lengths.
		_ = img.ApplyICCProfile()
		for i := 0; i < img.NumComponents(); i++ {
			c := img.Component(i)
			if len(c.Data) != int(c.W)*int(c.H) {
				t.Fatalf("comp %d data length %d != %dx%d after apply",
					i, len(c.Data), c.W, c.H)
			}
		}
	})
}
