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
