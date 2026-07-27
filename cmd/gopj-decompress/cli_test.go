package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

// buildCLI compiles this package into a temp dir and returns the binary path.
func buildCLI(t *testing.T) string {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(self)
	bin := filepath.Join(t.TempDir(), "gopj-decompress")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = pkgDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gopj-decompress: %v\n%s", err, out)
	}
	return bin
}

// writeTestJ2K encodes a small grayscale gradient with the library and returns
// the file path.
func writeTestJ2K(t *testing.T, dir string) string {
	t.Helper()
	const w, h = 64, 64
	data := make([]int32, w*h)
	for i := range data {
		data[i] = int32(i % 256)
	}
	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, w, h, []gopenjpeg.Component{
		{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: data},
	})
	var buf bytes.Buffer
	if err := gopenjpeg.Encode(img, &buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	path := filepath.Join(dir, "in.j2k")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// C47: an invalid -threads value must be an error, matching gopj-compress,
// not a silent fallback to one thread.
func TestInvalidThreadsRejected(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	in := writeTestJ2K(t, dir)

	for _, v := range []string{"0", "-2", "banana", ""} {
		out, err := exec.Command(bin, "-i", in, "-o", filepath.Join(dir, "out.pgx"), "-threads", v).CombinedOutput()
		if err == nil {
			t.Errorf("-threads %q: expected nonzero exit, got success", v)
		}
		if !strings.Contains(string(out), "positive integer or ALL_CPUS") {
			t.Errorf("-threads %q: missing error message, got: %s", v, out)
		}
	}
}

// C48: an uppercase .PGX extension is accepted by format detection and must
// not then be rejected by the writer.
func TestUppercasePGXAccepted(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	in := writeTestJ2K(t, dir)

	out := filepath.Join(dir, "OUT.PGX")
	if o, err := exec.Command(bin, "-i", in, "-o", out, "-quiet").CombinedOutput(); err != nil {
		t.Fatalf("uppercase .PGX rejected: %v\n%s", err, o)
	}
	written := filepath.Join(dir, "OUT_0.pgx")
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("expected %s to exist: %v", written, err)
	}
}
