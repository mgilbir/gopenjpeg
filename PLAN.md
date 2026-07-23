# gopenjpeg — Pure-Go port of OpenJPEG

Goal: full capability parity with the OpenJPEG reference implementation
(github.com/uclouvain/openjpeg, currently 2.5.x), in pure Go, with
correctness, security and performance as first-class requirements.

## Oracle

The C reference lives (gitignored) under `oracle/`:

- `oracle/openjpeg` — cloned source, built into `oracle/openjpeg/build/bin`
  (`opj_compress`, `opj_decompress`, `opj_dump`, `libopenjp2.{a,so}`).
- `oracle/data` — the official `openjpeg-data` conformance corpus
  (ETS/GHT conformance files, non-regression inputs, baseline hashes).

Two oracle strategies, used together:

1. **Module-level vectors.** For each ported module (MQ coder, DWT, T1, …)
   a tiny C harness is compiled against `libopenjp2.a` (or directly against
   the relevant `.c` file) to dump input/output vectors. Vectors small
   enough to check in live under `testdata/vectors/<module>/`; harness
   sources live under `oracle/harness/` (gitignored, regenerable).
2. **End-to-end differential tests.** `oracletest/` runs the Go decoder and
   `opj_decompress`/`opj_compress` over `oracle/data` and compares outputs
   bit-for-bit (decode) or by oracle re-decode + PSNR/codestream-dump
   comparison (encode). Guarded by a build tag / env var so `go test ./...`
   works without the oracle present.

## Package layout

```
gopenjpeg/               public API: Decode/Encode, options, image.Image interop
  internal/opjmath/      opj_intmath.h, fixed point helpers
  internal/cio/          cio.c   — byte stream reader/writer
  internal/bio/          bio.c   — bit I/O
  internal/tgt/          tgt.c   — tag trees
  internal/mqc/          mqc.c, mqc_inl.h — MQ arithmetic coder (enc+dec, RAW)
  internal/sparse/       sparse_array.c
  internal/image/        image.c — opj_image_t model
  internal/mct/          mct.c, invert.c — RCT/ICT + custom MCT
  internal/dwt/          dwt.c   — 5/3 + 9/7, fwd+inv, region/partial variants
  internal/t1/           t1.c, t1_luts.h — EBCOT tier-1 enc+dec
  internal/ht/           ht_dec.c, t1_ht_luts.h — HTJ2K (High Throughput) decode
  internal/pi/           pi.c    — packet iterator (all progression orders, POC)
  internal/t2/           t2.c    — tier-2 packet enc/dec
  internal/tcd/          tcd.c   — tile coder/decoder, rate allocation
  internal/j2k/          j2k.c   — codestream markers, decode+encode state machines
  internal/jp2/          jp2.c   — JP2/JPH container boxes
  cmd/gopj-decompress/   opj_decompress parity CLI
  cmd/gopj-compress/     opj_compress parity CLI
  cmd/gopj-dump/         opj_dump parity CLI
  oracletest/            differential harness vs C binaries
  testdata/vectors/      checked-in module-level oracle vectors
```

## Porting rules

- **The library never panics.** Every failure — malformed input,
  violated internal contract, impossible geometry — is returned as an
  error and bubbles up to the caller. No `panic()` in library code, no
  reliance on runtime bounds-check panics as control flow: validate
  explicitly before indexing when the index derives from untrusted
  input. Fuzz targets enforce this (any panic is a bug).
- Port semantics faithfully, including integer overflow guards and every
  bounds/error check in the C code — those checks are the security surface
  (many CVEs in this codebase were missing-bounds bugs). Do not "simplify
  away" defensive code.
- Go-idiomatic surface (errors, slices, `io.Reader`), C-faithful internals.
  When in doubt, mirror the C control flow so diffs against the oracle stay
  reviewable; cite the C function name in a doc comment (`// port of
  opj_mqc_decode`).
- All array indexing naturally bounds-checked by Go; replace C pointer
  arithmetic with slices, never `unsafe` (until a profiled hot spot proves
  otherwise, and only then behind a build tag with a safe fallback).
- Every package: unit tests against checked-in oracle vectors + fuzz targets
  (`go test -fuzz`) for anything parsing untrusted bytes.
- License: derivative of OpenJPEG → BSD 2-Clause, original copyright
  retained in LICENSE.

## Phases

- **Phase 0 — infra** (done in bootstrap): module, layout, oracle build,
  conformance data, this plan.
- **Phase 1 — foundations** (parallel, independent):
  - W1: `opjmath`, `cio`, `bio`, `tgt`
  - W2: `mqc` (encode+decode+RAW bypass) with C-generated vectors
  - W3: `dwt` (5/3+9/7, fwd+inv, full and region-constrained) with vectors
  - W4: `image`, `sparse`, `mct` (RCT/ICT fwd+inv, custom matrix + invert)
- **Phase 2 — coding engine**:
  - W5: `t1` EBCOT tier-1, decode + encode, all cblk styles (lazy, reset,
    vsc, segsym, termall, pterm), with per-codeblock C vectors
  - W6: `pi` + `t2` (packet headers, all 5 progressions, POC, PPM/PPT/TLM)
