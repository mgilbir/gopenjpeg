# gopenjpeg

A pure-Go port of [OpenJPEG](https://github.com/uclouvain/openjpeg), the
JPEG 2000 reference codec. Both directions are complete and gated against the C
reference: decoded samples are bit-exact with `opj_decompress`, and encoded
codestreams are byte-identical to `opj_compress`, across the conformance and
non-regression corpora (see `oracletest/`). There are no documented exclusions.

No cgo, no C toolchain, no `unsafe`. The only dependency is
[golittlecms](https://github.com/mgilbir/golittlecms), a pure-Go Little CMS port
used for embedded ICC profiles.

The library never panics: every malformed input, violated invariant or
impossible geometry is returned as an error.

## Library API

The root package `github.com/mgilbir/gopenjpeg` reads and writes both raw
codestreams (`.j2k`/`.j2c`/`.jpc`) and JP2/JPH containers (`.jp2`/`.jph`),
detecting the input format from its magic bytes.

### Decoding

```go
import "github.com/mgilbir/gopenjpeg"

f, _ := os.Open("picture.jp2")
defer f.Close()

img, err := gopenjpeg.Decode(f,
    gopenjpeg.WithReduce(1),                  // discard 1 resolution level (-r)
    gopenjpeg.WithLayers(2),                  // decode only 2 quality layers (-l)
    gopenjpeg.WithDecodeArea(0, 0, 512, 512), // region of interest (-d)
    gopenjpeg.WithComponents(0, 1, 2),        // component subset (-c)
    gopenjpeg.WithStrictMode(false),          // relaxed conformance (default)
    gopenjpeg.WithConcurrency(runtime.NumCPU()),
    gopenjpeg.WithWarningHandler(func(s string) { log.Print(s) }),
)
if err != nil {
    log.Fatal(err)
}
```

`Decode` returns a `*gopenjpeg.Image` that preserves full fidelity: per-component
geometry, sub-sampling, precision, signedness, colour space and any embedded ICC
profile. Access components directly:

```go
for i := 0; i < img.NumComponents(); i++ {
    c := img.Component(i) // Dx, Dy, W, H, Prec, Sgnd, Alpha, Data []int32
    _ = c
}
```

Decode options: `WithFormat`, `WithReduce`, `WithLayers`, `WithDecodeArea`,
`WithComponents`, `WithTile`, `WithStrictMode`, `WithConcurrency`,
`WithMaxInputSize`, `WithWarningHandler`, `WithErrorHandler`, `WithInfoHandler`.

`WithConcurrency(n)` fans the parallelizable decode stages (per-code-block
tier-1, inverse-DWT rows and columns) across `n` goroutines, mirroring
OpenJPEG's thread-pool use sites. The decoded output is identical at any `n`.

If the reader is an `io.ReadSeeker` it is read on demand and never fully
buffered, and its current position is treated as the start of the stream. Any
other `io.Reader` is read entirely into memory first — use `WithMaxInputSize`
to bound that buffer for untrusted, non-seekable input.

### Colour handling

`ConvertToRGB` reproduces, in order, exactly what `opj_decompress` does before
writing an output file: colour-space label normalisation, then sYCC / eYCC /
CMYK to sRGB, then colr-box colour management — enumerated CIELab, or an
embedded ICC profile applied through the pure-Go Little CMS port. It is a no-op
for images that are already sRGB or greyscale with no profile, and it is
idempotent.

```go
_ = img.ConvertToRGB()
std, err := img.ToStandard() // image.Gray / Gray16 / NRGBA / NRGBA64
```

`ApplyICCProfile` is the ICC step on its own, for callers that want to apply a
profile without the rest of the pipeline; `ConvertToRGB` already calls it when a
profile is present, so calling both is unnecessary. Both are best-effort, as in
C: a profile that cannot be applied returns `ErrICCApply` and leaves the ICC
step undone.

`ToStandard` is a convenience conversion to a Go standard-library image. It is
lossy when the native precision is not exactly 8 or 16 bits, and returns an
error for shapes it cannot faithfully render (components with differing
dimensions, such as un-upsampled chroma sub-sampling, and precision above 16
bits) — use the `Component` accessors for full fidelity in those cases.

### Encoding

```go
out, _ := os.Create("picture.jp2")
defer out.Close()

err := gopenjpeg.Encode(img, out,
    gopenjpeg.WithEncodeFormat(gopenjpeg.FormatJP2),
    gopenjpeg.WithIrreversible(),          // 9/7 wavelet (-I)
    gopenjpeg.WithRates(40, 20, 10),       // one ratio per quality layer (-r)
    gopenjpeg.WithTileSize(512, 512),      // (-t)
    gopenjpeg.WithProgressionOrder(gopenjpeg.ProgRPCL),
    gopenjpeg.WithEncodeConcurrency(runtime.NumCPU()),
)
```

`Encode` does not modify `img`: the sample data is read, never consumed, so the
same `Image` can be encoded repeatedly with different options. The whole output
is assembled in memory before it is written (the JP2 `jp2c` length back-patch
needs a seekable stream). Build an image to encode with `NewImage`, or reuse one
returned by `Decode`.

Encode options mirror the codestream capabilities of `opj_compress`:

| Area | Options |
|---|---|
| container | `WithEncodeFormat` |
| wavelet | `WithLossless` (default), `WithIrreversible` |
| rate control | `WithRates`, `WithQualityLayers`, `WithMaxCodestreamSize`, `WithMaxComponentSize` |
| structure | `WithResolutions`, `WithCodeBlockSize`, `WithPrecincts`, `WithTileSize`, `WithTileOrigin`, `WithTileParts` |
| progression | `WithProgressionOrder`, `WithPOC` |
| coding | `WithModeSwitches`, `WithGuardBits`, `WithROI`, `WithSubsampling` |
| colour | `WithMCT`, `WithCustomMCT` |
| markers | `WithSOP`, `WithEPH`, `WithPLT`, `WithTLM`, `WithComment` |
| profiles | `WithCinema2K`, `WithCinema4K`, `WithProfile` |
| diagnostics | `WithEncodeWarningHandler`, `WithEncodeErrorHandler`, `WithEncodeInfoHandler` |
| concurrency | `WithEncodeConcurrency` |

`WithEncodeConcurrency(n)` parallelizes tier-1 encoding; distortion is summed in
canonical order, so the codestream bytes are identical at any `n`.

Option values are validated at the `Encode` entry point, which is the single
choke point OpenJPEG placed in its CLI: out-of-range layer and POC counts,
degenerate tile grids, invalid sub-sampling and malformed components are
rejected with an error rather than carried into the encoder.

Two caveats are worth calling out. `WithSubsampling` records the factors that
`opj_compress`'s image loaders stamp onto components; sub-sampling is a property
of the image, so set `Component.Dx/Dy` and size the reference grid accordingly —
`Encode` rejects a `WithSubsampling` value that disagrees with the image rather
than ignoring it. And `WithCustomMCT` emits a JPEG 2000 Part-2 array-based MCT
that neither this library nor stock OpenJPEG can decode; use it only when
targeting a third-party Part-2 decoder.

### Reading headers only

`ReadInfo` parses the main header and returns structural metadata — geometry,
tile grid, per-component precision/sign/sub-sampling, colour space, and the
JP2 box fields — without decoding any samples. It accepts the same options as
`Decode`; its doc comment lists which of them actually apply.

## Command-line tools

Three tools mirror their `opj_*` counterparts.

### gopj-compress

Compresses a raster image into a JPEG 2000 file, choosing the codestream or
container format from the output extension. Its flag grammar follows
`opj_compress`: hand-parsed, case-sensitive, values as separate arguments
(`-i in.pgm`, not `-i=in.pgm`). Run `gopj-compress -h` for the full usage text.

```
gopj-compress -i input.pgm -o output.j2k [flags]

  -i  input file: .pbm/.pgm/.ppm/.pnm/.pam (netpbm), .pgx,
      .raw/.yuv (big-endian) or .rawl (little-endian)     (required)
  -o  output file: .j2k/.j2c/.jpc (codestream) or .jp2/.jph (container)
                                                          (required)
  -F  raw/yuv geometry w,h,ncomp,bitdepth,{s|u}[@dxXdy:...]
      (required for .raw/.yuv/.rawl input)
  -r  compression ratios per layer, comma-separated (1 = lossless)
  -q  target PSNR per layer, comma-separated (mutually exclusive with -r)
  -n  number of resolutions (decompositions + 1); default 6
  -b  code-block size w,h; default 64,64
  -c  precinct sizes [w,h],[w,h],... highest resolution first
  -t  tile size w,h; default one tile covering the image
  -T  tile origin x,y
  -d  image origin x,y
  -s  sub-sampling factors subX,subY; default 1,1
  -p  progression order LRCP|RLCP|RPCL|PCRL|CPRL; default LRCP
  -POC  progression-order changes, '/'-separated records
        T<tile>=<resStart>,<compStart>,<layerEnd>,<resEnd>,<compEnd>,<prog>
  -I  irreversible 9/7 wavelet (default: reversible 5/3)
  -M  mode-switch bitmask: 1 BYPASS, 2 RESET, 4 TERMALL, 8 VSC,
      16 PTERM, 32 SEGSYM (add the values)
  -G  quantization guard bits, 0..7
  -ROI  region of interest  c=<component>,U=<upshift>
  -TP   tile-parts divided by R (resolution), L (layer) or C (component)
  -SOP  write an SOP marker before each packet
  -EPH  write an EPH marker after each packet header
  -PLT  write PLT markers in the tile-part headers
  -TLM  write a TLM marker in the main header
  -mct  0 none, 1 RGB->YCC, 2 custom (requires -m)
  -m    array-based MCT matrix file (Part-2; see the -mct 2 caveat above)
  -C    COM marker content
  -cinema2K <24|48>   Digital Cinema 2K profile
  -cinema4K           Digital Cinema 4K profile
  -IMF <PROFILE>[,mainlevel=X][,sublevel=Y][,framerate=F]
                      IMF profile; PROFILE is 2K, 4K, 8K, 2K_R, 4K_R or 8K_R
  -threads <n|ALL_CPUS>  worker goroutines for the encode
  -quiet  suppress informational output
  -h      print the full usage text
```

The output file is written atomically (temporary file plus rename), so a failed
encode never truncates or replaces a previous good file.

### gopj-decompress

Decodes a JPEG 2000 file and writes a raster image, choosing the output format
from the file extension. It mirrors the decode-side flags and writer behaviour
of `opj_decompress`; PGX, PNM and RAW outputs are byte-identical to the
reference. Flags use the Go `flag` grammar, so `-r 1` and `-r=1` both work.

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
  -t  decode only tile index N (default -1: all tiles)
  -threads <n|ALL_CPUS>  worker goroutines for the decode (default 1)
  -strict   reject truncated/non-compliant codestreams
  -quiet    suppress informational output
```

Colour conversion (`ConvertToRGB`) is always applied before writing, as
`opj_decompress` does.

Note: the reference `imagetoraw`/`imagetorawl` writers ignore their endianness
argument and emit samples in host byte order; on a little-endian host `.raw` and
`.rawl` are therefore identical, and this port matches that.

### gopj-dump

Prints the main-header structure (image and tile geometry, per-component
precision/sign/sub-sampling, colour space, JP2 box fields). It approximates
`opj_dump`; the output is structured and complete but not textually identical
to the C tool (it omits the full coding-style tables).

```
gopj-dump -i input.jp2
```

## Testing

Two layers of testing pin this port to the C reference.

**Module-level vectors.** Every internal package replays vectors captured from
the C implementation and checked in under `testdata/vectors/` — the MQ coder,
DWT (whole-tile and region), T1/EBCOT, HTJ2K code-blocks, MCT and matrix
inversion, the packet iterator, tier-2 packets, tag trees, bit and byte I/O,
and JP2 box parsing. The C programs that generate them are tracked under
`tools/oracle-harness/`, together with a `regen.sh` that rebuilds every vector
from an OpenJPEG checkout; they are outside the Go build. These tests need
nothing but a Go toolchain.

**Differential gates.** `oracletest/` runs the pure-Go codec and the built C
binaries side by side over the `openjpeg-data` corpus:

- decode of raw codestreams and of JP2/JPH containers must produce samples
  bit-exact with `opj_decompress`, or fail exactly where it fails;
- encoding must produce codestreams byte-identical to `opj_compress` over a
  settings matrix, for `.j2k` and `.jp2` output alike, including the Digital
  Cinema and IMF profiles (the sole exception is the Part-2 custom MCT, which
  `opj_compress` cannot emit at all, so that cell checks the marker set against
  the C source instead of against a running binary);
- `gopj-decompress` and `gopj-compress` must produce output files byte-identical
  to their C counterparts;
- concurrent decode and encode must be byte-identical to the sequential paths at
  any worker count.

**There are zero exclusions.** Every colour path `opj_decompress` uses is
reproduced, including CMYK, sYCC/eYCC, embedded ICC profiles (via golittlecms)
and enumerated CIELab. The one gate that is not a bit-exact equality is CIELab,
which is checked to within 1/65535 per sample against the oracle's liblcms2.

The gates locate the oracle at `oracle/` (a gitignored local OpenJPEG build plus
corpus) and skip themselves when it is absent, so `go test ./...` works on a
fresh clone. Absence is the only reason they skip: once the oracle is present, a
gate that runs a C binary and gets a failure fails the test rather than skipping
it.

**CI** (`.github/workflows/ci.yml`) runs build, vet, gofmt, the full test suite
and the fuzz seed corpora on amd64 for both the declared Go version and the
current stable one, plus the same suite natively on arm64 and a cross-compile
check that no FMA contraction survives in the float kernels. CI does *not* build
the C oracle — see the comments at the top of the workflow for why — so the
differential gates skip there and remain a maintainer step run against a local
`oracle/`.

**Fuzzing.** The public API (`Decode`, `ReadInfo`, encode round-trips, the
colour pipeline and the encode option surface) and every parser package carry
fuzz targets seeded from the corpus. Any panic is a bug: the no-panic rule is
enforced, not aspirational.

### Bit-exactness across architectures

Bit-exactness holds on every `GOARCH`, not only the one the gates run on. Go's
specification lets the compiler contract `x*y + z` into a single-rounding FMA,
and gc does so on arm64, ppc64, s390x, riscv64 and loong64 — but never on amd64.
The C oracle rounds each product separately, so a contracted kernel would diverge
on those architectures while the amd64 gates stayed green. Every float product
that feeds an add or a subtract in the float pipeline (the irreversible 9/7 DWT,
the irreversible MCT and custom-MCT matrix paths, and the sYCC/eYCC/CMYK colour
conversions) is therefore wrapped in an explicit `float32(...)`/`float64(...)`
conversion, which the spec defines as a rounding step and hence an FMA barrier.
The barriers forbid fusion only — operand order and associativity are unchanged,
and the amd64 code generation is identical, so the byte-identical gate results
are unaffected. Compiling for each fusing `GOARCH` confirms no FMA instruction
remains in those kernels, and CI replays the float vectors on a native arm64
runner.
