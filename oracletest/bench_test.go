package oracletest

// Phase-7 performance harness: measures Go decode/encode wall-time against the
// C reference (opj_decompress / opj_compress) on representative corpus files.
//
// Usage:
//
//	# comparison tables (needs the oracle present):
//	GOPENJPEG_BENCH=1 go test ./oracletest/ -run 'TestPerf(Decode|Encode)Report' -v -timeout 30m
//
//	# Go-only micro-benchmarks for pprof (single-thread hotspot analysis):
//	go test ./oracletest/ -run x -bench BenchmarkGoDecode -benchtime 5x -cpuprofile cpu.out
//
// Methodology: for each file the first run is discarded (warm-up) and the median
// of the next 3 runs is reported, for both Go and C. The C side parses the
// "decode time: N ms" / "encode time: N ms" line opj_decompress/opj_compress
// print (which isolates the transform time, excluding file I/O and header
// parse). The Go side times the whole public Decode()/Encode() call.
//
// ============================================================================
// BASELINE (recorded 2026-07-22, only per-code-block tier-1 parallelism landed;
// DWT/MCT still sequential; before profile-driven single-thread tuning.
// 16-core machine, Go 1.26). Decode median-of-3, milliseconds:
//
//	file          kind                    C(1)  Go(1)  Go1/C1  Go(16)  C(16)
//	Bretagne2     2592x1944 5/3 RCT 5x5    591   1046   1.77x     249    133
//	p0_08         513x3072 5/3 LRCP        412    811   1.97x     126     56
//	zoo1.jp2      3906x2602 5/3 (JP2)      494    976   1.98x     285     88
//	issue135      9/7 + ICT irreversible   276    559   2.03x     195     58
//	Bretagne1_ht  640x480 HTJ2K             11     18   1.66x       9      4
//	syn2048       2048x2048 5/3 random     111    274   2.47x     173     48
//
// Single-thread Go was 1.66x-2.47x of C here (target <=1.5x).
//
// FINAL (after adding whole-tile inverse-DWT row/column parallelism, same
// machine). Decode median-of-3, milliseconds:
//
//	file          C(1)  Go(1)  Go1/C1  Go(16)  C(16)  Go16-speedup
//	Bretagne2      589   1048   1.78x     202    133      5.2x
//	p0_08          415    812   1.96x     115     56      7.0x
//	zoo1.jp2       496    989   1.99x     197     89      5.0x
//	issue135       278    570   2.05x     149     59      3.8x
//	Bretagne1_ht    11     18   1.68x       6      4      3.0x
//	syn2048        111    272   2.45x      71     45      3.9x
//
// Multithread scaling 3-7x; DWT parallelism roughly halved Go(16) on the
// single-tile files (syn2048 173->71, zoo1 285->197). The residual single-
// thread gap (~1.7-2.4x) is the MQ arithmetic coder + tier-1 pass loops: the C
// reference macro-inlines the whole coder (opj_mqc_decode_macro) keeping a/c/ct
// in registers across a pass, whereas Go cannot inline the decoder (its
// renormalization contains a loop) so every decision is a call. Closing it
// needs manually inlining the MQC registers into the tier-1 passes (the C
// DOWNLOAD/UPLOAD_MQC_VARIABLES pattern) — deferred as a large, bit-exactness-
// sensitive refactor. See the worker report.
//
// ============================================================================
// AFTER W17 (2026-07-22, same 16-logical-core Ryzen 9 6900HX, Go 1.26).
//
// Lever 1 — MQ-coder register inlining into the tier-1 DECODE passes (the C
// DOWNLOAD/UPLOAD_MQC_VARIABLES pattern). internal/mqc gained a register-block
// value type (DecState) plus an inlinable-fast-path DecodeReg + a single
// renormDec call; the three hot MQC decode passes (sig/ref/cln) now Load the
// registers once, thread them by value with the per-column step logic inlined
// directly into the loop (so Go's register ABI keeps a/c/ct/bp resident and no
// step-call arg-spilling occurs), and Store them once. Bit-identical (mqc + t1
// vectors, decode gate zero-exclusions, race-clean). Isolated tier-1 decode
// microbench (BenchmarkDecodeCblkVectors over all t1 decode vectors):
// 47.9ms -> 39.9ms (-16.7%). Whole-decode single-thread, median-of-3, ms:
//
//	file          C(1)  Go(1)  Go1/C1  Go(16)  C(16)  Go16-spdup   (was Go1/C1)
//	Bretagne2      582    888   1.53x     179    133     4.96x       (1.78x)
//	p0_08          412    659   1.60x      94     56     7.02x       (1.96x)
//	zoo1.jp2       491    841   1.71x     179     86     4.71x       (1.99x)
//	issue135       277    496   1.79x     134     57     3.71x       (2.05x)
//	Bretagne1_ht    10     18   1.77x       6      4     3.04x       (1.68x, HT: no MQ)
//	syn2048        108    256   2.37x      68     48     3.78x       (2.45x)
//
// Single-thread gap closed from 1.68-2.45x to 1.53-2.37x of C; the MQ-heavy
// 5/3 files gained most (p0_08 1.96->1.60, Bretagne2 1.78->1.53). Bretagne1_ht
// is unchanged because HTJ2K uses a different entropy coder, not the MQ coder.
// BCE note: after inlining, the dominant remaining cost is the DecodeReg
// arithmetic + call, NOT bounds checks (ctxs[curctx], states[..], buf[bp]);
// eliminating those safely (no unsafe, no panic, bit-identical) has poor
// reward, so left in place.
//
// Lever 2 — per-code-block tier-1 ENCODE parallelism (mirrors the decode pool
// in internal/tcd: encodeCblks now fans code-blocks across NumThreads workers,
// each with a private t1.T1 encode state, and sums per-cblk distortion in
// canonical code-block order so tile.Distotile — hence rate allocation and the
// output bytes — is bit-identical to the sequential encode for every worker
// count). Wired through WithEncodeConcurrency + gopj-compress -threads.
// Empirical C finding: opj_compress DOES sum distotile under a mutex in
// completion order (nondeterministic float order), yet its output is byte-
// stable across -threads 1/2/8 for all tested settings — the tiny float
// differences never cross the discrete rate-allocation truncation boundaries.
// Our deterministic cblk-order sum sidesteps the dependency entirely.
// gopj-compress output is byte-identical across -threads and equals
// opj_compress. Encode CLI walltime on syn2048 2048x2048x3, ms (full pipeline;
// only tier-1 is parallelized, so DWT/rate-alloc/T2 bound the speedup):
//
//	setting          C(1)  Go(t1)  Go(t8)  t1/t8
//	rate20           1762    3047     730   4.17x
//	multilayer       1668    3052     777   3.92x
//	irrev_9x7 r20    1531    2935     763   3.84x
//	lossless         1703    3045     766   3.97x
//
// Go(8) (~760ms) now beats C(1) (~1700ms). Peak scaling is ~8 workers on this
// 8-core/16-thread box; -threads 16 oversubscribes given the sequential stages.
//
// Lever 3 — in-place 9/7 float handling: DEFERRED with evidence. The whole-
// buffer int32<->float32 bit-cast round-trips (dwt.DecodeTile97's convert-
// in/out and tcd's bitsToFloat/floatToBits MCT bridge) measure ~2-5% of decode
// and ONLY on 9/7 images (issue135: bitsToFloat 0.9% + floatToBits 0.9% +
// DecodeTile97 convert loops ~2%). Eliminating them cleanly needs either unsafe
// aliasing (forbidden) or a float-typed tcd pipeline (large, risky); the payoff
// does not clear the task's "only if it matters after lever 1" bar.
//
// Lever 1 (encode) — DEFERRED with evidence. The encode tier-1 has the same MQ
// structure and register inlining would help (encode MQ coder is ~half of
// encode-side CPU), but the growable output buffer + bit-stuffing byteout make
// holding bp in registers delicate under the strict byte-identity gate, and
// lever 2 already addresses encode wall-time (~4x). Clean follow-up.
//
// ============================================================================
// AFTER W18 (2026-07-22, same 16-logical-core Ryzen 9 6900HX; go1.25.5).
//
// Lever 1 (encode) — MQ-coder register inlining into the tier-1 ENCODE passes,
// the mirror image of W17's decode lever. internal/mqc gained an encoder
// register-block value type (EncState{A,C,Ct,Bp}) plus LoadEnc/StoreEnc, an
// inlinable-fast-path EncodeReg, and a single renormeEnc call that owns the
// renorm loop (with byteoutEnc inlined). The three hot MQ encode passes
// (sig/ref/cln) now Load the registers once, thread them by value with the
// per-column step bodies inlined directly into the stripe loop (so Go's register
// ABI keeps a/c/ct/bp resident, no step-call arg-spilling), and Store them once;
// the cold partial-stripe remainders and the RAW/bypass + flush/termination
// paths keep the method API. Bit-identical (mqc + 2805 t1 vectors, encode gate,
// concurrency-encode gate, CLI compress byte-parity, all zero-diff; -race clean).
//
// Buffer-growth boundary. W17 flagged the growable output buffer + 0xFF
// bit-stuffing byteout as the delicacy that made encode-side bp-in-registers
// risky. The resolution: in this Go port bp is an integer *offset* into m.buf,
// not a raw pointer (as in C), so a mid-pass grow/realloc leaves a held Bp valid
// — only the slice header changes, and byteoutEnc re-reads m.buf fresh on every
// call and never caches it in the hot loop. Growth therefore happens safely at
// the byteout (ct-wrap) boundary, the one place the buffer is touched, with no
// pointer fixup; the hot EncodeReg MPS fast path never touches m.buf at all.
//
// Isolated tier-1 ENCODE microbench (BenchmarkEncodeCblkVectors, all t1 encode
// vectors): 29.96ms -> 24.83ms (-17.1%). Incremental: register threading via
// step methods alone 28.9ms (-3.4%, EncodeReg/DecodeReg are too complex to
// inline so the win comes from residency, not from inlining the coder itself);
// +sig main-loop inline 26.7; +ref 25.9; +cln 24.8. Whole-encode CLI walltime,
// syn2048 2048x2048x3 random (incompressible => MQ-heavy), median-of-3, ms:
//
//	setting         C(1)  Go(t1)  Go(t8)  Go1/C1   (was Go(t1) @ W17)
//	lossless_5x3    1559    2297     628   1.47x     (3045)
//	rate20_5x3      1553    2241     560   1.44x     (3047)
//	irrev_9x7_r20   1471    2229     638   1.52x     (2935)
//
// Single-thread whole-encode dropped ~25% (rate20 3047->2241) — larger than the
// 17% tier-1 microbench because this file is incompressible, so the MQ coder is
// a bigger share of the pipeline than on the mixed vector set. Go(t8) also fell
// (766->560 on rate20) since tier-1 is the parallelized stage. Environment
// parity with the W17 tables is confirmed: the decode microbench reproduces at
// 39.4ms here vs W17's 39.9ms (within noise), so the cross-entry comparison is
// sound. Profile after the change: EncodeReg ~20% flat (irreducible MQ
// arithmetic), the three inlined passes ~20% flat combined, and a residual
// ~15% in the untouched RAW/lazy (BypassEnc) path — a different coder, left
// as-is per the MQ-only scope. nmsedec LUTs / getctxno are each <3%, so no
// second lever was warranted.
// ============================================================================

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/mgilbir/gopenjpeg"
)

