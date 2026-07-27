package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

// buildSelf compiles this command once per test binary run and returns the
// executable path. Running the real binary is what proves a malformed input
// exits cleanly instead of printing a goroutine dump.
var selfBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gopj-compress-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	selfBin = filepath.Join(dir, "gopj-compress")
	cmd := exec.Command("go", "build", "-o", selfBin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build gopj-compress: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runCLI runs the built binary and returns its combined output and exit code.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(selfBin, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return string(out), code
}

// writePGM writes a w x h 8-bit P5 gradient with the given line terminator.
func writePGM(t *testing.T, path string, w, h int, eol string) {
	t.Helper()
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "P5%s%d %d%s255%s", eol, w, h, eol, eol)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf.WriteByte(byte((x*7 + y*13) & 0xff))
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMalformedInputExitsCleanly covers the five reproduced C7 panics: every one
// must exit 1 with a diagnostic and no goroutine dump.
func TestMalformedInputExitsCleanly(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	small := write("small.raw", make([]byte, 64))

	cases := []struct {
		name string
		args []string
	}{
		{
			"pgm_maxval_zero",
			[]string{"-i", write("maxval0.pgm", []byte("P2\n1 1\n0\n0\n")), "-o", filepath.Join(dir, "a.j2k")},
		},
		{
			"pnm_huge_dimensions",
			[]string{"-i", write("huge.pgm", []byte("P5\n999999999 999999999\n255\n")), "-o", filepath.Join(dir, "b.j2k")},
		},
		{
			"pgx_negative_dimension",
			[]string{"-i", write("neg.pgx", []byte("PG ML +8 -1 4\n")), "-o", filepath.Join(dir, "c.j2k")},
		},
		{
			"raw_zero_subsampling",
			[]string{"-i", small, "-o", filepath.Join(dir, "d.j2k"), "-F", "8,8,1,8,u@0x0"},
		},
		{
			"raw_negative_ncomp",
			[]string{"-i", small, "-o", filepath.Join(dir, "e.j2k"), "-F", "8,8,-1,8,u"},
		},
		{
			"pgx_huge_dimensions",
			[]string{"-i", write("hugepgx.pgx", []byte("PG ML +8 100000 100000\n")), "-o", filepath.Join(dir, "f.j2k")},
		},
		{
			"raw_truncated",
			[]string{"-i", small, "-o", filepath.Join(dir, "g.j2k"), "-F", "4096,4096,3,8,u"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runCLI(t, tc.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, out)
			}
			if strings.Contains(out, "goroutine ") || strings.Contains(out, "panic:") {
				t.Fatalf("stack trace in output:\n%s", out)
			}
			if !strings.Contains(out, "ERROR") {
				t.Fatalf("no diagnostic in output:\n%s", out)
			}
			t.Logf("%s", strings.SplitN(out, "\n", 2)[0])
		})
	}
}

// TestPGXRoundTripFromSiblingWriter builds the PGX header the sibling
// gopj-decompress writes ("PG ML + 8 W H", sign as a separate token) and checks
// the compress-side reader accepts it and recovers the samples (C13). Both the
// attached and detached sign spellings must parse identically.
func TestPGXRoundTripFromSiblingWriter(t *testing.T) {
	const w, h = 12, 9
	samples := make([]byte, w*h)
	for i := range samples {
		samples[i] = byte((i*7 + 3) & 0xff)
	}
	dir := t.TempDir()

	for _, hdr := range []string{
		"PG ML + 8 %d %d\n", // what gopj-decompress / opj_decompress write
		"PG ML +8 %d %d\n",  // the attached-sign spelling
		"PG ML 8 %d %d\n",   // no sign at all
	} {
		t.Run(strings.TrimSpace(fmt.Sprintf(hdr, w, h)), func(t *testing.T) {
			p := filepath.Join(dir, "in.pgx")
			body := append([]byte(fmt.Sprintf(hdr, w, h)), samples...)
			if err := os.WriteFile(p, body, 0o644); err != nil {
				t.Fatal(err)
			}
			img, err := readPGX(p, readerParams{subX: 1, subY: 1, mctMode: -1})
			if err != nil {
				t.Fatalf("readPGX: %v", err)
			}
			c := img.Component(0)
			if c.W != w || c.H != h {
				t.Fatalf("dimensions = %dx%d, want %dx%d", c.W, c.H, w, h)
			}
			for i, want := range samples {
				if c.Data[i] != int32(want) {
					t.Fatalf("sample %d = %d, want %d", i, c.Data[i], want)
				}
			}
		})
	}
}

