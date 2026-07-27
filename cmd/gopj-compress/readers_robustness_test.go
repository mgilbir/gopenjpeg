package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestReadersSurviveGarbage is the standing guard for C7: it throws pseudo-random bytes (and mutated valid
// headers) at every reader; none may panic.
func TestReadersSurviveGarbage(t *testing.T) {
	dir := t.TempDir()
	rp := readerParams{subX: 1, subY: 1, mctMode: -1}
	seeds := [][]byte{
		[]byte("P5\n4 4\n255\n0123456789abcdef"),
		[]byte("P2\n2 2\n255\n1 2 3 4\n"),
		[]byte("P1\n4 4\n0101010101010101"),
		[]byte("P4\n8 2\n\xaa\x55"),
		[]byte("P7\nWIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\nabcd"),
		[]byte("PG ML + 8 4 4\nabcdefghijklmnop"),
		[]byte("PG LM -16 2 2\n01234567"),
		make([]byte, 64),
	}
	r := rand.New(rand.NewSource(1))
	exts := []string{"pgm", "ppm", "pnm", "pbm", "pam", "pgx", "raw", "rawl", "yuv"}
	geoms := []string{"", "4,4,1,8,u", "4,4,3,8,u@1x1:2x2:2x2", "1,1,1,16,s", "0,0,0,0,u", "-2,4,1,8,u"}
	for i := 0; i < 4000; i++ {
		var b []byte
		if i%3 == 0 {
			b = make([]byte, r.Intn(80))
			r.Read(b)
		} else {
			s := seeds[r.Intn(len(seeds))]
			b = append([]byte(nil), s...)
			for m := 0; m < 1+r.Intn(4) && len(b) > 0; m++ {
				b[r.Intn(len(b))] = byte(r.Intn(256))
			}
			if r.Intn(2) == 0 && len(b) > 2 {
				b = b[:r.Intn(len(b))]
			}
		}
		ext := exts[r.Intn(len(exts))]
		p := filepath.Join(dir, "in."+ext)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		var g *rawGeometry
		if gs := geoms[r.Intn(len(geoms))]; gs != "" {
			g, _ = parseRawGeometry(gs)
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panic on %q ext=%s data=%q: %v", ext, ext, b, rec)
				}
			}()
			_, _ = readInput(p, g, rp)
		}()
	}
}
