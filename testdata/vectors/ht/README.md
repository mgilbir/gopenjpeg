# HT (HTJ2K) code-block decode vectors

`cleanup_vectors.bin.gz` is a gzip-compressed stream of per-code-block vectors
captured from an **instrumented OpenJPEG** `opj_t1_ht_decode_cblk` (the C
reference), used by `internal/ht` to prove bit-exact decoding.

Each record is (all integers little-endian):

| field            | type            | notes                                  |
|------------------|-----------------|----------------------------------------|
| width, height    | int32 x2        | code-block dimensions (x1-x0, y1-y0)   |
| orient           | uint32          | sub-band orientation (ignored by HT)   |
| roishift         | uint32          | ROI shift (always 0 for HT)            |
| cblksty          | uint32          | code-block style (0x40 HT, +0x08 VSC)  |
| Mb               | uint32          | cblk->Mb (Kmax = band->numbps)         |
| numbps           | uint32          | cblk->numbps                           |
| numsegs          | uint32          | cblk->numsegs                          |
| s0p, s0l         | uint32 x2       | seg[0].real_num_passes, seg[0].len     |
| s1p, s1l         | uint32 x2       | seg[1].real_num_passes, seg[1].len     |
| total            | uint32          | concatenated coded-data length         |
| coded[total]     | bytes           | concatenated chunk bytes (the stream)  |
| outcount         | uint32          | width*height                           |
| out[outcount]    | int32           | decoded t1->data (raster, sign+mag)    |

## Coverage

The checked-in file holds 96 records (subsampled from 574) — up to 3 per
distinct `(width, height, cblksty, Mb, numbps)` signature — spanning all 17
code-block sizes present in the corpus (including partial blocks such as 20x15,
16x60, 32x64), both HT and HT+VSC styles, and `Mb` 9-14. The Go test replays the
full 574-record set locally when regenerated; the subset is what ships.

**All available HT conformance streams are cleanup-pass-only** (`numsegs=1`,
`num_passes=1`). The corpus contains no multi-pass HT (SigProp/MagRef) streams
and OpenJPEG has no HT *encoder* to synthesize them, so the SigProp/MagRef
refinement paths in `internal/ht` are validated only by the fuzz target
(`FuzzDecodeCblk`, which drives 2-segment multi-pass cases for panic-safety),
not by oracle vectors. This is a known coverage gap.

## Regeneration

1. Build the instrumented decoder (source lives under
   `oracle/harness/w10/openjpeg-instr`, a copy of `oracle/openjpeg` with a dump
   hook added at the tail of `opj_t1_ht_decode_cblk` in
   `src/lib/openjp2/ht_dec.c`, guarded by the `OPJ_W10_DUMP` env var):

   ```
   cd oracle/harness/w10/openjpeg-instr && mkdir -p build && cd build
   cmake -DCMAKE_BUILD_TYPE=Release -DBUILD_CODEC=ON ..
   make -j openjp2 opj_decompress
   ```

2. Dump vectors by decoding the HT conformance files:

   ```
   DUMP=/tmp/vectors.bin; rm -f "$DUMP"
   for f in oracle/data/input/nonregression/htj2k/*.j2k \
            oracle/data/input/nonregression/htj2k/*.jph \
            oracle/data/input/nonregression/htj2k/*.jhc; do
     OPJ_W10_DUMP="$DUMP" \
       oracle/harness/w10/openjpeg-instr/build/bin/opj_decompress -i "$f" -o /tmp/o.png
   done
   ```

3. Subsample (up to 3 per signature) and gzip into
   `testdata/vectors/ht/cleanup_vectors.bin.gz` (see the git history of this
   directory for the selection script).