// TestPGXRoundTripThroughSiblingBinary is the end-to-end form of C13: encode a
// PGM, decompress it to PGX with gopj-decompress, and feed that PGX straight
// back to gopj-compress.
func TestPGXRoundTripThroughSiblingBinary(t *testing.T) {
	dir := t.TempDir()
	decompress := filepath.Join(dir, "gopj-decompress")
	build := exec.Command("go", "build", "-o", decompress, "../gopj-decompress")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build gopj-decompress: %v\n%s", err, out)
	}

	src := filepath.Join(dir, "src.pgm")
	writePGM(t, src, 64, 48, "\n")
	j2k := filepath.Join(dir, "src.j2k")
	if out, code := runCLI(t, "-i", src, "-o", j2k, "-quiet"); code != 0 {
		t.Fatalf("compress: exit %d\n%s", code, out)
	}
	pgx := filepath.Join(dir, "out.pgx")
	if out, err := exec.Command(decompress, "-i", j2k, "-o", pgx, "-quiet").CombinedOutput(); err != nil {
		t.Fatalf("gopj-decompress: %v\n%s", err, out)
	}
	written := filepath.Join(dir, "out_0.pgx")
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("gopj-decompress did not write %s: %v", written, err)
	}
	back := filepath.Join(dir, "back.j2k")
	if out, code := runCLI(t, "-i", written, "-o", back, "-quiet"); code != 0 {
		t.Fatalf("re-compress of the sibling's PGX failed: exit %d\n%s", code, out)
	}

	// The samples must survive the loop: the PGX the sibling wrote holds the
	// decoded gradient, so re-reading it must reproduce the source samples.
	img, err := readPGX(written, readerParams{subX: 1, subY: 1, mctMode: -1})
	if err != nil {
		t.Fatalf("readPGX: %v", err)
	}
	c := img.Component(0)
	if c.W != 64 || c.H != 48 {
		t.Fatalf("dimensions = %dx%d, want 64x48", c.W, c.H)
	}
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			want := int32((x*7 + y*13) & 0xff)
			if got := c.Data[y*64+x]; got != want {
				t.Fatalf("sample (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

// TestCRLFHeaderMatchesLF proves a CRLF-terminated PNM header no longer shifts
// the samples by one byte (C16).
func TestCRLFHeaderMatchesLF(t *testing.T) {
	dir := t.TempDir()
	lf := filepath.Join(dir, "lf.pgm")
	crlf := filepath.Join(dir, "crlf.pgm")
	writePGM(t, lf, 64, 48, "\n")
	writePGM(t, crlf, 64, 48, "\r\n")

	rp := readerParams{subX: 1, subY: 1, mctMode: -1}
	a, err := readPNM(lf, rp)
	if err != nil {
		t.Fatalf("read LF: %v", err)
	}
	b, err := readPNM(crlf, rp)
	if err != nil {
		t.Fatalf("read CRLF: %v", err)
	}
	ca, cb := a.Component(0), b.Component(0)
	if ca.W != cb.W || ca.H != cb.H {
		t.Fatalf("dimensions differ: %dx%d vs %dx%d", ca.W, ca.H, cb.W, cb.H)
	}
	for i := range ca.Data {
		if ca.Data[i] != cb.Data[i] {
			t.Fatalf("sample %d differs: LF=%d CRLF=%d", i, ca.Data[i], cb.Data[i])
		}
	}

	// And end to end: the two codestreams must be identical.
	outLF := filepath.Join(dir, "lf.j2k")
	outCRLF := filepath.Join(dir, "crlf.j2k")
	if out, code := runCLI(t, "-i", lf, "-o", outLF, "-quiet"); code != 0 {
		t.Fatalf("compress LF: exit %d\n%s", code, out)
	}
	if out, code := runCLI(t, "-i", crlf, "-o", outCRLF, "-quiet"); code != 0 {
		t.Fatalf("compress CRLF: exit %d\n%s", code, out)
	}
	gotLF, err := os.ReadFile(outLF)
	if err != nil {
		t.Fatal(err)
	}
	gotCRLF, err := os.ReadFile(outCRLF)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotLF, gotCRLF) {
		t.Fatalf("CRLF codestream differs from LF (%d vs %d bytes)", len(gotLF), len(gotCRLF))
	}
}

// TestSubsamplingReachesTheImage checks -s actually sub-samples: the components
// carry the factors and the reference grid widens accordingly (C14).
func TestSubsamplingReachesTheImage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.pgm")
	writePGM(t, src, 64, 48, "\n")

	img, err := readPNM(src, readerParams{subX: 2, subY: 3, mctMode: -1})
	if err != nil {
		t.Fatal(err)
	}
	c := img.Component(0)
	if c.Dx != 2 || c.Dy != 3 {
		t.Fatalf("component dx,dy = %d,%d, want 2,3", c.Dx, c.Dy)
	}
	_, _, x1, y1 := img.Bounds()
	if want := uint32((64-1)*2 + 1); x1 != want {
		t.Fatalf("x1 = %d, want %d", x1, want)
	}
	if want := uint32((48-1)*3 + 1); y1 != want {
		t.Fatalf("y1 = %d, want %d", y1, want)
	}

	// The SIZ must carry the factors: XRsiz/YRsiz live at a fixed offset in the
	// single-component SIZ marker (SOC + SIZ header + 8 grid fields + Csiz).
	out := filepath.Join(dir, "s.j2k")
	if o, code := runCLI(t, "-i", src, "-o", out, "-s", "2,3", "-quiet"); code != 0 {
		t.Fatalf("compress: exit %d\n%s", code, o)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	const sizPayload = 2 + 2 + 2 // SOC + SIZ marker + Lsiz
	const gridEnd = sizPayload + 2 + 8*4 + 2
	if len(b) < gridEnd+3 {
		t.Fatalf("codestream too short: %d bytes", len(b))
	}
	if b[gridEnd+1] != 2 || b[gridEnd+2] != 3 {
		t.Fatalf("SIZ XRsiz,YRsiz = %d,%d, want 2,3", b[gridEnd+1], b[gridEnd+2])
	}

	// -s 1,1 must be indistinguishable from no -s at all.
	plain := filepath.Join(dir, "plain.j2k")
	one := filepath.Join(dir, "one.j2k")
	if o, code := runCLI(t, "-i", src, "-o", plain, "-quiet"); code != 0 {
		t.Fatalf("compress: exit %d\n%s", code, o)
	}
	if o, code := runCLI(t, "-i", src, "-o", one, "-s", "1,1", "-quiet"); code != 0 {
		t.Fatalf("compress: exit %d\n%s", code, o)
	}
	pb, _ := os.ReadFile(plain)
	ob, _ := os.ReadFile(one)
	if !bytes.Equal(pb, ob) {
		t.Fatalf("-s 1,1 changed the codestream")
	}
}

// TestWithSubsamplingIsNotSilentlyIgnored covers the library half of C14: the
// option no longer sets a field nothing reads.
func TestWithSubsamplingIsNotSilentlyIgnored(t *testing.T) {
	data := make([]int32, 64*64)
	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, 64, 64, []gopenjpeg.Component{
		{Dx: 1, Dy: 1, W: 64, H: 64, Prec: 8, Data: data},
	})
	err := gopenjpeg.Encode(img, &bytes.Buffer{}, gopenjpeg.WithSubsampling(2, 2))
	if err == nil {
		t.Fatal("WithSubsampling(2,2) on a non-subsampled image was accepted")
	}
	if !strings.Contains(err.Error(), "WithSubsampling") {
		t.Fatalf("unhelpful error: %v", err)
	}
	// Agreeing with the image is fine.
	if err := gopenjpeg.Encode(img, &bytes.Buffer{}, gopenjpeg.WithSubsampling(1, 1)); err != nil {
		t.Fatalf("WithSubsampling(1,1) rejected: %v", err)
	}
}