- **Phase 3 — decode parity**:
  - W7: `tcd` + `j2k` decode path (all markers, tile-part handling, region
    decode, resolution discard, layer truncation)
  - W8: `jp2` container decode (boxes, palette, cdef, channel packing)
  - Gate: bit-exact vs `opj_decompress` on the conformance corpus
    (class-0/class-1 ETS as applicable + nonregression corpus).
- **Phase 4 — encode parity**:
  - W9: `tcd` rate allocation + `j2k`/`jp2` encode, `opj_compress` parity
    (quality layers, tile parts, POC, MCT, ROI, cinema/IMF profiles).
  - Gate: oracle `opj_decompress` decodes our streams bit-exact vs its
    decode of C-encoded streams at same settings; `opj_dump` structural diff.
- **Phase 5 — HTJ2K**: W10: `ht` decode (`ht_dec.c`), HT conformance files.
- **Phase 6 — CLIs + hardening**: `cmd/*` parity flags, corpus-wide fuzzing
  (seed from `oracle/data`), sanitizer-style invariant checks.
- **Phase 7 — performance**: benchmarks vs C (same files),
  goroutine parallelism mirroring `thread.c` use sites (per-tile,
  per-codeblock T1, DWT row-parallel), allocation reduction. Target: within
  ~1.5× of C single-threaded, beat C on multi-core decode.

## Future work

- SIMD kernels for the 9/7 DWT lifting and ICT (mirroring C's v4dwt
  lane structure, which is bit-identical to scalar): adopt the stdlib
  `simd` package once it stabilizes AND the module's minimum Go
  version reaches a release that carries it (experimental in 1.26 as
  simd/archsimd behind GOEXPERIMENT=simd; API changes again in 1.27;
  we are pinned to go 1.25). Expected win ~10-15% on 9/7 content
  only — the MQ coder (70-90% of decode) is inherently serial. Gate
  behind the goexperiment build tag with the scalar path as fallback
  and the byte-identity gates as verifier. Hand-written assembly
  (Avo/vek) rejected: same ceiling, cuts against the pure-Go
  security posture.
- ~~ICC color management~~ DONE 2026-07-23: embedded ICC profiles
  applied via github.com/mgilbir/golittlecms (pure-Go lcms2 port);
  all ICC gate files bit-exact vs opj_decompress. Zero gate
  exclusions remain anywhere.

## Status log

- 2026-07-22: Phase 0 complete. Oracle = openjpeg@402ef586 (2.5.4).
- 2026-07-22: W4 landed: internal/image, internal/mct (+invert). Open
  follow-ups: image.CompHeaderUpdateParams is a stand-in for opj_cp
  fields (reconcile when j2k lands); image_math.go and mct.intFixMul
  carry TODOs to switch to internal/opjmath once W1 lands.
- 2026-07-22: W2 landed (internal/mqc; note the 2-scratch-byte buffer
  contract for tcd). W1 landed (opjmath/cio/bio/tgt/event); W4's local
  math helpers reconciled to opjmath. In flight: W3 (dwt+sparse),
  W5 (t1), W6 (pi+t2+cparams+tile shared type packages).
