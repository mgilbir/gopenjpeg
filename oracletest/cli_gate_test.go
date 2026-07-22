package oracletest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// This gate builds cmd/gopj-decompress and runs it side by side with the
// reference opj_decompress on a spread of inputs, output formats and flags,
// requiring the written output files to be byte-for-byte identical. It
// validates the CLI writers (PGX/PNM/RAW), the extension-based format
// selection, and the flag handling (-r/-d) end to end, on top of the decode
// parity proved by the other gates.

// repoRoot returns the module root (the parent of the oracle directory).
func repoRoot() string {
	_, self, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(self))
}

// buildDecompress compiles cmd/gopj-decompress into a temp dir and returns the
// binary path.
func buildDecompress(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gopj-decompress")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gopj-decompress")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gopj-decompress: %v\n%s", err, out)
	}
	return bin
}

// cliCase is one CLI comparison: an input file, an output extension, and extra
// flags applied to both binaries.
type cliCase struct {
	in    string   // relative to oracle data dir parts
	dir   []string // sub-path under input/
	ext   string
	flags []string
}

func listDir(t *testing.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestCLIByteParity runs gopj-decompress and opj_decompress across formats and
// flags and requires byte-identical output files.
func TestCLIByteParity(t *testing.T) {
	Require(t)
	bin := buildDecompress(t)

	cases := []cliCase{
		{in: "p0_02.j2k", dir: []string{"conformance"}, ext: "pgx"},
		{in: "file1.jp2", dir: []string{"conformance"}, ext: "ppm"},
		{in: "file1.jp2", dir: []string{"conformance"}, ext: "pgx"},
		{in: "file4.jp2", dir: []string{"conformance"}, ext: "pgm"},
		{in: "file3.jp2", dir: []string{"conformance"}, ext: "ppm"}, // sYCC -> RGB
		{in: "p0_02.j2k", dir: []string{"conformance"}, ext: "raw"},
		{in: "p0_01.j2k", dir: []string{"conformance"}, ext: "pgx", flags: []string{"-r", "1"}},
		{in: "p0_01.j2k", dir: []string{"conformance"}, ext: "pgx", flags: []string{"-d", "32,32,96,96"}},
	}

	for _, c := range cases {
		c := c
		name := c.in + "." + c.ext
		if len(c.flags) > 0 {
			name += "_" + c.flags[0] + c.flags[len(c.flags)-1]
		}
		t.Run(name, func(t *testing.T) {
			inPath := DataDir(append(append([]string{"input"}, c.dir...), c.in)...)

			gdir := t.TempDir()
			cdir := t.TempDir()
			gOut := filepath.Join(gdir, "out."+c.ext)
			cOut := filepath.Join(cdir, "out."+c.ext)

			// Go CLI.
			gArgs := append([]string{"-i", inPath, "-o", gOut, "-quiet"}, c.flags...)
			if out, err := exec.Command(bin, gArgs...).CombinedOutput(); err != nil {
				t.Fatalf("gopj-decompress %v: %v\n%s", gArgs, err, out)
			}
			// Oracle.
			cArgs := append([]string{"-i", inPath, "-o", cOut}, c.flags...)
			if out, err := execOracle("opj_decompress", cArgs...); err != nil {
				t.Skipf("opj_decompress %v failed: %v\n%s", cArgs, err, out)
			}

			gNames := listDir(t, gdir)
			cNames := listDir(t, cdir)
			if len(gNames) != len(cNames) {
				t.Fatalf("output file count mismatch: go=%v oracle=%v", gNames, cNames)
			}
			for i := range gNames {
				if gNames[i] != cNames[i] {
					t.Fatalf("output file name mismatch: go=%v oracle=%v", gNames, cNames)
				}
				gb, err := os.ReadFile(filepath.Join(gdir, gNames[i]))
				if err != nil {
					t.Fatal(err)
				}
				cb, err := os.ReadFile(filepath.Join(cdir, cNames[i]))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(gb, cb) {
					t.Fatalf("%s: output not byte-identical (go=%d bytes, oracle=%d bytes)",
						gNames[i], len(gb), len(cb))
				}
			}
		})
	}
}
