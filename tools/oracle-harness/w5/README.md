# W5 tier-1 (t1) oracle harness

Generates the checked-in `testdata/vectors/t1/*.bin.gz` vectors used by
`internal/t1` tests. Regenerable via `tools/oracle-harness/regen.sh`; only the
`oracle/` checkout it builds against is gitignored.

## Build & run (from the repo root)

```sh
gcc -O2 \
    -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    tools/oracle-harness/w5/harness.c \
    oracle/openjpeg/build/bin/libopenjp2.a \
    -lm -lpthread -o /tmp/w5harness
./w5harness            # writes testdata/vectors/t1/t1_{encode,decode}.bin
gzip -9 -f testdata/vectors/t1/t1_encode.bin testdata/vectors/t1/t1_decode.bin
```

## How it links

`harness.c` does `#include "t1.c"`, so it compiles its own copy of every t1
symbol (including the `static` `opj_t1_encode_cblk` / `opj_t1_decode_cblk` /
`opj_t1_allocate_buffers` that the harness drives directly). Because the
harness object already defines all of t1.c's *non-static* externs
(`opj_t1_encode_cblks`, `opj_t1_create`, ...), the linker never pulls `t1.o`
out of `libopenjp2.a`, so there is no duplicate-symbol clash. Every other
dependency (mqc, dwt norms, `opj_aligned_malloc`, ...) is resolved normally
from the archive.

`t1.c` issues `#pragma GCC poison malloc calloc realloc free` after its
includes, so the harness code (which lives after the `#include`) uses only
`opj_*` allocators, stack/static buffers, and `fwrite`.

## Vector format (little-endian)

`t1_encode.bin`: `"T1EN0001"`, `uint32 count`, then per record:
`w,h,orient,compno,level,qmfbid,cblksty,numcomps (u32)`, `stepsize (f64)`,
`int32[w*h] input`, `numbps,totalpasses (u32)`,
`totalpasses × {rate u32, term u32, distortiondec f64}`,
`fullLen u32`, `fullLen bytes` of coded stream.

`t1_decode.bin`: `"T1DE0001"`, `uint32 count`, then per record:
`w,h,orient,roishift,cblksty,numbps,nsegs (u32)`,
`nsegs × {len u32, real_num_passes u32}`, `chunkLen u32`, `chunkLen bytes`,
`int32[w*h]` reconstructed `t1->data`.

The decode records are produced by encoding a block, then re-decoding it (via
the static `opj_t1_decode_cblk`) at several truncation points — 1 pass, the
mid pass, the full pass count, and up to two terminated-pass/segment
boundaries — for `roishift` ∈ {0,3}. Segments are derived from the encoder's
per-pass `term` flags.