// TestMCT2WithoutMatrixFails covers C15: -mct 2 alone must not exit 0 with an
// undecodable codestream.
func TestMCT2WithoutMatrixFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "c.ppm")
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "P6\n%d %d\n255\n", 64, 64)
	for i := 0; i < 64*64*3; i++ {
		buf.WriteByte(byte(i))
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "c.j2k")
	o, code := runCLI(t, "-i", src, "-o", out, "-mct", "2")
	if code == 0 {
		t.Fatalf("-mct 2 without -m exited 0:\n%s", o)
	}
	if !strings.Contains(o, "-m") {
		t.Fatalf("error does not mention the missing matrix:\n%s", o)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a failed encode left %s behind", out)
	}
}

// TestLibraryRejectsMCT2WithoutMatrix confirms the library-side guard is still
// the backstop when the CLI check is bypassed.
func TestLibraryRejectsMCT2WithoutMatrix(t *testing.T) {
	comps := make([]gopenjpeg.Component, 3)
	for i := range comps {
		comps[i] = gopenjpeg.Component{Dx: 1, Dy: 1, W: 64, H: 64, Prec: 8, Data: make([]int32, 64*64)}
	}
	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceSRGB, 0, 0, 64, 64, comps)
	var diag strings.Builder
	err := gopenjpeg.Encode(img, &bytes.Buffer{},
		gopenjpeg.WithMCT(2),
		gopenjpeg.WithEncodeErrorHandler(func(s string) { diag.WriteString(s) }))
	if err == nil {
		t.Fatal("library accepted mct=2 without a coding matrix")
	}
	if !strings.Contains(diag.String(), "MCT") {
		t.Fatalf("no diagnostic reached the error handler: %q", diag.String())
	}
}

