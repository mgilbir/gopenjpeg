# JP2 header-parse vectors (`internal/jp2`)

Golden vectors for the JP2 container (box) parser. These drive
`internal/jp2`'s `TestHeaderGolden` fully offline (no oracle clone needed).

## Contents

- `files/` — 25 small real `.jp2` files (total ~330 KB) copied from the
  OpenJPEG conformance corpus. Every file originates from
  `oracle/data/input/nonregression/<same-name>` in the checked-out
  `openjpeg-data` repository (upstream
  <https://github.com/uclouvain/openjpeg-data>). They were chosen to exercise
  the full JP2 box surface:
  - palette + component-mapping + channel-definition: `issue412.jp2`,
    `451.pdf.SIGSEGV.f4c.3723.jp2`,
    `147af3f1083de4393666b7d99b01b58b_signal_sigsegv_130c531_6155_5136.jp2`
  - `bpcc` (variable bit depth) + `cdef`: `issue774.jp2`, `issue458.jp2`,
    `issue725.jp2`, `issue733.jp2`
  - `cdef` only: `4149.pdf.SIGSEGV.cf7.3501.jp2`, `Marrin.jp2`
  - unknown box inside/after `jp2h`: `issue653-zero-unknownbox.jp2`
  - enumerated colour spaces: sRGB (16), grayscale (17), sYCC (18),
    e-YCC (24, `issue725.jp2`), CMYK (12, `issue774.jp2`,
    `147af3f…5136.jp2`)
  - CIELab special case (enumcs 14): `issue559-eci-090-CIELab.jp2`
  - ICC profiles (colr meth 2): `relax.jp2` (278-byte profile),
    `orb-blue10-lin-jp2.jp2`
  - ignored colr (meth > 2): `issue211.jp2`
  - sub-sampled components: `issue411-ycc420/422/444.jp2`
  - assorted plain-colr images: `merged.jp2`, `file409752.jp2`,
    `issue134.jp2`, `issue206_image-000.jp2`, `issue390.jp2`, `issue165.jp2`,
    `issue408.jp2`, `dwt_interleave_h.gsr105.jp2`

- `golden.json` — for each file, the JP2-box-level header facts our parser
  produces up to (but not including) the codestream:
  - `ihdr`: `[height, width, numcomps, bpc, C, UnkC, IPR]`
  - `comp_bpcc`: per-component bit-depth byte (0 unless a `bpcc` box was read)
  - `colr`: `[meth, precedence, approx, enumcs]`
  - `has_colr`, `icc_len`, `icc_buf_bytes` (36 for the packed CIELab case)
  - `pclr`: `[nr_entries, nr_channels]` when present
  - `cmap_n`, `cdef_n`, `img_state_unknown`

## Regenerating

`golden.json` is regenerated from the files by the parser itself:

```
GOPENJPEG_GEN_GOLDEN=1 go test ./internal/jp2 -run TestGenerateGolden
```

The values are independently grounded against the C reference: when the oracle
is present, `TestOracleIHDRConsistency` runs `opj_dump -i <file>` and checks the
`ihdr` width/height/numcomps against opj_dump's reported image geometry.
