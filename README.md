# gopenjpeg

A pure-Go port of [OpenJPEG](https://github.com/uclouvain/openjpeg), the
JPEG 2000 reference codec. The decode path is complete and bit-exact with
`opj_decompress` across the conformance and non-regression corpora (see
`oracletest/`). Encoding is not yet implemented.

The library never panics: every malformed input, violated invariant or
impossible geometry is returned as an error.

## Library API

The root package `github.com/mgilbir/gopenjpeg` is the public decode surface.
It reads both raw codestreams (`.j2k`/`.j2c`/`.jpc`) and JP2/JPH containers
(`.jp2`/`.jph`), detecting the format from the input's magic bytes.

```go
import "github.com/mgilbir/gopenjpeg"

f, _ := os.Open("picture.jp2")
defer f.Close()

img, err := gopenjpeg.Decode(f,
    gopenjpeg.WithReduce(1),               // discard 1 resolution level (-r)
    gopenjpeg.WithLayers(2),               // decode only 2 quality layers (-l)
    gopenjpeg.WithDecodeArea(0, 0, 512, 512), // region of interest (-d)
    gopenjpeg.WithComponents(0, 1, 2),     // component subset (-c)
    gopenjpeg.WithStrictMode(false),       // relaxed conformance (default)
    gopenjpeg.WithWarningHandler(func(s string) { log.Print(s) }),
)
if err != nil {
    log.Fatal(err)
}
```

`Decode` returns a `*gopenjpeg.Image` that preserves full fidelity: per-
component geometry, sub-sampling, precision, signedness, colour space and any
embedded ICC profile. Access components directly:

```go
for i := 0; i < img.NumComponents(); i++ {
    c := img.Component(i) // Dx, Dy, W, H, Prec, Sgnd, Alpha, Data []int32
    _ = c
}
```

To reproduce what `opj_decompress` does before writing an output file (colour-
space normalisation plus sYCC/eYCC/CMYK to sRGB), call `ConvertToRGB`. Images
carrying an ICC profile return `ErrICCUnsupported`, because this port embeds no
colour-management engine.

```go
_ = img.ConvertToRGB()
std, err := img.ToStandard() // image.Gray / Gray16 / NRGBA / NRGBA64
```

`ToStandard` is a convenience conversion to a Go standard-library image. It is
lossy when the native precision is not exactly 8 or 16 bits, and returns an
error for shapes it cannot faithfully render (sub-sampled components, precision
above 16 bits) — use the `Component` accessors for full fidelity in those
cases.

Available decode options: `WithFormat`, `WithReduce`, `WithLayers`,
`WithDecodeArea`, `WithComponents`, `WithTile`, `WithStrictMode`,
`WithWarningHandler`, `WithErrorHandler`, `WithInfoHandler`.

`ReadInfo` reads only the header and returns structural metadata without
decoding samples.

## Command-line tools

### gopj-decompress

Decodes a JPEG 2000 file and writes a raster image, choosing the output format
from the file extension. It mirrors the decode-side flags and writer behaviour
of `opj_decompress`; PGX, PNM and RAW outputs are byte-identical to the
reference for the supported cases.

```
gopj-decompress -i input.jp2 -o output.ppm [flags]

  -i  input file (.j2k/.j2c/.jpc/.jp2/.jph)   (required)
  -o  output file; format chosen by extension:
        .pgx            portable graymap-X, one file per component
        .pgm/.ppm/.pnm  netpbm (P5/P6/P7)
        .raw/.rawl      headerless samples (little-endian on this host)
  -r  discard the N highest resolutions (reduce)
  -l  decode only the first N quality layers
  -d  decode area  x0,y0,x1,y1
  -c  component subset, comma-separated indices
  -t  decode only tile index N
  -strict   reject truncated/non-compliant codestreams
  -quiet    suppress informational output
```

Note: the reference `imagetoraw`/`imagetorawl` writers ignore their
endianness argument and emit samples in host byte order; on a little-endian
host `.raw` and `.rawl` are therefore identical, and this port matches that.

### gopj-dump

Prints the main-header structure (image and tile geometry, per-component
precision/sign/sub-sampling, colour space, JP2 box fields). It approximates
`opj_dump`; the output is structured and complete but not textually identical
to the C tool (it omits the full coding-style tables).

```
gopj-dump -i input.jp2
```

## Differential testing

`oracletest/` compares the pure-Go decoder against the built C
`opj_decompress`/`opj_dump` binaries over the `openjpeg-data` corpus. The
gates are skipped automatically when the oracle is absent, so `go test ./...`
works without it. See the package for the documented exclusions (ICC/CIELab
colour management and CMYK float-rounding, which a pure-Go port cannot
reproduce bit-exactly).