// TestOutputExtensionHandling covers C32.
func TestOutputExtensionHandling(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.pgm")
	writePGM(t, src, 64, 48, "\n")

	if err := os.Mkdir(filepath.Join(dir, "out.dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		filepath.Join(dir, "x.xyz"),
		filepath.Join(dir, "out.dir", "file"),
		filepath.Join(dir, "noext"),
	}
	for _, o := range bad {
		t.Run(filepath.Base(o), func(t *testing.T) {
			got, code := runCLI(t, "-i", src, "-o", o, "-quiet")
			if code != 1 {
				t.Fatalf("exit %d, want 1\n%s", code, got)
			}
			if !strings.Contains(got, "unknown output format") {
				t.Fatalf("unexpected error:\n%s", got)
			}
			if _, err := os.Stat(o); err == nil {
				t.Fatalf("%s was created anyway", o)
			}
		})
	}
	for _, ext := range []string{"j2k", "j2c", "jpc", "jp2", "JP2"} {
		o := filepath.Join(dir, "ok."+ext)
		if got, code := runCLI(t, "-i", src, "-o", o, "-quiet"); code != 0 {
			t.Fatalf(".%s rejected: exit %d\n%s", ext, code, got)
		}
	}
}

// TestRatesAndQualityConflict covers C33.
func TestRatesAndQualityConflict(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.pgm")
	writePGM(t, src, 64, 48, "\n")
	out := filepath.Join(dir, "s.j2k")
	got, code := runCLI(t, "-i", src, "-o", out, "-r", "20", "-q", "30")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, got)
	}
	if !strings.Contains(got, "cannot be used together") {
		t.Fatalf("unexpected error:\n%s", got)
	}
}

