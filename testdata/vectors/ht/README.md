# HT (HTJ2K) code-block decode vectors

Two gzip-compressed streams of per-code-block vectors captured from an
**instrumented OpenJPEG** `opj_t1_ht_decode_cblk` (the C reference), used by
`internal/ht` to prove bit-exact decoding:

- `cleanup_vectors.bin.gz` — cleanup-pass-only records captured by decoding the
  HTJ2K conformance corpus with the instrumented `opj_decompress`.
- `multipass_vectors.bin.gz` — multi-pass records (CUP+SPP and CUP+SPP+MRP)
  captured by driving `opj_t1_ht_decode_cblk` directly with synthesized
  code-blocks (see "Multi-pass vectors" below).

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

## Multi-pass vectors

**All available HT conformance streams are cleanup-pass-only** (`numsegs=1`,
`num_passes=1`, and `numbps==1` i.e. `zero_bplanes == Mb`, which would force
multi-pass decoding off anyway). No open-source encoder emits HT SigProp/MagRef
passes: OpenJPEG has no HT encoder at all, and OpenJPH's block encoder asserts
`num_passes == 1`.

`multipass_vectors.bin.gz` therefore comes from the module-level harness
`oracle/harness/w10/gen_multipass.c`, which calls the instrumented
`opj_t1_ht_decode_cblk` directly with synthesized code-blocks built from the
574 real corpus records:

- the real cleanup segment is kept verbatim (it stays a valid cleanup segment
  for any `(Mb', numbps')` with unchanged `zero_bplanes = Mb'+1-numbps'`,
  because cleanup decoding uses only `p = numbps` as a shift and
  `zero_bplanes+1` as a bound);
- `numbps'` is raised to 2/3/8 (with `Mb' = zero_bplanes + numbps' - 1`) so the
  SPP/MRP passes are legal (`p >= 2`);
- a deterministic pseudorandom segment 2 of varied length (including 0-length
  for the "zero length for 2nd pass" warning path) is appended, and
  `segs[1].real_num_passes` is 1 (CUP+SPP) or 2 (CUP+SPP+MRP);
- the VSC (stripe-causal) bit is flipped on one variant per record.

Any byte string is a decodable refinement segment (SPP/MRP consume bits on
demand; exhaustion fills zeros), so the C decoder's output on these inputs is
well-defined ground truth. The checked-in subset holds 189 records (up to 3 per
`(w, h, cblksty, numbps, num_passes, seg2==0)` signature; 162 with active
SPP/MRP data, 574 CUP+SPP + 1148 CUP+SPP+MRP in the full set). A cleanup-only
Go decode differs from the expected output on 1603 of the 1630 non-empty-seg2
records, confirming the refinement passes carry real effect.

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

3. Subsample (up to 3 per `(w,h,cblksty,Mb,numbps)` signature) and gzip into
   `testdata/vectors/ht/cleanup_vectors.bin.gz` (see the git history of this
   directory for the selection script).

4. Multi-pass vectors: build and run the direct harness against the *raw*
   (unsubsampled) dump from step 2:

   ```
   cd oracle/harness/w10
   gcc -O2 -o gen_multipass gen_multipass.c \
     -I openjpeg-instr/src/lib/openjp2 \
     -I openjpeg-instr/build/src/lib/openjp2 \
     openjpeg-instr/build/bin/libopenjp2.a -lm -lpthread
   # (build the static lib first: cmake -DBUILD_STATIC_LIBS=ON && make openjp2_static)
   OPJ_W10_DUMP=/tmp/multipass.bin ./gen_multipass /tmp/vectors.bin
   ```

   Then subsample (up to 3 per `(w,h,cblksty,numbps,num_passes,seg2==0)`
   signature, 1 for zero-length-seg2 records) and gzip into
   `testdata/vectors/ht/multipass_vectors.bin.gz`.
