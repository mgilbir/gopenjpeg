package oracletest

// Concurrency correctness gate: proves the parallel decode (WithConcurrency /
// SetThreads > 1) produces byte-identical output to the sequential decode.
// Since the sequential path is separately gated bit-exact against the C
// reference (TestDecodeConformanceP0P1 / TestDecodeNonregression), identity
// with it establishes the concurrent path is also bit-exact vs C.
//
// Run under the race detector for the concurrency-safety guarantee:
//
//	go test ./oracletest/ -run TestDecodeConcurrentMatchesSequential -race

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"

	"os"
)

// decodeGoThreads decodes a raw codestream via the internal path with the given
// worker count (0/1 == sequential).
func decodeGoThreads(path string, reduce uint32, threads int) (*image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := cio.NewMemoryInputStream(data)
	var mgr *event.Manager
	d := j2k.CreateDecompress()
	d.SetupDecoder(reduce, 0)
	d.SetStrictMode(false)
	d.SetThreads(threads)
	img, err := d.ReadHeader(s, mgr)
	if err != nil {
		return nil, fmt.Errorf("ReadHeader: %w", err)
	}
	if err := d.SetDecodeArea(img, 0, 0, 0, 0); err != nil {
		return nil, fmt.Errorf("SetDecodeArea: %w", err)
	}
	if err := d.Decode(s, img, mgr); err != nil {
		return nil, fmt.Errorf("Decode: %w", err)
	}
	return img, nil
}

func imagesIdentical(a, b *image.Image) error {
	if a.Numcomps != b.Numcomps {
		return fmt.Errorf("numcomps %d != %d", a.Numcomps, b.Numcomps)
	}
	for c := uint32(0); c < a.Numcomps; c++ {
		da, db := a.Comps[c].Data, b.Comps[c].Data
		if len(da) != len(db) {
			return fmt.Errorf("comp %d len %d != %d", c, len(da), len(db))
		}
		for i := range da {
			if da[i] != db[i] {
				return fmt.Errorf("comp %d sample %d: %d != %d", c, i, da[i], db[i])
			}
		}
	}
	return nil
}

// concurrencyTestFiles returns a broad set of raw codestreams: the p0/p1
// conformance corpus, the curated nonregression pass list, and the HT files.
func concurrencyTestFiles(t *testing.T) []string {
	var files []string
	g, _ := filepath.Glob(DataDir("input", "conformance", "p[01]_*.j2k"))
	files = append(files, g...)
	for _, n := range nonregPass {
		files = append(files, DataDir("input", "nonregression", n))
	}
	ht, _ := filepath.Glob(DataDir("input", "nonregression", "htj2k", "*.j2k"))
	files = append(files, ht...)
	// Keep only existing files.
	var out []string
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func TestDecodeConcurrentMatchesSequential(t *testing.T) {
	Require(t)
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	files := concurrencyTestFiles(t)
	if len(files) == 0 {
		t.Skip("no corpus files")
	}
	for _, in := range files {
		in := in
		t.Run(filepath.Base(in), func(t *testing.T) {
			seq, err := decodeGoThreads(in, 0, 1)
			if err != nil {
				t.Skipf("sequential decode failed (not a concurrency concern): %v", err)
			}
			par, err := decodeGoThreads(in, 0, n)
			if err != nil {
				t.Fatalf("concurrent decode (n=%d) failed but sequential succeeded: %v", n, err)
			}
			if err := imagesIdentical(seq, par); err != nil {
				t.Fatalf("concurrent decode differs from sequential: %v", err)
			}
		})
	}
}