- 2026-07-22: W5 landed (internal/t1, bit-exact on 2805 vectors).
  Follow-ups: t1's local Chunk/Seg/CodeBlock types must be mapped to
  internal/tile by tcd; PTERM leftover-bytes check approximates C
  (mqc doesn't expose bp/end — consider adding accessors); HT
  dispatch seam in DecodeCblk awaits the ht worker; specialized
  64x64 decode variants deferred as a perf follow-up (Phase 7).
- 2026-07-22: W3 landed (internal/dwt + internal/sparse). Follow-ups:
  dwt local geometry types to be mapped by tcd; C reads OOB on 1x1
  tiles with >1 level (UB in reference) — tcd/j2k must guard so the
  Go port never hits that path (would panic, not corrupt).
- 2026-07-22: no-panic rule adopted (user decision): library code
  never panics; mqc contract assert converted to ErrShortBuffer.
- 2026-07-22: W8 landed (internal/jp2). Follow-ups: j2k must
  implement jp2.CodestreamCodec; jp2.DecoderParams/EncoderParams to
  reconcile with the public API layer; CIELab capture packs the 9
  words big-endian into ICCProfileBuf (documented in read_boxes.go).
- 2026-07-22: W10 landed (internal/ht). tcd must pass mb=band.numbps
  and route cblksty&HT blocks to ht.Decoder.DecodeCblk. GAP: HT
  SigProp/MagRef passes have no oracle vectors (corpus HT streams
  are cleanup-only and OpenJPEG cannot encode HT) — CLOSED same day:
  multi-pass segments synthesized via instrumented oracle (OpenJPH is
  cleanup-only too); SigProp/MagRef now bit-exact on 1722 records.
  Caveat: synthesized rather than encoder-produced conformance
  streams; revisit if a true multi-pass HT encoder becomes available.
- 2026-07-22: W7 landed (internal/tcd + internal/j2k decode). Decode
  differential gate passing (23/23 conformance bit-exact, HT wired
  directly into tcd and bit-exact e2e). In flight: W11 (public API +
  CLIs + jp2 wiring + jp2/HT-container gate), W12 (eliminate the 5
  gate exclusions: 9/7+ICT LSB deviations, t2 segment bound,
  chroma-subsampled fixtures), W9 (encode path, gated on
  byte-identical codestreams vs opj_compress).
- 2026-07-22: W11 landed (public API, gopj-decompress/gopj-dump,
  jp2<->j2k wiring; 31 public-API parity subtests + CLI byte-parity).
  Known permanent-ish exclusions: ICC/CIELab color transforms need a
  CMS engine (oracle links LCMS2; no pure-Go equivalent) — revisit in
  hardening; CMYK conversion has float-rounding-order LSB diffs
  (same class as the 9/7 issue W12 is chasing — apply its fix here).
- 2026-07-22: W9 landed (encode path): byte-identical .j2k output vs
  opj_compress on a 20-cell settings matrix; zero module bugs found
  in the landed encode-side packages. Deferred to W13 (in flight):
  jp2 encode wiring + public Encode API + gopj-compress CLI,
  cinema/IMF profiles, PLT emission, Part-2 custom MCT markers.
- 2026-07-22: W12 landed: decode gate now has ZERO exclusions.
  Root cause of LSB deviations: C release build uses -ffast-math and
  the shipped .so reassociates the ICT green term; our DecodeReal
  now matches the shipped binary. Follow-ups: apply the same
  -ffast-math analysis to root-package CMYK conversion (re-enable
  the excluded jp2-gate CMYK files) and audit the encode-side ICT
  (encode gate passed byte-identical, but add noisier inputs).
- 2026-07-22: W13 landed (jp2 encode, gopj-compress, cinema/IMF,
  PLT, custom MCT). Zero-pass code-block panic in mqc fixed (Bytes
  nil guard; NumBytes keeps the C uint32 wraparound that t1 rate
  clamping needs — the encode gate caught the over-correction).
  In flight: W14 (cinema t2 full-stream parity, CMYK/CIELab color
  parity), W15 (Phase 7 performance: concurrency mirroring the C
  thread-pool sites, profile-driven optimization, benchmarks vs C).
- 2026-07-22: W15 landed (Phase 7): per-cblk t1 + DWT row/col
  parallel decode, WithConcurrency/-threads, byte-identical and
  race-clean; Go(16) within 1.3-2.2x of C(16); single-thread
  1.7-2.5x of C (MQ macro-inlining is the deferred lever, along
  with t1-encode parallelism and in-place 9/7 float buffers).
- 2026-07-22: W14 landed: cinema cells full-stream byte-identical
  (two more -ffast-math reassociations: forward ICT grouping and
  hoisted quantizer division), CMYK bit-exact and un-excluded,
  CIELab implemented (<=1/65535 vs LCMS, tolerance-gated). Only
  remaining exclusion anywhere: embedded ICC profiles (needs CMS).
  Remaining roadmap: hardening (corpus fuzz + no-panic audit),
  deferred perf items above.
- 2026-07-22: go.mod lowered to go 1.25 (user requirement); verify
  with GOTOOLCHAIN=go1.25.5.
- 2026-07-22: W17 landed: MQ register inlining in t1 decode passes
  (single-thread 1.53-1.79x of C on codec files), parallel tier-1
  encode with deterministic distortion order (byte-identical at any
  thread count). Deferred: encode-side MQ inlining, in-place 9/7
  floats (~2-5%). Next: hardening wave.
- 2026-07-22: W16 landed (hardening wave): corpus-seeded public-API
  fuzz targets (root fuzz_test.go: FuzzDecode, FuzzDecodeConcurrent,
  FuzzEncodeDecodeRoundTrip, FuzzReadInfo) with a 64MB decoded-size
  cap and seeds from testdata/fuzzseed (15 small checked-in files) +
  a curated ~40-file oracle subset. One crasher found and fixed:
  truncated QCD/SQcd with SIQNT made j2k mreader.u over-read (C does
  the same heap over-read, tolerated only by the following header-size
  check) -> mreader.u is now bounds-safe (zero-extends past the
  segment end), so every j2k marker parser is OOB-proof and the
  existing size checks reject the input (regression: root fuzz corpus
  + TestReadSQcdSQccTruncated/TestMreaderBoundsSafe). Sustained clean
  fuzz runs: FuzzDecode 10m/580k execs, FuzzDecodeConcurrent
  10m/832k, FuzzEncodeDecodeRoundTrip 5m/1.5M, FuzzReadInfo 3m/391k;
  all 11 internal targets 2m each clean (pi 21M, t2 24M, ht 25M,
  bio/tgt/mqc ~9M). Static audit: zero panic() in library; SIZ/ihdr/
  ftyp/box/QCD allocations and tile-count math all carry the C sanity
  guards. -race clean on all internal packages and the full oracletest
  suite (decode/jp2/encode/cli/concurrency, 0 data races). Only fix
  needed was the mreader OOB.