// benchFile names a file to benchmark.
type benchFile struct {
	name string // short label
	path string // absolute path
	kind string // human description
}

// corpusBenchFiles returns the fixed corpus files (skips any that are absent).
func corpusBenchFiles() []benchFile {
	cand := []benchFile{
		{"Bretagne2", DataDir("input", "nonregression", "Bretagne2.j2k"), "2592x1944 5/3 RCT, 5x5 tiles"},
		{"p0_08", DataDir("input", "conformance", "p0_08.j2k"), "513x3072 5/3 LRCP"},
		{"zoo1.jp2", DataDir("input", "conformance", "zoo1.jp2"), "3906x2602 5/3 (JP2)"},
		{"issue135", DataDir("input", "nonregression", "issue135.j2k"), "9/7 + ICT irreversible"},
		{"Bretagne1_ht", DataDir("input", "nonregression", "htj2k", "Bretagne1_ht.j2k"), "640x480 HTJ2K"},
	}
	var out []benchFile
	for _, f := range cand {
		if _, err := os.Stat(f.path); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// syntheticBenchFile generates (once, cached) a 2048x2048x3 random-content j2k
// via opj_compress and returns it. Random content makes it an incompressible,
// decode-heavy single-tile file.
func syntheticBenchFile(t *testing.T) (benchFile, bool) {
	dir := filepath.Join(os.TempDir(), "gopenjpeg-bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return benchFile{}, false
	}
	j2k := filepath.Join(dir, "syn2048.j2k")
	if _, err := os.Stat(j2k); err == nil {
		return benchFile{"syn2048", j2k, "2048x2048 5/3 random, single tile"}, true
	}
	raw := filepath.Join(dir, "syn2048.raw")
	const w, h = 2048, 2048
	buf := make([]byte, 3*w*h)
	// Deterministic pseudo-random fill (xorshift) so the file is reproducible.
	x := uint32(0x9e3779b9)
	for i := range buf {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		buf[i] = byte(x)
	}
	if err := os.WriteFile(raw, buf, 0o644); err != nil {
		return benchFile{}, false
	}
	cmd := exec.Command(Bin("opj_compress"), "-i", raw, "-o", j2k,
		"-F", "2048,2048,3,8,u", "-r", "20")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("synthetic compress failed: %v\n%s", err, out)
		return benchFile{}, false
	}
	return benchFile{"syn2048", j2k, "2048x2048 5/3 random, single tile"}, true
}

func median(ds []time.Duration) time.Duration {
	c := append([]time.Duration(nil), ds...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[len(c)/2]
}

// timeGoDecode times a full public Decode() with the given worker count.
func timeGoDecode(path string, threads int) (time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	_, err = gopenjpeg.Decode(bytes.NewReader(data), gopenjpeg.WithConcurrency(threads))
	return time.Since(start), err
}

var timeRe = regexp.MustCompile(`(?:decode|encode) time:\s*(\d+)\s*ms`)

// timeCDecode runs opj_decompress and returns its reported "decode time".
func timeCDecode(path, outDir string, threads int) (time.Duration, error) {
	out := filepath.Join(outDir, "c_out.raw")
	args := []string{"-i", path, "-o", out}
	if threads > 1 {
		args = append(args, "-threads", fmt.Sprint(threads))
	}
	b, err := exec.Command(Bin("opj_decompress"), args...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%v: %s", err, b)
	}
	m := timeRe.FindSubmatch(b)
	if m == nil {
		return 0, fmt.Errorf("no decode time in output: %s", b)
	}
	var ms int
	fmt.Sscanf(string(m[1]), "%d", &ms)
	return time.Duration(ms) * time.Millisecond, nil
}

// medianOf runs fn 4 times, discards the first, returns median of the last 3.
func medianOf(fn func() (time.Duration, error)) (time.Duration, error) {
	var ds []time.Duration
	for i := 0; i < 4; i++ {
		d, err := fn()
		if err != nil {
			return 0, err
		}
		if i > 0 {
			ds = append(ds, d)
		}
	}
	return median(ds), nil
}

// TestPerfDecodeReport prints the Go-vs-C decode comparison table.
func TestPerfDecodeReport(t *testing.T) {
	if os.Getenv("GOPENJPEG_BENCH") == "" {
		t.Skip("set GOPENJPEG_BENCH=1 to run the performance report")
	}
	Require(t)
	outDir := t.TempDir()
	ncpu := runtime.NumCPU()

	files := corpusBenchFiles()
	if sf, ok := syntheticBenchFile(t); ok {
		files = append(files, sf)
	}

	fmt.Printf("\n=== Decode Go vs C (median-of-3, ms) — %d CPUs ===\n", ncpu)
	fmt.Printf("%-14s %-26s %7s %7s %8s %8s %8s %9s\n",
		"file", "kind", "C(1)", "Go(1)", "Go1/C1", fmt.Sprintf("Go(%d)", ncpu), fmt.Sprintf("C(%d)", ncpu), "spdup")
	for _, f := range files {
		c1, err := medianOf(func() (time.Duration, error) { return timeCDecode(f.path, outDir, 1) })
		if err != nil {
			t.Logf("%s: C(1) failed: %v", f.name, err)
			continue
		}
		g1, err := medianOf(func() (time.Duration, error) { return timeGoDecode(f.path, 1) })
		if err != nil {
			t.Logf("%s: Go(1) failed: %v", f.name, err)
			continue
		}
		gN, gErr := medianOf(func() (time.Duration, error) { return timeGoDecode(f.path, ncpu) })
		cN, cErr := medianOf(func() (time.Duration, error) { return timeCDecode(f.path, outDir, ncpu) })
		ratio := float64(g1) / float64(c1)
		spd := 0.0
		if gN > 0 {
			spd = float64(g1) / float64(gN)
		}
		gnStr, cnStr := "err", "err"
		if gErr == nil {
			gnStr = fmt.Sprintf("%.0f", float64(gN)/1e6)
		}
		if cErr == nil {
			cnStr = fmt.Sprintf("%.0f", float64(cN)/1e6)
		}
		fmt.Printf("%-14s %-26s %7.0f %7.0f %8.2f %8s %8s %8.2fx\n",
			f.name, f.kind,
			float64(c1)/1e6, float64(g1)/1e6, ratio, gnStr, cnStr, spd)
	}
}

// encodeSetting is one opj_compress / gopj-compress configuration.
type encodeSetting struct {
	name string
	args []string // extra opj_compress args (both binaries share the same flags)
}

// TestPerfEncodeReport prints a Go-vs-C encode comparison over a few settings.
func TestPerfEncodeReport(t *testing.T) {
	if os.Getenv("GOPENJPEG_BENCH") == "" {
		t.Skip("set GOPENJPEG_BENCH=1 to run the performance report")
	}
	Require(t)
	// Build the Go compressor once, by module path (cwd-independent).
	bin := filepath.Join(t.TempDir(), "gopj-compress")
	if out, err := exec.Command("go", "build", "-o", bin,
		"github.com/mgilbir/gopenjpeg/cmd/gopj-compress").CombinedOutput(); err != nil {
		t.Skipf("cannot build gopj-compress: %v\n%s", err, out)
	}

	dir := filepath.Join(os.TempDir(), "gopenjpeg-bench")
	os.MkdirAll(dir, 0o755)
	raw := filepath.Join(dir, "syn2048.raw")
	if _, err := os.Stat(raw); err != nil {
		if _, ok := syntheticBenchFile(t); !ok {
			t.Skip("no synthetic input available")
		}
	}
	fArg := []string{"-F", "2048,2048,3,8,u"}

	settings := []encodeSetting{
		{"lossless_5x3", append([]string{}, fArg...)},
		{"rate20_5x3", append(append([]string{}, fArg...), "-r", "20")},
		{"irrev_9x7_r20", append(append([]string{}, fArg...), "-I", "-r", "20")},
	}

	timeC := func(s encodeSetting) (time.Duration, error) {
		out := filepath.Join(t.TempDir(), "c.j2k")
		args := append([]string{"-i", raw, "-o", out}, s.args...)
		b, err := exec.Command(Bin("opj_compress"), args...).CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("%v: %s", err, b)
		}
		m := timeRe.FindSubmatch(b)
		if m == nil {
			return 0, fmt.Errorf("no encode time: %s", b)
		}
		var ms int
		fmt.Sscanf(string(m[1]), "%d", &ms)
		return time.Duration(ms) * time.Millisecond, nil
	}
	timeGo := func(s encodeSetting) (time.Duration, error) {
		out := filepath.Join(t.TempDir(), "go.j2k")
		args := append([]string{"-i", raw, "-o", out}, s.args...)
		start := time.Now()
		b, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("%v: %s", err, b)
		}
		return time.Since(start), nil
	}

	fmt.Printf("\n=== Encode Go vs C (median-of-3, ms; Go=full CLI walltime, C=reported encode time) ===\n")
	fmt.Printf("%-16s %8s %8s %8s\n", "setting", "C", "Go", "Go/C")
	for _, s := range settings {
		c, err := medianOf(func() (time.Duration, error) { return timeC(s) })
		if err != nil {
			t.Logf("%s: C encode failed: %v", s.name, err)
			continue
		}
		g, err := medianOf(func() (time.Duration, error) { return timeGo(s) })
		if err != nil {
			t.Logf("%s: Go encode failed: %v", s.name, err)
			continue
		}
		fmt.Printf("%-16s %8.0f %8.0f %8.2f\n",
			s.name, float64(c)/1e6, float64(g)/1e6, float64(g)/float64(c))
	}
}

// --- Go-only micro-benchmarks for pprof (single-thread hotspots) ---

func BenchmarkGoDecode(b *testing.B) {
	if _, err := os.Stat(Bin("opj_decompress")); err != nil {
		b.Skip("oracle corpus not available")
	}
	files := corpusBenchFiles()
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		b.Run(f.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				if _, err := gopenjpeg.Decode(bytes.NewReader(data), gopenjpeg.WithConcurrency(1)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