// TestFailedEncodeDoesNotClobberOutput covers C34.
func TestFailedEncodeDoesNotClobberOutput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.pgm")
	writePGM(t, src, 64, 48, "\n")
	out := filepath.Join(dir, "keep.j2k")
	const sentinel = "PREEXISTING CONTENT"
	if err := os.WriteFile(out, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	// 30 resolution levels on a 16x16 image is rejected by the encoder.
	got, code := runCLI(t, "-i", src, "-o", out, "-n", "30")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, got)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != sentinel {
		t.Fatalf("output was clobbered: %q", string(b))
	}
	// The library diagnostic must reach stderr (C35).
	if !strings.Contains(got, "Number of resolutions") {
		t.Fatalf("encoder diagnostic was discarded:\n%s", got)
	}
	// No stray temp file next to the output.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// TestTilePartFlagValidation covers C49.
func TestTilePartFlagValidation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.pgm")
	writePGM(t, src, 64, 48, "\n")
	for _, bad := range []string{"X", "r", "RL", ""} {
		got, code := runCLI(t, "-i", src, "-o", filepath.Join(dir, "s.j2k"), "-TP", bad)
		if code != 1 {
			t.Fatalf("-TP %q: exit %d, want 1\n%s", bad, code, got)
		}
		if !strings.Contains(got, "-TP") {
			t.Fatalf("-TP %q: unexpected error:\n%s", bad, got)
		}
	}
	for _, ok := range []string{"R", "L", "C"} {
		got, code := runCLI(t, "-i", src, "-o", filepath.Join(dir, "s.j2k"), "-TP", ok, "-quiet")
		if code != 0 {
			t.Fatalf("-TP %s rejected: exit %d\n%s", ok, code, got)
		}
	}
}

// TestPOCMultipleRecords covers C31: every '/'-separated record must be parsed.
func TestPOCMultipleRecords(t *testing.T) {
	pocs, err := parsePOC("T1=0,0,1,5,3,CPRL/T1=5,0,1,6,3,LRCP")
	if err != nil {
		t.Fatal(err)
	}
	if len(pocs) != 2 {
		t.Fatalf("parsed %d records, want 2", len(pocs))
	}
	if pocs[0].ResStart != 0 || pocs[0].ResEnd != 5 || pocs[0].Order != gopenjpeg.ProgCPRL {
		t.Fatalf("record 0 = %+v", pocs[0])
	}
	if pocs[1].ResStart != 5 || pocs[1].ResEnd != 6 || pocs[1].Order != gopenjpeg.ProgLRCP {
		t.Fatalf("record 1 = %+v", pocs[1])
	}
	three, err := parsePOC("T1=0,0,1,2,3,LRCP/T1=2,0,1,4,3,RLCP/T1=4,0,1,6,3,CPRL")
	if err != nil {
		t.Fatal(err)
	}
	if len(three) != 3 {
		t.Fatalf("parsed %d records, want 3", len(three))
	}
}

// TestHelpExitsZero covers the C35 usage gap.
func TestHelpExitsZero(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		out, code := runCLI(t, flag)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", flag, code, out)
		}
		for _, want := range []string{"-i <file>", "-o <compressed file>", "-mct", "-TP <R|L|C>"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: usage text lacks %q", flag, want)
			}
		}
	}
}

// TestQuietSuppressesInfo covers the dead -quiet flag (C35).
func TestQuietSuppressesInfo(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.pgm")
	writePGM(t, src, 64, 48, "\n")
	loud, code := runCLI(t, "-i", src, "-o", filepath.Join(dir, "a.j2k"))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, loud)
	}
	if !strings.Contains(loud, "[INFO]") {
		t.Fatalf("no informational output without -quiet:\n%s", loud)
	}
	quiet, code := runCLI(t, "-i", src, "-o", filepath.Join(dir, "b.j2k"), "-quiet")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, quiet)
	}
	if strings.Contains(quiet, "[INFO]") {
		t.Fatalf("-quiet did not suppress informational output:\n%s", quiet)
	}
}

