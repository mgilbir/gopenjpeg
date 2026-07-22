# dwt oracle vectors

Deterministic vectors generated from the OpenJPEG C library and replayed
bit-for-bit against the Go port in `internal/dwt`. For the 9/7 (irreversible)
transform the int32 words are float32 bit patterns and are compared by bits.

| file          | test                     | contents |
|---------------|--------------------------|----------|
| `whole.bin`   | `TestWholeTileVectors`   | whole-tile 5/3 + 9/7, forward + inverse |
| `partial.bin` | `TestPartialVectors`     | region/partial-decode 5/3 + 9/7 |
| `norms.bin`   | `TestNormsVectors`       | `getnorm`/`getnorm_real` + `calc_explicit_stepsizes` |

All harness sources live under `oracle/harness/w3/` (gitignored). Resolution
and band coordinates use the standard `ceildivpow2` reduction; 1x1 tiles with
more than one resolution level are excluded because the C reference reads out of
bounds (undefined behaviour) on the single-element buffer, and over-decomposed
tiles whose processed resolutions collapse to a zero-size band are excluded from
lossless round-trip checks.

## Regeneration

```sh
INC="-I oracle/openjpeg/src/lib/openjp2 -I oracle/openjpeg/build/src/lib/openjp2"
LIB="oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread"
gcc -O2 $INC oracle/harness/w3/dwt_gen.c     $LIB -o /tmp/dwt_gen
gcc -O2 $INC oracle/harness/w3/partial_gen.c $LIB -o /tmp/partial_gen
gcc -O2 $INC oracle/harness/w3/norms_gen.c   $LIB -o /tmp/norms_gen
/tmp/dwt_gen     testdata/vectors/dwt/whole.bin
/tmp/partial_gen testdata/vectors/dwt/partial.bin
/tmp/norms_gen   testdata/vectors/dwt/norms.bin
```

## Formats (little-endian)

### whole.bin

```
u32 ncases
repeat ncases:
  u32 type            # 0=enc53, 1=dec53, 2=enc97, 3=dec97
  u32 numres
  u32 w, u32 h        # tile-component extent
  i32 x0, i32 y0      # tile-component origin
  repeat numres: i32 x0,y0,x1,y1    # resolution extents
  i32 input[w*h]      # transform input
  i32 output[w*h]     # C transform output
```

### partial.bin

```
u32 ncases
repeat ncases:
  u32 type            # 0=53, 1=97
  u32 numres
  i32 x0,y0,x1,y1     # tile-component extent
  u32 wx0,wy0,wx1,wy1 # window of interest (tile-component coords)
  repeat numres:
    i32 rx0,ry0,rx1,ry1
    u32 numbands
    repeat numbands:
      u32 bandno
      i32 bx0,by0,bx1,by1
      u32 datalen                 # (bx1-bx0)*(by1-by0) or 0 for empty band
      i32 decoded_data[datalen]   # one whole-band code-block's coefficients
  i32 data_win[(wx1-wx0)*(wy1-wy0)]   # C reconstructed window
```

The Go replay rebuilds the tile with `pw=ph=1`, one precinct and one whole-band
code-block per band (the sparse array reconstructs the same Mallat layout).

### norms.bin

```
u32 levels (=12), u32 orients (=4)
repeat levels*orients:
  f64 getnorm(level,orient)
  f64 getnorm_real(level,orient)
u32 ncfg
repeat ncfg:
  u32 numresolutions, qmfbid, qntsty, prec, numbands
  repeat numbands: i32 expn, i32 mant   # calc_explicit_stepsizes output
```
