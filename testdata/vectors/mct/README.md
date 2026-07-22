# MCT oracle vectors

`vectors.json` holds bit-exact input/output vectors for the multi-component
transforms, generated from the OpenJPEG C reference (`mct.c`, `invert.c`) so the
Go port in `internal/mct` can be validated bit-for-bit.

All `float32` values are stored as their raw IEEE-754 bit pattern, encoded as a
decimal `uint32`, so comparison is exact (NaN-safe). Integer values are stored
as plain JSON integers.

## Structure

- `rct` — reversible color transform (`opj_mct_encode` / `opj_mct_decode`),
  int32. `n` samples; `c0/c1/c2` are the inputs, `enc_*` the forward-transform
  outputs, `dec_*` the inverse-transform outputs (inverse applied to the same
  `c0/c1/c2` inputs, not to `enc_*`).
- `ict` — irreversible color transform (`opj_mct_encode_real` /
  `opj_mct_decode_real`), float32 bits. Same layout as `rct`.
- `custom` — arbitrary NxN matrix transform, one object per case
  (`nbcomp` = 3..6):
  - `matrix` — `nbcomp*nbcomp` float32 bits, row-major.
  - `enc_in` / `enc_out` — `opj_mct_encode_custom`, int32 component data
    (`nbcomp` arrays of `n` samples). The matrix is applied in 1<<13 fixed
    point via `opj_int_fix_mul`.
  - `dec_in` / `dec_out` — `opj_mct_decode_custom`, float32-bits component data.
    The matrix is applied directly in float32.
- `inversion` — `opj_matrix_inversion_f`, one object per case:
  - `in` — source matrix, `nbcomp*nbcomp` float32 bits, row-major.
  - `ok` — whether the C inversion succeeded (false for the singular case).
  - `out` — inverse matrix float32 bits (meaningful only when `ok`).

The last inversion case has a zeroed first column, so the C pivot search fails
and `ok` is false — this exercises the singular-matrix error path.

## Regeneration

The harness source is `oracle/harness/w4/mct_gen.c` (gitignored). From the repo
root:

```
gcc -O2 -I oracle/openjpeg/src/lib/openjp2 -I oracle/openjpeg/build/src/lib/openjp2 \
    oracle/harness/w4/mct_gen.c oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread \
    -o /tmp/mct_gen
/tmp/mct_gen > testdata/vectors/mct/vectors.json
```

The harness uses a fixed-seed xorshift64 PRNG, so output is deterministic.
