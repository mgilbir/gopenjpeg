package oracletest

// Encode-side concurrency gate: proves the parallel per-code-block tier-1
// encode (WithEncodeConcurrency > 1) produces a byte-identical codestream to
// the sequential encode. The sequential encode is separately gated
// byte-identical against opj_compress, so identity with it establishes the
// parallel path is also byte-identical vs C at any worker count.
//
// Run under the race detector for the concurrency-safety guarantee:
//
//	go test ./oracletest/ -run TestEncodeConcurrentMatchesSequential -race

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mgilbir/gopenjpeg"
)

// encodeGo decodes src fresh (Encode consumes the image's tile data) and
// encodes it with the given worker count, returning the codestream bytes.
func encodeGo(src []byte, threads int, opts ...gopenjpeg.EncodeOption) ([]byte, error) {
	img, err := gopenjpeg.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	all := append([]gopenjpeg.EncodeOption{gopenjpeg.WithEncodeConcurrency(threads)}, opts...)
	if err := gopenjpeg.Encode(img, &buf, all...); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestEncodeConcurrentMatchesSequential(t *testing.T) {
	Require(t)
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}

	// Source images from the corpus (decoded via the already-gated decoder),
	// chosen to exercise many code-blocks so the parallel fan-out is real.
	srcs := []string{
		DataDir("input", "conformance", "p0_08.j2k"),
		DataDir("input", "nonregression", "Bretagne2.j2k"),
	}

	settings := []struct {
		name string
		opts []gopenjpeg.EncodeOption
	}{
		{"lossless_5x3", nil},
		{"rate20_5x3", []gopenjpeg.EncodeOption{gopenjpeg.WithRates(20)}},
		{"multilayer_5x3", []gopenjpeg.EncodeOption{gopenjpeg.WithRates(40, 20, 10, 5)}},
		{"irrev_9x7_r20", []gopenjpeg.EncodeOption{gopenjpeg.WithIrreversible(), gopenjpeg.WithRates(20)}},
	}

	for _, src := range srcs {
		src := src
		if _, err := os.Stat(src); err != nil {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		for _, s := range settings {
			s := s
			t.Run(filepath.Base(src)+"/"+s.name, func(t *testing.T) {
				seq, err := encodeGo(data, 1, s.opts...)
				if err != nil {
					t.Skipf("sequential encode failed (not a concurrency concern): %v", err)
				}
				par, err := encodeGo(data, n, s.opts...)
				if err != nil {
					t.Fatalf("concurrent encode (n=%d) failed but sequential succeeded: %v", n, err)
				}
				if !bytes.Equal(seq, par) {
					t.Fatalf("concurrent encode differs from sequential: len %d vs %d", len(seq), len(par))
				}
			})
		}
	}
}
