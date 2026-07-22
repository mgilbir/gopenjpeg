# Fuzz seed corpus

Small (<50 KB) JPEG 2000 inputs checked in so the root-package fuzz targets
(`FuzzDecode`, `FuzzDecodeConcurrent`, `FuzzEncodeDecodeRoundTrip`,
`FuzzReadInfo` in `../../fuzz_test.go`) have meaningful, structure-valid seeds
even when the (gitignored) `oracle/data` corpus is absent.

All files are copied verbatim from the OpenJPEG conformance / non-regression
data set (`github.com/uclouvain/openjpeg-data`, mirrored under
`oracle/data/input`). Origins:

| file | origin (`oracle/data/input/...`) | notes |
|------|----------------------------------|-------|
| `p0_09.j2k` | `conformance/p0_09.j2k` | valid Part-1 codestream |
| `p0_11.j2k` | `conformance/p0_11.j2k` | valid Part-1 codestream |
| `p0_12.j2k` | `conformance/p0_12.j2k` | valid Part-1 codestream |
| `p1_07.j2k` | `conformance/p1_07.j2k` | valid Part-1 (profile-1) codestream |
| `basn4a08.jp2` | `nonregression/basn4a08.jp2` | valid JP2 with alpha |
| `byte.jph` | `nonregression/htj2k/byte.jph` | valid HTJ2K (JPH container) |
| `byte_causal.jhc` | `nonregression/htj2k/byte_causal.jhc` | valid HTJ2K raw codestream |
| `issue726.j2k` | `nonregression/issue726.j2k` | malformed codestream (regression) |
| `issue979.j2k` | `nonregression/issue979.j2k` | malformed codestream (regression) |
| `issue1438.j2k` | `nonregression/issue1438.j2k` | malformed codestream (regression) |
| `issue1472-bigloop.j2k` | `nonregression/issue1472-bigloop.j2k` | pathological loop bound (regression) |
| `issue427-null-image-size.jp2` | `nonregression/issue427-null-image-size.jp2` | zero image size (regression) |
| `issue823.jp2` | `nonregression/issue823.jp2` | malformed JP2 box structure |
| `gdal_fuzzer_check_number_of_tiles.jp2` | `nonregression/gdal_fuzzer_check_number_of_tiles.jp2` | GDAL fuzzer crasher |
| `huge-tile-size.jp2` | `nonregression/huge-tile-size.jp2` | absurd tile geometry (size-cap exercise) |

License: these inputs are part of the OpenJPEG data set (BSD-2-Clause, same as
this port). See `oracle/data/README` for the upstream copyright.

When the full oracle corpus is present, the fuzz targets additionally seed from
a curated ~40-file subset read live from `oracle/data/input` (see
`oracleSeedFiles` in `fuzz_test.go`).
