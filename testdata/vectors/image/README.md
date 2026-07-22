# Image geometry oracle vectors

`vectors.json` holds component-geometry vectors generated from the OpenJPEG C
reference function `opj_image_comp_header_update` (`image.c`), used to validate
the Go port in `internal/image`.

Each case describes a single-tile grid (`tw = th = 1`) spanning the whole image,
so the update reduces to the standard per-component geometry derivation from the
image bounds, the component sub-sampling (`dx`/`dy`) and the reduce factor. The
Go test reconstructs the same single-tile `CompHeaderUpdateParams`
(`tx0 = x0`, `ty0 = y0`, `tdx = x1 - x0`, `tdy = y1 - y0`, `tw = th = 1`).

## Structure

`cases` — array of objects, each with:

- Inputs: `x0`, `y0`, `x1`, `y1` (image bounds), `dx`, `dy` (component
  sub-sampling), `prec` (precision, carried for reference; not used by the
  geometry math), `reduce` (component `factor`, the resolution-reduction count).
- Outputs (from C): `w`, `h` (component data width/height), `cx0`, `cy0`
  (component offsets).

Cases cover subsampling (dx/dy = 2,3,4), odd origins, odd image sizes, and
reduce factors 0..5, including 1x1 images.

## Regeneration

The harness source is `oracle/harness/w4/image_gen.c` (gitignored). From the
repo root:

```
gcc -O2 -I oracle/openjpeg/src/lib/openjp2 -I oracle/openjpeg/build/src/lib/openjp2 \
    oracle/harness/w4/image_gen.c oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread \
    -o /tmp/image_gen
/tmp/image_gen > testdata/vectors/image/vectors.json
```

Cases are a fixed table in the harness, so output is deterministic.
