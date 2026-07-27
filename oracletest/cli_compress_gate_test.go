package oracletest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This gate builds cmd/gopj-compress and runs it side by side with the reference
// opj_compress on a spread of inputs, output formats and flags, requiring the
// produced codestream/container files to be byte-for-byte identical. It
// validates the CLI input readers (PNM), the extension-based output format
// selection, and the encoder flag handling end to end.

// buildCompress compiles cmd/gopj-compress into a temp dir and returns the path.
func buildCompress(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gopj-compress")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gopj-compress")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gopj-compress: %v\n%s", err, out)
	}
	return bin
}

// writeGrayPGM writes a w x h 8-bit P5 gradient.
func writeGrayPGM(path string, w, h int) error {
	return writeGrayPGMEOL(path, w, h, "\n")
}

// writeGrayPGMEOL writes the same gradient with an explicit header line
// terminator, so the gate can exercise CRLF headers (C16).
func writeGrayPGMEOL(path string, w, h int, eol string) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "P5%s%d %d%s255%s", eol, w, h, eol, eol)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf.WriteByte(byte((x*7 + y*13 + ((x ^ y) * 3)) & 0xff))
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// writeColorPPM writes a w x h 8-bit P6 image.
func writeColorPPM(path string, w, h int) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "P6\n%d %d\n255\n", w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf.WriteByte(byte((x * 5) & 0xff))
			buf.WriteByte(byte((y * 3) & 0xff))
			buf.WriteByte(byte((x*2 + y*2) & 0xff))
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// TestCLICompressByteParity runs gopj-compress and opj_compress across formats
// and flags and requires byte-identical output files.
func TestCLICompressByteParity(t *testing.T) {
	Require(t)
	bin := buildCompress(t)

	dir := t.TempDir()
	gray := filepath.Join(dir, "g.pgm")
	color := filepath.Join(dir, "c.ppm")
	grayCRLF := filepath.Join(dir, "gcrlf.pgm")
	if err := writeGrayPGM(gray, 160, 96); err != nil {
		t.Fatal(err)
	}
	if err := writeColorPPM(color, 96, 64); err != nil {
		t.Fatal(err)
	}
	if err := writeGrayPGMEOL(grayCRLF, 160, 96, "\r\n"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		in    string
		ext   string
		flags []string
	}{
		{"gray_lossless_j2k", gray, "j2k", nil},
		{"rgb_lossless_jp2", color, "jp2", nil},
		{"gray_irrev_rates", gray, "j2k", []string{"-I", "-r", "20,10,5"}},
		{"gray_numres_sop_eph", gray, "j2k", []string{"-n", "3", "-SOP", "-EPH"}},
		{"gray_plt_jp2", gray, "jp2", []string{"-PLT"}},
		{"rgb_prog_mct0", color, "j2k", []string{"-p", "RLCP", "-mct", "0"}},
		{"gray_mode_cblk", gray, "j2k", []string{"-M", "8", "-b", "32,32"}},
		// C14: -s must actually sub-sample, in the SIZ and in the samples.
		{"gray_subsampled", gray, "j2k", []string{"-s", "2,2"}},
		{"rgb_subsampled_jp2", color, "jp2", []string{"-s", "2,2"}},
		{"gray_subsampled_asym", gray, "j2k", []string{"-s", "2,1"}},
		// C16: a CRLF-terminated header must read exactly like an LF one.
		{"gray_crlf_header", grayCRLF, "j2k", nil},
		{"gray_crlf_header_rates", grayCRLF, "j2k", []string{"-I", "-r", "20,10,5"}},
		// C31: every -POC record must reach the encoder, not only the first.
		// opj_compress indexes POC tiles from 1, so T1 is the single tile here.
		{"rgb_poc_two_records", color, "j2k",
			[]string{"-POC", "T1=0,0,1,5,3,CPRL/T1=5,0,1,6,3,CPRL"}},
		{"rgb_poc_two_records_layers", color, "j2k",
			[]string{"-r", "20,10", "-POC", "T1=0,0,1,5,3,CPRL/T1=5,0,2,6,3,LRCP"}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			gOut := filepath.Join(t.TempDir(), "g."+c.ext)
			cOut := filepath.Join(t.TempDir(), "c."+c.ext)

			gArgs := append([]string{"-i", c.in, "-o", gOut}, c.flags...)
			if out, err := exec.Command(bin, gArgs...).CombinedOutput(); err != nil {
				t.Fatalf("gopj-compress %v: %v\n%s", gArgs, err, out)
			}
			cArgs := append([]string{"-i", c.in, "-o", cOut}, c.flags...)
			// A curated case the oracle cannot serve is a defect, not a reason to
			// skip: the Go CLI is held to the same bar two lines above (C23).
			if out, err := execOracle("opj_compress", cArgs...); err != nil {
				t.Fatalf("opj_compress %v failed: %v\n%s", cArgs, err, out)
			}

			gb, err := os.ReadFile(gOut)
			if err != nil {
				t.Fatal(err)
			}
			cb, err := os.ReadFile(cOut)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gb, cb) {
				t.Fatalf("%s: not byte-identical (go=%d oracle=%d firstdiff=%d)",
					c.name, len(gb), len(cb), firstDiff(gb, cb))
			}
		})
	}
}