// TestPNMSubtypes covers C51: P1, P4 and P7 must load with the layout convert.c
// gives them rather than being silently misread.
func TestPNMSubtypes(t *testing.T) {
	dir := t.TempDir()
	const w, h = 8, 4
	bit := func(x, y int) bool { return (x+y)%3 == 0 }

	var p1 bytes.Buffer
	fmt.Fprintf(&p1, "P1\n%d %d\n", w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if bit(x, y) {
				p1.WriteString("1 ")
			} else {
				p1.WriteString("0 ")
			}
		}
		p1.WriteString("\n")
	}
	var p4 bytes.Buffer
	fmt.Fprintf(&p4, "P4\n%d %d\n", w, h)
	for y := 0; y < h; y++ {
		var acc byte
		for x := 0; x < w; x++ {
			acc <<= 1
			if bit(x, y) {
				acc |= 1
			}
		}
		p4.WriteByte(acc)
	}
	var p7 bytes.Buffer
	fmt.Fprintf(&p7, "P7\nWIDTH %d\nHEIGHT %d\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", w, h)
	for i := 0; i < w*h; i++ {
		p7.WriteByte(byte(i * 3))
	}

	rp := readerParams{subX: 1, subY: 1, mctMode: -1}
	check := func(name string, content []byte, want func(i int) int32) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
		img, err := readPNM(p, rp)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := img.Component(0)
		if c.W != w || c.H != h {
			t.Fatalf("%s: dims %dx%d", name, c.W, c.H)
		}
		for i := range c.Data {
			if c.Data[i] != want(i) {
				t.Fatalf("%s: sample %d = %d, want %d", name, i, c.Data[i], want(i))
			}
		}
	}
	// P1/P4: a set bit is black (0), a clear bit is white (255).
	bw := func(i int) int32 {
		if bit(i%w, i/w) {
			return 0
		}
		return 255
	}
	check("b.pbm", p1.Bytes(), bw)
	check("b4.pbm", p4.Bytes(), bw)
	check("g.pam", p7.Bytes(), func(i int) int32 { return int32(byte(i * 3)) })
}

// TestRawSubsamplingCeildiv covers the floor-division half of C51: a component
// buffer sized with floor() is one row short of what the encoder addresses.
func TestRawSubsamplingCeildiv(t *testing.T) {
	dir := t.TempDir()
	// 9x9 with a 2x2 sub-sampled second and third component: ceil(9/2)=5, not 4.
	const w, h = 9, 9
	raw := make([]byte, w*h+2*((w*h)/4))
	for i := range raw {
		raw[i] = byte(i)
	}
	p := filepath.Join(dir, "s.raw")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := parseRawGeometry("9,9,3,8,u@1x1:2x2:2x2")
	if err != nil {
		t.Fatal(err)
	}
	img, err := readRAW(p, g, readerParams{subX: 1, subY: 1, mctMode: -1}, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 3; i++ {
		c := img.Component(i)
		if c.W != 5 || c.H != 5 {
			t.Fatalf("component %d = %dx%d, want 5x5 (ceildiv)", i, c.W, c.H)
		}
		if len(c.Data) != 25 {
			t.Fatalf("component %d data = %d samples, want 25", i, len(c.Data))
		}
	}
	// The whole image must still encode without an out-of-range access.
	out := filepath.Join(dir, "s.j2k")
	if o, code := runCLI(t, "-i", p, "-o", out, "-F", "9,9,3,8,u@1x1:2x2:2x2", "-n", "1", "-quiet"); code != 0 {
		t.Fatalf("encode: exit %d\n%s", code, o)
	}
}

// TestUnknownInputExtension checks the input side of the format sniffing.
func TestUnknownInputExtension(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.bogus")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, code := runCLI(t, "-i", src, "-o", filepath.Join(dir, "o.j2k"))
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, got)
	}
	if !strings.Contains(got, "unknown input file format") {
		t.Fatalf("unexpected error:\n%s", got)
	}
}
